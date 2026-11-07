// Command eci-safe-import resolves one regular read-only source alias, then
// copies its opened file into a fixed ECI current-session leaf without
// following session or destination symlinks.
package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// LeafWaitReport selects the fixed current-session wait-report leaf.
	LeafWaitReport = "wait-report"
	// LeafSingletonManifest selects the fixed singleton manifest leaf.
	LeafSingletonManifest = "singleton-manifest"
	// LeafAggregateManifest selects the fixed aggregate-member manifest leaf.
	LeafAggregateManifest = "aggregate-manifest"

	waitReportName        = "eci_user_owned_wait.md"
	singletonManifestName = "eci-required-critics.json"
	temporaryLeafPrefix   = ".eci-safe-import."
)

// Request identifies a regular source and one fixed destination leaf below a
// canonical proof-root current-session directory.
type Request struct {
	ProofRoot  string
	SessionDir string
	Leaf       string
	RepoID     string
	Source     string
}

// importHooks make descriptor-swap unit tests deterministic. Normal imports
// always use the zero value and therefore expose no race-control interface.
type importHooks struct {
	afterSourceOpen     func()
	afterExistingOpen   func()
	beforeAbsentPublish func()
}

// errInvalidRequest distinguishes a rejected selector or session binding from
// an ordinary filesystem error.
var errInvalidRequest = errors.New("invalid safe import request")

// main parses the fixed-leaf command interface and reports one concise error
// when a concrete source or destination boundary cannot be satisfied.
func main() {
	request, err := parseRequest(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "eci-safe-import: %v\n", err)
		os.Exit(2)
	}
	if err := Import(request); err != nil {
		fmt.Fprintf(os.Stderr, "eci-safe-import: %v\n", err)
		os.Exit(1)
	}
}

// Import copies request.Source to the one fixed leaf selected by request.
func Import(request Request) error {
	return importRequest(request, importHooks{})
}

// importRequest carries out one descriptor-bound copy. Its hooks are only for
// deterministic tests of pathname swaps after an FD has already been opened.
func importRequest(request Request, hooks importHooks) error {
	destination, err := leafDestination(request.Leaf, request.RepoID)
	if err != nil {
		return err
	}
	rootFD, err := openCanonicalDirectory(request.ProofRoot)
	if err != nil {
		return fmt.Errorf("proof root: %w", err)
	}
	defer unix.Close(rootFD)

	sessionFD, err := openSessionDirectory(rootFD, request.ProofRoot, request.SessionDir)
	if err != nil {
		return fmt.Errorf("session directory: %w", err)
	}
	defer unix.Close(sessionFD)

	sourceFD, err := openRegularSource(request.Source)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	defer unix.Close(sourceFD)
	if hooks.afterSourceOpen != nil {
		hooks.afterSourceOpen()
	}

	var destinationStat unix.Stat_t
	err = statNamed(sessionFD, destination, &destinationStat)
	switch {
	case err == nil:
		return replaceExistingLeaf(sessionFD, destination, sourceFD, hooks)
	case errors.Is(err, unix.ENOENT):
		return publishAbsentLeaf(sessionFD, destination, sourceFD, hooks)
	default:
		return fmt.Errorf("inspect destination: %w", err)
	}
}

// parseRequest accepts only fixed fields. It deliberately has no destination
// pathname argument: the selected leaf determines the only output location.
func parseRequest(arguments []string) (Request, error) {
	flags := flag.NewFlagSet("eci-safe-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var request Request
	flags.StringVar(&request.ProofRoot, "proof-root", "", "canonical proof root")
	flags.StringVar(&request.SessionDir, "session-dir", "", "canonical current session directory")
	flags.StringVar(&request.Leaf, "leaf", "", "fixed destination leaf")
	flags.StringVar(&request.RepoID, "repo-id", "", "aggregate repository ID")
	flags.StringVar(&request.Source, "source", "", "regular source file")
	if err := flags.Parse(arguments); err != nil {
		return Request{}, fmt.Errorf("%w: parse arguments", errInvalidRequest)
	}
	if flags.NArg() != 0 {
		return Request{}, fmt.Errorf("%w: positional arguments are not accepted", errInvalidRequest)
	}
	if request.ProofRoot == "" || request.SessionDir == "" || request.Leaf == "" || request.Source == "" {
		return Request{}, fmt.Errorf("%w: proof root, session directory, leaf, and source are required", errInvalidRequest)
	}
	if _, err := leafDestination(request.Leaf, request.RepoID); err != nil {
		return Request{}, err
	}
	return request, nil
}

// leafDestination maps a closed leaf selector to its one session-relative
// filename. Aggregate names are derived only from the bounded repository ID.
func leafDestination(leaf string, repoID string) (string, error) {
	switch leaf {
	case LeafWaitReport:
		if repoID != "" {
			return "", fmt.Errorf("%w: wait report does not use a repository ID", errInvalidRequest)
		}
		return waitReportName, nil
	case LeafSingletonManifest:
		if repoID != "" {
			return "", fmt.Errorf("%w: singleton manifest does not use a repository ID", errInvalidRequest)
		}
		return singletonManifestName, nil
	case LeafAggregateManifest:
		if !validRepoID(repoID) {
			return "", fmt.Errorf("%w: aggregate repository ID", errInvalidRequest)
		}
		return "eci-aggregate." + repoID + ".required-critics.json", nil
	default:
		return "", fmt.Errorf("%w: unknown fixed leaf %q", errInvalidRequest, leaf)
	}
}

// validRepoID matches the aggregate member ID grammar shared by the shell
// lifecycle commands without treating an ID as a pathname.
func validRepoID(value string) bool {
	if len(value) == 0 || len(value) > 32 || !isASCIIAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isASCIIAlphaNumeric(value[index]) && value[index] != '-' && value[index] != '_' {
			return false
		}
	}
	return true
}

// isASCIIAlphaNumeric reports whether byte is an ASCII letter or digit.
func isASCIIAlphaNumeric(byte byte) bool {
	return byte >= 'a' && byte <= 'z' || byte >= 'A' && byte <= 'Z' || byte >= '0' && byte <= '9'
}

// openCanonicalDirectory opens an absolute canonical directory by descriptor
// traversal, refusing every symlink in its component path.
func openCanonicalDirectory(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, fmt.Errorf("%w: proof root must be canonical and absolute", errInvalidRequest)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return -1, fmt.Errorf("%w: proof root is not a canonical directory", errInvalidRequest)
	}
	fd, err := openNoFollowPath(path, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		unix.Close(fd)
		return -1, errors.New("not a directory")
	}
	return fd, nil
}

// openSessionDirectory opens exactly one current-session child of proofRoot.
func openSessionDirectory(rootFD int, proofRoot string, sessionDir string) (int, error) {
	if !filepath.IsAbs(sessionDir) || filepath.Clean(sessionDir) != sessionDir {
		return -1, fmt.Errorf("%w: session directory must be canonical and absolute", errInvalidRequest)
	}
	relative, err := filepath.Rel(proofRoot, sessionDir)
	if err != nil || relative == "." || strings.Contains(relative, string(filepath.Separator)) ||
		relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !validSessionID(relative) {
		return -1, fmt.Errorf("%w: session directory is not a direct proof-root child", errInvalidRequest)
	}
	if sessionDir != filepath.Join(proofRoot, relative) {
		return -1, fmt.Errorf("%w: session directory binding", errInvalidRequest)
	}
	fd, err := unix.Openat(rootFD, relative, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		unix.Close(fd)
		return -1, errors.New("not a directory")
	}
	return fd, nil
}

// validSessionID matches the proof-state session directory grammar.
func validSessionID(value string) bool {
	if len(value) == 0 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !isASCIIAlphaNumeric(value[index]) && value[index] != '-' && value[index] != '_' {
			return false
		}
	}
	return true
}

// openRegularSource resolves the caller's read-only source aliases once, then
// opens the resolved pathname through no-follow descriptors and proves the FD
// is a regular file before any copy begins.
func openRegularSource(source string) (int, error) {
	if source == "" {
		return -1, fmt.Errorf("%w: source is required", errInvalidRequest)
	}
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return -1, err
	}
	fd, err := openNoFollowPath(resolvedSource, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return -1, errors.New("not a regular file")
	}
	return fd, nil
}

// openNoFollowPath opens every pathname component from an already-opened
// directory descriptor, preventing intermediate symlink traversal as well as
// a symlink at the final target.
func openNoFollowPath(path string, finalFlags int) (int, error) {
	cleaned := filepath.Clean(path)
	if cleaned == "" {
		return -1, errors.New("empty path")
	}
	var fd int
	var err error
	var components []string
	if filepath.IsAbs(cleaned) {
		fd, err = unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		components = strings.Split(strings.TrimPrefix(cleaned, string(filepath.Separator)), string(filepath.Separator))
	} else {
		fd, err = unix.Open(".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		components = strings.Split(cleaned, string(filepath.Separator))
	}
	if err != nil {
		return -1, err
	}
	if len(components) == 1 && components[0] == "" {
		return fd, nil
	}
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if index < len(components)-1 {
			flags |= unix.O_DIRECTORY
		} else {
			flags |= finalFlags
		}
		nextFD, openErr := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = nextFD
	}
	return fd, nil
}

// replaceExistingLeaf writes through a validated existing destination FD only
// when it differs from the source FD, then rejects a pathname binding change
// before reporting success.
func replaceExistingLeaf(sessionFD int, destination string, sourceFD int, hooks importHooks) error {
	destinationFD, err := unix.Openat(sessionFD, destination, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(destinationFD)
	stat, err := validateDestinationFD(destinationFD)
	if err != nil {
		return err
	}
	var sourceStat unix.Stat_t
	if err := unix.Fstat(sourceFD, &sourceStat); err != nil {
		return err
	}
	sameInode := sourceStat.Dev == stat.Dev && sourceStat.Ino == stat.Ino
	if hooks.afterExistingOpen != nil {
		hooks.afterExistingOpen()
	}
	if !sameInode {
		if err := unix.Ftruncate(destinationFD, 0); err != nil {
			return err
		}
		if _, err := unix.Seek(destinationFD, 0, io.SeekStart); err != nil {
			return err
		}
		if err := copyDescriptor(sourceFD, destinationFD); err != nil {
			return err
		}
		if err := unix.Fsync(destinationFD); err != nil {
			return err
		}
	}
	if !destinationBindingMatches(sessionFD, destination, stat) {
		return errors.New("destination changed during descriptor-bound write")
	}
	return nil
}

// publishAbsentLeaf creates a private session-local file, copies the opened
// source into it, then creates the destination with linkat's no-replace rule.
func publishAbsentLeaf(sessionFD int, destination string, sourceFD int, hooks importHooks) error {
	temporaryName, temporaryFD, err := createPrivateTemporaryLeaf(sessionFD)
	if err != nil {
		return err
	}
	defer unix.Close(temporaryFD)
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = unix.Unlinkat(sessionFD, temporaryName, 0)
		}
	}()

	if err := copyDescriptor(sourceFD, temporaryFD); err != nil {
		return err
	}
	if err := unix.Fsync(temporaryFD); err != nil {
		return err
	}
	if hooks.beforeAbsentPublish != nil {
		hooks.beforeAbsentPublish()
	}
	if err := linkAt(sessionFD, temporaryName, sessionFD, destination); err != nil {
		return fmt.Errorf("publish without replacement: %w", err)
	}
	if err := unix.Unlinkat(sessionFD, temporaryName, 0); err != nil {
		return err
	}
	removeTemporary = false
	var stat unix.Stat_t
	if err := statNamed(sessionFD, destination, &stat); err != nil {
		return err
	}
	if !validDestinationStat(stat) {
		return errors.New("published destination is unsafe")
	}
	return nil
}

// linkAt creates newPath from oldPath using descriptor-relative Linux linkat.
// A destination that appears after the initial absence check makes this call
// fail with EEXIST; it never replaces that raced name.
func linkAt(oldDirectoryFD int, oldPath string, newDirectoryFD int, newPath string) error {
	return unix.Linkat(oldDirectoryFD, oldPath, newDirectoryFD, newPath, 0)
}

// createPrivateTemporaryLeaf makes a mode-0600 regular file under the opened
// session directory. Its random name never selects a public destination.
func createPrivateTemporaryLeaf(sessionFD int) (string, int, error) {
	for {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", -1, err
		}
		name := fmt.Sprintf("%s%x", temporaryLeafPrefix, entropy)
		fd, err := unix.Openat(sessionFD, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", -1, err
		}
		if _, err := validateDestinationFD(fd); err != nil {
			unix.Close(fd)
			unix.Unlinkat(sessionFD, name, 0)
			return "", -1, err
		}
		return name, fd, nil
	}
}

// validateDestinationFD verifies the opened destination's concrete type,
// owner, and one-link binding before it is written.
func validateDestinationFD(fd int) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return unix.Stat_t{}, err
	}
	if !validDestinationStat(stat) {
		return unix.Stat_t{}, errors.New("destination is not a current-user-owned single-link regular file")
	}
	return stat, nil
}

// validDestinationStat reports whether stat is safe to use as one fixed
// current-session leaf without following another inode through a hard link.
func validDestinationStat(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && int(stat.Uid) == os.Geteuid() && stat.Nlink == 1
}

// destinationBindingMatches proves the fixed session leaf still names the
// same validated inode after an existing-destination descriptor write.
func destinationBindingMatches(sessionFD int, destination string, expected unix.Stat_t) bool {
	var current unix.Stat_t
	if err := statNamed(sessionFD, destination, &current); err != nil {
		return false
	}
	return current.Dev == expected.Dev && current.Ino == expected.Ino && validDestinationStat(current)
}

// statNamed reads descriptor-relative entry metadata without following its final symlink.
//
// Example: a destination symlink is inspected itself, including when its target is absent.
func statNamed(parentFD int, name string, metadata *unix.Stat_t) error {
	// fstatat with AT_SYMLINK_NOFOLLOW reads the entry itself without opening it,
	// so FIFOs and entries without read permission are never opened for I/O.
	return unix.Fstatat(parentFD, name, metadata, unix.AT_SYMLINK_NOFOLLOW)
}

// copyDescriptor copies all bytes from an already-validated source FD to an
// already-validated destination FD without reopening either pathname.
func copyDescriptor(sourceFD int, destinationFD int) error {
	buffer := make([]byte, 32*1024)
	for {
		readCount, readErr := unix.Read(sourceFD, buffer)
		if readCount > 0 {
			if err := writeAll(destinationFD, buffer[:readCount]); err != nil {
				return err
			}
		}
		if readErr == nil {
			if readCount == 0 {
				return nil
			}
			continue
		}
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		return readErr
	}
}

// writeAll writes bytes to one descriptor even when the kernel accepts a
// partial regular-file write.
func writeAll(fd int, bytes []byte) error {
	for len(bytes) > 0 {
		written, err := unix.Write(fd, bytes)
		if written > 0 {
			bytes = bytes[written:]
		}
		if err == nil {
			continue
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
	return nil
}
