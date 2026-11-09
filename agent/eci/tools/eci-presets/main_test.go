package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReconcile checks missing, stale and obsolete files without touching unrelated files.
//
// Example: a profile update makes --check fail until regeneration.
func TestReconcile(t *testing.T) {
	dir := t.TempDir()
	want := map[string][]byte{"eci-preset-a.md": stamp([]byte("new\n"))}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.md"), []byte("keep"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci-preset-old.md"), stamp([]byte("old\n")), 0600))
	require.Error(t, reconcile(dir, want, true))
	_, err := os.Stat(filepath.Join(dir, "eci-preset-old.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "eci-preset-a.md"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, reconcile(dir, want, false))
	require.NoError(t, reconcile(dir, want, true))
	data, err := os.ReadFile(filepath.Join(dir, "other.md"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci-preset-a.md"), stamp([]byte("stale\n")), 0600))
	require.ErrorContains(t, reconcile(dir, want, true), "stale")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci-preset-user.md"), []byte("user"), 0600))
	require.Error(t, reconcile(dir, want, false))
	data, err = os.ReadFile(filepath.Join(dir, "eci-preset-user.md"))
	require.NoError(t, err)
	require.Equal(t, "user", string(data))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci-preset-a.md"), []byte("user"), 0600))
	require.Error(t, reconcile(dir, want, false))
}

// TestGenerateRejectsIncompleteProfiles ensures missing selectors fail before deployment.
//
// Example: removing a camp cannot silently create a fallback to the parent.
func TestGenerateRejectsIncompleteProfiles(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "model-selection.yaml")
	require.NoError(t, os.WriteFile(config, []byte("openai:\n  default:\n    model: openai/example\n    reasoning_effort: medium\n"), 0600))
	_, err := generate(config, dir)
	require.Error(t, err)
}

// TestGenerateMatrix checks direct selectors and reverse-camp fallbacks.
//
// Example: an openai reviewer falls back to the anthropic profile of the same level.
func TestGenerateMatrix(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "model-selection.yaml")
	data := "openai:\n  default: {model: openai/a, reasoning_effort: low}\n  higher: {model: openai/b, reasoning_effort: medium}\n  max: {model: openai/c, reasoning_effort: high}\nanthropic:\n  default: {model: anthropic/a, reasoning_effort: low}\n  higher: {model: anthropic/b, reasoning_effort: medium}\n  max: {model: anthropic/c, reasoning_effort: high}\n"
	require.NoError(t, os.WriteFile(config, []byte(data), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "task-base.tmpl"), []byte("base directives"), 0600))
	for _, role := range []string{"explorer", "critic-step2", "implementer", "fast-owner", "reviewer", "independent-e2e", "orchestrator", "helper"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, role+".tmpl"), []byte("{{.Name}} {{.Primary}} {{.Fallback}}"), 0600))
	}
	files, err := generate(config, dir)
	require.NoError(t, err)
	allowed := map[string][]string{
		"explorer":        {"default", "higher", "max"},
		"critic-step2":    {"max"},
		"implementer":     {"default", "higher", "max"},
		"fast-owner":      {"default", "higher", "max"},
		"reviewer":        {"higher", "max"},
		"independent-e2e": {"higher", "max"},
		"orchestrator":    {"default", "higher", "max"},
		"helper":          {"default", "higher", "max"},
	}
	selectors := map[string]map[string]string{
		"openai":    {"default": "openai/a:low", "higher": "openai/b:medium", "max": "openai/c:high"},
		"anthropic": {"default": "anthropic/a:low", "higher": "anthropic/b:medium", "max": "anthropic/c:high"},
	}
	require.Len(t, files, 40)
	for role, levels := range allowed {
		for _, camp := range []string{"openai", "anthropic"} {
			other := "anthropic"
			if camp == "anthropic" {
				other = "openai"
			}
			for _, level := range levels {
				name := "eci-preset-" + role + "-" + camp + "-" + level + ".md"
				value, ok := files[name]
				require.True(t, ok, name)
				require.Contains(t, string(value), selectors[camp][level]+" "+selectors[other][level])
				require.Contains(t, string(value), "base directives")
			}
		}
	}
	for _, camp := range []string{"openai", "anthropic"} {
		for _, suffix := range []string{"critic-step2-" + camp + "-default", "critic-step2-" + camp + "-higher", "reviewer-" + camp + "-default", "independent-e2e-" + camp + "-default"} {
			_, exists := files["eci-preset-"+suffix+".md"]
			require.False(t, exists, suffix)
		}
		require.Contains(t, files, "eci-preset-implementer-"+camp+"-max.md")
		require.Contains(t, files, "eci-preset-fast-owner-"+camp+"-higher.md")
	}
}

// TestReconcileRejectsSymlinks ensures managed-looking links cannot modify another file.
//
// Example: a user-owned target stays untouched even if a link has a generated name.
func TestReconcileRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "other.md")
	require.NoError(t, os.WriteFile(target, []byte(generatedMarker), 0600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "eci-preset-a.md")))
	require.Error(t, reconcile(dir, map[string][]byte{"eci-preset-a.md": []byte("replacement")}, false))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, generatedMarker, string(data))
}

// TestReconcilePreservesEditedAndLegacyFiles checks preflight protects user edits.
//
// Example: a modified generated file prevents all updates.
func TestReconcilePreservesEditedAndLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	name := "eci-preset-a.md"
	other := "eci-preset-b.md"
	want := map[string][]byte{name: stamp([]byte("new\n")), other: stamp([]byte("second\n"))}
	edited := append(stamp([]byte("old\n")), []byte("user edit")...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), edited, 0600))
	require.Error(t, reconcile(dir, want, false))
	data, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	require.Equal(t, edited, data)
	_, err = os.Stat(filepath.Join(dir, other))
	require.ErrorIs(t, err, os.ErrNotExist)

	changedLegacy := []byte("changed\n" + generatedMarker + "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), changedLegacy, 0600))
	require.Error(t, reconcile(dir, want, false))
	data, err = os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	require.Equal(t, changedLegacy, data)

	legacy := []byte("new\n" + generatedMarker + "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), legacy, 0600))
	require.NoError(t, reconcile(dir, want, false))
	data, err = os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	require.Equal(t, want[name], data)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "eci-preset-obsolete.md"), legacy, 0600))
	require.Error(t, reconcile(dir, want, false))
	data, err = os.ReadFile(filepath.Join(dir, "eci-preset-obsolete.md"))
	require.NoError(t, err)
	require.Equal(t, legacy, data)
}

// TestReconcilePreflightBlocksNonRegularTargets checks that conflicts cause no writes.
//
// Example: a directory with a preset filename is never overwritten.
func TestReconcilePreflightBlocksNonRegularTargets(t *testing.T) {
	dir := t.TempDir()
	name := "eci-preset-a.md"
	require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0700))
	want := map[string][]byte{
		name:              stamp([]byte("new\n")),
		"eci-preset-b.md": stamp([]byte("second\n")),
	}
	require.Error(t, reconcile(dir, want, false))
	info, err := os.Stat(filepath.Join(dir, name))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	_, err = os.Stat(filepath.Join(dir, "eci-preset-b.md"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestReconcileUpdatesUnmodifiedOutput checks normal regeneration after source edits.
//
// Example: changing a selector replaces an intact generated definition.
func TestReconcileUpdatesUnmodifiedOutput(t *testing.T) {
	dir := t.TempDir()
	name := "eci-preset-a.md"
	before := map[string][]byte{name: stamp([]byte("first\n"))}
	after := map[string][]byte{name: stamp([]byte("second\n"))}
	require.NoError(t, reconcile(dir, before, false))
	require.NoError(t, reconcile(dir, after, false))
	data, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	require.Equal(t, after[name], data)
}

// TestPublishFilePreservesChangedTargets checks revalidation at the publication boundary.
//
// Example: a user edit made after preflight remains intact.
func TestPublishFilePreservesChangedTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eci-preset-a.md")
	before := stamp([]byte("before\n"))
	after := stamp([]byte("after\n"))
	edit := []byte("user edit\n")
	require.NoError(t, os.WriteFile(path, edit, 0600))
	require.Error(t, publishFile(path, before, after))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, edit, data)
	require.Error(t, publishFile(path, nil, after))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, edit, data)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestRemoveUnchangedPreservesEditedOutput checks obsolete-file revalidation.
//
// Example: a user-edited obsolete file is not removed.
func TestRemoveUnchangedPreservesEditedOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eci-preset-old.md")
	before := stamp([]byte("before\n"))
	edit := []byte("user edit\n")
	require.NoError(t, os.WriteFile(path, edit, 0600))
	require.Error(t, removeUnchanged(path, before))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, edit, data)
}

// TestPublishHonorsUmask checks that new files retain the process creation policy.
//
// Example: umask 077 prevents group and world access to a new preset.
func TestPublishHonorsUmask(t *testing.T) {
	if path := os.Getenv("ECI_TEST_UMASK_TARGET"); path != "" {
		require.NoError(t, publishFile(path, nil, []byte("test\n")))
		return
	}
	path := filepath.Join(t.TempDir(), "eci-preset-a.md")
	cmd := exec.Command("sh", "-c", "umask 077; exec \"$@\"", "sh", os.Args[0], "-test.run=^TestPublishHonorsUmask$")
	cmd.Env = append(os.Environ(), "ECI_TEST_UMASK_TARGET="+path)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

// TestPublishPreservesExistingMode checks replacement does not broaden permissions.
//
// Example: a private preset remains private after regeneration.
func TestPublishPreservesExistingMode(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0640} {
		dir := t.TempDir()
		path := filepath.Join(dir, "eci-preset-a.md")
		before := []byte("before\n")
		after := []byte("after\n")
		require.NoError(t, os.WriteFile(path, before, 0600))
		require.NoError(t, os.Chmod(path, mode))
		require.NoError(t, publishFile(path, before, after))
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, mode, info.Mode().Perm())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, after, data)
	}
}
