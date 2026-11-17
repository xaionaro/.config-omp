package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseReplayDocument creates a lossless AST for value-only replay inspection.
//
// Example: Read an exact numeric token from a JSON flow report.
func parseReplayDocument(data []byte) (yaml.Node, error) {
	if json.Valid(data) {
		var err error
		data, err = reportJSONToReplayYAML(data)
		if err != nil { return yaml.Node{}, err }
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil { return yaml.Node{}, err }
	return document, nil
}

// isolateReportOpaque separates generic values from a typed validation view.
//
// Example: Validate a report while retaining an opaque 1e400 token.
func isolateReportOpaque(data []byte) ([]byte, map[string]any, error) {
	if json.Valid(data) {
		var err error
		data, err = reportJSONToReplayYAML(data)
		if err != nil { return nil, nil, err }
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil { return nil, nil, err }
	if len(document.Content) != 1 { return nil, nil, fmt.Errorf("invalid report document") }
	values := make(map[string]any)
	if err := isolateReportFields(document.Content[0], reflect.TypeOf(report{}), "", values); err != nil { return nil, nil, err }
	view, err := yaml.Marshal(&document)
	return view, values, err
}

// isolateReportFields follows schema fields and retains generic subtrees losslessly.
//
// Example: Preserve source_projection inside any supported forecast record.
func isolateReportFields(node *yaml.Node, typ reflect.Type, path string, values map[string]any) error {
	for typ.Kind() == reflect.Pointer { typ = typ.Elem() }
	if node.Tag == "!!null" { return nil }
	switch typ.Kind() {
	case reflect.Map:
		if typ.Key().Kind() != reflect.String || typ.Elem().Kind() != reflect.Interface { return nil }
		if node.Kind != yaml.MappingNode { return fmt.Errorf("%s must be an object or null", path) }
		value, err := replayJSONValue(node); if err != nil { return err }; values[path] = value
		*node = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Interface {
			if node.Kind != yaml.SequenceNode { return fmt.Errorf("%s must be an array or null", path) }
			value, err := replayJSONValue(node); if err != nil { return err }; values[path] = value
			*node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}; return nil
		}
		if node.Kind != yaml.SequenceNode { return nil }
		for i, child := range node.Content { if err := isolateReportFields(child, typ.Elem(), fmt.Sprintf("%s/%d", path, i), values); err != nil { return err } }
	case reflect.Struct:
		if node.Kind != yaml.MappingNode { return nil }
		for i := range typ.NumField() {
			field := typ.Field(i); name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" { continue }
			if child := mapValue(node, name); child != nil { if err := isolateReportFields(child, field.Type, path+"/"+name, values); err != nil { return err } }
		}
	}
	return nil
}

// restoreReportOpaque returns retained generic values to a validated report view.
//
// Example: Render opaque details without rounding their numeric tokens.
func restoreReportOpaque(value *report, values map[string]any) {
	restoreReportFields(reflect.ValueOf(value), reflect.TypeOf(value), "", values)
}

// restoreReportFields restores generic subtrees at schema-derived locations.
//
// Example: Restore a lane's nested source projection after validation.
func restoreReportFields(value reflect.Value, typ reflect.Type, path string, values map[string]any) {
	for typ.Kind() == reflect.Pointer { if value.IsNil() { return }; value = value.Elem(); typ = typ.Elem() }
	if saved, ok := values[path]; ok { value.Set(reflect.ValueOf(saved)); return }
	switch typ.Kind() {
	case reflect.Slice:
		for i := range value.Len() { restoreReportFields(value.Index(i), typ.Elem(), fmt.Sprintf("%s/%d", path, i), values) }
	case reflect.Struct:
		for i := range typ.NumField() {
			field := typ.Field(i); name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name != "" && name != "-" { restoreReportFields(value.Field(i), field.Type, path+"/"+name, values) }
		}
	}
}

// encodeFlowValue encodes a JSON-compatible value as a tag-preserving flow node.
//
// Example: Insert a forecast projection containing an exact numeric token.
func encodeFlowValue(value any) ([]byte, error) {
	node, err := replayYAMLNode(value); if err != nil { return nil, err }
	setFlowStyle(node)
	encoded, err := yaml.Marshal(node); if err != nil { return nil, err }
	return []byte(strings.TrimSpace(string(encoded))), nil
}

// setFlowStyle selects flow layout recursively for inserted transient nodes.
//
// Example: Insert a nested mapping into an existing flow sequence.
func setFlowStyle(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode { node.Style |= yaml.FlowStyle }
	for _, child := range node.Content { setFlowStyle(child) }
}

// opaqueReportMap reads a complete transient report without numeric conversion.
//
// Example: Compare two reports containing 1e400 as an opaque value.
func opaqueReportMap(data []byte) (map[string]any, error) {
	if json.Valid(data) {
		value, err := decodeReportValue(data)
		if err != nil { return nil, err }
		result, ok := value.(map[string]any)
		if !ok { return nil, fmt.Errorf("report must be an object") }
		return result, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil { return nil, err }
	if len(document.Content) != 1 { return nil, fmt.Errorf("invalid report document") }
	if containsLegacyYAML(document.Content[0]) {
		var legacy map[string]any
		if err := yaml.Unmarshal(data, &legacy); err != nil { return nil, err }
		encoded, err := json.Marshal(legacy)
		if err != nil { return nil, err }
		value, err := decodeReportValue(encoded)
		if err != nil { return nil, err }
		result, ok := value.(map[string]any)
		if !ok { return nil, fmt.Errorf("report must be an object") }
		return result, nil
	}
	value, err := replayJSONValue(document.Content[0]); if err != nil { return nil, err }
	result, ok := value.(map[string]any); if !ok { return nil, fmt.Errorf("report must be an object") }
	return result, nil
}

// containsLegacyYAML identifies syntax not representable in database JSON.
//
// Example: Decode an unquoted legacy timestamp using its original YAML semantics.
func containsLegacyYAML(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Tag == "!!timestamp" || node.Tag == "!!merge" { return true }
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!int" || node.Tag == "!!float") && !json.Valid([]byte(node.Value)) { return true }
	for _, child := range node.Content { if containsLegacyYAML(child) { return true } }
	return false
}

// lexicalScalar reads a selector's original spelling without numeric conversion.
//
// Example: Numeric 1.10 selects the identifier "1.10".
func lexicalScalar(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode { return "", fmt.Errorf("identity must be a scalar") }
	switch node.Tag {
	case "!!str", "!!int", "!!float", "!!bool": return node.Value, nil
	default: return "", fmt.Errorf("invalid identity type")
	}
}

// UnmarshalYAML preserves a task identifier's lexical spelling.
//
// Example: A numeric 1.10 remains distinct from 1.1.
func (id *taskID) UnmarshalYAML(node *yaml.Node) error {
	value, err := lexicalScalar(node); if err != nil { return err }; *id = taskID(value); return nil
}

// UnmarshalYAML preserves a priority selector's lexical spelling.
//
// Example: A numeric priority selector is not rounded by YAML.
func (priority *priorityLabel) UnmarshalYAML(node *yaml.Node) error {
	value, err := lexicalScalar(node); if err != nil { return err }; *priority = priorityLabel(value); return nil
}

// validateDBReportScalars rejects scalar coercions in the typed report schema.
//
// Example: An owner must be a string even when YAML could print a number.
func validateDBReportScalars(data []byte) error {
	if json.Valid(data) {
		var err error
		data, err = reportJSONToReplayYAML(data)
		if err != nil { return err }
	}
	normalized, err := normalizeNarratives(data)
	if err != nil { return err }
	data = normalized
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil { return err }
	if len(document.Content) != 1 { return fmt.Errorf("invalid report document") }
	return validateReportScalarFields(document.Content[0], reflect.TypeOf(report{}), "")
}

// validateReportScalarFields walks typed fields without coercing source scalars.
//
// Example: A task ID accepts numeric spelling, but its title must be a string.
func validateReportScalarFields(node *yaml.Node, typ reflect.Type, path string) error {
	for typ.Kind() == reflect.Pointer { typ = typ.Elem() }
	if node.Tag == "!!null" { return nil }
	if typ == reflect.TypeOf(taskID("")) || typ == reflect.TypeOf(priorityLabel("")) {
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!int" && node.Tag != "!!float") {
			return fmt.Errorf("%s must be a string or number", path)
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.String:
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!timestamp") { return fmt.Errorf("%s must be a string", path) }
	case reflect.Struct:
		if node.Kind != yaml.MappingNode { return nil }
		for i := range typ.NumField() {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" { continue }
			if child := mapValue(node, name); child != nil {
				if err := validateReportScalarFields(child, field.Type, path+"/"+name); err != nil { return err }
			}
		}
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Interface || node.Kind != yaml.SequenceNode { return nil }
		for i, child := range node.Content {
			if err := validateReportScalarFields(child, typ.Elem(), fmt.Sprintf("%s/%d", path, i)); err != nil { return err }
		}
	case reflect.Map:
		if typ.Elem().Kind() == reflect.Interface || node.Kind != yaml.MappingNode { return nil }
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := validateReportScalarFields(node.Content[i+1], typ.Elem(), path+"/"+node.Content[i].Value); err != nil { return err }
		}
	}
	return nil
}
