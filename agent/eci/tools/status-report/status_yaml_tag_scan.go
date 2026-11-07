package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// maskYAMLTagPositions hides explicit tags only in a scanner copy.
//
// Example: A tag before a flow scalar cannot shift later delimiter positions.
func maskYAMLTagPositions(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	return maskYAMLNodeTags(data, &doc)
}

// maskYAMLNodeTags walks syntax nodes without following aliases.
//
// Example: A tagged scalar and its siblings retain their original byte columns.
func maskYAMLNodeTags(
	data []byte,
	node *yaml.Node,
) error {
	if node.Style&yaml.TaggedStyle != 0 {
		if err := maskOneYAMLTag(data, node); err != nil {
			return err
		}
	}
	for _, child := range node.Content {
		if err := maskYAMLNodeTags(data, child); err != nil {
			return err
		}
	}
	return nil
}

// maskOneYAMLTag replaces one explicit ASCII tag with equal-width spaces.
//
// Example: The scanner sees spaces in place of !!str, not altered source bytes.
func maskOneYAMLTag(
	data []byte,
	node *yaml.Node,
) error {
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil {
		return err
	}
	if node.Anchor != "" && strings.HasPrefix(string(data[start:]), "&"+node.Anchor) {
		start = skipNullPropertyTrivia(data, start+len(node.Anchor)+1)
	}
	if start >= len(data) || data[start] != '!' {
		return fmt.Errorf("unsupported YAML tag position")
	}
	end, err := yamlTagEnd(data, start)
	if err != nil {
		return err
	}
	for i := start; i < end; i++ {
		if data[i] >= 0x80 {
			return nil
		}
	}
	for i := start; i < end; i++ {
		data[i] = ' '
	}
	return nil
}

// yamlTagEnd locates an explicit tag without consuming adjacent owner content.
//
// Example: A verbatim tag ends at its closing angle bracket.
func yamlTagEnd(
	data []byte,
	start int,
) (int, error) {
	if start+1 < len(data) && data[start+1] == '<' {
		end := start + 2
		for end < len(data) && data[end] != '>' {
			end++
		}
		if end == len(data) {
			return 0, fmt.Errorf("unterminated YAML tag")
		}
		return end + 1, nil
	}
	end := start + 1
	for end < len(data) && !strings.ContainsRune(" \t\r\n,[]{}", rune(data[end])) {
		end++
	}
	return end, nil
}
