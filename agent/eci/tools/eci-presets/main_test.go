package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReconcile checks missing, stale and obsolete files without touching unrelated files.
//
// Example: a profile update makes --check fail until regeneration.
func TestReconcile(t *testing.T) {
	dir := t.TempDir()
	want := map[string][]byte{"eci-preset-a.md": []byte("new\n" + generatedMarker + "\n")}
	if err := os.WriteFile(filepath.Join(dir, "other.md"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eci-preset-old.md"), []byte(generatedMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, want, true); err == nil {
		t.Fatal("check accepted missing and obsolete output")
	}
	if _, err := os.Stat(filepath.Join(dir, "eci-preset-old.md")); err != nil {
		t.Fatalf("check removed obsolete file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "eci-preset-a.md")); !os.IsNotExist(err) {
		t.Fatalf("check created missing file: %v", err)
	}
	if err := reconcile(dir, want, false); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, want, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "other.md")); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated file changed: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eci-preset-a.md"), []byte("stale\n"+generatedMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, want, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale not detected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eci-preset-user.md"), []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, want, false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "eci-preset-user.md")); err != nil || string(data) != "user" {
		t.Fatalf("unowned file changed: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eci-preset-a.md"), []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, want, false); err == nil {
		t.Fatal("unowned target collision accepted")
	}
}

// TestGenerateRejectsIncompleteProfiles ensures missing selectors fail before deployment.
//
// Example: removing a camp cannot silently create a fallback to the parent.
func TestGenerateRejectsIncompleteProfiles(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "model-selection.yaml")
	if err := os.WriteFile(config, []byte("openai:\n  default:\n    model: openai/example\n    reasoning_effort: medium\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(config, dir); err == nil {
		t.Fatal("accepted incomplete profile matrix")
	}
}

// TestGenerateMatrix checks direct selectors and reverse-camp fallbacks.
//
// Example: an openai reviewer falls back to the anthropic profile of the same level.
func TestGenerateMatrix(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "model-selection.yaml")
	data := "openai:\n  default: {model: openai/a, reasoning_effort: low}\n  higher: {model: openai/b, reasoning_effort: medium}\n  max: {model: openai/c, reasoning_effort: high}\nanthropic:\n  default: {model: anthropic/a, reasoning_effort: low}\n  higher: {model: anthropic/b, reasoning_effort: medium}\n  max: {model: anthropic/c, reasoning_effort: high}\n"
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-base.tmpl"), []byte("base directives"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"explorer", "critic-step2", "implementer", "fast-owner", "reviewer", "independent-e2e", "orchestrator"} {
		if err := os.WriteFile(filepath.Join(dir, role+".tmpl"), []byte("{{.Name}} {{.Primary}} {{.Fallback}}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := generate(config, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 42 {
		t.Fatalf("got %d definitions, want 42", len(files))
	}
	value := string(files["eci-preset-reviewer-openai-higher.md"])
	if !strings.Contains(value, "openai/b:medium anthropic/b:medium") || strings.Contains(value, "openai/a:low") {
		t.Fatalf("wrong selectors: %s", value)
	}
	if !strings.Contains(value, "base directives") {
		t.Fatal("missing task baseline")
	}
}

// TestReconcileRejectsSymlinks ensures managed-looking links cannot modify another file.
//
// Example: a user-owned target stays untouched even if a link has a generated name.
func TestReconcileRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "other.md")
	if err := os.WriteFile(target, []byte(generatedMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "eci-preset-a.md")); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(dir, map[string][]byte{"eci-preset-a.md": []byte("replacement")}, false); err == nil {
		t.Fatal("accepted symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != generatedMarker {
		t.Fatalf("target changed: %q %v", data, err)
	}
}

// TestTaskRoleTemplatesPreserveDefaults checks general roles do not override task capabilities.
//
// Example: design critics inherit the caller's structured-output policy.
func TestTaskRoleTemplatesPreserveDefaults(t *testing.T) {
	for _, role := range []string{"explorer", "critic-step2", "implementer", "fast-owner", "reviewer", "independent-e2e", "orchestrator"} {
		data, err := os.ReadFile(filepath.Join("templates", role+".tmpl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"\ntools:", "\nspawns:", "\noutput:", "\nstructured:"} {
			if strings.Contains(string(data), field) {
				t.Errorf("%s overrides %s", role, field)
			}
		}
	}
}
