package main

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// appendReportField inserts a missing field in a block or flow report mapping.
//
// Example: Add sync metadata inside a JSON-root report without rewriting its fields.
func appendReportField(
	data []byte,
	key string,
	value string,
) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("report is not a mapping")
	}
	root := doc.Content[0]
	if mapValue(root, key) != nil {
		return nil, fmt.Errorf("report field %s already exists", key)
	}
	if root.Style&yaml.FlowStyle != 0 {
		end, err := flowMappingEndCached(&flowDocument{data: data}, root)
		if err != nil {
			return nil, err
		}
		sep := ""
		if len(root.Content) > 0 {
			sep = ", "
			trailing, err := flowMappingHasTrailingComma(data, end)
			if err != nil {
				return nil, err
			}
			if trailing {
				sep = " "
			}
		}
		insert := sep + strconv.Quote(key) + ": " + value
		return append(append(append([]byte(nil), data[:end]...), []byte(insert)...), data[end:]...), nil
	}
	ending := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		ending = "\r\n"
	}
	suffix := key + ": " + value + ending
	if len(data) > 0 && data[len(data)-1] != '\n' {
		suffix = ending + suffix
	}
	return append(append([]byte(nil), data...), []byte(suffix)...), nil
}

// flowMappingHasTrailingComma checks the final syntax token before a flow mapping closes.
//
// Example: A comma in a trailing comment does not count as a separator.
func flowMappingHasTrailingComma(
	data []byte,
	end int,
) (bool, error) {
	scan, err := flowScanBytes(data)
	if err != nil {
		return false, err
	}
	tokens := tokenizeReport(string(scan))
	if bad := tokens.InvalidToken(); bad != nil {
		return false, fmt.Errorf("invalid YAML token: %s", bad.Error)
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		current := tokens[i]
		if current.Type != token.MappingEndType {
			continue
		}
		if current.Position == nil {
			return false, fmt.Errorf("YAML mapping end lacks position")
		}
		pos, err := yamlByteOffset(data, current.Position.Line, current.Position.Column)
		if err != nil {
			return false, err
		}
		if pos != end {
			return false, fmt.Errorf("YAML mapping end mismatch")
		}
		for j := i - 1; j >= 0; j-- {
			if tokens[j].Type != token.CommentType {
				return tokens[j].Type == token.CollectEntryType, nil
			}
		}
		break
	}
	return false, fmt.Errorf("YAML mapping end not found")
}
