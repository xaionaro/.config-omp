//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// O_PATH is absent from package syscall; the value is the same on every Linux architecture Go supports.
const oPath = 0x200000

// denial carries the artifact name and the reason; the name is the target or, during the self-check, eci-verexec.
type denial struct{ name, reason string }

func (d *denial) Error() string { return denialText(d.name, d.reason) }

func deny(name, reason string) *denial { return &denial{name, reason} }

// testHookBeforeOpen runs between the path fstatat (L4) and the open (L5); tests use it to inject a rename race.
var testHookBeforeOpen func()

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

func writeAll(fd int, b []byte) {
	for len(b) > 0 {
		n, err := syscall.Write(fd, b)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		b = b[n:]
	}
}

// verify runs the full check sequence and returns the open, verified target descriptor and its argv[0].
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

func fdTrusted(fd int, euid uint32) bool {
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil {
		return false
	}
	return directoryTrusted(st.Mode, st.Uid, euid)
}

// hostHeader reads the first 20 bytes of the running image, as the loader reads /proc/$$/exe.
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
func openVerified(binDir int, name string, euid uint32, host []byte, hostOK bool) (int, *denial) {
	var path syscall.Stat_t
	// L4: the bash test is [ -L p ] || [ ! -f p ].
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
func verifyFD(fd, binDir int, name string, path *syscall.Stat_t, euid uint32, host []byte, hostOK bool) *denial {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		if err == syscall.EBADF {
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
	// L16: the bash test is [ -f r ] && [ ! -L r ].
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
// malformed, as the loader's `read ... || true` leaves an empty line.
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
		if err == syscall.EINTR {
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
func hashFD(fd int, size int64) (string, error) {
	h := sha256.New()
	buf := make([]byte, 256<<10)
	for off := int64(0); off < size; {
		want := buf
		if rem := size - off; rem < int64(len(want)) {
			want = want[:rem]
		}
		n, err := syscall.Pread(fd, want, off)
		if err == syscall.EINTR {
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

func preadFull(fd int, b []byte, off int64) error {
	for n := 0; n < len(b); {
		m, err := syscall.Pread(fd, b[n:], off+int64(n))
		if err == syscall.EINTR {
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
func fstatatNoFollow(dir int, name string, st *syscall.Stat_t) error {
	fd, err := syscall.Openat(dir, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	return syscall.Fstat(fd, st)
}

// execFD runs the verified inode itself: execveat(fd, "", AT_EMPTY_PATH). Without execveat (ENOSYS,
// or no syscall number for this architecture) it execs /proc/self/fd/N, the loader's former path.
// It returns only on failure.
func execFD(fd int, argv, env []string) error {
	if sysExecveat >= 0 {
		err := execveat(fd, argv, env)
		if err != syscall.ENOSYS {
			return err
		}
	}
	return syscall.Exec("/proc/self/fd/"+strconv.Itoa(fd), argv, env)
}

// execveat is a raw syscall rather than syscall.Exec, so it skips Go's ForkLock. That lock only keeps a
// concurrent fork from inheriting descriptors mid-exec; this process is single-threaded at this point in
// practice (no goroutines are started), every descriptor it holds is O_CLOEXEC, and a successful exec
// replaces the whole process, so there is no fork to race with.
func execveat(fd int, argv, env []string) error {
	empty, err := syscall.BytePtrFromString("")
	if err != nil {
		return err
	}
	av, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		return err
	}
	ev, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return err
	}
	_, _, e := syscall.Syscall6(uintptr(sysExecveat), uintptr(fd), uintptr(unsafe.Pointer(empty)),
		uintptr(unsafe.Pointer(&av[0])), uintptr(unsafe.Pointer(&ev[0])), 0x1000 /* AT_EMPTY_PATH */, 0)
	return e
}

func execStatus(err error) int {
	if err == syscall.ENOENT {
		return exitNotFound
	}
	return exitCannotExec
}
