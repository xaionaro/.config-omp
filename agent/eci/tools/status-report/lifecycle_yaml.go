package main

import (
	"fmt"
	"gopkg.in/yaml.v3"
)

// validateLifecycleScalars rejects null and tagged lifecycle nodes after typed decoding.
// Typed decoding first rejects cyclic aliases before the merge traversal.
//
// Example: An omitted lifecycle is allowed, while a YAML null is not.
func validateLifecycleScalars(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return nil
	}
	lanes := lifecycleMapValue(doc.Content[0], "lanes")
	if lanes == nil {
		return nil
	}
	lanes = lifecycleDeref(lanes)
	if lanes == nil || lanes.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range lanes.Content {
		value := lifecycleMapValue(item, "lifecycle")
		if value == nil {
			continue
		}
		value = lifecycleDeref(value)
		if value == nil {
			return fmt.Errorf("lane lifecycle has invalid alias")
		}
		if err := checkEnumScalar(value); err != nil {
			return fmt.Errorf("lane lifecycle: %w", err)
		}
	}
	return nil
}

// lifecycleDeref follows a YAML alias to its supplied scalar or collection.
//
// Example: A string alias retains its string tag.
func lifecycleDeref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// lifecycleMapValue returns a mapping value when it is present.
//
// Example: Locate a lane's lifecycle scalar.
func lifecycleMapValue(
	n *yaml.Node,
	key string,
) *yaml.Node {
	n = lifecycleDeref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "<<" {
			continue
		}
		merged := lifecycleDeref(n.Content[i+1])
		if merged == nil {
			continue
		}
		if value := lifecycleMergedValue(merged, key); value != nil {
			return value
		}
	}
	return nil
}

// lifecycleMergedValue resolves a YAML merge mapping or sequence.
//
// Example: A merged null lifecycle remains invalid.
func lifecycleMergedValue(
	merged *yaml.Node,
	key string,
) *yaml.Node {
	if merged.Kind != yaml.SequenceNode {
		return lifecycleMapValue(merged, key)
	}
	for _, item := range merged.Content {
		if value := lifecycleMapValue(item, key); value != nil {
			return value
		}
	}
	return nil
}
