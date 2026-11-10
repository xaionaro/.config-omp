package main

import (
	"slices"
	"testing"
)

// TestEvalTargets distinguishes executable writes from reads and inert text.
//
// Example: a quoted Bun.write example does not select a target.
func TestEvalTargets(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{`write('a.go', 'x')`, "a.go"},
		{`Bun.write('a.go', 'x')`, "a.go"},
		{`Path('a.py').write_text('x')`, "a.py"},
		{`open('a.py', 'w')`, "a.py"},
		{`open('a.py', 'r')`, ""},
		{`Path('a.py').read_text()`, ""},
		{`print("Bun.write('a.go', 'x')")`, ""},
		{`/* write('a.go', 'x') */ print('ok')`, ""},
		{`sys.stdout.write('a.py')`, ""},
	} {
		got := evalTargets(tc.code)
		if tc.want == "" && len(got) != 0 { t.Errorf("%q: unexpected %v",tc.code,got) }
		if tc.want != "" && !slices.Contains(got,tc.want) { t.Errorf("%q: got %v, missing %q",tc.code,got,tc.want) }
	}
}

// TestEditTargetsPreferHostInspection preserves source and move destinations.
//
// Example: the host parser's Go rename destination is checked even when source is text.
func TestEditTargetsPreferHostInspection(t *testing.T) {
	req := Request{Tool:"edit",Input:map[string]any{"path":"source.txt"},EditTargets:[]string{"source.txt","dest.go"}}
	got, err := writeTargets(req)
	if err != nil || !slices.Contains(got,"dest.go") { t.Fatalf("targets %v error %v",got,err) }
	req.EditTargets = []string{}
	got, err = writeTargets(req)
	if err != nil || len(got) != 0 { t.Fatalf("empty host inspection was replaced by heuristic %v, error %v",got,err) }
}
