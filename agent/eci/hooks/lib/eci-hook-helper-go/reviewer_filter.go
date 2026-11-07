package main

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// runReviewerFilter ports the python3 program that filter_violations ran in
// hooks/lib/reviewer-filter.sh. Args: static corpus text, extra text (USER_HISTORY
// and CURRENT_TURN sections), threshold. Stdin: reviewer result JSON. On success it
// writes the filtered JSON plus a newline. Any input the Python program rejected
// (bad JSON, bad threshold, wrong shapes, values whose truthiness would crash it)
// returns statusRejected with no output, so callers keep the original result.
func runReviewerFilter(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		return statusRejected
	}
	staticText := pyArgString(args[0])
	extraText := pyArgString(args[1])
	threshold, ok := pyParseFloat(pyArgString(args[2]))
	if !ok {
		return statusRejected
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return statusFailed
	}
	out, ok := reviewerFilterViolations(raw, staticText, extraText, threshold)
	if !ok {
		return statusRejected
	}
	if _, err := io.WriteString(stdout, out+"\n"); err != nil {
		return statusFailed
	}
	return statusOK
}

var reviewerStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "any": true, "as": true, "be": true, "by": true,
	"for": true, "from": true, "if": true, "in": true, "is": true, "it": true, "of": true,
	"on": true, "or": true, "that": true, "the": true, "then": true, "this": true, "to": true,
	"was": true, "were": true, "with": true, "you": true, "your": true, "after": true,
	"again": true, "all": true, "always": true, "before": true, "current": true, "each": true,
	"end": true, "ending": true, "every": true, "must": true, "need": true, "needs": true,
	"requested": true, "required": true, "should": true, "stop": true, "stopping": true,
	"turn": true, "assistant": true, "entry": true, "user": true,
}

var reviewerSynonyms = map[string]string{
	"ran": "execute", "run": "execute", "running": "execute", "runs": "execute",
	"executed": "execute", "executes": "execute", "executing": "execute",
	"suite": "test",
	"ended": "stop", "ending": "stop", "stopped": "stop", "stopping": "stop",
}

var (
	reviewerEntryPattern = regexp.MustCompile(`(?is)<entry>(.*?)</entry>`)
	pyFloatPattern       = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
)

// reviewerFilter holds the corpus-derived state shared by every violation.
type reviewerFilter struct {
	corpus        map[string]bool   // grams of the static corpus
	history       []map[string]bool // signal tokens of each USER_HISTORY entry
	current       string            // CURRENT_TURN text, joined with newlines
	currentTokens map[string]bool
	threshold     float64
}

func newReviewerFilter(staticText, extraText string, threshold float64) *reviewerFilter {
	f := &reviewerFilter{threshold: threshold}
	f.corpus = reviewerGrams(pyFields(pyLower(staticText)))
	for _, section := range reviewerSectionBodies(extraText, "USER_HISTORY") {
		for _, entry := range reviewerEntryTexts(section) {
			f.history = append(f.history, reviewerSignalTokens(entry))
		}
	}
	f.current = strings.Join(reviewerSectionBodies(extraText, "CURRENT_TURN"), "\n")
	f.currentTokens = reviewerSignalTokens(f.current)
	return f
}

// reviewerFilterViolations mirrors the Python program's main loop and output.
func reviewerFilterViolations(raw []byte, staticText, extraText string, threshold float64) (string, bool) {
	value, ok := parsePyJSON(pyArgString(string(raw)))
	if !ok {
		return "", false
	}
	data, ok := value.(*jsonObject)
	if !ok {
		return "", false
	}
	filter := newReviewerFilter(staticText, extraText, threshold)

	kept := []jsonValue{}
	if v, present := data.get("violations"); present && pyTruthy(v) {
		list, ok := v.([]jsonValue)
		if !ok {
			return "", false
		}
		for _, item := range list {
			violation, ok := item.(*jsonObject)
			if !ok {
				return "", false
			}
			keep, ok := filter.keepViolation(violation)
			if !ok {
				return "", false
			}
			if keep {
				kept = append(kept, violation)
			}
		}
	}
	data.set("violations", kept)
	if len(kept) == 0 {
		if verdict, present := data.get("verdict"); present {
			if s, ok := verdict.(string); ok && s == "fail" {
				data.set("verdict", "pass")
			}
		}
	}
	return string(appendPyJSON(nil, data)), true
}

// keepViolation reports whether a violation is grounded in the static corpus or in
// USER_HISTORY agreement. The second result is false where the Python program raised.
func (f *reviewerFilter) keepViolation(v *jsonObject) (bool, bool) {
	ruleValue, present := v.get("rule")
	rule, ok := pyStringOrEmpty(ruleValue, present)
	if !ok {
		return false, false
	}
	ruleGrams := reviewerGrams(pyFields(pyLower(rule)))
	if len(ruleGrams) > 0 {
		shared := 0
		for g := range ruleGrams {
			if f.corpus[g] {
				shared++
			}
		}
		if float64(shared)/float64(len(ruleGrams)) >= f.threshold {
			return true, true
		}
	}
	return f.matchesHistory(v, rule)
}

func (f *reviewerFilter) matchesHistory(v *jsonObject, rule string) (bool, bool) {
	if len(f.history) == 0 {
		return false, true
	}
	evidenceValue, present := v.get("evidence")
	evidence, ok := pyStringOrEmpty(evidenceValue, present)
	if !ok {
		return false, false
	}
	if !f.evidenceGrounded(evidence) {
		return false, true
	}
	ruleTokens := reviewerSignalTokens(rule)
	if len(ruleTokens) < 2 {
		return false, true
	}
	for _, entry := range f.history {
		shared := 0
		for t := range ruleTokens {
			if entry[t] {
				shared++
			}
		}
		minLen := min(len(ruleTokens), len(entry))
		required := 2
		if minLen >= 3 {
			required = 3
		}
		if shared >= required && float64(shared)/float64(minLen) >= 0.5 {
			return true, true
		}
	}
	return false, true
}

func (f *reviewerFilter) evidenceGrounded(evidence string) bool {
	evidence = strings.Join(pyFields(pyLower(evidence)), " ")
	current := strings.Join(pyFields(pyLower(f.current)), " ")
	if evidence == "" {
		return false
	}
	if strings.Contains(current, evidence) {
		return true
	}
	evidenceTokens := reviewerSignalTokens(evidence)
	if len(evidenceTokens) == 0 {
		return false
	}
	shared := 0
	for t := range evidenceTokens {
		if f.currentTokens[t] {
			shared++
		}
	}
	return float64(shared)/float64(len(evidenceTokens)) >= 0.5
}

// reviewerGrams returns the set of 3-word joins, or the words themselves when fewer than 3.
func reviewerGrams(words []string) map[string]bool {
	set := map[string]bool{}
	if len(words) < 3 {
		for _, w := range words {
			set[w] = true
		}
		return set
	}
	for i := 0; i+3 <= len(words); i++ {
		set[words[i]+" "+words[i+1]+" "+words[i+2]] = true
	}
	return set
}

// reviewerSignalTokens mirrors signal_tokens: maximal [a-z0-9] runs of the lowered text,
// normalized, keeping tokens of length >= 2 that are not stopwords.
func reviewerSignalTokens(text string) map[string]bool {
	set := map[string]bool{}
	lowered := pyLower(text)
	for i := 0; i < len(lowered); {
		if !reviewerAlnum(lowered[i]) {
			i++
			continue
		}
		j := i
		for j < len(lowered) && reviewerAlnum(lowered[j]) {
			j++
		}
		tok := reviewerNormalizeToken(lowered[i:j])
		if len(tok) >= 2 && !reviewerStopwords[tok] {
			set[tok] = true
		}
		i = j
	}
	return set
}

func reviewerAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func reviewerNormalizeToken(t string) string {
	if s, ok := reviewerSynonyms[t]; ok {
		t = s
	}
	if len(t) > 3 && strings.HasSuffix(t, "s") {
		t = t[:len(t)-1]
	}
	if s, ok := reviewerSynonyms[t]; ok {
		t = s
	}
	return t
}

// reviewerEntryTexts mirrors entry_texts: the <entry> bodies, or the whole section when none.
func reviewerEntryTexts(section string) []string {
	matches := reviewerEntryPattern.FindAllStringSubmatchIndex(section, -1)
	if len(matches) == 0 {
		return []string{section}
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, section[m[2]:m[3]])
	}
	return out
}

// reviewerSectionBodies mirrors section_text: findall of
// (?ims)^## NAME\s*\n(.*?)(?=^## [A-Z_]+|\Z), with IGNORECASE applied to the name and the lookahead.
// Go's regexp has no lookahead, so the scan is hand-rolled over line starts.
func reviewerSectionBodies(source, name string) []string {
	var bodies []string
	for p := 0; p <= len(source); {
		if p == 0 || source[p-1] == '\n' {
			if body, end, ok := reviewerSectionAt(source, p, name); ok {
				bodies = append(bodies, body)
				p = end
				continue
			}
		}
		nl := strings.IndexByte(source[p:], '\n')
		if nl < 0 {
			break
		}
		p += nl + 1
	}
	return bodies
}

func reviewerSectionAt(source string, p int, name string) (string, int, bool) {
	if !strings.HasPrefix(source[p:], "## ") {
		return "", 0, false
	}
	q := p + 3
	for i := range len(name) {
		if q >= len(source) {
			return "", 0, false
		}
		r, size := wtf8DecodeAt(source, q)
		if !reviewerNameRuneMatches(name[i], r) {
			return "", 0, false
		}
		q += size
	}
	// \s*\n: greedy whitespace, backtracking to the last newline of the run.
	bodyStart := -1
	for q < len(source) {
		r, size := wtf8DecodeAt(source, q)
		if !pySpace(r) {
			break
		}
		if r == '\n' {
			bodyStart = q + size
		}
		q += size
	}
	if bodyStart < 0 {
		return "", 0, false
	}
	for j := bodyStart; ; {
		if reviewerSectionEnds(source, j) {
			return source[bodyStart:j], j, true
		}
		_, size := wtf8DecodeAt(source, j)
		j += size
	}
}

// reviewerSectionEnds is the lookahead (?=^## [A-Z_]+|\Z) at position j.
func reviewerSectionEnds(source string, j int) bool {
	if j >= len(source) {
		return true
	}
	if j == 0 || source[j-1] != '\n' || !strings.HasPrefix(source[j:], "## ") {
		return false
	}
	if j+3 >= len(source) {
		return false
	}
	r, _ := wtf8DecodeAt(source, j+3)
	return reviewerHeaderRune(r)
}

// reviewerNameRuneMatches matches one name byte (uppercase ASCII) under IGNORECASE,
// including Python's re fixes for dotless i and long s.
func reviewerNameRuneMatches(c byte, r rune) bool {
	lower := unicode.ToLower(r)
	want := unicode.ToLower(rune(c))
	if lower == want {
		return true
	}
	return (want == 's' && r == 0x17F) || (want == 'i' && r == 0x131)
}

// reviewerHeaderRune matches [A-Z_] under IGNORECASE.
func reviewerHeaderRune(r rune) bool {
	lower := unicode.ToLower(r)
	return (lower >= 'a' && lower <= 'z') || lower == '_' || r == 0x131 || r == 0x17F
}

// pyLower mirrors str.lower(): simple case mapping, U+0130 expanding to i + combining dot,
// and the final-sigma rule.
func pyLower(s string) string {
	rs := wtf8Decode(s)
	out := make([]rune, 0, len(rs))
	for i, r := range rs {
		switch r {
		case 0x130:
			out = append(out, 'i', 0x307)
		case 0x3A3:
			if pyFinalSigma(rs, i) {
				out = append(out, 0x3C2)
			} else {
				out = append(out, 0x3C3)
			}
		default:
			out = append(out, unicode.ToLower(r))
		}
	}
	return wtf8Encode(out)
}

func pyFinalSigma(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && pyCaseIgnorable(rs[j]) {
		j--
	}
	if j < 0 || !pyCased(rs[j]) {
		return false
	}
	k := i + 1
	for k < len(rs) && pyCaseIgnorable(rs[k]) {
		k++
	}
	return k == len(rs) || !pyCased(rs[k])
}

func pyCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func pyCaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027,
		0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return false
}

// pySpace mirrors str.isspace().
func pySpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0D, r >= 0x1C && r <= 0x20:
		return true
	case r == 0x85, r == 0xA0, r == 0x1680, r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

// pyFields mirrors str.split() with no arguments, returning WTF-8 pieces.
func pyFields(s string) []string {
	rs := wtf8Decode(s)
	var out []string
	start := -1
	for i, r := range rs {
		if pySpace(r) {
			if start >= 0 {
				out = append(out, wtf8Encode(rs[start:i]))
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, wtf8Encode(rs[start:]))
	}
	return out
}

// pyParseFloat mirrors float() for ASCII input: surrounding whitespace, optional sign,
// inf/infinity/nan, underscores only between digits. Unicode decimal digits are mapped to ASCII first, as CPython does.
func pyParseFloat(raw string) (float64, bool) {
	rs := pyASCIIDigits(wtf8Decode(raw))
	lo, hi := 0, len(rs)
	for lo < hi && pySpace(rs[lo]) {
		lo++
	}
	for hi > lo && pySpace(rs[hi-1]) {
		hi--
	}
	s := wtf8Encode(rs[lo:hi])
	sign := ""
	body := s
	if strings.HasPrefix(body, "+") || strings.HasPrefix(body, "-") {
		sign = body[:1]
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if sign == "-" {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case "nan":
		return math.NaN(), true
	}
	var clean strings.Builder
	for i := range len(body) {
		c := body[i]
		if c == '_' {
			if i == 0 || i+1 >= len(body) || !pyDigit(body[i-1]) || !pyDigit(body[i+1]) {
				return 0, false
			}
			continue
		}
		clean.WriteByte(c)
	}
	num := clean.String()
	if !pyFloatPattern.MatchString(num) {
		return 0, false
	}
	f, err := strconv.ParseFloat(sign+num, 64)
	if err != nil && !strings.Contains(err.Error(), "range") {
		return 0, false
	}
	return f, true
}

// pyASCIIDigits replaces Unicode decimal digits (category Nd) with their ASCII value.
// Nd runs are contiguous blocks of ten in ascending order, so the value is the offset
// into the run modulo ten.
func pyASCIIDigits(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		if r >= '0' && r <= '9' || !unicode.IsDigit(r) {
			out[i] = r
			continue
		}
		run := 0
		for q := r - 1; unicode.IsDigit(q); q-- {
			run++
		}
		out[i] = '0' + rune(run%10)
	}
	return out
}

func pyDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// pyArgString converts raw bytes (argv or stdin) to WTF-8 the way Python decodes them with
// surrogateescape: invalid bytes become lone surrogates U+DC80..U+DCFF.
func pyArgString(raw string) string {
	var b []byte
	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r == utf8.RuneError && size == 1 {
			b = wtf8Append(b, 0xDC00+rune(raw[i]))
		} else {
			b = append(b, raw[i:i+size]...)
		}
		i += size
	}
	return string(b)
}

// wtf8DecodeAt decodes the code point at byte offset i, accepting encoded surrogates.
func wtf8DecodeAt(s string, i int) (rune, int) {
	if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
		return rune(0xD)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
	}
	return utf8.DecodeRuneInString(s[i:])
}

func wtf8Decode(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := wtf8DecodeAt(s, i)
		out = append(out, r)
		i += size
	}
	return out
}

func wtf8Encode(rs []rune) string {
	var b []byte
	for _, r := range rs {
		b = wtf8Append(b, r)
	}
	return string(b)
}

// wtf8Append appends a code point, encoding surrogates as 3-byte sequences.
func wtf8Append(b []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(b, byte(0xE0|(r>>12)), byte(0x80|((r>>6)&0x3F)), byte(0x80|(r&0x3F)))
	}
	return utf8.AppendRune(b, r)
}

// jsonValue is one of: nil, bool, jsonInt (canonical decimal text), jsonFloat,
// string (WTF-8), []jsonValue, *jsonObject.
type jsonValue any

type jsonInt string

type jsonFloat float64

// jsonObject keeps insertion order; setting an existing key keeps its position.
type jsonObject struct {
	keys   []string
	values map[string]jsonValue
}

func (o *jsonObject) get(k string) (jsonValue, bool) {
	v, ok := o.values[k]
	return v, ok
}

func (o *jsonObject) set(k string, v jsonValue) {
	if o.values == nil {
		o.values = map[string]jsonValue{}
	}
	if _, ok := o.values[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.values[k] = v
}

// pyTruthy mirrors Python truthiness of a decoded JSON value.
func pyTruthy(v jsonValue) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case jsonInt:
		return string(t) != "0"
	case jsonFloat:
		return float64(t) != 0
	case string:
		return t != ""
	case []jsonValue:
		return len(t) > 0
	case *jsonObject:
		return len(t.keys) > 0
	}
	return true
}

// pyStringOrEmpty mirrors `value or ""` followed by use as a str. The second result
// is false when the value is truthy but not a string (Python would raise).
func pyStringOrEmpty(v jsonValue, present bool) (string, bool) {
	if !present || !pyTruthy(v) {
		return "", true
	}
	s, ok := v.(string)
	return s, ok
}

// pyJSONMaxDepth bounds container nesting. CPython 3.14's own limit is nondeterministic between
// about 52100 and 52160 nested containers, so the port rejects above 52000 (fail closed).
const pyJSONMaxDepth = 52000

// pyJSONIntDigitLimit matches CPython's int string conversion limit.
const pyJSONIntDigitLimit = 4300

type jsonParser struct {
	s     string
	pos   int
	depth int
}

// parsePyJSON mirrors json.loads(strict=True) on a UTF-8 string.
func parsePyJSON(s string) (jsonValue, bool) {
	if strings.HasPrefix(s, "\uFEFF") {
		return nil, false
	}
	p := &jsonParser{s: s}
	p.skipSpace()
	v, ok := p.value()
	if !ok {
		return nil, false
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return nil, false
	}
	return v, true
}

func (p *jsonParser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value() (jsonValue, bool) {
	if p.pos >= len(p.s) {
		return nil, false
	}
	switch c := p.s[p.pos]; {
	case c == '"':
		return p.str()
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == 'n':
		return p.literal("null", nil)
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'N':
		return p.literal("NaN", jsonFloat(math.NaN()))
	case c == 'I':
		return p.literal("Infinity", jsonFloat(math.Inf(1)))
	case c == '-' && strings.HasPrefix(p.s[p.pos:], "-Infinity"):
		return p.literal("-Infinity", jsonFloat(math.Inf(-1)))
	case c == '-' || pyDigit(c):
		return p.number()
	}
	return nil, false
}

func (p *jsonParser) literal(lit string, v jsonValue) (jsonValue, bool) {
	if !strings.HasPrefix(p.s[p.pos:], lit) {
		return nil, false
	}
	p.pos += len(lit)
	return v, true
}

func (p *jsonParser) number() (jsonValue, bool) {
	start := p.pos
	if p.s[p.pos] == '-' {
		p.pos++
	}
	if p.pos >= len(p.s) || !pyDigit(p.s[p.pos]) {
		return nil, false
	}
	if p.s[p.pos] == '0' {
		p.pos++
	} else {
		for p.pos < len(p.s) && pyDigit(p.s[p.pos]) {
			p.pos++
		}
	}
	isFloat := false
	if p.pos+1 < len(p.s) && p.s[p.pos] == '.' && pyDigit(p.s[p.pos+1]) {
		p.pos += 2
		for p.pos < len(p.s) && pyDigit(p.s[p.pos]) {
			p.pos++
		}
		isFloat = true
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		q := p.pos + 1
		if q < len(p.s) && (p.s[q] == '+' || p.s[q] == '-') {
			q++
		}
		if q < len(p.s) && pyDigit(p.s[q]) {
			p.pos = q
			for p.pos < len(p.s) && pyDigit(p.s[p.pos]) {
				p.pos++
			}
			isFloat = true
		}
	}
	num := p.s[start:p.pos]
	if isFloat {
		f, err := strconv.ParseFloat(num, 64)
		if err != nil && !strings.Contains(err.Error(), "range") {
			return nil, false
		}
		return jsonFloat(f), true
	}
	if len(strings.TrimPrefix(num, "-")) > pyJSONIntDigitLimit {
		return nil, false
	}
	if num == "-0" {
		num = "0"
	}
	return jsonInt(num), true
}

func (p *jsonParser) hex4() (rune, bool) {
	if p.pos+4 > len(p.s) {
		return 0, false
	}
	var cp rune
	for i := range 4 {
		c := p.s[p.pos+i]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		cp = cp<<4 | rune(d)
	}
	p.pos += 4
	return cp, true
}

func (p *jsonParser) str() (jsonValue, bool) {
	p.pos++ // opening quote
	var b []byte
	for {
		if p.pos >= len(p.s) {
			return nil, false
		}
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			return string(b), true
		case c == '\\':
			p.pos++
			if p.pos >= len(p.s) {
				return nil, false
			}
			e := p.s[p.pos]
			p.pos++
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				cp, ok := p.hex4()
				if !ok {
					return nil, false
				}
				if cp >= 0xD800 && cp <= 0xDBFF && strings.HasPrefix(p.s[p.pos:], `\u`) {
					save := p.pos
					p.pos += 2
					lo, ok := p.hex4()
					if !ok {
						return nil, false
					}
					if lo >= 0xDC00 && lo <= 0xDFFF {
						cp = 0x10000 + (cp-0xD800)<<10 + (lo - 0xDC00)
					} else {
						p.pos = save
					}
				}
				b = wtf8Append(b, cp)
			default:
				return nil, false
			}
		case c < 0x20:
			return nil, false
		default:
			_, size := wtf8DecodeAt(p.s, p.pos)
			b = append(b, p.s[p.pos:p.pos+size]...)
			p.pos += size
		}
	}
}

func (p *jsonParser) array() (jsonValue, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > pyJSONMaxDepth {
		return nil, false
	}
	p.pos++
	arr := []jsonValue{}
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
		return arr, true
	}
	for {
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		arr = append(arr, v)
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, false
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipSpace()
		case ']':
			p.pos++
			return arr, true
		default:
			return nil, false
		}
	}
}

func (p *jsonParser) object() (jsonValue, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > pyJSONMaxDepth {
		return nil, false
	}
	p.pos++
	obj := &jsonObject{}
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return obj, true
	}
	for {
		if p.pos >= len(p.s) || p.s[p.pos] != '"' {
			return nil, false
		}
		key, ok := p.str()
		if !ok {
			return nil, false
		}
		p.skipSpace()
		if p.pos >= len(p.s) || p.s[p.pos] != ':' {
			return nil, false
		}
		p.pos++
		p.skipSpace()
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		obj.set(key.(string), v)
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, false
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipSpace()
		case '}':
			p.pos++
			return obj, true
		default:
			return nil, false
		}
	}
}

// appendPyJSON mirrors json.dumps(separators=(",", ":")) with ensure_ascii and allow_nan.
func appendPyJSON(b []byte, v jsonValue) []byte {
	switch t := v.(type) {
	case nil:
		return append(b, "null"...)
	case bool:
		if t {
			return append(b, "true"...)
		}
		return append(b, "false"...)
	case jsonInt:
		return append(b, string(t)...)
	case jsonFloat:
		return append(b, pyFloatRepr(float64(t))...)
	case string:
		return appendPyJSONString(b, t)
	case []jsonValue:
		b = append(b, '[')
		for i, item := range t {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendPyJSON(b, item)
		}
		return append(b, ']')
	case *jsonObject:
		b = append(b, '{')
		for i, k := range t.keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendPyJSONString(b, k)
			b = append(b, ':')
			b = appendPyJSON(b, t.values[k])
		}
		return append(b, '}')
	}
	return b
}

func appendPyJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range wtf8Decode(s) {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		default:
			switch {
			case r >= 0x20 && r <= 0x7E:
				b = append(b, byte(r))
			case r >= 0x10000:
				v := r - 0x10000
				b = fmt.Appendf(b, "\\u%04x\\u%04x", 0xD800+(v>>10), 0xDC00+(v&0x3FF))
			default:
				b = fmt.Appendf(b, "\\u%04x", r)
			}
		}
	}
	return append(b, '"')
}

// pyFloatRepr mirrors Python's float.__repr__ with JSON spellings for non-finite values.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	mant, expText, _ := strings.Cut(strconv.FormatFloat(math.Abs(f), 'e', -1, 64), "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	var out string
	if exp >= -4 && exp < 16 {
		switch {
		case exp >= 0 && len(digits) <= exp+1:
			out = digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
		case exp >= 0:
			out = digits[:exp+1] + "." + digits[exp+1:]
		default:
			out = "0." + strings.Repeat("0", -exp-1) + digits
		}
	} else {
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		out += fmt.Sprintf("e%+03d", exp)
	}
	if f < 0 {
		out = "-" + out
	}
	return out
}
