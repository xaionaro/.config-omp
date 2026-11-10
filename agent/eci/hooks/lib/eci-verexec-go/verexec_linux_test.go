//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Paths TestMain sets up. Fixture homes live under fixtureBase, a directory whose ancestors the CLI trusts
// (trustedFixtureBase); the default temporary directory (/tmp, mode 1777) is not one.
//
// Example: verexecBin is fixtureBase/eci-verexec, the binary each newHome installs as bin/eci-verexec.
var (
	fixtureBase string
	verexecBin  string
	targetBin   string
	loaderPath  string
)

// TestMain builds eci-verexec and the target once for every test.
//
// Example: go test ./... builds both into a fixture base with trusted ancestors.
func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// runMain is TestMain's body, so deferred cleanup runs before os.Exit.
//
// Example: it removes the fixture base after m.Run returns.
func runMain(m *testing.M) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	loaderPath = filepath.Join(wd, "..", "eci-native-binary.sh")
	fixtureBase, err = trustedFixtureBase(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(fixtureBase)
	if err := os.Chmod(fixtureBase, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	verexecBin = filepath.Join(fixtureBase, "eci-verexec")
	targetBin = filepath.Join(fixtureBase, "target")
	for _, b := range [][2]string{{verexecBin, "."}, {targetBin, "./testdata/target"}} {
		cmd := exec.Command("go", "build", "-trimpath", "-o", b[0], b[1])
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", b[1], err, out)
			return 1
		}
	}
	return m.Run()
}

// trustedFixtureBase makes a 0700 directory under the first candidate whose ancestors the CLI trusts:
// $ECI_VEREXEC_TEST_DIR, the user cache directory, $HOME, then the module directory.
//
// Example: with HOME=/home/u it returns /home/u/.cache/.eci-verexec-test-123 when /home/u/.cache is trusted.
func trustedFixtureBase(wd string) (string, error) {
	var candidates []string
	if d := os.Getenv("ECI_VEREXEC_TEST_DIR"); d != "" {
		candidates = append(candidates, d)
	}
	if d, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, d)
	}
	candidates = append(candidates, os.Getenv("HOME"), wd)
	for _, c := range candidates {
		real, err := filepath.EvalSymlinks(c)
		if err != nil || !filepath.IsAbs(real) {
			continue
		}
		fd, ok := openTrustedChain(real, uint32(os.Geteuid()))
		if !ok {
			continue
		}
		syscall.Close(fd)
		base, err := os.MkdirTemp(real, ".eci-verexec-test-")
		if err != nil {
			continue
		}
		if err := os.Chmod(base, 0o700); err != nil {
			os.RemoveAll(base)
			continue
		}
		return base, nil
	}
	return "", fmt.Errorf("no fixture directory with trusted ancestors among %q; set ECI_VEREXEC_TEST_DIR to a directory whose parents are owned by you or root and not group/other-writable", candidates)
}

// home is one fixture HOME with an eci bin directory.
//
// Example: h := newHome(t); h.root is HOME and h.bin its .omp/agent/eci/bin.
type home struct {
	t    *testing.T
	root string
	bin  string
}

// newHome makes HOME/.omp/agent/eci/bin with eci-verexec and the test target installed as
// eci-hook-helper, each with a receipt.
//
// Example: newHome(t).path("eci-verexec") is an installed verifier with its receipt.
func newHome(t *testing.T) *home {
	t.Helper()
	root, err := os.MkdirTemp(fixtureBase, "home-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &home{t: t, root: root, bin: filepath.Join(root, ".omp/agent/eci/bin")}
	if err := os.MkdirAll(h.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{".omp", ".omp/agent", ".omp/agent/eci", ".omp/agent/eci/bin"} {
		os.Chmod(filepath.Join(root, d), 0o755)
	}
	h.install(selfName, mustRead(t, verexecBin))
	h.install("eci-hook-helper", mustRead(t, targetBin))
	return h
}

// mustRead returns the file's bytes or fails the test.
//
// Example: mustRead(t, targetBin) is the target image.
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// path names an entry of the fixture bin directory.
//
// Example: h.path("eci-hook-helper.sha256") is the target's receipt.
func (h *home) path(name string) string { return filepath.Join(h.bin, name) }

// install writes name with mode 0755 and a matching receipt.
//
// Example: h.install("eci-hook-helper", []byte("#!/bin/sh\n")) installs a script that L15 denies.
func (h *home) install(name string, content []byte) {
	h.t.Helper()
	os.Remove(h.path(name))
	if err := os.WriteFile(h.path(name), content, 0o755); err != nil {
		h.t.Fatal(err)
	}
	os.Chmod(h.path(name), 0o755)
	h.receipt(name, content)
}

// receipt writes a correct receipt for content, whatever the installed bytes are.
//
// Example: h.receipt(name, []byte("other")) makes L20 deny the installed file.
func (h *home) receipt(name string, content []byte) {
	h.t.Helper()
	sum := sha256.Sum256(content)
	os.Remove(h.path(name + ".sha256"))
	if err := os.WriteFile(h.path(name+".sha256"), []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// result is one CLI run: status, stdout, deny-descriptor text and stderr.
//
// Example: a denial is result{rc: 125, deny: "ECI native gate binary ...\n"}.
type result struct {
	rc         int
	stdout     string
	deny       string
	stderrText string
}

// run executes bin/eci-verexec (or the binary at exe when set) with HOME=h.root and deny fd 3.
//
// Example: h.run("", "", []string{"--check", "--deny-fd", "3", "eci-hook-helper"}) returns rc 0 and no deny text.
func (h *home) run(exe string, homeEnv string, args []string, env ...string) result {
	h.t.Helper()
	if exe == "" {
		exe = h.path(selfName)
	}
	if homeEnv == "" {
		homeEnv = h.root
	}
	denyFile, err := os.CreateTemp(fixtureBase, "deny-")
	if err != nil {
		h.t.Fatal(err)
	}
	defer os.Remove(denyFile.Name())
	defer denyFile.Close()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Env = append([]string{"HOME=" + homeEnv, "PATH=/usr/bin:/bin"}, env...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.ExtraFiles = []*os.File{denyFile}
	err = cmd.Run()
	rc := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		rc = ee.ExitCode()
	} else if err != nil {
		h.t.Fatal(err)
	}
	deny, _ := os.ReadFile(denyFile.Name())
	return result{rc, stdout.String(), string(deny), stderr.String()}
}

// shimReason runs eci_runtime_verify from lib/eci-native-binary.sh, the shell entry every caller uses, for name
// in the same HOME and returns its denial reason ("" on accept), with the trailing newline the native text carries.
//
// Example: after chmod 0775 of the target it returns the reasonWritable line.
func (h *home) shimReason(homeEnv, name string) string {
	h.t.Helper()
	if homeEnv == "" {
		homeEnv = h.root
	}
	cmd := exec.Command("bash", "--norc", "--noprofile", "-c",
		`. "$1" || exit 99; eci_runtime_verify "$2" || printf '%s\n' "$ECI_RUNTIME_DENY_REASON"`,
		"bash", loaderPath, name)
	cmd.Env = []string{"HOME=" + homeEnv, "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("eci_runtime_verify: %v", err)
	}
	return string(out)
}

// patchedCopy returns src's bytes after patch, leaving src untouched.
//
// Example: patchedCopy(t, targetBin, setMachine2) is a wrong-machine ELF.
func patchedCopy(t *testing.T, src string, patch func([]byte) []byte) []byte {
	t.Helper()
	return patch(append([]byte(nil), mustRead(t, src)...))
}

// Every deny of the CLI, with the byte-exact text, and the same text through the shell entry
// eci_runtime_verify. The bash loader no longer verifies on Linux; TestELFRuleMatchesBash keeps the
// Go-vs-bash differential for the host-format rule that runtime sync still applies in bash.
//
// Example: the L13 group-writable case expects exit 125 and the reasonWritable line on fd 3.
func TestCLIDenials(t *testing.T) {
	const helper = "eci-hook-helper"
	cases := []struct {
		name      string
		target    string
		setup     func(h *home)
		homeEnv   func(h *home) string
		fromCache bool   // run a verifier outside bin (bin missing or unreachable)
		denyName  string // "" accepts
		reason    string
		noShim    bool // compared on the CLI only: the shim runs bin/eci-verexec, not the binary under test
	}{
		{name: "accept", target: helper},
		{name: "L1 name not allowed", target: "eci-worker-git", fromCache: true, denyName: "eci-worker-git", reason: reasonNameOrHome},
		{name: "L1 verexec is not a target", target: selfName, fromCache: true, denyName: selfName, reason: reasonNameOrHome},
		{name: "L1 HOME relative", target: helper, homeEnv: func(*home) string { return "relative/home" }, fromCache: true, denyName: helper, reason: reasonNameOrHome},
		{name: "L2 bin missing", target: helper, setup: func(h *home) { os.RemoveAll(h.bin) }, fromCache: true, denyName: helper, reason: reasonBinMissing},
		{name: "L3 group-writable ancestor", target: helper, setup: func(h *home) { os.Chmod(filepath.Join(h.root, ".omp"), 0o775) }, denyName: helper, reason: reasonChain},
		{name: "L3 other-writable bin", target: helper, setup: func(h *home) { os.Chmod(h.bin, 0o757) }, denyName: helper, reason: reasonChain},
		{name: "L4 missing", target: "eci-command-plan", denyName: "eci-command-plan", reason: reasonMissingOrLink},
		{name: "L4 symlink", target: helper, setup: func(h *home) {
			os.Rename(h.path(helper), h.path("real"))
			os.Symlink("real", h.path(helper))
		}, denyName: helper, reason: reasonMissingOrLink},
		{name: "L4 fifo", target: helper, setup: func(h *home) {
			os.Remove(h.path(helper))
			syscall.Mkfifo(h.path(helper), 0o755)
		}, denyName: helper, reason: reasonMissingOrLink},
		{name: "L4 directory", target: helper, setup: func(h *home) {
			os.Remove(h.path(helper))
			os.Mkdir(h.path(helper), 0o755)
		}, denyName: helper, reason: reasonMissingOrLink},
		{name: "L5 unreadable", target: helper, setup: func(h *home) { os.Chmod(h.path(helper), 0o311) }, denyName: helper, reason: reasonCannotOpen},
		{name: "L10 hard link", target: helper, setup: func(h *home) { os.Link(h.path(helper), h.path("alias")) }, denyName: helper, reason: reasonOutside},
		{name: "L13 group-writable", target: helper, setup: func(h *home) { os.Chmod(h.path(helper), 0o775) }, denyName: helper, reason: reasonWritable},
		{name: "L13 other-writable", target: helper, setup: func(h *home) { os.Chmod(h.path(helper), 0o757) }, denyName: helper, reason: reasonWritable},
		{name: "L14 not executable", target: helper, setup: func(h *home) { os.Chmod(h.path(helper), 0o644) }, denyName: helper, reason: reasonNotExecutable},
		{name: "L15 script", target: helper, setup: func(h *home) { h.install(helper, []byte("#!/bin/sh\nexit 0\n")) }, denyName: helper, reason: reasonNotELF},
		{name: "L15 fat magic", target: helper, setup: func(h *home) {
			h.install(helper, patchedCopy(h.t, targetBin, func(b []byte) []byte { copy(b, []byte{0xca, 0xfe, 0xba, 0xbe}); return b }))
		}, denyName: helper, reason: reasonNotELF},
		{name: "L15 machine patched", target: helper, setup: func(h *home) {
			h.install(helper, patchedCopy(h.t, targetBin, func(b []byte) []byte { b[18], b[19] = 2, 0; return b }))
		}, denyName: helper, reason: reasonNotELF},
		{name: "L15 short file", target: helper, setup: func(h *home) { h.install(helper, []byte("\x7fELF")) }, denyName: helper, reason: reasonNotELF},
		{name: "L16 receipt missing", target: helper, setup: func(h *home) { os.Remove(h.path(helper + ".sha256")) }, denyName: helper, reason: reasonNoReceipt},
		{name: "L16 receipt symlink", target: helper, setup: func(h *home) {
			os.Rename(h.path(helper+".sha256"), h.path("r"))
			os.Symlink("r", h.path(helper+".sha256"))
		}, denyName: helper, reason: reasonNoReceipt},
		{name: "L18 malformed receipt", target: helper, setup: func(h *home) {
			os.WriteFile(h.path(helper+".sha256"), []byte("not a receipt\n"), 0o644)
		}, denyName: helper, reason: reasonMalformed},
		{name: "L18 unreadable receipt", target: helper, setup: func(h *home) { os.Chmod(h.path(helper+".sha256"), 0o200) }, denyName: helper, reason: reasonMalformed},
		{name: "L18 NUL in receipt", target: helper, setup: func(h *home) {
			sum := sha256.Sum256(mustRead(h.t, targetBin))
			d := hex.EncodeToString(sum[:])
			os.WriteFile(h.path(helper+".sha256"), []byte(d[:10]+"\x00"+d[10:]+"  "+helper+"\n"), 0o644)
		}, denyName: helper, reason: reasonMalformed, noShim: true},
		{name: "L20 tampered binary", target: helper, setup: func(h *home) {
			f, _ := os.OpenFile(h.path(helper), os.O_WRONLY|os.O_APPEND, 0)
			f.Write([]byte{0})
			f.Close()
		}, denyName: helper, reason: reasonMismatch},
		{name: "L20 receipt of another build", target: helper, setup: func(h *home) { h.receipt(helper, []byte("other")) }, denyName: helper, reason: reasonMismatch},
		{name: "self group-writable", target: helper, setup: func(h *home) { os.Chmod(h.path(selfName), 0o775) }, denyName: selfName, reason: reasonWritable, noShim: true},
		{name: "self hard link", target: helper, setup: func(h *home) { os.Link(h.path(selfName), h.path("verexec-alias")) }, denyName: selfName, reason: reasonOutside, noShim: true},
		{name: "self receipt mismatch", target: helper, setup: func(h *home) { h.receipt(selfName, []byte("other")) }, denyName: selfName, reason: reasonMismatch, noShim: true},
		{name: "self receipt missing", target: helper, setup: func(h *home) { os.Remove(h.path(selfName + ".sha256")) }, denyName: selfName, reason: reasonNoReceipt, noShim: true},
		{name: "self not the bin copy", target: helper, fromCache: true, denyName: selfName, reason: reasonOutside, noShim: true},
		{name: "self missing from bin", target: helper, setup: func(h *home) { os.Remove(h.path(selfName)) }, fromCache: true, denyName: selfName, reason: reasonMissingOrLink, noShim: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHome(t)
			if c.setup != nil {
				c.setup(h)
			}
			homeEnv := ""
			if c.homeEnv != nil {
				homeEnv = c.homeEnv(h)
			}
			exe := ""
			if c.fromCache {
				exe = verexecBin
			}
			want := ""
			if c.denyName != "" {
				want = denialText(c.denyName, c.reason)
			}
			r := h.run(exe, homeEnv, []string{"--deny-fd", "3", c.target, "--", "a b", ""})
			if r.deny != want {
				t.Fatalf("deny text\n got %q\nwant %q", r.deny, want)
			}
			if want != "" {
				if r.rc != exitDenied || r.stdout != "" {
					t.Fatalf("denial: rc %d stdout %q", r.rc, r.stdout)
				}
			} else if r.rc != 0 {
				t.Fatalf("accept: rc %d stderr %q", r.rc, r.stderrText)
			}
			r = h.run(exe, homeEnv, []string{"--check", "--deny-fd", "3", c.target})
			if r.deny != want || (want == "") != (r.rc == 0) || r.stdout != "" {
				t.Fatalf("--check: rc %d deny %q stdout %q", r.rc, r.deny, r.stdout)
			}
			if !c.noShim && !c.fromCache {
				if got := h.shimReason(homeEnv, c.target); got != want {
					t.Fatalf("eci_runtime_verify differs\n shim %q\n  cli %q", got, want)
				}
			}
		})
	}
}

// TestCLIExecutesVerifiedTarget checks argv[0], argument passing and that no descriptor reaches the target.
//
// Example: args "a b", "" and "--" arrive as "a b||--" with fds=[].
func TestCLIExecutesVerifiedTarget(t *testing.T) {
	h := newHome(t)
	r := h.run("", "", []string{"--deny-fd", "3", "eci-hook-helper", "--", "a b", "", "--"})
	real, _ := filepath.EvalSymlinks(h.bin)
	want := "argv0=" + real + "/eci-hook-helper\nargs=a b||--\nfds=[]\n"
	if r.rc != 0 || r.deny != "" || r.stdout != want {
		t.Fatalf("rc %d deny %q stdout %q, want %q", r.rc, r.deny, r.stdout, want)
	}
}

// The Go runtime raises its own soft RLIMIT_NOFILE at start; the target must see the caller's limit.
// The target is a copy of bash, a non-Go ELF that reports the limit it was started with.
//
// Example: under `ulimit -Sn 512` the target reports nofile=512, not the runtime's raised limit.
func TestCLITargetKeepsCallerNofileLimit(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	bash, err = filepath.EvalSymlinks(bash)
	if err != nil {
		t.Fatal(err)
	}
	h := newHome(t)
	h.install("eci-hook-helper", mustRead(t, bash))
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	soft := uint64(512)
	if lim.Max < soft {
		soft = lim.Max
	}
	cmd := exec.Command("bash", "-c", `ulimit -Sn "$1" && exec "$2" --deny-fd 2 eci-hook-helper -- -c 'echo "nofile=$(ulimit -Sn)"'`, "bash",
		strconv.FormatUint(soft, 10), h.path(selfName))
	cmd.Env = []string{"HOME=" + h.root, "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v %q", err, out)
	}
	if want := fmt.Sprintf("nofile=%d\n", soft); string(out) != want {
		t.Fatalf("target saw %q, want %q", out, want)
	}
}

// A target that exits 125 is not a denial: the status passes through and the deny fd stays empty.
//
// Example: ECI_VEREXEC_TARGET_EXIT=125 gives status 125 with an empty deny file.
func TestCLITargetExit125IsNotADenial(t *testing.T) {
	h := newHome(t)
	r := h.run("", "", []string{"--deny-fd", "3", "eci-hook-helper"}, "ECI_VEREXEC_TARGET_EXIT=125")
	if r.rc != 125 || r.deny != "" || !strings.HasPrefix(r.stdout, "argv0=") {
		t.Fatalf("rc %d deny %q stdout %q", r.rc, r.deny, r.stdout)
	}
}

// A verified file the kernel will not execute maps to 126 without deny text.
//
// Example: an ELF header followed by zeros verifies, then exec fails with 126.
func TestCLIExecFormatErrorIs126(t *testing.T) {
	h := newHome(t)
	hdr := make([]byte, 20)
	copy(hdr, mustRead(t, targetBin)[:20])
	h.install("eci-hook-helper", append(hdr, make([]byte, 64)...))
	r := h.run("", "", []string{"--deny-fd", "3", "eci-hook-helper"})
	if r.rc != exitCannotExec || r.deny != "" {
		t.Fatalf("rc %d deny %q", r.rc, r.deny)
	}
}

// A verified ELF whose interpreter does not exist maps to 127 without deny text.
//
// Example: /bin/true with PT_INTERP pointing at /nonexistent/ld.so exits 127.
func TestCLIMissingInterpreterIs127(t *testing.T) {
	src, err := filepath.EvalSymlinks("/bin/true")
	if err != nil {
		t.Skip("no /bin/true")
	}
	f, err := elf.Open(src)
	if err != nil {
		t.Skip("/bin/true is not ELF")
	}
	var interp *elf.Prog
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			interp = p
		}
	}
	f.Close()
	if interp == nil {
		t.Skip("/bin/true is static")
	}
	h := newHome(t)
	b := mustRead(t, src)
	missing := "/nonexistent/ld.so"
	if uint64(len(missing)) >= interp.Filesz {
		t.Skip("interpreter path too short to patch")
	}
	field := b[interp.Off : interp.Off+interp.Filesz]
	for i := range field {
		field[i] = 0
	}
	copy(field, missing)
	h.install("eci-hook-helper", b)
	r := h.run("", "", []string{"--deny-fd", "3", "eci-hook-helper"})
	if r.rc != exitNotFound || r.deny != "" {
		t.Fatalf("rc %d deny %q", r.rc, r.deny)
	}
}

// TestCLIUsage checks that malformed command lines and a closed deny descriptor exit 2.
//
// Example: --deny-fd 9 with descriptor 9 closed exits 2.
func TestCLIUsage(t *testing.T) {
	h := newHome(t)
	for _, args := range [][]string{{}, {"eci-hook-helper"}, {"--deny-fd", "3"}} {
		if r := h.run("", "", args); r.rc != exitUsage || r.deny != "" {
			t.Errorf("%q: rc %d deny %q", args, r.rc, r.deny)
		}
	}
	// A deny descriptor that is not open is a usage error, not a silent accept.
	cmd := exec.Command(h.path(selfName), "--deny-fd", "9", "eci-hook-helper")
	cmd.Env = []string{"HOME=" + h.root}
	if err := cmd.Run(); err == nil {
		t.Fatal("closed deny fd accepted")
	} else if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() != exitUsage {
		t.Fatalf("rc %d", ee.ExitCode())
	}
}

// openFixture returns the trusted bin directory descriptor of a fresh home.
//
// Example: h, binDir, host := openFixture(t) is ready for openVerified.
func openFixture(t *testing.T) (*home, int, []byte) {
	t.Helper()
	h := newHome(t)
	real, _ := filepath.EvalSymlinks(h.bin)
	binDir, ok := openTrustedChain(real, uint32(os.Geteuid()))
	if !ok {
		t.Fatal("fixture chain untrusted")
	}
	t.Cleanup(func() { syscall.Close(binDir) })
	host, ok := hostHeader()
	if !ok {
		t.Fatal("host header")
	}
	return h, binDir, host
}

// L7: the name is swapped to another inode between the path check and the open.
//
// Example: the race hook renames another file over the target and L7 denies.
func TestRenameRaceBetweenStatAndOpen(t *testing.T) {
	h, binDir, host := openFixture(t)
	h.install("other", mustRead(t, targetBin))
	testHookBeforeOpen = func() { os.Rename(h.path("other"), h.path("eci-hook-helper")) }
	defer func() { testHookBeforeOpen = nil }()
	fd, d := openVerified(binDir, "eci-hook-helper", uint32(os.Geteuid()), host, true)
	if d == nil || d.name != "eci-hook-helper" || d.reason != reasonOutside {
		t.Fatalf("got fd %d denial %+v", fd, d)
	}
}

// L5 through the race hook: the file disappears before the open.
//
// Example: the race hook removes the target and L5 denies.
func TestVanishBetweenStatAndOpen(t *testing.T) {
	h, binDir, host := openFixture(t)
	testHookBeforeOpen = func() { os.Remove(h.path("eci-hook-helper")) }
	defer func() { testHookBeforeOpen = nil }()
	if _, d := openVerified(binDir, "eci-hook-helper", uint32(os.Geteuid()), host, true); d == nil || d.reason != reasonCannotOpen {
		t.Fatalf("got %+v", d)
	}
}

// The descriptor table of the loader test (checks 77-81) on verifyFD.
//
// Example: fd-closed gives reasonFDUnresolved.
func TestVerifyFDTable(t *testing.T) {
	h, binDir, host := openFixture(t)
	euid := uint32(os.Geteuid())
	stat := func(name string) *syscall.Stat_t {
		var st syscall.Stat_t
		if err := fstatatNoFollow(binDir, name, &st); err != nil {
			t.Fatal(err)
		}
		return &st
	}
	open := func(name string, flags int) int {
		fd, err := syscall.Openat(binDir, name, flags|syscall.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { syscall.Close(fd) })
		return fd
	}
	h.install("eci-command-plan", patchedCopy(t, targetBin, func(b []byte) []byte { b[18], b[19] = 2, 0; return b }))
	valid := open("eci-hook-helper", syscall.O_RDONLY)
	const closed = 1 << 20 // above any descriptor this process opens
	dir := open(".", syscall.O_RDONLY|syscall.O_DIRECTORY)
	other := open("eci-command-plan", syscall.O_RDONLY)
	cases := []struct {
		name   string
		fd     int
		path   *syscall.Stat_t
		art    string
		reason string
	}{
		{"fd-valid", valid, stat("eci-hook-helper"), "eci-hook-helper", ""},
		{"fd-closed", closed, stat("eci-hook-helper"), "eci-hook-helper", reasonFDUnresolved},
		{"fd-other-inode", valid, stat("eci-command-plan"), "eci-hook-helper", reasonOutside},
		{"fd-non-regular", dir, stat("."), "eci-hook-helper", reasonNotRegular},
		{"fd-wrong-machine-elf", other, stat("eci-command-plan"), "eci-command-plan", reasonNotELF},
		{"fd-host-header-unreadable", valid, stat("eci-hook-helper"), "eci-hook-helper", reasonNotELF},
	}
	for _, c := range cases {
		hostOK := c.name != "fd-host-header-unreadable"
		d := verifyFD(c.fd, binDir, c.art, c.path, euid, host, hostOK)
		got := ""
		if d != nil {
			got = d.reason
		}
		if got != c.reason {
			t.Errorf("%s: got %q, want %q", c.name, got, c.reason)
		}
	}
	os.Link(h.path("eci-hook-helper"), h.path("alias"))
	if d := verifyFD(valid, binDir, "eci-hook-helper", stat("eci-hook-helper"), euid, host, true); d == nil || d.reason != reasonOutside {
		t.Errorf("fd-hardlink-path: got %+v", d)
	}
}

// L19: a read error while hashing. The CLI cannot reach it on a regular file it just opened.
//
// Example: hashFD(-1, 10) fails; hashFD(-1, 0) hashes nothing.
func TestHashReadError(t *testing.T) {
	if _, err := hashFD(-1, 10); err == nil {
		t.Fatal("hash of a bad descriptor succeeded")
	}
	d, err := hashFD(-1, 0)
	if err != nil || d != hex.EncodeToString(func() []byte { s := sha256.Sum256(nil); return s[:] }()) {
		t.Fatalf("empty: %q %v", d, err)
	}
}

// Differential check of the ELF rule: elfMatchesHost against the bash eci_runtime_binary_is_native on
// the same files.
//
// Example: the osabi9 file is refused by both Go and eci_runtime_binary_is_native.
func TestELFRuleMatchesBash(t *testing.T) {
	h := newHome(t)
	host, ok := hostHeader()
	if !ok {
		t.Fatal("host header")
	}
	files := map[string][]byte{
		"host":      mustRead(t, targetBin),
		"machine":   patchedCopy(t, targetBin, func(b []byte) []byte { b[18], b[19] = 2, 0; return b }),
		"class":     patchedCopy(t, targetBin, func(b []byte) []byte { b[4] ^= 3; return b }),
		"order":     patchedCopy(t, targetBin, func(b []byte) []byte { b[5] ^= 3; return b }),
		"osabi3":    patchedCopy(t, targetBin, func(b []byte) []byte { b[7] = 3; return b }),
		"osabi9":    patchedCopy(t, targetBin, func(b []byte) []byte { b[7] = 9; return b }),
		"etype-dyn": patchedCopy(t, targetBin, func(b []byte) []byte { b[16] = 3; return b }),
		"fat":       patchedCopy(t, targetBin, func(b []byte) []byte { copy(b, []byte{0xca, 0xfe, 0xba, 0xbe}); return b }),
		"script":    []byte("#!/bin/sh\nexit 0\n#####################"),
		"short":     []byte("\x7fELF\x02\x01"),
	}
	for name, b := range files {
		p := filepath.Join(h.root, name)
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
		hdr := b
		if len(hdr) > 20 {
			hdr = hdr[:20]
		}
		goSays := elfMatchesHost(hdr, host)
		err := exec.Command("bash", "--norc", "--noprofile", "-c", `. "$1" && eci_runtime_binary_is_native "$2"`, "bash", loaderPath, p).Run()
		if bashSays := err == nil; bashSays != goSays {
			t.Errorf("%s: bash %v, go %v", name, bashSays, goSays)
		}
	}
}

// The verifier finishes quickly on a FIFO swapped in after the path check (O_NONBLOCK open).
//
// Example: a FIFO swapped in after L4 is denied within 5 s.
func TestFIFOAfterStatDoesNotHang(t *testing.T) {
	h, binDir, host := openFixture(t)
	testHookBeforeOpen = func() {
		os.Remove(h.path("eci-hook-helper"))
		syscall.Mkfifo(h.path("eci-hook-helper"), 0o755)
	}
	defer func() { testHookBeforeOpen = nil }()
	done := make(chan *denial, 1)
	go func() {
		_, d := openVerified(binDir, "eci-hook-helper", uint32(os.Geteuid()), host, true)
		done <- d
	}()
	select {
	case d := <-done:
		// The FIFO may reuse the removed file's inode number; either way it is denied.
		if d == nil || (d.reason != reasonOutside && d.reason != reasonNotRegular) {
			t.Fatalf("got %+v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open of a FIFO blocked")
	}
}

// TestDirectoryDenialGuidance checks that a writable bin requires an owner action, not a rebuild.
//
// Example: a 0775 bin produces guidance to inspect and correct directory permissions.
func TestDirectoryDenialGuidance(t *testing.T) {
	h := newHome(t)
	if err := os.Chmod(h.bin, 0o775); err != nil {
		t.Fatal(err)
	}
	r := h.run("", "", []string{"--check", "--deny-fd", "3", "eci-hook-helper"})
	if r.rc != exitDenied || !strings.Contains(r.deny, "ask its owner") || !strings.Contains(r.deny, "group/other write") || strings.Contains(r.deny, "provision it") {
		t.Fatalf("unsafe directory guidance: rc %d deny %q", r.rc, r.deny)
	}
}

// Every reason constant appears exactly once in the module's non-test sources.
//
// Example: a second quoted copy of a reason in any non-test file fails this test.
func TestReasonStringsAppearOnce(t *testing.T) {
	var src bytes.Buffer
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			src.Write(mustRead(t, f))
		}
	}
	for _, r := range []string{reasonNameOrHome, reasonBinMissing, reasonChain, reasonMissingOrLink, reasonCannotOpen,
		reasonFDUnresolved, reasonOutside, reasonCannotStat, reasonNotRegular, reasonNotOwned, reasonUnreadableMode,
		reasonWritable, reasonNotExecutable, reasonNotELF, reasonNoReceipt, reasonNameNotAllowed, reasonMalformed,
		reasonCannotHash, reasonMismatch} {
		if n := strings.Count(src.String(), `"`+r+`"`); n != 1 {
			t.Errorf("%q appears %d times", r, n)
		}
	}
}
