package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// testRootEnv names the provisioned test root. A re-executed test child inherits it and keeps the
// HOME its parent chose instead of provisioning again.
const testRootEnv = "ECI_COMMAND_PLAN_TEST_ROOT"

// testRootPrefix starts every test root name; the owning process ID follows, so a later run can
// remove the roots of runs that died before their cleanup.
//
// Example: run-41532-1729635463.
const testRootPrefix = "run-"

// testPlannerBinary is the planner that build-native-binaries.sh published into the provisioned
// HOME's bin/, the artifact hooks/lib/eci-native-binary.sh verifies at runtime.
//
// Example: TestInstalledBinaryProviderParity runs testPlannerBinary with a JSON request.
var testPlannerBinary string

// TestMain provisions a private install of this checkout and runs the tests against it.
// <root>/home/.omp/agent/eci holds copies of hooks/ and the tracked bin/ scripts plus freshly built
// natives with receipts; <root>/home/.kimi-code is a Kimi peer whose gate binary is a hardlink of
// the omp gate. TMPDIR is <root>/tmp and the umask is 022, so t.TempDir() children pass the native
// loader's group/other-writable check. A failed provision exits 2 before any test runs.
//
// Example: on a clean clone, go test ./... builds the natives into $HOME/.eci-testhome/run-<pid>-*.
func TestMain(m *testing.M) {
	syscall.Umask(0o022)
	if root := os.Getenv(testRootEnv); root != "" {
		if err := adoptParentTestRoot(root); err != nil {
			fmt.Fprintf(os.Stderr, "eci-command-plan tests: %s=%q: %v\n", testRootEnv, root, err)
			os.Exit(2)
		}
		os.Exit(m.Run())
	}
	root, err := newTestRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "eci-command-plan tests: create test root: %v\n", err)
		os.Exit(2)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		received := <-signals
		fmt.Fprintf(os.Stderr, "eci-command-plan tests: %v; removing test root %s\n", received, root)
		_ = os.RemoveAll(root)
		os.Exit(128 + int(received.(syscall.Signal)))
	}()
	code := 2
	if err := provisionTestHome(root); err != nil {
		fmt.Fprintf(os.Stderr, "eci-command-plan tests: provision test HOME: %v\n", err)
	} else {
		code = m.Run()
	}
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintf(os.Stderr, "eci-command-plan tests: remove test root %s: %v\n", root, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// adoptParentTestRoot accepts an inherited root only as a re-executed child of a provisioned run:
// the root carries the run prefix and HOME resolves inside it. Anything else would run the suite
// against the caller's real HOME.
//
// Example: TestEciAuthorityRootsIgnoreECIHOME's child has HOME=<root>/tmp/<test>/001/home.
func adoptParentTestRoot(root string) error {
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), testRootPrefix) {
		return errors.New("not a provisioned test root")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("not a provisioned test root: %v", err)
	}
	home, err := filepath.EvalSymlinks(os.Getenv("HOME"))
	if err != nil {
		return fmt.Errorf("resolve HOME: %w", err)
	}
	if home != root && !strings.HasPrefix(home, root+string(filepath.Separator)) {
		return fmt.Errorf("HOME %s is outside the test root", home)
	}
	testPlannerBinary = filepath.Join(root, "home", ".omp", "agent", "eci", "bin", "eci-command-plan")
	return nil
}

// newTestRoot removes the roots of dead runs and creates this run's root in the private (0700)
// base directory. The returned path is physical: the gate-mode identity and the loader compare
// lexical and resolved paths.
//
// Example: run-41532-1729635463 is removed once process 41532 no longer exists.
func newTestRoot() (string, error) {
	base, err := testRootBase()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := requirePrivateDirectory(base); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		pid, ok := testRootOwner(entry.Name())
		if !ok || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, entry.Name())); err != nil {
			return "", fmt.Errorf("remove dead run's test root: %w", err)
		}
	}
	root, err := os.MkdirTemp(base, fmt.Sprintf("%s%d-", testRootPrefix, os.Getpid()))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

// testRootBase returns the directory that holds test roots, directly below the physical HOME. It
// is not under $HOME/.cache: that is often group-writable or a symlink to such a directory, and
// the native loader rejects a bin directory with a group/other-writable ancestor.
//
// Example: HOME=/home/u selects /home/u/.eci-testhome.
func testRootBase() (string, error) {
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("HOME=%q is not an absolute path", home)
	}
	physical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve HOME: %w", err)
	}
	return filepath.Join(physical, ".eci-testhome"), nil
}

// requirePrivateDirectory accepts a real directory owned by this user with no group/other access.
//
// Example: a mode-0755 directory is rejected.
func requirePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	state, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || state.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is not a private directory of this user", path)
	}
	return nil
}

// testRootOwner parses the owning process ID from a test root name.
//
// Example: "run-41532-1729635463" returns 41532.
func testRootOwner(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, testRootPrefix)
	if !ok {
		return 0, false
	}
	digits, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	return pid, err == nil && pid > 0
}

// accountHome returns the account's home directory from the user database, never from $HOME.
//
// Example: resolve the account home before HOME moves into the test root.
func accountHome() (string, error) {
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return "", fmt.Errorf("look up account home: %w", err)
	}
	if account.HomeDir == "" {
		return "", fmt.Errorf("account %q has no home directory", account.Username)
	}
	return account.HomeDir, nil
}

// provisionTestHome builds the private install under root and points this process at it.
//
// Example: HOME becomes <root>/home and TMPDIR becomes <root>/tmp.
func provisionTestHome(root string) error {
	hooks, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return fmt.Errorf("resolve source hooks: %w", err)
	}
	if _, err := os.Stat(filepath.Join(hooks, "build-native-binaries.sh")); err != nil {
		return fmt.Errorf("locate source hooks: %w", err)
	}
	account, err := accountHome()
	if err != nil {
		return err
	}
	if err := pinGoEnvironment(account); err != nil {
		return err
	}
	home := filepath.Join(root, "home")
	temporary := filepath.Join(root, "tmp")
	eci := filepath.Join(home, ".omp", "agent", "eci")
	kimi := filepath.Join(home, ".kimi-code")
	for _, directory := range []string{temporary, filepath.Join(home, "tmp"), filepath.Join(eci, "bin"), filepath.Join(kimi, "bin")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	for _, providerRoot := range []string{eci, kimi} {
		if err := copyInstallTree(hooks, filepath.Join(providerRoot, "hooks")); err != nil {
			return fmt.Errorf("copy hooks: %w", err)
		}
		if err := copyTrackedBinScripts(filepath.Join(filepath.Dir(hooks), "bin"), filepath.Join(providerRoot, "bin")); err != nil {
			return fmt.Errorf("copy bin scripts: %w", err)
		}
	}
	// The suite runs the planner and, on Linux, stats the gate; the other natives have no test consumer.
	natives := []string{"eci-command-plan"}
	if runtime.GOOS == "linux" {
		natives = append(natives, "eci-command-gate-mode")
	}
	installed := append(os.Environ(), "HOME="+home, "TMPDIR="+temporary)
	build := exec.Command("bash", append([]string{filepath.Join(eci, "hooks", "build-native-binaries.sh")}, natives...)...)
	build.Env = installed
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build-native-binaries.sh: %w\n%s", err, output)
	}
	verify := exec.Command("bash", "-c",
		`source "$1" && eci_runtime_open_verified eci-command-plan || { printf '%s\n' "$ECI_RUNTIME_DENY_REASON" >&2; exit 1; }`,
		"verify", filepath.Join(eci, "hooks", "lib", "eci-native-binary.sh"))
	verify.Env = installed
	if output, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("the native loader rejects the provisioned planner: %w\n%s", err, output)
	}
	if runtime.GOOS == "linux" {
		gate := filepath.Join("bin", "eci-command-gate-mode")
		if err := os.Link(filepath.Join(eci, gate), filepath.Join(kimi, gate)); err != nil {
			return fmt.Errorf("hardlink Kimi gate: %w", err)
		}
	}
	// PATH entries inside the real provider roots would let exec.LookPath reach the real install.
	realRoots := []string{
		filepath.Join(account, ".omp", "agent", "eci"),
		filepath.Join(account, ".kimi-code"),
		filepath.Join(os.Getenv("HOME"), ".omp", "agent", "eci"),
		filepath.Join(os.Getenv("HOME"), ".kimi-code"),
		os.Getenv("KIMI_CODE_HOME"),
		os.Getenv("ECI_HOME"),
	}
	for name, value := range map[string]string{
		"PATH":      pathWithoutRoots(os.Getenv("PATH"), realRoots),
		"HOME":      home,
		"TMPDIR":    temporary,
		testRootEnv: root,
	} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	// These would select provider roots outside the private install.
	for _, name := range []string{"KIMI_CODE_HOME", "ECI_HOME", "ECI_PROOF_ROOT"} {
		if err := os.Unsetenv(name); err != nil {
			return err
		}
	}
	testPlannerBinary = filepath.Join(eci, "bin", "eci-command-plan")
	return nil
}

// pinGoEnvironment fixes Go's HOME-derived settings to what `go env` reports under the account
// home, before HOME moves, so builds keep the account's caches and go env file. `go env` already
// reports a value the caller set explicitly.
//
// Example: with GOCACHE unset, GOCACHE becomes `HOME=<account> go env GOCACHE`.
func pinGoEnvironment(account string) error {
	names := []string{"GOCACHE", "GOENV", "GOPATH", "GOMODCACHE"}
	query := exec.Command("go", append([]string{"env"}, names...)...)
	query.Env = append(os.Environ(), "HOME="+account)
	output, err := query.Output()
	if err != nil {
		return fmt.Errorf("go env: %w", err)
	}
	values := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(values) != len(names) {
		return fmt.Errorf("go env printed %q, want %d lines", output, len(names))
	}
	for index, name := range names {
		value := values[index]
		if !filepath.IsAbs(value) && !(name == "GOENV" && value == "off") {
			return fmt.Errorf("go env %s=%q is not an absolute path", name, value)
		}
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

// pathWithoutRoots drops PATH entries that resolve at or below any of roots.
//
// Example: with root /home/u/.kimi-code, /home/u/.kimi-code/bin is dropped and /usr/bin is kept.
func pathWithoutRoots(path string, roots []string) string {
	resolve := func(value string) string {
		if resolved, err := filepath.EvalSymlinks(value); err == nil {
			return resolved
		}
		return filepath.Clean(value)
	}
	var physicalRoots []string
	for _, root := range roots {
		if filepath.IsAbs(root) {
			physicalRoots = append(physicalRoots, resolve(root))
		}
	}
	var kept []string
	for _, entry := range filepath.SplitList(path) {
		resolved := resolve(entry)
		inside := false
		for _, root := range physicalRoots {
			if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
				inside = true
				break
			}
		}
		if !inside {
			kept = append(kept, entry)
		}
	}
	return strings.Join(kept, string(filepath.ListSeparator))
}

// untrackedInstallProducts are build outputs inside hooks/ that are not part of an install;
// bin/eci-runtime-sync skips the same paths.
var untrackedInstallProducts = map[string]bool{
	filepath.Join("lib", "eci-command-plan-go", "eci-command-plan"):             true,
	filepath.Join("lib", "eci-command-plan-go", ".eci-command-plan.provenance"): true,
	filepath.Join("lib", "eci-hook-helper-go", "eci-hook-helper"):               true,
}

// copyInstallTree copies directories, regular files and symlinks from source to destination with
// group/other write removed, as an install under umask 022 would have them.
//
// Example: hooks/validate-bash.sh at mode 0775 is copied at mode 0755.
func copyInstallTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if untrackedInstallProducts[relative] {
			return nil
		}
		target := filepath.Join(destination, relative)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case entry.Type().IsRegular():
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return copyInstallFile(path, target, info.Mode().Perm()&^0o022)
		default:
			return fmt.Errorf("%s: unsupported file type %s", path, entry.Type())
		}
	})
}

// copyTrackedBinScripts copies the shebang scripts of bin/, the tracked wrappers. Native binaries,
// receipts and on-demand build products in a working install are skipped; the build replaces them.
//
// Example: bin/eci-active is copied, bin/eci-command-plan and bin/.eci-active.<digest> are not.
func copyTrackedBinScripts(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(source, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		prefix := make([]byte, 2)
		_, readErr := io.ReadFull(file, prefix)
		file.Close()
		if readErr != nil || !bytes.Equal(prefix, []byte("#!")) {
			continue
		}
		if err := copyInstallFile(path, filepath.Join(destination, entry.Name()), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// copyInstallFile copies one regular file and sets mode exactly.
//
// Example: copyInstallFile(src, dst, 0o755) leaves dst executable and not group-writable.
func copyInstallFile(source, destination string, mode fs.FileMode) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(destination, contents, mode); err != nil {
		return err
	}
	return os.Chmod(destination, mode)
}
