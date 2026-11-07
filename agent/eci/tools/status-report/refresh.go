package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

const (
	// refreshDiagnostic names the required human scope review after a requirement change.
	//
	// Example: Mark scope incomplete after a requirement change.
	refreshDiagnostic = "Requirement change requires scope review and status report refresh."

	// refreshIssuePrefix identifies diagnostics produced by source reconciliation.
	//
	// Example: Mark an omitted work record as incomplete.
	refreshIssuePrefix = "Status refresh incomplete: "
)

// errRefreshRetry identifies a changed input that requires a fresh retry.
//
// Example: Retry after another writer replaces the report.
var errRefreshRetry = errors.New("retry needed")

// refreshObserver observes the moment before publication.
//
// Example: A test mutates a source before the final change check.
type refreshObserver interface {
	// BeforePublish observes the publication boundary.
	//
	// Example: Replace a report before its identity is checked.
	BeforePublish()
}

// refreshLinkedStatus reads the current sources and prepares a bounded status refresh.
//
// Example: Refresh a session after a forecast change.
func refreshLinkedStatus(
	dir string,
	event string,
	observer refreshObserver,
) error {
	return refreshLinkedStatusAt(dir, event, observer, time.Now().UTC())
}

// refreshLinkedStatusAt refreshes using one supplied evaluation instant.
//
// Example: Evaluate all forecasts at the same captured time.
func refreshLinkedStatusAt(
	dir string,
	event string,
	observer refreshObserver,
	evaluation time.Time,
) error {
	path := filepath.Join(dir, "latest-status-report.yaml")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("report is not a regular file")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := parse(original); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	candidate := original
	if event == "requirement_change" {
		candidate, err = prepareInvalidation(original, refreshDiagnostic)
		if err != nil {
			return err
		}
	}
	err = refreshForecastTargets(dir, path, info, original, candidate, observer, evaluation)
	if err == nil || errors.Is(err, errRefreshRetry) {
		return err
	}
	diagnostics := []string{refreshIssuePrefix + err.Error()}
	if event == "requirement_change" {
		diagnostics = append([]string{refreshDiagnostic}, diagnostics...)
	}
	if markErr := invalidateStatus(dir, observer, diagnostics...); markErr != nil {
		return fmt.Errorf("%w; could not persist diagnostic: %w", err, markErr)
	}
	return err
}

// runRefresh updates the current report under the caller's ECI mutation lock.
//
// Example: Run a refresh using a session directory and event name.
func runRefresh(
	args []string,
	errOut io.Writer,
) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		if err := writeRefreshDiagnostic(errOut, "usage: status-report refresh SESSION_DIR EVENT\n"); err != nil {
			return 1
		}
		return 2
	}
	if err := refreshStatus(args[0], args[1]); err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report refresh:", err)); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}

// writeRefreshDiagnostic writes a complete diagnostic or returns the write failure.
//
// Example: Detect a short write to a closed error stream.
func writeRefreshDiagnostic(
	out io.Writer,
	message string,
) error {
	written, err := io.WriteString(out, message)
	if err != nil {
		return err
	}
	if written != len(message) {
		return io.ErrShortWrite
	}
	return nil
}

// refreshStatus refreshes source projections and retains scope review requirements.
//
// Example: Refresh a report after a requirement change.
func refreshStatus(
	dir string,
	event string,
) error {
	return refreshLinkedStatus(dir, event, nil)
}

// refreshStatusAt refreshes a session at an explicit evaluation instant.
//
// Example: Evaluate a report against a fixed time in a regression test.
func refreshStatusAt(
	dir string,
	event string,
	evaluation time.Time,
) error {
	return refreshLinkedStatusAt(dir, event, nil, evaluation)
}

// invalidateStatus persists scope and diagnostic updates after a refresh failure.
//
// Example: Persist a diagnostic when a source cannot be read.
func invalidateStatus(
	dir string,
	observer refreshObserver,
	diagnostics ...string,
) error {
	path := filepath.Join(dir, "latest-status-report.yaml")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("report is not a regular file")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := parse(original); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	updated := original
	for _, diagnostic := range diagnostics {
		updated, err = prepareInvalidation(updated, diagnostic)
		if err != nil {
			return err
		}
	}
	if bytes.Equal(updated, original) {
		return nil
	}
	if _, err := parse(updated); err != nil {
		return fmt.Errorf("refreshed report invalid: %w", err)
	}
	if err := verifyRefresh(original, updated, ledgerSource{}, forecastSource{}); err != nil {
		return fmt.Errorf("unsafe refresh: %w", err)
	}
	return publishStatus(dir, path, info, original, updated, nil, observer)
}

// prepareInvalidation prepares a selective scope and diagnostic update.
//
// Example: Set scope incomplete and add the scope review diagnostic.
func prepareInvalidation(
	original []byte,
	diagnostic string,
) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	var scope, blockers *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "scope_complete":
			scope = root.Content[i+1]
		case "blockers_risks":
			blockers = root.Content[i+1]
		}
	}
	if scope == nil || blockers == nil || scope.Kind != yaml.ScalarNode || blockers.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("unsupported report fields")
	}
	lines := strings.Split(string(original), "\n")
	// Only replace the scalar token; retain surrounding comments and layout.
	line := scope.Line - 1
	if line < 0 || line >= len(lines) {
		return nil, fmt.Errorf("unsupported scope layout")
	}
	start, err := yamlByteOffset([]byte(lines[line]), 1, scope.Column)
	if err != nil {
		return nil, err
	}
	if line < 0 || line >= len(lines) || start < 0 || start+len(scope.Value) > len(lines[line]) || lines[line][start:start+len(scope.Value)] != scope.Value {
		return nil, fmt.Errorf("unsupported scope layout")
	}
	lines[line] = lines[line][:start] + "false" + lines[line][start+len(scope.Value):]
	if blockers.Line == scope.Line && blockers.Column > scope.Column {
		blockers.Column += len("false") - len(scope.Value)
	}
	lines, err = appendDiagnostic(lines, blockers, diagnostic, nextMappingFieldLine(root, "blockers_risks"))
	if err != nil {
		return nil, err
	}
	updated := []byte(strings.Join(lines, "\n"))
	return updated, nil
}

// nextMappingFieldLine identifies the field following a named YAML mapping key.
//
// Example: Find the line where verification follows blockers.
func nextMappingFieldLine(
	root *yaml.Node,
	key string,
) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		if i+2 < len(root.Content) {
			return root.Content[i+2].Line
		}
		return 0
	}
	return 0
}

// appendDiagnostic adds a missing diagnostic without changing existing entries.
//
// Example: Add a new risk to a flow-style blocker list.
func appendDiagnostic(
	lines []string,
	blockers *yaml.Node,
	diagnostic string,
	nextFieldLine int,
) ([]string, error) {
	for _, n := range blockers.Content {
		if n.Value == diagnostic {
			return lines, nil
		}
	}
	if blockers.Style&yaml.FlowStyle == 0 {
		return appendBlockDiagnostic(lines, blockers, diagnostic, nextFieldLine)
	}
	data := []byte(strings.Join(lines, "\n"))
	start, err := yamlByteOffset(data, blockers.Line, blockers.Column)
	if err != nil {
		return nil, err
	}
	if start < 0 || start >= len(data) {
		return nil, fmt.Errorf("unsupported blockers layout")
	}
	end, err := flowEnd(data, start, token.SequenceStartType)
	if err != nil {
		return nil, err
	}
	sep := ""
	if len(blockers.Content) > 0 {
		sep = ", "
	}
	updated := string(data[:end]) + sep + strconv.Quote(diagnostic) + string(data[end:])
	return strings.Split(updated, "\n"), nil
}

// appendBlockDiagnostic adds a diagnostic to a supported block-style list.
//
// Example: Append a diagnostic to an indented blocker list.
func appendBlockDiagnostic(
	lines []string,
	blockers *yaml.Node,
	diagnostic string,
	nextFieldLine int,
) ([]string, error) {
	insert := len(lines)
	if nextFieldLine > 0 {
		insert = nextFieldLine - 1
	}
	indent := "  "
	if len(blockers.Content) > 0 {
		last := blockers.Content[len(blockers.Content)-1]
		if last.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.Contains(last.Value, "\n") {
			return nil, fmt.Errorf("multiline blocker requires manual status refresh")
		}
		if last.Line < 1 || last.Line > len(lines) || insert < last.Line || insert > len(lines) {
			return nil, fmt.Errorf("unsupported blockers layout")
		}
		dash := strings.Index(lines[last.Line-1], "- ")
		if dash < 0 || strings.TrimSpace(lines[last.Line-1][:dash]) != "" {
			return nil, fmt.Errorf("unsupported blockers layout")
		}
		indent = lines[last.Line-1][:dash]
		insert = trimFollowingComments(lines, insert, last.Line)
	}
	if insert < 0 || insert > len(lines) {
		return nil, fmt.Errorf("unsupported blockers layout")
	}
	ending := ""
	if len(lines) > 0 && strings.HasSuffix(lines[0], "\r") {
		ending = "\r"
	}
	return append(lines[:insert], append([]string{indent + "- " + strconv.Quote(diagnostic) + ending}, lines[insert:]...)...), nil
}

// trimFollowingComments keeps comments for the next field after a new list item.
//
// Example: Insert a diagnostic before a comment introducing verification.
func trimFollowingComments(
	lines []string,
	insert int,
	minimum int,
) int {
	for insert > minimum {
		line := strings.TrimSpace(lines[insert-1])
		if line != "" && !strings.HasPrefix(line, "#") {
			break
		}
		insert--
	}
	return insert
}

// publishStatus publishes a checked candidate using same-directory replacement.
//
// Example: Replace the report after checking its source files.
func publishStatus(
	dir string,
	path string,
	info os.FileInfo,
	original []byte,
	updated []byte,
	inputs map[string][]byte,
	observer refreshObserver,
) (_err error) {
	temp, err := os.CreateTemp(dir, ".status-refresh-*")
	if err != nil {
		return err
	}
	defer
	// Callback removes the temporary file and preserves unexpected cleanup failures.
	//
	// Example: A failed rename still removes the unpublished candidate.
	func() {
		_err = errors.Join(_err, removeUnpublished(temp.Name()))
	}()
	if err = temp.Chmod(info.Mode().Perm()); err == nil {
		_, err = temp.Write(updated)
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		return err
	}
	if observer != nil {
		observer.BeforePublish()
	}
	for p, before := range inputs {
		if before == nil {
			// Match the read's resolved-target presence, including dangling links.
			_, err := os.Stat(p)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("input changed during refresh: %w: %w", errRefreshRetry, err)
			}
			return fmt.Errorf("input changed during refresh: %w", errRefreshRetry)
		}
		current, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("input changed during refresh: %w: %w", errRefreshRetry, err)
		}
		if !bytes.Equal(current, before) {
			return fmt.Errorf("input changed during refresh: %w", errRefreshRetry)
		}
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("report changed during refresh: %w: %w", errRefreshRetry, err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return fmt.Errorf("report changed during refresh: %w", errRefreshRetry)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("report changed during refresh: %w: %w", errRefreshRetry, err)
	}
	if !bytes.Equal(contents, original) {
		return fmt.Errorf("report changed during refresh: %w", errRefreshRetry)
	}
	return os.Rename(temp.Name(), path)
}

// removeUnpublished removes a temporary candidate unless it has already been renamed.
//
// Example: Ignore a missing temporary path after successful publication.
func removeUnpublished(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
