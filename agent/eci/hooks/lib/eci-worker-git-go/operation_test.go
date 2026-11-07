package main

import (
	"reflect"
	"testing"
)

// TestParseOperationRequiresExactNamedModes preserves the bounded public grammar.
//
// Example: native add options cannot be smuggled through the CLI.
func TestParseOperationRequiresExactNamedModes(t *testing.T) {
	tests := []struct {
		Name      string
		Arguments []string
		Allowed   bool
	}{
		{"stage content", []string{"stage-content", "--", "file.txt"}, true},
		{"stage removals", []string{"stage-removals", "--", "missing.txt"}, true},
		{"unstage", []string{"unstage", "--", "file.txt"}, true},
		{"literal magic", []string{"stage-content", "--", ":(glob)*"}, true},
		{"literal option", []string{"stage-content", "--", "--amend"}, true},
		{"escape", []string{"stage-content", "--", "../file.txt"}, false},
		{"absolute", []string{"stage-content", "--", "/tmp/file"}, false},
		{"native passthrough", []string{"add", "-A"}, false},
		{"empty selection", []string{"stage-content", "--"}, false},
		{"commit", []string{"commit", "--message", "normal"}, true},
		{"amend", []string{"commit", "--amend"}, false},
		{"commit paths", []string{"commit", "--message", "normal", "--", "file.txt"}, false},
		{"restore both", []string{"restore", "--source", "head", "--destination", "both", "--", "file.txt"}, true},
		{"index to index", []string{"restore", "--source", "index", "--destination", "index", "--", "file.txt"}, false},
		{"move", []string{"move", "--", "first", "second"}, true},
		{"move extra", []string{"move", "--", "first", "second", "third"}, false},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			operation, err := ParseOperation(append([]string{"--repo", "/repository"}, test.Arguments...))
			if (err == nil) != test.Allowed {
				t.Fatalf("operation=%+v error=%v allowed=%v", operation, err, test.Allowed)
			}
		})
	}
	operation, err := ParseOperation([]string{"--repo", "/repository", "stage-content", "--", "line\nname", ":(glob)*"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(operation.Paths, []string{"line\nname", ":(glob)*"}) {
		t.Fatalf("literal paths changed: %#v", operation.Paths)
	}
}

// TestParseOperationRejectsMalformedFixedOptions covers invalid grammar without native execution.
//
// Example: duplicated source values never silently replace a prior mode.
func TestParseOperationRejectsMalformedFixedOptions(t *testing.T) {
	cases := [][]string{
		{}, {"--repo", ""}, {"--repo", "/repo", "stage-content", "--unknown", "value"},
		{"--repo", "/repo", "restore", "--source", "index", "--source", "head", "--", "file"},
		{"--repo", "/repo", "restore", "--source", "bad-ref", "--destination", "worktree", "--", "file"},
		{"--repo", "/repo", "restore", "--source", "head", "--destination", "bad", "--", "file"},
		{"--repo", "/repo", "restore", "--source", "head", "--", "file"},
		{"--repo", "/repo", "remove", "--", "file"},
		{"--repo", "/repo", "stage-hunks", "--", "file"},
		{"--repo", "/repo", "commit", "--message"},
		{"--repo", "/repo", "commit", "--message", ""},
		{"--repo", "/repo", "stage-content", "--", ""},
		{"--repo", "/repo", "stage-content", "--", "file\x00name"},
		{"--repo", "/repo", "stage-content", "--", "directory//file"},
		{"--repo", "/repo", "stage-content", "--", "./file"},
	}
	for _, arguments := range cases {
		if _, err := ParseOperation(arguments); err == nil {
			t.Fatalf("malformed operation admitted: %#v", arguments)
		}
	}
	for _, source := range []string{"index", "head", "0123456789012345678901234567890123456789", "0123456789012345678901234567890123456789012345678901234567890123"} {
		if _, err := ParseOperation([]string{"--repo", "/repo", "restore", "--source", source, "--destination", "worktree", "--", "file"}); err != nil {
			t.Fatal(err)
		}
	}
}
