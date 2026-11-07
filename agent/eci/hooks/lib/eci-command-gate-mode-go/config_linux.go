//go:build linux

package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// pathOpenFlag obtains metadata without opening the object for I/O (Linux O_PATH).
	//
	// Example: paired with O_NOFOLLOW, it opens a dangling symlink itself.
	pathOpenFlag = 0x200000
	// directoryOpenFlags open one directory component without following aliases.
	directoryOpenFlags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// regularReadFlags open an existing configuration file without following aliases.
	regularReadFlags = syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// regularWriteFlags create a temporary configuration file without following aliases.
	regularWriteFlags = syscall.O_WRONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	// ownedDirectoryMode is the required mode for created configuration directories.
	ownedDirectoryMode = 0o700
	// ownedRegularFileMode is the required mode for configuration files.
	ownedRegularFileMode = 0o600
)

// ConfigStore identifies the user-owned root containing command-gate configuration.
//
// Example: ConfigStore{Root: "/home/user/.config"} addresses `.config/eci/command-gate-mode`.
type ConfigStore struct {
	Root string
}

// configPathError preserves the diagnostic state associated with a path failure.
//
// Example: an unsafe symlink path becomes ConfigStateInvalidPath while normal work falls back to permissive mode.
type configPathError struct {
	state ConfigState
	err   error
}

// Error returns the path failure with its configuration classification.
//
// Example: callers can retain the original errno through Unwrap.
func (err *configPathError) Error() string {
	return fmt.Sprintf("config path rejected (%s): %v", err.state, err.err)
}

// Unwrap returns the underlying operating-system error.
//
// Example: errors.Is can distinguish a missing path from an unsafe path.
func (err *configPathError) Unwrap() error {
	return err.err
}

// configDirectoryHandle owns one descriptor chain opened beneath the configuration root.
//
// Example: Close releases the component descriptors after a read or write.
type configDirectoryHandle struct {
	fd       int
	identity fileIdentity
	parentFD int
	name     string
	path     string
	ownedFDs []int
}

// configFileHandle owns one descriptor validated against its parent directory entry.
//
// Example: a temporary file remains tied to its descriptor through rename validation.
type configFileHandle struct {
	fd       int
	identity fileIdentity
	parentFD int
	name     string
}

// fileIdentity captures the fields needed to detect replacement and aliasing.
//
// Example: descriptor and directory-entry identities must match before mutation continues.
type fileIdentity struct {
	device uint64
	inode  uint64
	mode   uint32
	uid    uint32
	nlink  uint64
	size   int64
}

// ReadMode returns the configured mode or a permissive fallback when configuration cannot be used.
//
// Example: an absent root returns ModePermissive with ConfigStateMissing.
//
// An unreadable or malformed preference record must not manufacture enforcement. Its diagnostic
// state remains available to permissive telemetry while ordinary command admission continues.
func (store ConfigStore) ReadMode() ModeState {
	parent, err := store.openConfigDirectory(false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return ModeState{Mode: ModePermissive, ConfigState: ConfigStateMissing}
		}
		return modeStateForConfigError(err)
	}
	defer parent.close()

	config, err := openConfigFile(parent, ConfigFileName, false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return ModeState{Mode: ModePermissive, ConfigState: ConfigStateMissing}
		}
		return modeStateForConfigError(err)
	}
	defer config.close()

	if config.identity.size > MaxConfigBytes {
		return ModeState{Mode: ModePermissive, ConfigState: ConfigStateOversize}
	}
	value, err := readConfigBytes(config)
	if err != nil {
		return ModeState{Mode: ModePermissive, ConfigState: ConfigStateReadError}
	}
	if err := validateConfigFileIdentity(config, ConfigFileName); err != nil {
		return modeStateForConfigError(err)
	}
	if len(value) > MaxConfigBytes {
		return ModeState{Mode: ModePermissive, ConfigState: ConfigStateOversize}
	}
	switch string(value) {
	case string(ModePermissive) + "\n":
		return ModeState{Mode: ModePermissive, ConfigState: ConfigStateConfiguredPermissive}
	case string(ModeEnforcing) + "\n":
		return ModeState{Mode: ModeEnforcing, ConfigState: ConfigStateConfiguredEnforcing}
	default:
		return ModeState{Mode: ModePermissive, ConfigState: ConfigStateInvalidBytes}
	}
}

// SetMode atomically installs one exact, owner-only mode record and rereads it.
//
// Example: SetMode(ModeEnforcing) replaces the configuration only after fsync validation.
func (store ConfigStore) SetMode(mode Mode) (returnErr error) {
	if mode != ModePermissive && mode != ModeEnforcing {
		return &UsageError{Reason: fmt.Sprintf("unsupported mode %q", mode)}
	}
	parent, err := store.openConfigDirectory(true)
	if err != nil {
		return err
	}
	defer parent.close()

	temporaryName, err := newTemporaryConfigName()
	if err != nil {
		return err
	}
	temporary, err := openConfigFile(parent, temporaryName, true)
	if err != nil {
		return err
	}
	defer temporary.close()
	renamed := false
	defer func() {
		if renamed {
			return
		}
		if cleanupErr := removeTemporaryConfig(parent, temporary); cleanupErr != nil && returnErr == nil {
			// The primary operation error is returned by the caller. Cleanup is
			// deliberately identity-checked and cannot alter the destination.
			returnErr = fmt.Errorf("remove temporary configuration: %w", cleanupErr)
		}
	}()

	value := []byte(string(mode) + "\n")
	if err := writeConfigBytes(temporary, value); err != nil {
		return err
	}
	if err := validateConfigFileIdentity(temporary, temporaryName); err != nil {
		return err
	}
	if err := syscall.Fsync(temporary.fd); err != nil {
		return fmt.Errorf("fsync temporary configuration: %w", err)
	}
	if err := validateConfigFileIdentity(temporary, temporaryName); err != nil {
		return err
	}
	if err := syscall.Renameat(parent.fd, temporaryName, parent.fd, ConfigFileName); err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	renamed = true
	if err := validateConfigFileIdentity(temporary, ConfigFileName); err != nil {
		return err
	}
	if err := syscall.Fsync(parent.fd); err != nil {
		return fmt.Errorf("fsync configuration directory: %w", err)
	}

	observed := store.ReadMode()
	wantState := ConfigStateConfiguredPermissive
	if mode == ModeEnforcing {
		wantState = ConfigStateConfiguredEnforcing
	}
	if observed.Mode != mode || observed.ConfigState != wantState {
		return fmt.Errorf("configuration reread = %#v, want mode=%q state=%q", observed, mode, wantState)
	}
	return nil
}

// modeStateForConfigError converts a classified path failure into a permissive fallback state.
//
// Example: invalid metadata is retained for telemetry without turning ordinary work into a denial.
func modeStateForConfigError(err error) ModeState {
	var pathErr *configPathError
	if errors.As(err, &pathErr) {
		return ModeState{Mode: ModePermissive, ConfigState: pathErr.state}
	}
	return ModeState{Mode: ModePermissive, ConfigState: ConfigStateReadError}
}

// openConfigDirectory opens the configured root and its owned `eci` child by descriptor.
//
// Example: create=true is used only by SetMode; reads never create state.
func (store ConfigStore) openConfigDirectory(create bool) (*configDirectoryHandle, error) {
	root, err := openDirectoryPath(store.Root, create)
	if err != nil {
		return nil, err
	}
	if err := validateOwnedWritableDirectory(root.fd); err != nil {
		root.close()
		return nil, err
	}
	child, err := openOwnedDirectory(root, ConfigDirectoryName, create)
	if err != nil {
		root.close()
		return nil, err
	}
	root.close()
	return child, nil
}

// openDirectoryPath traverses an absolute normalized path without following components.
//
// Example: `/home/user/.config` is opened one directory descriptor at a time.
func openDirectoryPath(path string, create bool) (*configDirectoryHandle, error) {
	if err := validateAbsoluteNormalizedPath(path); err != nil {
		return nil, err
	}
	fd, err := syscall.Open("/", directoryOpenFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("open root directory: %w", err)
	}
	rootIdentity, err := statDescriptor(fd)
	if err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("stat root directory: %w", err)
	}
	current := &configDirectoryHandle{
		fd:       fd,
		identity: rootIdentity,
		parentFD: -1,
		path:     "/",
		ownedFDs: []int{fd},
	}
	components := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, component := range components {
		if component == "" {
			continue
		}
		nextFD, openErr := syscall.Openat(current.fd, component, directoryOpenFlags, 0)
		if errors.Is(openErr, syscall.ENOENT) && create {
			if err := validateOwnedWritableDirectory(current.fd); err != nil {
				current.close()
				return nil, err
			}
			if mkdirErr := syscall.Mkdirat(current.fd, component, ownedDirectoryMode); mkdirErr != nil && !errors.Is(mkdirErr, syscall.EEXIST) {
				current.close()
				return nil, &configPathError{state: ConfigStateInvalidPath, err: mkdirErr}
			}
			nextFD, openErr = syscall.Openat(current.fd, component, directoryOpenFlags, 0)
		}
		if openErr != nil {
			current.close()
			if errors.Is(openErr, syscall.ENOENT) {
				return nil, openErr
			}
			return nil, &configPathError{state: ConfigStateInvalidPath, err: openErr}
		}
		nextIdentity, statErr := statDescriptor(nextFD)
		if statErr != nil {
			syscall.Close(nextFD)
			current.close()
			return nil, &configPathError{state: ConfigStateInvalidPath, err: statErr}
		}
		next := &configDirectoryHandle{
			fd:       nextFD,
			identity: nextIdentity,
			parentFD: current.fd,
			name:     component,
			path:     filepath.Join(current.path, component),
			ownedFDs: append(current.ownedFDs, nextFD),
		}
		if err := validateDirectoryIdentity(next); err != nil {
			next.close()
			return nil, err
		}
		current = next
	}
	return current, nil
}

// openOwnedDirectory opens and validates one 0700 directory beneath a parent descriptor.
//
// Example: the `eci` configuration child is created only under a validated parent.
func openOwnedDirectory(parent *configDirectoryHandle, name string, create bool) (*configDirectoryHandle, error) {
	if err := validateDirectoryIdentity(parent); err != nil {
		return nil, err
	}
	childFD, err := syscall.Openat(parent.fd, name, directoryOpenFlags, 0)
	if errors.Is(err, syscall.ENOENT) && create {
		if err := validateOwnedWritableDirectory(parent.fd); err != nil {
			return nil, err
		}
		if mkdirErr := syscall.Mkdirat(parent.fd, name, ownedDirectoryMode); mkdirErr != nil && !errors.Is(mkdirErr, syscall.EEXIST) {
			return nil, &configPathError{state: ConfigStateInvalidPath, err: mkdirErr}
		}
		childFD, err = syscall.Openat(parent.fd, name, directoryOpenFlags, 0)
	}
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil, err
		}
		return nil, &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	identity, err := statDescriptor(childFD)
	if err != nil {
		syscall.Close(childFD)
		return nil, &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	child := &configDirectoryHandle{
		fd:       childFD,
		identity: identity,
		parentFD: parent.fd,
		name:     name,
		path:     filepath.Join(parent.path, name),
		ownedFDs: append(parent.ownedFDs, childFD),
	}
	parent.ownedFDs = nil
	if err := validateOwnedDirectory(child); err != nil {
		child.close()
		return nil, err
	}
	if err := validateDirectoryIdentity(parent); err != nil {
		child.close()
		return nil, err
	}
	return child, nil
}

// openConfigFile opens one configuration file with regular-file identity checks.
//
// Example: create=true is restricted to a unique temporary file name.
func openConfigFile(parent *configDirectoryHandle, name string, create bool) (*configFileHandle, error) {
	if err := validateDirectoryIdentity(parent); err != nil {
		return nil, err
	}
	flags := regularReadFlags
	if create {
		flags = regularWriteFlags
	}
	fd, err := syscall.Openat(parent.fd, name, flags, 0)
	created := false
	if errors.Is(err, syscall.ENOENT) && create {
		fd, err = syscall.Openat(parent.fd, name, flags|syscall.O_CREAT|syscall.O_EXCL, ownedRegularFileMode)
		created = err == nil
	}
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil, err
		}
		state := ConfigStateInvalidPath
		if !create {
			state = ConfigStateInvalidPath
		}
		return nil, &configPathError{state: state, err: err}
	}
	if created {
		if err := syscall.Fchmod(fd, ownedRegularFileMode); err != nil {
			syscall.Close(fd)
			return nil, &configPathError{state: ConfigStateInvalidMetadata, err: err}
		}
	}
	identity, err := statDescriptor(fd)
	if err != nil {
		syscall.Close(fd)
		return nil, &configPathError{state: ConfigStateInvalidMetadata, err: err}
	}
	file := &configFileHandle{fd: fd, identity: identity, parentFD: parent.fd, name: name}
	if err := validateRegularMetadata(identity); err != nil {
		file.close()
		return nil, err
	}
	if err := validateConfigFileIdentity(file, name); err != nil {
		file.close()
		return nil, err
	}
	return file, nil
}

// validateAbsoluteNormalizedPath rejects relative, aliased, and NUL-containing roots.
//
// Example: `/home/user/.config` is valid while `./.config` and `/home/../user` are not.
func validateAbsoluteNormalizedPath(path string) error {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("path is not absolute and normalized")}
	}
	return nil
}

// validateOwnedWritableDirectory checks metadata for a user-owned configuration directory.
//
// Example: a world-writable configuration root is diagnostic-only and cannot enable enforcement.
func validateOwnedWritableDirectory(fd int) error {
	identity, err := statDescriptor(fd)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if identity.mode&syscall.S_IFMT != syscall.S_IFDIR || identity.uid != uint32(os.Getuid()) || identity.mode&0o300 != 0o300 || identity.mode&0o002 != 0 || identity.mode&0o7000 != 0 {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("creation parent metadata rejected")}
	}
	return nil
}

// validateDirectoryIdentity checks a descriptor against its immutable directory entry.
//
// Example: replacing `eci` after opening it is detected before a config file is touched.
func validateDirectoryIdentity(directory *configDirectoryHandle) error {
	observed, err := statDescriptor(directory.fd)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if observed.device != directory.identity.device || observed.inode != directory.identity.inode {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("directory descriptor identity changed")}
	}
	var named fileIdentity
	if directory.parentFD >= 0 {
		named, err = statNamed(directory.parentFD, directory.name)
	} else {
		named, err = statPathNoFollow(directory.path)
	}
	if err != nil {
		return &configPathError{state: ConfigStateInvalidPath, err: err}
	}
	if named.device != observed.device || named.inode != observed.inode {
		return &configPathError{state: ConfigStateInvalidPath, err: errors.New("directory entry identity changed")}
	}
	return nil
}

// validateOwnedDirectory enforces the exact owner and mode for the `eci` child.
//
// Example: an existing group-writable configuration directory is rejected.
func validateOwnedDirectory(directory *configDirectoryHandle) error {
	if err := validateDirectoryIdentity(directory); err != nil {
		return err
	}
	if directory.identity.mode&syscall.S_IFMT != syscall.S_IFDIR || directory.identity.uid != uint32(os.Getuid()) || directory.identity.mode&0o7777 != ownedDirectoryMode {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("owned directory metadata rejected")}
	}
	return nil
}

// validateRegularMetadata enforces the exact owner-only config file metadata.
//
// Example: a hard-linked config file is rejected because nlink must be one.
func validateRegularMetadata(identity fileIdentity) error {
	if identity.mode&syscall.S_IFMT != syscall.S_IFREG || identity.uid != uint32(os.Getuid()) || identity.mode&0o7777 != ownedRegularFileMode || identity.nlink != 1 {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("regular configuration metadata rejected")}
	}
	return nil
}

// validateConfigFileIdentity checks the open descriptor and current named entry.
//
// Example: renaming a temporary file before validation is detected by inode mismatch.
func validateConfigFileIdentity(file *configFileHandle, name string) error {
	observed, err := statDescriptor(file.fd)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidMetadata, err: err}
	}
	if err := validateRegularMetadata(observed); err != nil {
		return err
	}
	if observed.device != file.identity.device || observed.inode != file.identity.inode {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("config descriptor identity changed")}
	}
	named, err := statNamed(file.parentFD, name)
	if err != nil {
		return &configPathError{state: ConfigStateInvalidMetadata, err: err}
	}
	if err := validateRegularMetadata(named); err != nil {
		return err
	}
	if named.device != observed.device || named.inode != observed.inode {
		return &configPathError{state: ConfigStateInvalidMetadata, err: errors.New("config entry identity changed")}
	}
	return nil
}

// statDescriptor reads identity metadata without path traversal.
//
// Example: fstat is the first half of every descriptor-vs-name validation.
func statDescriptor(fd int) (fileIdentity, error) {
	var metadata syscall.Stat_t
	if err := syscall.Fstat(fd, &metadata); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentityFromStat(metadata), nil
}

// statNamed reads one directory entry without following a symlink.
//
// Example: a dangling symlink yields its own metadata rather than its target's.
func statNamed(parentFD int, name string) (fileIdentity, error) {
	// O_PATH preserves entry metadata without requiring read access or activating
	// special files; syscall.Fstatat is unavailable on some Linux architectures.
	fd, err := syscall.Openat(parentFD, name, pathOpenFlag|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fileIdentity{}, err
	}
	identity, statErr := statDescriptor(fd)
	return identity, errors.Join(statErr, syscall.Close(fd))
}

// statPathNoFollow validates a root path whose parent descriptor is not retained.
//
// Example: the absolute root path is checked with lstat after traversal.
func statPathNoFollow(path string) (fileIdentity, error) {
	var metadata syscall.Stat_t
	if err := syscall.Lstat(path, &metadata); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentityFromStat(metadata), nil
}

// fileIdentityFromStat converts Linux stat metadata into the stable comparison fields.
//
// Example: the conversion keeps comparisons independent of syscall struct layout.
func fileIdentityFromStat(metadata syscall.Stat_t) fileIdentity {
	return fileIdentity{
		device: uint64(metadata.Dev),
		inode:  uint64(metadata.Ino),
		mode:   metadata.Mode,
		uid:    metadata.Uid,
		nlink:  uint64(metadata.Nlink),
		size:   int64(metadata.Size),
	}
}

// readConfigBytes reads at most MaxConfigBytes plus one byte from a validated file.
//
// Example: an exact 33-byte read is classified as oversize without unbounded allocation.
func readConfigBytes(file *configFileHandle) ([]byte, error) {
	value := make([]byte, MaxConfigBytes+1)
	read := 0
	for read < len(value) {
		n, err := syscall.Read(file.fd, value[read:])
		read += n
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
	}
	return value[:read], nil
}

// writeConfigBytes writes every configuration byte and rejects short writes.
//
// Example: a partial write cannot be mistaken for a valid mode record.
func writeConfigBytes(file *configFileHandle, value []byte) error {
	written := 0
	for written < len(value) {
		n, err := syscall.Write(file.fd, value[written:])
		written += n
		if err != nil {
			return err
		}
		if n == 0 {
			return syscall.EIO
		}
	}
	return nil
}

// newTemporaryConfigName creates a collision-resistant hidden config filename.
//
// Example: each SetMode call uses a distinct O_EXCL temporary entry.
func newTemporaryConfigName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate temporary configuration name: %w", err)
	}
	return fmt.Sprintf(".command-gate-mode.%d.%x", os.Getpid(), random), nil
}

// removeTemporaryConfig removes only an identity-validated unrenamed temporary file.
//
// Example: cleanup cannot unlink an attacker-replaced name.
func removeTemporaryConfig(parent *configDirectoryHandle, file *configFileHandle) error {
	if err := validateConfigFileIdentity(file, file.name); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return err
	}
	if err := syscall.Unlinkat(parent.fd, file.name); err != nil {
		return err
	}
	return nil
}

// close closes a directory descriptor and any descriptors retained for its path chain.
//
// Example: a failed traversal releases every already-open component.
func (directory *configDirectoryHandle) close() {
	if directory == nil {
		return
	}
	for index := len(directory.ownedFDs) - 1; index >= 0; index-- {
		_ = syscall.Close(directory.ownedFDs[index])
	}
	directory.ownedFDs = nil
}

// close releases a validated configuration file descriptor.
//
// Example: callers defer close immediately after openConfigFile succeeds.
func (file *configFileHandle) close() {
	if file == nil || file.fd < 0 {
		return
	}
	_ = syscall.Close(file.fd)
	file.fd = -1
}
