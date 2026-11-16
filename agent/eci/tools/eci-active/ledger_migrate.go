package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"gopkg.in/yaml.v3"
)

// migrationDocument describes the rows imported from one legacy snapshot.
//
// Example: a forecast snapshot has records but no sections.
type migrationDocument struct {
	Schema string
	Sections map[string]json.RawMessage
	Records map[string]json.RawMessage
}

// parseMigrationDocument accepts one legacy document without YAML aliases.
//
// Example: timestamp-like scalar values remain strings in database JSON.
func parseMigrationDocument(data []byte) (migrationDocument, error) {
	var result migrationDocument
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := dec.Decode(&document); err != nil {
		return result, fmt.Errorf("decode YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return result, fmt.Errorf("multiple YAML documents")
		}
		return result, fmt.Errorf("decode YAML tail: %w", err)
	}
	encoded, err := migrationNodeJSON(&document)
	if err != nil {
		return result, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil || fields == nil {
		return result, fmt.Errorf("snapshot envelope must be an object")
	}
	for key := range fields {
		if key != "schema" && key != "sections" && key != "records" {
			return result, fmt.Errorf("unsupported snapshot field %q", key)
		}
	}
	var envelope struct {
		Schema string `json:"schema"`
		Sections map[string]json.RawMessage `json:"sections"`
		Records map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return result, fmt.Errorf("decode snapshot envelope: %w", err)
	}
	if envelope.Records == nil {
		return result, fmt.Errorf("snapshot records must be an object")
	}
	if envelope.Schema == "project-understanding/v1" && envelope.Sections == nil {
		return result, fmt.Errorf("ledger sections must be an object")
	}
	if envelope.Schema == "forecasts/v1" {
		if _, ok := fields["sections"]; ok {
			return result, fmt.Errorf("forecast sections are unsupported")
		}
	}
	result.Schema, result.Sections, result.Records = envelope.Schema, envelope.Sections, envelope.Records
	return result, nil
}

// migrationNodeJSON preserves YAML mapping order while rejecting aliases and merges.
//
// Example: a timestamp-like scalar becomes a JSON string rather than a date object.
func migrationNodeJSON(n *yaml.Node) ([]byte, error) {
	if n.Anchor != "" || n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return nil, fmt.Errorf("YAML anchors, aliases, and merges are unsupported")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("empty YAML document")
		}
		return migrationNodeJSON(n.Content[0])
	case yaml.MappingNode:
		var out bytes.Buffer
		out.WriteByte('{')
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" {
				return nil, fmt.Errorf("mapping keys must be strings")
			}
			if seen[key.Value] {
				return nil, fmt.Errorf("duplicate YAML key %q", key.Value)
			}
			seen[key.Value] = true
			if i > 0 {
				out.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key.Value)
			if err != nil {
				return nil, err
			}
			out.Write(encodedKey)
			out.WriteByte(':')
			value, err := migrationNodeJSON(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out.Write(value)
		}
		out.WriteByte('}')
		return out.Bytes(), nil
	case yaml.SequenceNode:
		var out bytes.Buffer
		out.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				out.WriteByte(',')
			}
			value, err := migrationNodeJSON(item)
			if err != nil {
				return nil, err
			}
			out.Write(value)
		}
		out.WriteByte(']')
		return out.Bytes(), nil
	case yaml.ScalarNode:
		numeric := n.Tag == "!!int" || n.Tag == "!!float"
		if n.Tag == "!!str" && n.Style == 0 && len(n.Value) > 0 && (n.Value[0] == '-' || n.Value[0] >= '0' && n.Value[0] <= '9') && json.Valid([]byte(n.Value)) {
			numeric = true
		}
		if numeric {
			if !json.Valid([]byte(n.Value)) {
				return nil, fmt.Errorf("unsupported YAML numeric spelling %q", n.Value)
			}
			return []byte(n.Value), nil
		}
		if n.Tag == "!!str" || n.Tag == "!!timestamp" {
			return json.Marshal(n.Value)
		}
		if n.Tag != "!!bool" && n.Tag != "!!null" {
			return nil, fmt.Errorf("unsupported YAML scalar tag %q", n.Tag)
		}
		var value any
		if err := n.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode YAML scalar: %w", err)
		}
		result, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode YAML scalar: %w", err)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
	}
}

// maintenanceSessionDir validates the operator and exact account-owned target.
//
// Example: migration and recovery share the same authorization boundary.
func maintenanceSessionDir(
	sid string,
	verb string,
) (string, error) {
	if !validSessionID(sid) {
		return "", fmt.Errorf("invalid target session")
	}
	account := accountHome()
	if account == "" { return "", fmt.Errorf("cannot resolve account home") }
	account, err := filepath.EvalSymlinks(account)
	if err != nil { return "", fmt.Errorf("resolve account home: %w", err) }
	home, err := filepath.EvalSymlinks(homeDir())
	if err != nil { return "", fmt.Errorf("resolve configured home: %w", err) }
	if home != account { return "", fmt.Errorf("maintenance requires the account home") }
	operator, err := currentSession()
	if err != nil { return "", err }
	if err := verifyCallerSession(verb, operator, contextSessionsRoot()); err != nil { return "", err }
	root := filepath.Join(account, ".agent", "context", "sessions")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root { return "", fmt.Errorf("context session root is not canonical") }
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 { return "", fmt.Errorf("unsafe context session root") }
	dir := filepath.Join(root, sid)
	info, err = os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 { return "", fmt.Errorf("unsafe or missing target session") }
	return dir, nil
}

// validateImportedReport delegates report semantics and ownership conversion to status-report.
//
// Example: malformed imported ownership is rejected before candidate creation.
func validateImportedReport(
	ctx context.Context,
	report json.RawMessage,
) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(eciBaseDir(), "bin", "status-report"), "import-report")
	cmd.Stdin = bytes.NewReader(report)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("status-report import: %w: %s", err, stderr.String())
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(result, &object); err != nil || object == nil {
		return nil, fmt.Errorf("status-report returned an invalid report")
	}
	return result, nil
}
