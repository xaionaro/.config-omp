//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// oPath is O_PATH, which package syscall does not define; the value is the same on every Linux
// architecture Go supports.
//
// Example: openTrustedChain opens each ancestor with oPath|O_NOFOLLOW, so a 0711 home needs no read permission.
const oPath = 0x200000

// denial carries the artifact name and the reason; the name is the target or, during the self-check,
// eci-verexec.
//
// Example: denial{"eci-hook-helper", reasonWritable} prints the reasonWritable deny line.
type denial struct{ name, reason string }

// Error returns the full deny line, newline included, exactly as the deny descriptor receives it.
//
// Example: deny("eci-hook-helper", reasonNotExecutable).Error() ends in `"...build-native-binaries.sh" and retry\n`.
func (d *denial) Error() string { return denialText(d.name, d.reason) }

// deny builds a denial for name with one of the reason constants.
//
// Example: deny(selfName, reasonMismatch) is the self-check denial for a stale eci-verexec.
func deny(name, reason string) *denial { return &denial{name, reason} }

// testHookBeforeOpen runs between the path check (L4) and the open (L5) when set; tests use it to
// inject a rename race.
//
// Example: TestRenameRaceBetweenStatAndOpen renames another file over the target here and expects L7.
var testHookBeforeOpen func()

// main parses the arguments, verifies the target, and then exits (--check) or execs it.
//
// Example: `eci-verexec --deny-fd 3 eci-command-plan -- --hook 3>deny` runs the verified planner.
func main() {
	inv, err := parseArgs(os.Args[1:])
	if err != nil {
		os.Stderr.WriteString("eci-verexec: " + err.Error() + "\n" + usageText)
		os.Exit(exitUsage)
	}
	// The deny descriptor belongs to this process only; the target never inherits it.
	if inv.denyFD > 2 {
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(inv.denyFD), syscall.F_SETFD, syscall.FD_CLOEXEC); e != 0 {
			os.Stderr.WriteString("eci-verexec: deny descriptor " + strconv.Itoa(inv.denyFD) + " is not open\n")
			os.Exit(exitUsage)
		}
	}
	fd, argv0, d := verify(inv.name, os.Getenv("HOME"))
	if d != nil {
		writeAll(inv.denyFD, []byte(d.Error()))
		os.Exit(exitDenied)
	}
	if inv.check {
		os.Exit(0)
	}
	argv := append([]string{argv0}, inv.args...)
	os.Exit(execStatus(execFD(fd, argv, os.Environ())))
}

// writeAll writes b to fd, retrying short writes and EINTR. Other errors end the write silently: the
// caller exits with exitDenied next, so the status still reports the denial.
//
// Example: writeAll(3, []byte(d.Error())) puts one deny line on descriptor 3.
func writeAll(fd int, b []byte) {
	for len(b) > 0 {
		n, err := syscall.Write(fd, b)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		b = b[n:]
	}
}

// verify runs the full check sequence (L1-L20, reasons.go) and returns the open, verified target
// descriptor and its argv[0].
//
// Example: verify("eci-hook-helper", "/home/u") returns fd, "/home/u/.omp/agent/eci/bin/eci-hook-helper", nil.
func verify(name, home string) (int, string, *denial) {
	// L1: allowed name and absolute HOME.
	if !allowedTarget(name) || !strings.HasPrefix(home, "/") {
		return -1, "", deny(name, reasonNameOrHome)
	}
	// L2: realpath -e of the bin directory.
	realBin, err := filepath.EvalSymlinks(home + "/.omp/agent/eci/bin")
	if err != nil {
		return -1, "", deny(name, reasonBinMissing)
	}
	realBin = filepath.Clean(realBin)
	euid := uint32(os.Geteuid())
	// L3: walk / -> bin without following symlinks; the final descriptor is the directory that was checked.
	binDir, ok := openTrustedChain(realBin, euid)
	if !ok {
		return -1, "", deny(name, reasonChain)
	}
	host, hostOK := hostHeader()
	if d := selfCheck(binDir, euid, host, hostOK); d != nil {
		return -1, "", d
	}
	fd, d := openVerified(binDir, name, euid, host, hostOK)
	if d != nil {
		return -1, "", d
	}
	return fd, realBin + "/" + name, nil
}

// openTrustedChain opens every component of the canonical path p from / with O_PATH|O_NOFOLLOW and
// requires each, including /, to pass directoryTrusted. A component that is a symlink fails.
//
// Example: openTrustedChain("/home/u/.omp/agent/eci/bin", 1000) fails if /home/u/.omp is mode 0775.
func openTrustedChain(p string, euid uint32) (int, bool) {
	dir, err := syscall.Open("/", oPath|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil || !fdTrusted(dir, euid) {
		return -1, false
	}
	rest := strings.Trim(p, "/")
	if rest == "" {
		return dir, true
	}
	parts := strings.Split(rest, "/")
	for i, c := range parts {
		flags := oPath | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, err := syscall.Openat(dir, c, flags, 0)
		syscall.Close(dir)
		if err != nil || !fdTrusted(next, euid) {
			if err == nil {
				syscall.Close(next)
			}
			return -1, false
		}
		dir = next
	}
	return dir, true
}

// fdTrusted applies directoryTrusted to the fstat of an open directory descriptor.
//
// Example: fdTrusted(rootFD, euid) is true for a root-owned 0755 /.
func fdTrusted(fd int, euid uint32) bool {
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil {
		return false
	}
	return directoryTrusted(st.Mode, st.Uid, euid)
}

// hostHeader reads the first 20 bytes of the running image, which define the host ELF class, byte
// order and machine.
//
// Example: on arm64 the result has bytes 18-19 = b7 00 (EM_AARCH64).
func hostHeader() ([]byte, bool) {
	fd, err := syscall.Open("/proc/self/exe", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, false
	}
	defer syscall.Close(fd)
	b := make([]byte, 20)
	return b, preadFull(fd, b, 0) == nil
}

// selfCheck applies the target checks L4 and L7-L20 to the running image under the name eci-verexec:
// /proc/self/exe must be the inode bin/eci-verexec names, owned by the user, not writable by others,
// with one link, an ELF for this host, and the sha256 in eci-verexec.sha256.
//
// Example: a copy of eci-verexec run from outside bin/ denies "eci-verexec ... resolves outside the verified bin directory".
func selfCheck(binDir int, euid uint32, host []byte, hostOK bool) *denial {
	var path syscall.Stat_t
	if err := fstatatNoFollow(binDir, selfName, &path); err != nil || path.Mode&modeTypeMask != modeRegular {
		return deny(selfName, reasonMissingOrLink)
	}
	fd, err := syscall.Open("/proc/self/exe", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return deny(selfName, reasonCannotOpen)
	}
	defer syscall.Close(fd)
	return verifyFD(fd, binDir, selfName, &path, euid, host, hostOK)
}

// openVerified is L4-L20 for a target in the trusted bin directory. The returned descriptor is O_CLOEXEC.
//
// Example: openVerified(binDir, "eci-hook-helper", ...) on a FIFO named eci-hook-helper denies L4 without blocking.
func openVerified(binDir int, name string, euid uint32, host []byte, hostOK bool) (int, *denial) {
	var path syscall.Stat_t
	// L4: the bash test was [ -L p ] || [ ! -f p ].
	if err := fstatatNoFollow(binDir, name, &path); err != nil || path.Mode&modeTypeMask != modeRegular {
		return -1, deny(name, reasonMissingOrLink)
	}
	if testHookBeforeOpen != nil {
		testHookBeforeOpen()
	}
	// L5: O_NONBLOCK so a FIFO swapped in after L4 cannot hang the open.
	fd, err := syscall.Openat(binDir, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, deny(name, reasonCannotOpen)
	}
	if d := verifyFD(fd, binDir, name, &path, euid, host, hostOK); d != nil {
		syscall.Close(fd)
		return -1, d
	}
	return fd, nil
}

// verifyFD is L6-L20 for descriptor fd, which must be the inode path describes.
//
// Example: a descriptor of a file with two names denies L10 (reasonOutside).
func verifyFD(fd, binDir int, name string, path *syscall.Stat_t, euid uint32, host []byte, hostOK bool) *denial {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		if errors.Is(err, syscall.EBADF) {
			return deny(name, reasonFDUnresolved)
		}
		return deny(name, reasonCannotStat)
	}
	if st.Dev != path.Dev || st.Ino != path.Ino {
		return deny(name, reasonOutside)
	}
	if r := descriptorReason(fileFacts{mode: st.Mode, nlink: uint64(st.Nlink), uid: st.Uid}, euid, true); r != "" {
		return deny(name, r)
	}
	header := make([]byte, 20)
	if !hostOK || preadFull(fd, header, 0) != nil || !elfMatchesHost(header, host) {
		return deny(name, reasonNotELF)
	}
	// L16: the bash test was [ -f r ] && [ ! -L r ].
	var rst syscall.Stat_t
	if err := fstatatNoFollow(binDir, name+".sha256", &rst); err != nil || rst.Mode&modeTypeMask != modeRegular {
		return deny(name, reasonNoReceipt)
	}
	receipt := readReceipt(binDir, name+".sha256")
	if !nameIsSafe(name) {
		return deny(name, reasonNameNotAllowed)
	}
	if _, ok := receiptDigest(name, receipt); !ok {
		return deny(name, reasonMalformed)
	}
	digest, err := hashFD(fd, st.Size)
	if err != nil {
		return deny(name, reasonCannotHash)
	}
	if r := receiptReason(name, receipt, digest); r != "" {
		return deny(name, r)
	}
	return nil
}

// readReceipt returns up to 256 bytes of the receipt; an unreadable receipt reads as empty, which is
// malformed, as the former loader's `read ... || true` left an empty line.
//
// Example: a 0200 receipt reads as nil and denies L18 (reasonMalformed).
func readReceipt(binDir int, name string) []byte {
	fd, err := syscall.Openat(binDir, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer syscall.Close(fd)
	b := make([]byte, 256)
	n := 0
	for n < len(b) {
		m, err := syscall.Pread(fd, b[n:], int64(n))
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || m <= 0 {
			break
		}
		n += m
	}
	return b[:n]
}

// hashFD hashes [0, size) of fd by pread; bytes appended after the fstat are not read.
//
// Example: hashFD(fd, 0) is the sha256 of the empty input, e3b0c442...b855.
func hashFD(fd int, size int64) (string, error) {
	h := sha256.New()
	buf := make([]byte, 256<<10)
	for off := int64(0); off < size; {
		want := buf
		if rem := size - off; rem < int64(len(want)) {
			want = want[:rem]
		}
		n, err := syscall.Pread(fd, want, off)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return "", err
		}
		if n == 0 {
			break // truncated since fstat: the digest of what is there will not match
		}
		h.Write(want[:n])
		off += int64(n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// preadFull fills b from fd at off, retrying short reads and EINTR; a file shorter than b is EIO.
//
// Example: preadFull(fd, header[:20], 0) reads the ELF header that L15 compares.
func preadFull(fd int, b []byte, off int64) error {
	for n := 0; n < len(b); {
		m, err := syscall.Pread(fd, b[n:], off+int64(n))
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if m == 0 {
			return syscall.EIO
		}
		n += m
	}
	return nil
}

// fstatatNoFollow is fstatat(dir, name, AT_SYMLINK_NOFOLLOW) through an O_PATH|O_NOFOLLOW descriptor,
// which names a symlink itself; package syscall has no Fstatat on every architecture.
//
// Example: for a symlink named eci-hook-helper, st.Mode has the S_IFLNK type and L4 denies.
func fstatatNoFollow(dir int, name string, st *syscall.Stat_t) error {
	fd, err := syscall.Openat(dir, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	return syscall.Fstat(fd, st)
}

// execFD runs the verified inode by exec of /proc/self/fd/N. The kernel resolves that path to the open
// descriptor's inode before it closes O_CLOEXEC descriptors, so the hashed inode is the one that runs;
// only #! scripts would need the descriptor to survive, and L15 already denies them. syscall.Exec also
// restores the soft RLIMIT_NOFILE the Go runtime raised at start, so the target sees the caller's limit.
// It returns only on failure.
//
// Example: execFD(5, []string{"/home/u/.omp/agent/eci/bin/eci-command-plan", "--hook"}, env) never returns on success.
func execFD(fd int, argv, env []string) error {
	return syscall.Exec("/proc/self/fd/"+strconv.Itoa(fd), argv, env)
}

// execStatus maps an exec failure to the shell's convention: 127 for ENOENT, 126 otherwise.
//
// Example: execStatus(syscall.ENOEXEC) is 126, as bash reports a file it cannot execute.
func execStatus(err error) int {
	if errors.Is(err, syscall.ENOENT) {
		return exitNotFound
	}
	return exitCannotExec
}
