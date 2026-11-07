package main

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// syncIdentityStyle describes a task ID's original YAML representation.
//
// Example: An integer scalar differs from a quoted string with the same text.
type syncIdentityStyle struct {
	Kind   yaml.Kind
	Tag    string
	Value  string
	Style  yaml.Style
	Alias  string
	Anchor string
}

// verifyTaskIdentityStyles protects the original representation of existing IDs.
//
// Example: Updating another field cannot change a numeric task ID into a string.
func verifyTaskIdentityStyles(
	original []byte,
	candidate []byte,
	effects []syncEffect,
) error {
	before, err := taskIdentityNodes(original)
	if err != nil {
		return err
	}
	after, err := taskIdentityNodes(candidate)
	if err != nil {
		return err
	}
	authorized := syncIdentityStyleEffects(effects)
	for id, previous := range before {
		current, exists := after[id]
		if !exists {
			continue
		}
		if err := verifyIdentityOccurrences(id, previous, current, authorized); err != nil {
			return err
		}
	}
	return nil
}

// syncIdentityStyleEffects identifies explicitly supplied mutable task fields.
//
// Example: A task priority update may replace the prior scalar representation.
func syncIdentityStyleEffects(effects []syncEffect) map[string]bool {
	allowed := map[string]bool{}
	for _, effect := range effects {
		if effect.Operation == syncEffectSet && effect.Target == syncTargetTask && (effect.Field == "priority" || effect.Field == "parent_id") {
			allowed["tasks\x00"+effect.ID+"\x00"+effect.Field] = true
		}
	}
	return allowed
}

// verifyIdentityOccurrences compares every retained record with the same key.
//
// Example: Two reviews for one task retain both original identity styles.
func verifyIdentityOccurrences(
	id string,
	before []map[string]syncIdentityStyle,
	after []map[string]syncIdentityStyle,
	authorized map[string]bool,
) error {
	for i, old := range before {
		if i >= len(after) {
			break
		}
		if err := verifyTaskIdentityFields(id, old, after[i], authorized); err != nil {
			return err
		}
	}
	return nil
}

// verifyTaskIdentityFields compares the original YAML styles for one task.
//
// Example: A parent ID cannot be rewritten as a differently typed scalar.
func verifyTaskIdentityFields(
	id string,
	before map[string]syncIdentityStyle,
	after map[string]syncIdentityStyle,
	authorized map[string]bool,
) error {
	for field, previous := range before {
		next, exists := after[field]
		if exists && previous.Value == next.Value && previous != next && !authorized[id+"\x00"+field] {
			return fmt.Errorf("task identity %q field %s representation changed", id, field)
		}
	}
	return nil
}

// taskIdentityNodes captures lexical identity types for existing task records.
//
// Example: A YAML integer ID and a quoted string ID remain distinguishable.
func taskIdentityNodes(data []byte) (map[string][]map[string]syncIdentityStyle, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	result := map[string][]map[string]syncIdentityStyle{}
	if len(doc.Content) == 0 {
		return result, nil
	}
	for _, section := range []string{"tasks", "lanes", "reviews", "priority_estimates"} {
		collectSyncIdentityNodes(result, doc.Content[0], section)
	}
	return result, nil
}

// collectSyncIdentityNodes records task-keyed identity styles in one section.
//
// Example: Two lanes sharing a task stay distinct by work and environment.
func collectSyncIdentityNodes(
	result map[string][]map[string]syncIdentityStyle,
	root *yaml.Node,
	section string,
) {
	list := mapValue(root, section)
	if list != nil && list.Kind == yaml.AliasNode {
		list = list.Alias
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range list.Content {
		if item.Kind == yaml.AliasNode {
			item = item.Alias
		}
		field := "task_id"
		if section == "tasks" {
			field = "id"
		}
		if section == "priority_estimates" {
			field = "priority"
		}
		id := mapValue(item, field)
		idStyle, ok := syncIdentityNodeStyle(id)
		if !ok {
			continue
		}
		fields := map[string]syncIdentityStyle{field: idStyle}
		if parent, ok := syncIdentityNodeStyle(mapValue(item, "parent_id")); section == "tasks" && ok {
			fields["parent_id"] = parent
		}
		if priority, ok := syncIdentityNodeStyle(mapValue(item, "priority")); section == "tasks" && ok {
			fields["priority"] = priority
		}
		key := section + "\x00" + idStyle.Value
		if section == "lanes" {
			key += "\x00" + syncNodeText(mapValue(item, "work_ref")) + "\x00" + syncNodeText(mapValue(item, "required_environment"))
		}
		result[key] = append(result[key], fields)
	}
}

// syncIdentityNodeStyle captures both the decoded ID and its source representation.
//
// Example: A parent alias retains its alias name and decoded task identifier.
func syncIdentityNodeStyle(node *yaml.Node) (syncIdentityStyle, bool) {
	if node == nil || (node.Kind != yaml.ScalarNode && node.Kind != yaml.AliasNode) {
		return syncIdentityStyle{}, false
	}
	var value string
	if node.Decode(&value) != nil {
		return syncIdentityStyle{}, false
	}
	style := syncIdentityStyle{Kind: node.Kind, Tag: node.Tag, Value: value, Style: node.Style, Anchor: node.Anchor}
	if node.Kind == yaml.AliasNode {
		style.Alias = node.Value
	}
	return style, true
}

// verifyTopPriorityStyles protects retained IDs unless the full list was supplied.
//
// Example: An unrelated task update cannot rewrite a numeric priority as text.
func verifyTopPriorityStyles(
	original []byte,
	candidate []byte,
	effects []syncEffect,
) error {
	for _, field := range []string{"top_priorities", "priority_order"} {
		if err := verifyStringListStyles(original, candidate, effects, field); err != nil {
			return err
		}
	}
	return nil
}

// verifyStringListStyles protects untouched lexical labels in one report list.
//
// Example: A numeric priority order label cannot be silently rewritten as text.
func verifyStringListStyles(
	original []byte,
	candidate []byte,
	effects []syncEffect,
	field string,
) error {
	for _, effect := range effects {
		if effect.Operation == syncEffectSet && effect.Target == syncTargetReport && effect.ID == "current" && effect.Field == field {
			return nil
		}
	}
	before, err := stringListIdentityStyles(original, field)
	if err != nil {
		return err
	}
	after, err := stringListIdentityStyles(candidate, field)
	if err != nil {
		return err
	}
	for id, previous := range before {
		if err := verifyIdentityOccurrences(field+"\x00"+id, previous, after[id], nil); err != nil {
			return err
		}
	}
	return nil
}

// stringListIdentityStyles records each supplied label's lexical representation.
//
// Example: An alias and an integer with the same ID remain distinguishable.
func stringListIdentityStyles(
	data []byte,
	field string,
) (map[string][]map[string]syncIdentityStyle, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	result := map[string][]map[string]syncIdentityStyle{}
	if len(doc.Content) == 0 {
		return result, nil
	}
	list := mapValue(doc.Content[0], field)
	if list != nil && list.Kind == yaml.AliasNode {
		list = list.Alias
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return result, nil
	}
	for _, item := range list.Content {
		style, ok := syncIdentityNodeStyle(item)
		if !ok {
			continue
		}
		result[style.Value] = append(result[style.Value], map[string]syncIdentityStyle{"value": style})
	}
	return result, nil
}

// syncNodeText reads a scalar selector without inventing a missing value.
//
// Example: A lane key includes its explicit required environment.
func syncNodeText(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// syncVerificationIDs preserves lexical task identifiers when verifying effects.
//
// Example: Unquoted 1.10 remains distinct from unquoted 1.1.
type syncVerificationIDs struct {
	Tasks []struct {
		ID       string `yaml:"id"`
		ParentID string `yaml:"parent_id"`
		Priority string `yaml:"priority"`
	} `yaml:"tasks"`
	Lanes []struct {
		TaskID string `yaml:"task_id"`
	} `yaml:"lanes"`
	Reviews []struct {
		TaskID string `yaml:"task_id"`
	} `yaml:"reviews"`
	PriorityEstimates []struct {
		Priority string `yaml:"priority"`
	} `yaml:"priority_estimates"`
	TopPriorities []string `yaml:"top_priorities"`
	PriorityOrder []string `yaml:"priority_order"`
}

// decodeSyncVerificationReport keeps task IDs as the report schema interprets them.
//
// Example: YAML numeric task identifiers are compared by their original scalar text.
func decodeSyncVerificationReport(data []byte) (map[string]any, error) {
	var report map[string]any
	if err := yaml.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	var ids syncVerificationIDs
	if err := yaml.Unmarshal(data, &ids); err != nil {
		return nil, err
	}
	taskIDs := make([]string, len(ids.Tasks))
	parentIDs := make([]string, len(ids.Tasks))
	taskPriorities := make([]string, len(ids.Tasks))
	for i, item := range ids.Tasks {
		taskIDs[i] = item.ID
		parentIDs[i] = item.ParentID
		taskPriorities[i] = item.Priority
	}
	if err := normalizeSyncVerificationIDs(report, "tasks", "id", taskIDs); err != nil {
		return nil, err
	}
	if err := normalizeSyncVerificationIDs(report, "tasks", "parent_id", parentIDs); err != nil {
		return nil, err
	}
	if err := normalizeSyncVerificationIDs(report, "tasks", "priority", taskPriorities); err != nil {
		return nil, err
	}
	laneIDs := make([]string, len(ids.Lanes))
	for i, item := range ids.Lanes {
		laneIDs[i] = item.TaskID
	}
	if err := normalizeSyncVerificationIDs(report, "lanes", "task_id", laneIDs); err != nil {
		return nil, err
	}
	reviewIDs := make([]string, len(ids.Reviews))
	for i, item := range ids.Reviews {
		reviewIDs[i] = item.TaskID
	}
	if err := normalizeSyncVerificationIDs(report, "reviews", "task_id", reviewIDs); err != nil {
		return nil, err
	}
	priorities := make([]string, len(ids.PriorityEstimates))
	for i, item := range ids.PriorityEstimates {
		priorities[i] = item.Priority
	}
	if err := normalizeSyncVerificationIDs(report, "priority_estimates", "priority", priorities); err != nil {
		return nil, err
	}
	if err := normalizeSyncStringList(report, "top_priorities", ids.TopPriorities); err != nil {
		return nil, err
	}
	if err := normalizeSyncStringList(report, "priority_order", ids.PriorityOrder); err != nil {
		return nil, err
	}
	return report, nil
}

// normalizeSyncStringList keeps each supplied label's lexical identifier.
//
// Example: A numeric-looking 1.10 remains distinct from 1.1.
func normalizeSyncStringList(
	report map[string]any,
	field string,
	ids []string,
) error {
	items, ok := report[field].([]any)
	if !ok {
		return nil
	}
	if len(items) != len(ids) {
		return fmt.Errorf("%s identity count mismatch", field)
	}
	for i := range items {
		if items[i] != nil {
			items[i] = ids[i]
		}
	}
	return nil
}

// normalizeSyncVerificationIDs applies schema-decoded identifiers to one record list.
//
// Example: Numeric YAML lane task IDs become their exact lexical strings.
func normalizeSyncVerificationIDs(
	report map[string]any,
	section string,
	field string,
	ids []string,
) error {
	items, ok := report[section].([]any)
	if !ok {
		return nil
	}
	if len(items) != len(ids) {
		return fmt.Errorf("%s identity count mismatch", section)
	}
	for i, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if value, exists := record[field]; exists && value != nil {
			record[field] = ids[i]
		}
	}
	return nil
}
