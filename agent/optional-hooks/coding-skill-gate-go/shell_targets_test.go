package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestShellWriteTargets checks concrete writes while leaving read-only commands alone.
//
// Example: a redirection to a literal Go file is detected.
func TestShellWriteTargets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.go"), []byte("package source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command string
		want string
	}{
		{"printf ok > out.go", "out.go"},
		{"target=out.go; printf ok > \"$target\"", ""},
		{"cat source.go", ""},
		{"printf 'out.go'", ""},
		{"gofmt source.go", ""},
		{"gofmt -w source.go", filepath.Join(dir, "source.go")},
		{"gofmt -w .", filepath.Join(dir, "source.go")},
		{"tee out.go", "out.go"},
		{"cp source.go out.go", "out.go"},
		{"sed -i 's/a/b/' source.go", "source.go"},
		{"sed 's/a/b/' source.go", ""},
		{"cat <<'EOF'\n> ignored.go\nEOF", ""},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			got, err := shellWriteTargets(tc.command, dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(got) != 0 { t.Fatalf("unexpected targets %v", got) }
				return
			}
			if !slices.Contains(got, tc.want) { t.Fatalf("targets %v do not contain %q", got, tc.want) }
		})
	}
}

// TestCopyIntoAbsoluteDirectoryIdentifiesDestination preserves Go target checks.
//
// Example: copying source.go into an absolute directory targets directory/source.go.
func TestCopyIntoAbsoluteDirectoryIdentifiesDestination(t *testing.T) {
	root := t.TempDir()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work,"source.go"),[]byte("package source\n"),0600); err != nil { t.Fatal(err) }
	dir := filepath.Join(root,"destination")
	if err := os.Mkdir(dir,0700); err != nil { t.Fatal(err) }
	got, err := shellWriteTargets("cp source.go '"+dir+"'", work)
	if err != nil || !slices.Contains(got,filepath.Join(dir,"source.go")) { t.Fatalf("targets %v; error %v",got,err) }
	got, err = shellWriteTargets("cp missing.go '"+dir+"'", work)
	if err != nil || slices.Contains(got,filepath.Join(dir,"missing.go")) { t.Fatalf("nonexistent source credited as write %v; error %v",got,err) }
}
