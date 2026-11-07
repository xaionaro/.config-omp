package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// reportByteOffset identifies a byte position within a report snapshot.
//
// Example: Record the start of a forecast value in UTF-8 bytes.
type reportByteOffset int

// scalarEdit describes one bounded byte replacement.
//
// Example: Record a byte range and replacement text.
type scalarEdit struct {
	Start reportByteOffset
	End   reportByteOffset
	Value string
}

// forecastFieldEdits accumulates replacements and fields to insert.
//
// Example: Collect a target replacement and a new source projection.
type forecastFieldEdits struct {
	Edits   []scalarEdit
	Missing []string
}

// refreshForecastTargets projects explicitly linked forecasts and reconciliation issues.
//
// Example: Update a linked lane forecast from its canonical record.
func refreshForecastTargets(
	dir string,
	path string,
	info os.FileInfo,
	original []byte,
	candidate []byte,
	observer refreshObserver,
	evaluation time.Time,
) error {
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	ledgerBytes, err := os.ReadFile(ledgerPath)
	if err != nil {
		return err
	}
	ledger, err := readSource[ledgerSource](ledgerPath)
	if err != nil {
		return err
	}
	var forecasts forecastSource
	if ledger.Schema != "project-understanding/v1" || ledger.Records == nil {
		return fmt.Errorf("unsupported sources")
	}
	var forecastBytes []byte
	if hasForecastReference(ledger) {
		forecastBytes, err = os.ReadFile(forecastPath)
		if err != nil {
			return err
		}
		forecasts, err = readSource[forecastSource](forecastPath)
		if err != nil {
			return err
		}
		if forecasts.Schema != "forecasts/v1" || forecasts.Records == nil {
			return fmt.Errorf("unsupported forecasts")
		}
	}
	projected, logBytes, err := projectLogStatus(dir, candidate, ledger, forecasts)
	if err != nil {
		return err
	}
	candidate = projected
	var doc yaml.Node
	if err := yaml.Unmarshal(candidate, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	report, err := parse(candidate)
	if err != nil {
		return fmt.Errorf("invalid report candidate: %w", err)
	}
	edits, err := collectForecastEdits(candidate, root, ledger, forecasts, &flowDocument{data: candidate}, reportLaneRefs(report))
	if err != nil {
		return err
	}
	sort.Slice(edits,
		// Callback orders edits from later offsets to earlier offsets.
		//
		// Example: Apply disjoint edits without shifting later offsets.
		func(
			i int,
			j int,
		) bool {
			return edits[i].Start > edits[j].Start
		})
	updated := append([]byte(nil), candidate...)
	for _, e := range edits {
		updated = append(append(append([]byte(nil), updated[:e.Start]...), []byte(e.Value)...), updated[e.End:]...)
	}
	report, err = parse(updated)
	if err != nil {
		return fmt.Errorf("refreshed report invalid: %w", err)
	}
	issues := reconcileAt(report, dir, evaluation)
	sort.Strings(issues)
	for _, issue := range issues {
		if issue == "report declares scope incomplete" {
			continue
		}
		updated, err = prepareInvalidation(updated, refreshIssuePrefix+issue)
		if err != nil {
			return err
		}
	}
	if bytes.Equal(updated, original) {
		return nil
	}
	if err := verifyRefresh(candidate, updated, ledger, forecasts); err != nil {
		return fmt.Errorf("unsafe refresh: %w", err)
	}
	updated, err = refreshOwnedForecasts(candidate, updated)
	if err != nil {
		return err
	}
	inputs := map[string][]byte{ledgerPath: ledgerBytes}
	// A nil log snapshot records absence, which must remain true until publication.
	inputs[filepath.Join(dir, "high_level_log.jsonl")] = logBytes
	if forecastBytes != nil {
		inputs[forecastPath] = forecastBytes
	}
	return publishStatus(dir, path, info, original, updated, inputs, observer)
}

// hasForecastReference reports whether work references a canonical forecast.
//
// Example: Determine whether a session needs a forecast file.
func hasForecastReference(ledger ledgerSource) bool {
	for _, record := range ledger.Records {
		if record.Type == "work" && record.Data.ForecastRef != "" {
			return true
		}
	}
	return false
}

// validateRefreshSource checks that a forecast source supports a complete projection.
//
// Example: Reject a provisional forecast without a target basis.
func validateRefreshSource(source map[string]any) error {
	for key := range source {
		if !supportedForecastField(key) {
			return fmt.Errorf("unsupported source field %s", key)
		}
	}
	for _, field := range []string{"outcome", "target", "assessment", "original_baseline", "baseline_evidence"} {
		if textField(source, field) == "" {
			return fmt.Errorf("missing %s", field)
		}
	}
	if _, err := parseUTC(textField(source, "target")); err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}
	assessment := textField(source, "assessment")
	if assessment != "complete" && assessment != "provisional" {
		return fmt.Errorf("invalid assessment")
	}
	if assessment == "provisional" && textField(source, "target_basis") == "" {
		return fmt.Errorf("missing target_basis")
	}
	if issues := validateCanonicalRecalibration(source); len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	if issues := forecastStructure(source); len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

// hasApplicableRoot finds a root forecast not represented by a report lane.
//
// Example: Skip a root whose work already has a lane.
func hasApplicableRoot(
	ledger ledgerSource,
	ref string,
	laneRefs []string,
) bool {
	for id, record := range ledger.Records {
		if record.Data.ForecastRef == sourceRef(ref) && forecastApplicable(record, reportingRoleRoot, laneReferencesWork(id, laneRefs)) {
			return true
		}
	}
	return false
}

// reportLaneRefs lists the explicit work references represented by report lanes.
//
// Example: Detect a root work record already represented by a lane.
func reportLaneRefs(r report) []string {
	var refs []string
	for _, lane := range r.Lanes {
		refs = append(refs, lane.WorkRef)
	}
	return refs
}

// mapValue looks up a key in a YAML mapping node.
//
// Example: Find the forecast reference in a YAML mapping.
func mapValue(
	n *yaml.Node,
	key string,
) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// scalarReplacement prepares a bounded edit for a proven single-line scalar.
//
// Example: Prepare a target timestamp replacement.
func scalarReplacement(
	doc *flowDocument,
	node *yaml.Node,
	value string,
) (scalarEdit, error) {
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return scalarEdit{}, fmt.Errorf("multiline forecast scalar requires manual refresh")
	}
	start, end, err := doc.scalarSpan(node)
	if err != nil {
		return scalarEdit{}, err
	}
	return scalarEdit{Start: reportByteOffset(start), End: reportByteOffset(end), Value: strconv.Quote(value)}, nil
}

// projectForecast prepares canonical edits for a linked forecast.
//
// Example: Update a linked forecast from its source record.
func projectForecast(
	original []byte,
	node *yaml.Node,
	source map[string]any,
	doc *flowDocument,
) ([]scalarEdit, error) {
	fields := map[string]any{}
	for _, field := range forecastFieldPolicies() {
		if field.Report == "" {
			continue
		}
		if value := textField(source, field.Source); value != "" {
			fields[field.Report] = value
		}
	}
	if recal, ok := source["recalibration"].(map[string]any); ok {
		if err := projectRecalibration(fields, node, recal); err != nil {
			return nil, err
		}
	}
	projection := map[string]any{}
	for _, field := range forecastFieldPolicies() {
		if !field.Nested {
			continue
		}
		if v, ok := source[field.Source]; ok {
			projection[field.Source] = v
		}
	}
	if len(projection) > 0 {
		fields["source_projection"] = projection
	}
	var fieldEdits forecastFieldEdits
	for key, value := range fields {
		if err := fieldEdits.add(original, node, key, value, doc); err != nil {
			return nil, err
		}
	}
	edits, missing := fieldEdits.Edits, fieldEdits.Missing
	if len(missing) == 0 {
		return edits, nil
	}
	sort.Strings(missing)
	if node.Style&yaml.FlowStyle == 0 {
		end, indent, err := blockMappingEnd(original, node)
		if err != nil {
			return nil, err
		}
		var addition strings.Builder
		for _, field := range missing {
			addition.WriteString(strings.Repeat(" ", indent) + field + "\n")
		}
		edits = append(edits, scalarEdit{Start: reportByteOffset(end), End: reportByteOffset(end), Value: addition.String()})
		return edits, nil
	}
	end, err := flowMappingEndCached(doc, node)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if len(node.Content) > 0 {
		prefix = ", "
	}
	edits = append(edits, scalarEdit{Start: reportByteOffset(end), End: reportByteOffset(end), Value: prefix + strings.Join(missing, ", ")})
	return edits, nil
}

// add collects the safe edit for one canonical forecast field.
//
// Example: Add a replacement when the reported target differs from its source.
func (f *forecastFieldEdits) add(
	original []byte,
	node *yaml.Node,
	key string,
	value any,
	doc *flowDocument,
) error {
	existing := mapValue(node, key)
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if existing == nil {
		f.Missing = append(f.Missing, strconv.Quote(key)+": "+string(encoded))
		return nil
	}
	if key == "source_projection" {
		next, err := sourceProjectionEdits(original, existing, value, string(encoded), doc)
		if err != nil {
			return err
		}
		f.Edits = append(f.Edits, next...)
		return nil
	}
	str, ok := value.(string)
	if !ok || existing.Kind != yaml.ScalarNode || sameValue(existing.Value, str) {
		return nil
	}
	edit, err := scalarReplacement(doc, existing, str)
	if err != nil {
		return err
	}
	f.Edits = append(f.Edits, edit)
	return nil
}

// sourceProjectionEdits prepares a replacement only when projection values differ.
//
// Example: Replace an outdated source basis without changing equal data.
func sourceProjectionEdits(
	original []byte,
	node *yaml.Node,
	value any,
	encoded string,
	doc *flowDocument,
) ([]scalarEdit, error) {
	var current any
	if err := node.Decode(&current); err != nil {
		return nil, err
	}
	if projectionEqual(current, value) {
		return nil, nil
	}
	edit, err := projectionReplacement(original, node, encoded, doc)
	if err != nil {
		return nil, err
	}
	return []scalarEdit{edit}, nil
}

// projectRecalibration prepares supported recalibration fields.
//
// Example: Copy the source evidence into a forecast projection.
func projectRecalibration(
	fields map[string]any,
	node *yaml.Node,
	recal map[string]any,
) error {
	for _, k := range forecastHistoryFields() {
		if value := textField(recal, k); value != "" {
			fields[k] = value
			continue
		}
		if mapValue(node, k) != nil {
			return fmt.Errorf("stale %s requires manual refresh", k)
		}
	}
	return nil
}

// projectionReplacement prepares a safe replacement of a source projection.
//
// Example: Replace a comment-free source projection mapping.
func projectionReplacement(
	data []byte,
	node *yaml.Node,
	value string,
	doc *flowDocument,
) (scalarEdit, error) {
	if node.Kind != yaml.MappingNode {
		return scalarEdit{}, fmt.Errorf("unsupported source_projection layout")
	}
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil {
		return scalarEdit{}, err
	}
	if node.Style&yaml.FlowStyle != 0 {
		end, err := flowMappingEndCached(doc, node)
		if err != nil {
			return scalarEdit{}, err
		}
		if yamlRangeHasComment(data, start, end+1) {
			return scalarEdit{}, fmt.Errorf("source_projection contains comments; manual refresh required")
		}
		return scalarEdit{Start: reportByteOffset(start), End: reportByteOffset(end + 1), Value: value}, nil
	}
	end, _, err := blockMappingEnd(data, node)
	if err != nil {
		return scalarEdit{}, err
	}
	if yamlRangeHasComment(data, start, end) {
		return scalarEdit{}, fmt.Errorf("source_projection contains comments; manual refresh required")
	}
	return scalarEdit{Start: reportByteOffset(start), End: reportByteOffset(end), Value: value + "\n"}, nil
}

// blockMappingEnd finds a supported block mapping boundary.
//
// Example: Locate the boundary after a block-style forecast.
func blockMappingEnd(
	data []byte,
	node *yaml.Node,
) (int, int, error) {
	lines := strings.Split(string(data), "\n")
	if node.Line < 1 || node.Line > len(lines) {
		return 0, 0, fmt.Errorf("invalid block forecast location")
	}
	indent := node.Column - 1
	offset := 0
	for i, line := range lines {
		if i <= node.Line-1 || strings.TrimSpace(line) == "" {
			offset += len(line) + 1
			continue
		}
		leading := len(line) - len(strings.TrimLeft(line, " "))
		if leading < indent {
			return offset, indent, nil
		}
		offset += len(line) + 1
	}
	return len(data), indent, nil
}

// flowMappingEnd locates a flow mapping boundary from YAML node coordinates.
//
// Example: Locate the closing brace of a flow forecast.
func flowMappingEnd(
	data []byte,
	node *yaml.Node,
) (int, error) {
	return flowMappingEndCached(&flowDocument{data: data}, node)
}

// flowMappingEndCached locates a flow mapping boundary using cached lexer pairs.
//
// Example: Reuse paired delimiters for a second forecast.
func flowMappingEndCached(
	doc *flowDocument,
	node *yaml.Node,
) (int, error) {
	start, err := yamlByteOffset(doc.data, node.Line, node.Column)
	if err != nil {
		return 0, err
	}
	return doc.end(start, token.MappingStartType)
}

// collectForecastEdits gathers edits for explicitly linked forecasts.
//
// Example: Collect linked lane and root forecast updates.
func collectForecastEdits(
	candidate []byte,
	root *yaml.Node,
	ledger ledgerSource,
	forecasts forecastSource,
	doc *flowDocument,
	laneRefs []string,
) ([]scalarEdit, error) {
	var edits []scalarEdit
	for _, kind := range []forecastGroup{forecastGroupLanes, forecastGroupRoots} {
		collection := mapValue(root, kind.String())
		if collection == nil || collection.Kind != yaml.SequenceNode {
			continue
		}
		next, err := collectForecastGroup(candidate, collection, kind, ledger, forecasts, doc, laneRefs)
		if err != nil {
			return nil, err
		}
		edits = append(edits, next...)
	}
	return edits, nil
}

// collectForecastGroup gathers edits for one forecast group.
//
// Example: Collect updates for all report lanes.
func collectForecastGroup(
	candidate []byte,
	collection *yaml.Node,
	kind forecastGroup,
	ledger ledgerSource,
	forecasts forecastSource,
	doc *flowDocument,
	laneRefs []string,
) ([]scalarEdit, error) {
	var edits []scalarEdit
	for _, item := range collection.Content {
		next, err := forecastItemEdits(candidate, item, kind, ledger, forecasts, doc, laneRefs)
		if err != nil {
			return nil, err
		}
		edits = append(edits, next...)
	}
	return edits, nil
}

// forecastItemEdits prepares edits for a single linked forecast.
//
// Example: Update the forecast linked to an active lane.
func forecastItemEdits(
	candidate []byte,
	item *yaml.Node,
	kind forecastGroup,
	ledger ledgerSource,
	forecasts forecastSource,
	doc *flowDocument,
	laneRefs []string,
) ([]scalarEdit, error) {
	forecast := item
	if kind == forecastGroupLanes {
		forecast = mapValue(item, "forecast")
		work := mapValue(item, "work_ref")
		if forecast == nil || work == nil || !validRef(work.Value, "project-understanding.yaml") {
			return nil, nil
		}
		record, ok := ledger.Records[sourceID(work.Value, "project-understanding.yaml")]
		if !ok || !forecastApplicable(record, reportingRoleLane, false) {
			return nil, nil
		}
		ref := mapValue(forecast, "forecast_ref")
		if ref == nil || sourceRef(ref.Value) != record.Data.ForecastRef {
			return nil, nil
		}
	}
	ref := mapValue(forecast, "forecast_ref")
	if ref == nil || !validRef(ref.Value, "forecasts.yaml") {
		return nil, nil
	}
	expected := forecastKindLane
	if kind == forecastGroupRoots {
		expected = forecastKindRoot
		if !hasApplicableRoot(ledger, ref.Value, laneRefs) {
			return nil, nil
		}
	}
	source, ok := forecasts.Records[sourceID(ref.Value, "forecasts.yaml")]
	if !ok || textField(source, "kind") != expected.String() || textField(source, "status") != "active" {
		return nil, fmt.Errorf("forecast %s missing or not an active %s forecast", ref.Value, expected)
	}
	if err := validateRefreshSource(source); err != nil {
		return nil, fmt.Errorf("forecast %s: %w", ref.Value, err)
	}
	edits, err := projectForecast(candidate, forecast, source, doc)
	if err != nil {
		return nil, fmt.Errorf("forecast %s: %w", ref.Value, err)
	}
	return edits, nil
}
