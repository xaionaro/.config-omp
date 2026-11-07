// Package main captures native ledger edits and prepares assignments from saved records.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// CaptureEventKind identifies only the event alternatives emitted by assignment preparation.
//
// Example: changed snapshot capture uses CaptureRequirementChange.
type CaptureEventKind int

const (
	// CaptureDecision records preparation that reuses a faithful existing snapshot.
	//
	// Example: empty edits prepare a new assignment without changing requirements.
	CaptureDecision CaptureEventKind = iota + 1
	// CaptureRequirementChange records preparation that publishes changed requirement facts.
	//
	// Example: a compactness correction is captured before its assignment is emitted.
	CaptureRequirementChange
)

// MarshalJSON preserves canonical wire names and rejects unsupported generated event kinds.
//
// Example: CaptureDecision serializes as the canonical JSON string "decision".
func (kind CaptureEventKind) MarshalJSON() ([]byte, error) {
	switch kind {
	case CaptureDecision:
		return json.Marshal("decision")
	case CaptureRequirementChange:
		return json.Marshal("requirement_change")
	default:
		return nil, fmt.Errorf("unknown capture event kind %d", kind)
	}
}

// RecordID names a canonical semantic subject rather than an occurrence in a transcript.
//
// Example: requirement:metrics identifies the existing compactness requirement.
type RecordID string

// NativeEdit replaces one exact current byte sequence in the native snapshot.
//
// Example: replace an obsolete source and acceptance block without reencoding YAML.
type NativeEdit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// Identity attributes preparation to its actual coordinator.
//
// Example: the Supervisor prepares an assignment and later forwards it through native tools.
type Identity struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Turn string `json:"turn,omitempty"`
}

// Request describes selective capture and operational work without a separately authored requirement message.
//
// Example: empty Edits reuses a faithful existing requirement without rewriting its inode.
type Request struct {
	Edits        []NativeEdit `json:"edits"`
	Requirements []RecordID   `json:"requirements"`
	Outcome      RecordID     `json:"outcome"`
	Target       string       `json:"target"`
	Instructions string       `json:"instructions"`
	Actor        Identity     `json:"actor"`
	Reason       string       `json:"reason"`
}

// Payload is the exact native collaboration target and message returned after successful logging.
//
// Example: pass Message unchanged to followup_task using Target.
type Payload struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

// Preparation keeps assignment output private until the existing canonical log append succeeds.
//
// Example: the shell wrapper appends Event before emitting Payload.
type Preparation struct {
	Payload Payload      `json:"payload"`
	Event   CaptureEvent `json:"event"`
}

// CaptureDetails records exactly what this preparation changed and where native delivery is intended.
//
// Example: SnapshotChanged is false for a faithful no-write reuse.
type CaptureDetails struct {
	Outcome         RecordID     `json:"outcome"`
	Target          string       `json:"target"`
	SnapshotChanged bool         `json:"snapshot_changed"`
	Edits           []NativeEdit `json:"edits"`
}

// CaptureEvent is the closed event payload supplied to the canonical log owner.
//
// Example: changed source capture is logged as requirement_change, without asserting delivery.
type CaptureEvent struct {
	Event        CaptureEventKind `json:"event"`
	Actor        Identity         `json:"actor"`
	Summary      string           `json:"summary"`
	Reason       string           `json:"reason"`
	Requirements []RecordID       `json:"requirements"`
	Details      CaptureDetails   `json:"details"`
}

// readRequest rejects unknown input fields so callers cannot supply independent requirement prose.
//
// Example: a requirements_message field is an error rather than ignored misleading input.
func readRequest(path string) (Request, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return request, fmt.Errorf("expected one JSON request: %v", err)
	}
	if len(request.Requirements) == 0 || request.Outcome == "" || strings.TrimSpace(request.Target) == "" || strings.TrimSpace(request.Instructions) == "" || strings.TrimSpace(request.Actor.ID) == "" || strings.TrimSpace(request.Actor.Role) == "" || strings.TrimSpace(request.Reason) == "" {
		return request, errors.New("requirements, outcome, target, instructions, actor id/role and reason are required")
	}
	return request, nil
}

// mapping reads a mapping without dropping duplicate keys or unsupported field values.
//
// Example: unknown domain fields remain in the complete projected record node.
func mapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, errors.New("expected a YAML mapping")
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, errors.New("record mappings require string keys")
		}
		if _, exists := result[key.Value]; exists {
			return nil, fmt.Errorf("duplicate mapping key %q", key.Value)
		}
		result[key.Value] = node.Content[index+1]
	}
	return result, nil
}

// stringList validates required nonempty textual links and binding acceptance values.
//
// Example: requirements links must resolve to actual saved requirement records.
func stringList(node *yaml.Node) ([]string, error) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, errors.New("expected a nonempty string sequence")
	}
	return textSequence(node)
}

// textSequence preserves valid empty evidence and scope lists without a new schema constraint.
//
// Example: an explicitly empty scope exclusion list remains empty in the assignment.
func textSequence(node *yaml.Node) ([]string, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, errors.New("expected a string sequence")
	}
	result := make([]string, 0, len(node.Content))
	for _, entry := range node.Content {
		if entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" || strings.TrimSpace(entry.Value) == "" {
			return nil, errors.New("expected a nonempty string")
		}
		result = append(result, entry.Value)
	}
	return result, nil
}

// savedChain validates and projects complete records reachable from the assigned outcome.
//
// Example: changing one requirement still includes unchanged requirements, scopes, evidence and domain details.
func savedChain(
	contents []byte,
	request Request,
) (string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return "", err
	}
	if len(document.Content) != 1 {
		return "", errors.New("expected one ledger document")
	}
	if err := decoder.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("expected one ledger document: %v", err)
	}
	root, err := mapping(document.Content[0])
	if err != nil {
		return "", err
	}
	records, err := mapping(root["records"])
	if err != nil {
		return "", fmt.Errorf("records: %w", err)
	}
	outcome, err := mapping(records[string(request.Outcome)])
	if err != nil {
		return "", fmt.Errorf("outcome %s: %w", request.Outcome, err)
	}
	if outcome["type"] == nil || outcome["type"].Value != "outcome" {
		return "", errors.New("assigned record is not an outcome")
	}
	links, err := mapping(outcome["links"])
	if err != nil {
		return "", err
	}
	requirements, err := stringList(links["requirements"])
	if err != nil {
		return "", fmt.Errorf("outcome requirements: %w", err)
	}
	scopes, err := stringList(links["scope"])
	if err != nil {
		return "", fmt.Errorf("outcome scopes: %w", err)
	}
	for _, selected := range request.Requirements {
		if !slices.Contains(requirements, string(selected)) {
			return "", fmt.Errorf("affected requirement %s is not linked to outcome %s", selected, request.Outcome)
		}
	}
	expectedTypes := make(map[string]string, len(requirements)+len(scopes))
	for _, id := range requirements {
		expectedTypes[id] = "requirement"
	}
	for _, id := range scopes {
		if expectedTypes[id] != "" {
			return "", fmt.Errorf("record %s cannot be both scope and requirement", id)
		}
		expectedTypes[id] = "scope"
	}
	// Preserve source order in the projection while traversing every named link.
	queue := []string{string(request.Outcome)}
	visited := make(map[string]bool)
	projected := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		record := records[id]
		fields, err := mapping(record)
		if err != nil {
			return "", fmt.Errorf("linked record %s: %w", id, err)
		}
		recordType := fields["type"]
		if recordType == nil || (expectedTypes[id] != "" && recordType.Value != expectedTypes[id]) {
			return "", fmt.Errorf("linked record %s has wrong type", id)
		}
		if err := validateRecordData(RecordID(id), fields); err != nil {
			return "", err
		}
		projected.Content = append(projected.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id}, record)
		targets, err := recordTargets(RecordID(id), fields["links"])
		if err != nil {
			return "", err
		}
		queue = append(queue, targets...)
	}
	encoded, err := yaml.Marshal(projected)
	if err != nil {
		return "", err
	}
	// Decode the projection too: aliases into omitted records cannot silently lose meaning.
	var checked yaml.Node
	if err := yaml.Unmarshal(encoded, &checked); err != nil {
		return "", fmt.Errorf("unsupported projection: %w", err)
	}
	return string(encoded), nil
}

// validateRecordData checks binding fields while preserving open supporting-record payloads.
//
// Example: requirements need exact source and acceptance, while context may retain scalar data.
func validateRecordData(
	id RecordID,
	fields map[string]*yaml.Node,
) error {
	if _, err := textSequence(fields["evidence"]); err != nil {
		return fmt.Errorf("record %s evidence: %w", id, err)
	}
	if fields["data"] == nil {
		return fmt.Errorf("record %s has no data", id)
	}
	kind := fields["type"].Value
	if kind != "requirement" && kind != "scope" && kind != "outcome" {
		return nil
	}
	data, err := mapping(fields["data"])
	if err != nil {
		return fmt.Errorf("record %s data: %w", id, err)
	}
	switch kind {
	case "requirement":
		source := data["source_text"]
		if source == nil || source.Kind != yaml.ScalarNode || source.Tag != "!!str" || strings.TrimSpace(source.Value) == "" {
			return fmt.Errorf("requirement %s has no exact source text", id)
		}
		if _, err := stringList(data["acceptance"]); err != nil {
			return fmt.Errorf("requirement %s acceptance: %w", id, err)
		}
	case "scope":
		if _, err := textSequence(data["include"]); err != nil {
			return fmt.Errorf("scope %s include: %w", id, err)
		}
		if _, err := textSequence(data["exclude"]); err != nil {
			return fmt.Errorf("scope %s exclude: %w", id, err)
		}
	case "outcome":
		if data["description"] == nil || data["description"].Kind != yaml.ScalarNode || data["description"].Tag != "!!str" || strings.TrimSpace(data["description"].Value) == "" {
			return fmt.Errorf("outcome %s has no description", id)
		}
	}
	return nil
}

// recordTargets extracts every named record link in native mapping order.
//
// Example: supporting links remain traversable without replacing the full projected node.
func recordTargets(
	id RecordID,
	links *yaml.Node,
) ([]string, error) {
	if links == nil {
		return nil, nil
	}
	if _, err := mapping(links); err != nil {
		return nil, fmt.Errorf("record %s links: %w", id, err)
	}
	var result []string
	for index := 0; index < len(links.Content); index += 2 {
		name := links.Content[index].Value
		targets, err := stringList(links.Content[index+1])
		if err != nil {
			return nil, fmt.Errorf("record %s link %s: %w", id, name, err)
		}
		result = append(result, targets...)
	}
	return result, nil
}

// prepare publishes selective bytes, then builds the authoritative assignment only from readback.
//
// Example: a failed rename returns no payload; a faithful reuse leaves inode and mtime unchanged.
func prepare(
	ledger string,
	request Request,
) (_preparation Preparation, _err error) {
	info, err := os.Lstat(ledger)
	if err != nil {
		return Preparation{}, err
	}
	if !info.Mode().IsRegular() {
		return Preparation{}, errors.New("canonical snapshot must be a regular direct file")
	}
	original, err := os.ReadFile(ledger)
	if err != nil {
		return Preparation{}, err
	}
	candidate := append([]byte(nil), original...)
	for _, edit := range request.Edits {
		if edit.Old == "" || bytes.Count(candidate, []byte(edit.Old)) != 1 {
			return Preparation{}, errors.New("native edit is stale, missing, empty or ambiguous; snapshot unchanged")
		}
		candidate = bytes.Replace(candidate, []byte(edit.Old), []byte(edit.New), 1)
	}
	changed := !bytes.Equal(original, candidate)
	if _, err := savedChain(candidate, request); err != nil {
		return Preparation{}, err
	}
	if changed {
		temporary, err := os.CreateTemp(filepath.Dir(ledger), ".requirement-assignment-*")
		if err != nil {
			return Preparation{}, err
		}
		defer
		// Remove only this operation's unpublished temporary file and retain cleanup errors.
		//
		// Example: failed publication leaves no owned staging file behind.
		func() {
			if err := os.Remove(temporary.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
				_err = errors.Join(_err, err)
			}
		}()
		if _, err := temporary.Write(candidate); err != nil {
			return Preparation{}, errors.Join(err, temporary.Close())
		}
		if err := temporary.Chmod(info.Mode().Perm()); err != nil {
			return Preparation{}, errors.Join(err, temporary.Close())
		}
		if err := temporary.Sync(); err != nil {
			return Preparation{}, errors.Join(err, temporary.Close())
		}
		if err := temporary.Close(); err != nil {
			return Preparation{}, err
		}
		staged, err := os.ReadFile(temporary.Name())
		if err != nil {
			return Preparation{}, err
		}
		if _, err := savedChain(staged, request); err != nil {
			return Preparation{}, err
		}
		current, err := os.ReadFile(ledger)
		if err != nil {
			return Preparation{}, err
		}
		if !bytes.Equal(current, original) {
			return Preparation{}, errors.New("snapshot changed during preparation; retry from latest records")
		}
		if err := os.Rename(temporary.Name(), ledger); err != nil {
			return Preparation{}, err
		}
	}
	defer
	// Report saved state and existing history recovery after a postpublication failure.
	//
	// Example: failed readback requires inspection and material-change log reconciliation before reuse.
	func() {
		if _err != nil {
			_err = fmt.Errorf("snapshot saved; no assignment emitted; inspect saved state and reconcile missing material-change history with ledger-append, then retry with empty edits for faithful reuse: %w", _err)
		}
	}()
	if changed {
		directory, err := os.Open(filepath.Dir(ledger))
		if err != nil {
			return Preparation{}, err
		}
		syncErr := directory.Sync()
		if err := errors.Join(syncErr, directory.Close()); err != nil {
			return Preparation{}, err
		}
	}
	saved, err := os.ReadFile(ledger)
	if err != nil {
		return Preparation{}, err
	}
	chain, err := savedChain(saved, request)
	if err != nil {
		return Preparation{}, err
	}
	message := fmt.Sprintf("Canonical assignment requirements from %s\nAssigned outcome: %s\nAll linked requirements, scopes and supporting records (saved values):\n\n```yaml\n%s```\n\nOperational instructions (separate from canonical requirements):\n%s", ledger, request.Outcome, chain, request.Instructions)
	event := CaptureEvent{
		Event: CaptureDecision, Actor: request.Actor,
		Summary:      "Requirement capture and assignment preparation; native delivery remains pending.",
		Reason:       request.Reason,
		Requirements: request.Requirements,
		Details:      CaptureDetails{Outcome: request.Outcome, Target: request.Target, SnapshotChanged: changed, Edits: request.Edits},
	}
	if changed {
		event.Event = CaptureRequirementChange
	}
	return Preparation{Payload: Payload{Target: request.Target, Message: message}, Event: event}, nil
}

// run parses private invocation arguments and emits an internal preparation envelope on success.
//
// Example: eci-active captures this output and logs before exposing the payload.
func run(arguments []string) error {
	flags := flag.NewFlagSet("requirement-assignment", flag.ContinueOnError)
	ledger := flags.String("ledger", "", "canonical snapshot path resolved by eci-active")
	requestPath := flags.String("request", "", "one JSON request file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *ledger == "" || *requestPath == "" || flags.NArg() != 0 {
		return errors.New("require --ledger and --request")
	}
	request, err := readRequest(*requestPath)
	if err != nil {
		return err
	}
	result, err := prepare(*ledger, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

// main exposes errors only on stderr and never emits a failed assignment envelope.
//
// Example: an invalid canonical chain exits nonzero with zero stdout bytes.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "requirement-assignment:", err)
		os.Exit(1)
	}
}
