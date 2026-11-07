package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// normalizeNarratives converts keyed narrative entries to display text for parsing.
//
// Example: A subject and its evidence become one readable bullet without altering the source file.
func normalizeNarratives(data []byte) ([]byte, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("expected one YAML document")
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil
	}
	root := doc.Content[0]
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, list := root.Content[i].Value, root.Content[i+1]
		if key != "progress" && key != "decisions" && key != "blockers_risks" && key != "verification" {
			continue
		}
		if list.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range list.Content {
			if entry.Kind != yaml.MappingNode {
				continue
			}
			if key == "decisions" {
				hasSubject := false
				for j := 0; j+1 < len(entry.Content); j += 2 {
					if entry.Content[j].Value == "subject_ref" {
						hasSubject = true
					}
				}
				if !hasSubject {
					continue
				}
			}
			fields := map[string]string{}
			for j := 0; j+1 < len(entry.Content); j += 2 {
				name, value := entry.Content[j].Value, entry.Content[j+1]
				if name != "subject_ref" && name != "text" && name != "evidence" {
					return nil, fmt.Errorf("unsupported narrative field %q", name)
				}
				if _, exists := fields[name]; exists || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return nil, fmt.Errorf("invalid narrative field %q", name)
				}
				fields[name] = value.Value
			}
			if strings.TrimSpace(fields["subject_ref"]) == "" || strings.TrimSpace(fields["text"]) == "" {
				return nil, fmt.Errorf("keyed narrative requires subject_ref and text")
			}
			text := fields["text"]
			if fields["evidence"] != "" {
				text += " (evidence: " + fields["evidence"] + ")"
			}
			*entry = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text}
			changed = true
		}
	}
	if !changed {
		return data, nil
	}
	return yaml.Marshal(&doc)
}
