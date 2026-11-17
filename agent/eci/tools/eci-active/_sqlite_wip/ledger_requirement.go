package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// runRequirementHelper projects an uncommitted ledger into a private assignment.
//
// Example: a failed helper returns neither its event nor its payload.
func runRequirementHelper(
	ctx context.Context,
	executable string,
	request json.RawMessage,
	projection ledgerProjection,
) (json.RawMessage, json.RawMessage, error) {
	input, err := json.Marshal(struct {
		Request  json.RawMessage  `json:"request"`
		Snapshot ledgerProjection `json:"snapshot"`
	}{Request: request, Snapshot: projection})
	if err != nil {
		return nil, nil, fmt.Errorf("encode requirement projection: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = os.Stderr
	spill, err := openPrivateSpill("eci-assignment-result-")
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = spill
	if err := cmd.Run(); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("prepare requirement assignment: %w", err), spill.Close())
	}
	if _, err := spill.Seek(0, io.SeekStart); err != nil {
		return nil, nil, errors.Join(err, spill.Close())
	}
	output, err := io.ReadAll(spill)
	if err = errors.Join(err, spill.Close()); err != nil {
		return nil, nil, err
	}
	if err := validateUniqueJSON(output); err != nil {
		return nil, nil, fmt.Errorf("invalid assignment envelope: %w", err)
	}
	var preparation map[string]json.RawMessage
	if err := json.Unmarshal(output, &preparation); err != nil || len(preparation) != 2 {
		return nil, nil, fmt.Errorf("invalid assignment envelope")
	}
	eventRaw, eventOK := preparation["event"]
	payloadRaw, payloadOK := preparation["payload"]
	if !eventOK || !payloadOK {
		return nil, nil, fmt.Errorf("assignment envelope requires event and payload")
	}
	event, err := validateLedgerEntry(string(eventRaw))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid assignment event: %w", err)
	}
	expected := "decision"
	if projection.SnapshotChanged {
		expected = "requirement_change"
	}
	if event["event"] != expected {
		return nil, nil, fmt.Errorf("assignment event does not match net change")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode assignment payload: %w", err)
	}
	var target, message string
	if err := json.Unmarshal(payload["target"], &target); err != nil {
		return nil, nil, fmt.Errorf("decode assignment target: %w", err)
	}
	if err := json.Unmarshal(payload["message"], &message); err != nil {
		return nil, nil, fmt.Errorf("decode assignment message: %w", err)
	}
	if target == "" || message == "" {
		return nil, nil, fmt.Errorf("assignment payload is incomplete")
	}
	return eventRaw, payloadRaw, nil
}

// prepareDatabaseRequirement validates, projects, and commits a capture batch.
//
// Example: a failed helper rolls back the proposed row changes.
func prepareDatabaseRequirement(
	ctx context.Context,
	dir string,
	data []byte,
	helperPath string,
) (_event json.RawMessage, _payload json.RawMessage, _err error) {
	base, ops, err := decodeNativeBatch(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode requirement operations: %w", err)
	}
	db, err := openLedger(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	defer
	// closeRequirementStore returns database close failures.
	//
	// Example: a helper failure still closes the database.
	func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin requirement capture: %w", err)
	}
	defer
	// rollbackRequirement aborts an uncommitted capture.
	//
	// Example: rejected chain projection rolls back the batch.
	func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_err = errors.Join(_err, err)
		}
	}()
	if _, err := validateNativeLedgerTransaction(ctx, tx, filepath.Base(dir)); err != nil {
		return nil, nil, err
	}
	changed, err := applyNativeLedgerBatch(ctx, tx, base, ops)
	if err != nil {
		return nil, nil, fmt.Errorf("apply requirement ops: %w", err)
	}
	projection, err := projectLedger(ctx, tx, changed)
	if err != nil {
		return nil, nil, err
	}
	event, payload, err := runRequirementHelper(ctx, helperPath, data, projection)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit requirement capture: %w", err)
	}
	return event, payload, nil
}
