package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestConfigStoreMissingPathDefaultsToPermissive checks the absent-file contract.
//
// Example: a first invocation without a configuration file must remain permissive.
func TestConfigStoreMissingPathDefaultsToPermissive(t *testing.T) {
	store := newTestConfigStore(t)

	state := store.ReadMode()
	if want := (ModeState{Mode: ModePermissive, ConfigState: ConfigStateMissing}); state != want {
		t.Fatalf("ReadMode() = %#v, want %#v", state, want)
	}
}

// TestConfigStoreSetAndReadPreservesExactValues checks atomic mode values and metadata.
//
// Example: setting enforcing must persist exactly `enforcing\n` in a 0600 file.
func TestConfigStoreSetAndReadPreservesExactValues(t *testing.T) {
	store := newTestConfigStore(t)

	for _, mode := range []Mode{ModePermissive, ModeEnforcing} {
		if err := store.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%q): %v", mode, err)
		}
		state := store.ReadMode()
		want := ModeState{
			Mode:        mode,
			ConfigState: ConfigStateConfiguredPermissive,
		}
		if mode == ModeEnforcing {
			want.ConfigState = ConfigStateConfiguredEnforcing
		}
		if state != want {
			t.Fatalf("ReadMode() = %#v, want %#v", state, want)
		}
		configPath := filepath.Join(store.Root, ConfigDirectoryName, ConfigFileName)
		contents, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if string(contents) != string(mode)+"\n" {
			t.Fatalf("config bytes = %q, want %q", contents, string(mode)+"\n")
		}
		info, err := os.Lstat(configPath)
		if err != nil {
			t.Fatalf("stat config: %v", err)
		}
		if info.Mode().Perm() != 0o600 || info.Mode().IsRegular() == false || info.Sys().(*syscall.Stat_t).Nlink != 1 {
			t.Fatalf("config metadata = mode %04o regular=%v nlink=%d", info.Mode().Perm(), info.Mode().IsRegular(), info.Sys().(*syscall.Stat_t).Nlink)
		}
	}
}

// TestConfigStoreMalformedValuesFallBackToPermissive checks invalid byte and size states.
//
// Example: CRLF, unknown values, and oversized data must not turn ordinary work into a denial.
func TestConfigStoreMalformedValuesFallBackToPermissive(t *testing.T) {
	store := newTestConfigStore(t)
	configDir := filepath.Join(store.Root, ConfigDirectoryName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatalf("chmod config directory: %v", err)
	}

	values := [][]byte{
		{},
		[]byte("permissive\r\n"),
		[]byte("permissive\nextra"),
		[]byte("observe\n"),
		[]byte("x"),
	}
	for _, value := range values {
		configPath := filepath.Join(configDir, ConfigFileName)
		if err := os.WriteFile(configPath, value, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		if err := os.Chmod(configPath, 0o600); err != nil {
			t.Fatalf("chmod fixture: %v", err)
		}
		state := store.ReadMode()
		if state.Mode != ModePermissive {
			t.Fatalf("ReadMode(%q) mode = %q, want permissive", value, state.Mode)
		}
		if state.ConfigState != ConfigStateInvalidBytes {
			t.Fatalf("ReadMode(%q) state = %q, want invalid-bytes", value, state.ConfigState)
		}
		if err := os.Remove(configPath); err != nil {
			t.Fatalf("remove fixture: %v", err)
		}
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	if err := os.WriteFile(configPath, make([]byte, MaxConfigBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized fixture: %v", err)
	}
	state := store.ReadMode()
	if want := (ModeState{Mode: ModePermissive, ConfigState: ConfigStateOversize}); state != want {
		t.Fatalf("oversized ReadMode() = %#v, want %#v", state, want)
	}
}

// TestConfigStoreWorldWritableModeRecordFallsBackToPermissive checks malformed mode-file metadata.
//
// Example: a group- or world-writable mode record is ignored rather than enabling enforcement by accident.
func TestConfigStoreWorldWritableModeRecordFallsBackToPermissive(t *testing.T) {
	store := newTestConfigStore(t)
	if err := store.SetMode(ModeEnforcing); err != nil {
		t.Fatalf("seed enforcing mode: %v", err)
	}

	configPath := filepath.Join(store.Root, ConfigDirectoryName, ConfigFileName)
	if err := os.Chmod(configPath, 0o666); err != nil {
		t.Fatalf("make mode record world writable: %v", err)
	}

	state := store.ReadMode()
	if want := (ModeState{Mode: ModePermissive, ConfigState: ConfigStateInvalidMetadata}); state != want {
		t.Fatalf("world-writable ReadMode() = %#v, want %#v", state, want)
	}
}

// TestConfigStoreWorldWritableConfigRootFallsBackToPermissive checks the preference root metadata.
//
// Example: an enforcing record below a world-writable configuration root cannot accidentally enable enforcement.
func TestConfigStoreWorldWritableConfigRootFallsBackToPermissive(t *testing.T) {
	store := newTestConfigStore(t)
	unsafeRoot := filepath.Join(store.Root, "world-writable-config")
	if err := os.Mkdir(unsafeRoot, 0o700); err != nil {
		t.Fatalf("create unsafe config root: %v", err)
	}
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatalf("make config root world writable: %v", err)
	}
	configDirectory := filepath.Join(unsafeRoot, ConfigDirectoryName)
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	configPath := filepath.Join(configDirectory, ConfigFileName)
	if err := os.WriteFile(configPath, []byte("enforcing\n"), 0o600); err != nil {
		t.Fatalf("write enforcing config: %v", err)
	}

	state := (ConfigStore{Root: unsafeRoot}).ReadMode()
	if want := (ModeState{Mode: ModePermissive, ConfigState: ConfigStateInvalidMetadata}); state != want {
		t.Fatalf("world-writable root ReadMode() = %#v, want %#v", state, want)
	}
}

// TestConfigStoreRejectsUnsafeConfigurationObjects checks path and metadata boundaries.
//
// Example: a symlink or hard link at the configuration name must never alter its target.
func TestConfigStoreRejectsUnsafeConfigurationObjects(t *testing.T) {
	store := newTestConfigStore(t)
	configDir := filepath.Join(store.Root, ConfigDirectoryName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatalf("chmod config directory: %v", err)
	}

	outside := filepath.Join(store.Root, "outside")
	if err := os.WriteFile(outside, []byte("permissive\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	configPath := filepath.Join(configDir, ConfigFileName)
	if err := os.Symlink(outside, configPath); err != nil {
		t.Fatalf("create config symlink: %v", err)
	}
	if state := store.ReadMode(); state.ConfigState != ConfigStateInvalidPath || state.Mode != ModePermissive {
		t.Fatalf("symlink state = %#v, want permissive invalid-path", state)
	}
	if err := store.SetMode(ModeEnforcing); err != nil {
		t.Fatalf("SetMode over config symlink: %v", err)
	}
	if contents, err := os.ReadFile(outside); err != nil || string(contents) != "permissive\n" {
		t.Fatalf("outside target changed: contents=%q err=%v", contents, err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("remove config symlink: %v", err)
	}

	hardlinkTarget := filepath.Join(store.Root, "hardlink-target")
	if err := os.WriteFile(hardlinkTarget, []byte("permissive\n"), 0o600); err != nil {
		t.Fatalf("write hardlink fixture: %v", err)
	}
	if err := os.Link(hardlinkTarget, configPath); err != nil {
		t.Fatalf("create config hardlink: %v", err)
	}
	if state := store.ReadMode(); state.ConfigState != ConfigStateInvalidMetadata || state.Mode != ModePermissive {
		t.Fatalf("hardlink state = %#v, want permissive invalid-metadata", state)
	}
}

// TestConfigStoreIdentityChecksRejectReplacement checks descriptor/name races.
//
// Example: replacing an opened config entry cannot make validation accept the new inode.
func TestConfigStoreIdentityChecksRejectReplacement(t *testing.T) {
	store := newTestConfigStore(t)
	if err := store.SetMode(ModePermissive); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	parent, err := store.openConfigDirectory(false)
	if err != nil {
		t.Fatalf("open config directory: %v", err)
	}
	defer parent.close()
	config, err := openConfigFile(parent, ConfigFileName, false)
	if err != nil {
		t.Fatalf("open config file: %v", err)
	}
	defer config.close()

	configPath := filepath.Join(store.Root, ConfigDirectoryName, ConfigFileName)
	movedPath := filepath.Join(store.Root, ConfigDirectoryName, "moved-config")
	if err := os.Rename(configPath, movedPath); err != nil {
		t.Fatalf("move config: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("enforcing\n"), 0o600); err != nil {
		t.Fatalf("replace config: %v", err)
	}
	if err := validateConfigFileIdentity(config, ConfigFileName); err == nil {
		t.Fatal("validateConfigFileIdentity accepted a replaced entry")
	}

	eciPath := filepath.Join(store.Root, ConfigDirectoryName)
	movedDirectory := filepath.Join(store.Root, "moved-eci")
	if err := os.Rename(eciPath, movedDirectory); err != nil {
		t.Fatalf("move config directory: %v", err)
	}
	if err := os.Mkdir(eciPath, 0o700); err != nil {
		t.Fatalf("replace config directory: %v", err)
	}
	if err := validateDirectoryIdentity(parent); err == nil {
		t.Fatal("validateDirectoryIdentity accepted a replaced directory")
	}
}

// TestConfigStoreRejectsSymlinkRootAndUnsafeCreationParent checks root traversal policy.
//
// Example: an explicit symlink root or world-writable root cannot receive configuration state.
func TestConfigStoreRejectsSymlinkRootAndUnsafeCreationParent(t *testing.T) {
	store := newTestConfigStore(t)
	target := filepath.Join(store.Root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target: %v", err)
	}
	alias := filepath.Join(store.Root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create root alias: %v", err)
	}
	aliasedStore := ConfigStore{Root: alias}
	if state := aliasedStore.ReadMode(); state.ConfigState != ConfigStateInvalidPath {
		t.Fatalf("aliased root state = %#v, want invalid-path", state)
	}
	if err := aliasedStore.SetMode(ModePermissive); err == nil {
		t.Fatal("SetMode accepted an aliased root")
	}
	if _, err := os.Stat(filepath.Join(target, ConfigDirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("aliased target received config directory: %v", err)
	}

	unsafeRoot := filepath.Join(store.Root, "unsafe-root")
	if err := os.Mkdir(unsafeRoot, 0o777); err != nil {
		t.Fatalf("create unsafe root: %v", err)
	}
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatalf("chmod unsafe root: %v", err)
	}
	unsafeStore := ConfigStore{Root: unsafeRoot}
	if err := unsafeStore.SetMode(ModePermissive); err == nil {
		t.Fatal("SetMode accepted a world-writable creation parent")
	}
	if _, err := os.Stat(filepath.Join(unsafeRoot, ConfigDirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("unsafe root received config directory: %v", err)
	}
}

// newTestConfigStore creates a canonical test root beneath the user-owned temporary directory.
//
// Example: every filesystem fixture is isolated from the repository and system temporary roots.
func newTestConfigStore(t *testing.T) ConfigStore {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	base, err := filepath.EvalSymlinks(filepath.Join(home, "tmp"))
	if err != nil {
		t.Fatalf("resolve home temporary directory: %v", err)
	}
	root, err := os.MkdirTemp(base, "eci-command-gate-mode.")
	if err != nil {
		t.Fatalf("create test root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove test root: %v", err)
		}
	})
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod test root: %v", err)
	}
	return ConfigStore{Root: root}
}
