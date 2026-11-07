package main

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// flowScanBytes normalizes CRLF for the flow lexer while retaining line and column positions.
//
// Example: Map lexer positions back to the original lines after removing CR from CRLF.
func flowScanBytes(data []byte) ([]byte, error) {
	scan := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if bytes.ContainsRune(scan, '\r') {
		return nil, fmt.Errorf("unsupported bare CR layout")
	}
	if bytes.ContainsRune(scan, '!') {
		if err := maskYAMLTagPositions(scan); err != nil {
			return nil, err
		}
	}
	return scan, nil
}

// yamlByteOffset maps a one-based YAML line and rune column into UTF-8 bytes.
//
// Example: Map a column after a multibyte character.
func yamlByteOffset(
	data []byte,
	line int,
	column int,
) (int, error) {
	if !utf8.Valid(data) {
		return 0, fmt.Errorf("unsupported UTF-8 layout")
	}
	lines := strings.Split(string(data), "\n")
	starts := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		starts[i] = starts[i-1] + len(lines[i-1]) + 1
	}
	return yamlOffsetForLines(lines, starts, line, column)
}

// yamlOffsetForLines maps a YAML position using precomputed line starts.
//
// Example: Resolve a token position using cached line starts.
func yamlOffsetForLines(
	lines []string,
	starts []int,
	line int,
	column int,
) (int, error) {
	if line < 1 || line > len(lines) || column < 1 {
		return 0, fmt.Errorf("invalid YAML position")
	}
	offset := starts[line-1]
	current := lines[line-1]
	for i := 1; i < column; i++ {
		if len(current) == 0 {
			return 0, fmt.Errorf("YAML column exceeds line")
		}
		_, size := utf8.DecodeRuneInString(current)
		offset += size
		current = current[size:]
	}
	return offset, nil
}

// flowPair records a matched pair of YAML flow delimiters.
//
// Example: Store the closing brace for an opening brace.
type flowPair struct {
	Opening token.Type
	End     reportByteOffset
}

// flowDocument indexes flow delimiters in one report.
// Its private fields keep the cached pairs consistent with the source data.
//
// Example: Cache delimiter pairs for a status report.
type flowDocument struct {
	data    []byte
	ready   bool
	pairs   map[reportByteOffset]flowPair
	scalars map[reportByteOffset]*token.Token
	err     error
}

// flowStart records an opening flow delimiter while pairing tokens.
//
// Example: Track an opening bracket while scanning nested collections.
type flowStart struct {
	Opening token.Type
	Offset  reportByteOffset
}

// flowEnd finds a matching lexer delimiter, excluding scalar and comment tokens.
//
// Example: Find the closing brace after a quoted delimiter.
func flowEnd(
	data []byte,
	start int,
	opening token.Type,
) (int, error) {
	return (&flowDocument{data: data}).end(start, opening)
}

// end returns the checked closing delimiter for a flow collection.
//
// Example: Resolve a previously indexed flow mapping.
func (d *flowDocument) end(
	start int,
	opening token.Type,
) (int, error) {
	if !d.ready {
		d.build()
	}
	if d.err != nil {
		return 0, d.err
	}
	pair, ok := d.pairs[reportByteOffset(start)]
	if !ok || pair.Opening != opening {
		return 0, fmt.Errorf("YAML delimiter position mismatch")
	}
	return int(pair.End), nil
}

// scalarSpan locates a proven single-line scalar using the cached lexer tokens.
//
// Example: Locate a block-style outcome containing a comma.
func (d *flowDocument) scalarSpan(node *yaml.Node) (int, int, error) {
	if !d.ready {
		d.build()
	}
	if d.err != nil {
		return 0, 0, d.err
	}
	start, err := yamlByteOffset(d.data, node.Line, node.Column)
	if err != nil {
		return 0, 0, err
	}
	tok, ok := d.scalars[reportByteOffset(start)]
	if !ok || tok.Value != node.Value {
		return 0, 0, fmt.Errorf("YAML scalar token mismatch")
	}
	raw := strings.TrimSpace(tok.Origin)
	if raw == "" || strings.ContainsAny(raw, "\r\n") || start+len(raw) > len(d.data) || string(d.data[start:start+len(raw)]) != raw {
		return 0, 0, fmt.Errorf("unsupported YAML scalar span")
	}
	return start, start + len(raw), nil
}

// build indexes lexer delimiter pairs for a report.
//
// Example: Pair nested brackets and braces in a report.
func (d *flowDocument) build() {
	d.ready = true
	if !utf8.Valid(d.data) {
		d.err = fmt.Errorf("unsupported UTF-8 layout")
		return
	}
	scan, err := flowScanBytes(d.data)
	if err != nil {
		d.err = err
		return
	}
	tokens := tokenizeReport(string(scan))
	if bad := tokens.InvalidToken(); bad != nil {
		d.err = fmt.Errorf("unsupported YAML token: %s", bad.Error)
		return
	}
	d.pairs = make(map[reportByteOffset]flowPair)
	d.scalars = make(map[reportByteOffset]*token.Token)
	lines := strings.Split(string(d.data), "\n")
	starts := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		starts[i] = starts[i-1] + len(lines[i-1]) + 1
	}
	var stack []flowStart
	for _, tok := range tokens {
		var err error
		stack, err = d.indexToken(tok, lines, starts, stack)
		if err != nil {
			d.err = err
			return
		}
	}
	if len(stack) > 0 {
		d.err = fmt.Errorf("unbalanced YAML flow delimiters")
	}
}

// indexToken indexes a scalar or pairs one flow delimiter.
//
// Example: Match a closing brace to the latest opening brace.
func (d *flowDocument) indexToken(
	tok *token.Token,
	lines []string,
	starts []int,
	stack []flowStart,
) ([]flowStart, error) {
	switch tok.Type {
	case token.StringType, token.SingleQuoteType, token.DoubleQuoteType, token.NullType,
		token.IntegerType, token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType,
		token.FloatType, token.BoolType, token.InfinityType, token.NanType:
		return stack, d.indexScalar(tok, lines, starts)
	case token.MappingStartType, token.MappingEndType, token.SequenceStartType, token.SequenceEndType:
		return d.indexDelimiter(tok, lines, starts, stack)
	default:
		return stack, nil
	}
}

// indexScalar indexes a scalar with a usable lexer position.
//
// Example: Store the token for a plain forecast outcome.
func (d *flowDocument) indexScalar(
	tok *token.Token,
	lines []string,
	starts []int,
) error {
	if tok.Position == nil {
		return fmt.Errorf("YAML token lacks position")
	}
	if tok.Position.Column < 1 {
		return nil
	}
	pos, err := yamlOffsetForLines(lines, starts, tok.Position.Line, tok.Position.Column)
	if err != nil {
		return err
	}
	d.scalars[reportByteOffset(pos)] = tok
	return nil
}

// indexDelimiter validates and pairs a flow delimiter.
//
// Example: Reject a closing bracket paired with an opening brace.
func (d *flowDocument) indexDelimiter(
	tok *token.Token,
	lines []string,
	starts []int,
	stack []flowStart,
) ([]flowStart, error) {
	if tok.Position == nil {
		return nil, fmt.Errorf("YAML token lacks position")
	}
	pos, err := yamlOffsetForLines(lines, starts, tok.Position.Line, tok.Position.Column)
	if err != nil {
		return nil, err
	}
	expected := map[token.Type]byte{token.MappingStartType: '{', token.MappingEndType: '}', token.SequenceStartType: '[', token.SequenceEndType: ']'}[tok.Type]
	if pos >= len(d.data) || d.data[pos] != expected {
		return nil, fmt.Errorf("YAML delimiter byte mismatch")
	}
	if tok.Type == token.MappingStartType || tok.Type == token.SequenceStartType {
		return append(stack, flowStart{Opening: tok.Type, Offset: reportByteOffset(pos)}), nil
	}
	if len(stack) == 0 {
		return nil, fmt.Errorf("unbalanced YAML flow delimiters")
	}
	top := stack[len(stack)-1]
	if (top.Opening == token.MappingStartType && tok.Type != token.MappingEndType) || (top.Opening == token.SequenceStartType && tok.Type != token.SequenceEndType) {
		return nil, fmt.Errorf("unbalanced YAML flow delimiters")
	}
	d.pairs[top.Offset] = flowPair{Opening: top.Opening, End: reportByteOffset(pos)}
	return stack[:len(stack)-1], nil
}

// yamlRangeHasComment reports comment tokens within a proposed edit range.
//
// Example: A hash in a quoted record reference is not a YAML comment.
func yamlRangeHasComment(
	data []byte,
	start int,
	end int,
) bool {
	scan, err := flowScanBytes(data)
	if err != nil {
		return true
	}
	tokens := tokenizeReport(string(scan))
	if tokens.InvalidToken() != nil {
		return true
	}
	for _, tok := range tokens {
		if tok.Type != token.CommentType {
			continue
		}
		if tok.Position == nil {
			return true
		}
		offset, err := yamlByteOffset(data, tok.Position.Line, tok.Position.Column)
		if err != nil {
			return true
		}
		if offset >= start && offset < end {
			return true
		}
	}
	return false
}
