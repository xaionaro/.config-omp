package main

// Platform-neutral decisions, kept free of syscalls so the tests can drive every branch. The L labels
// are the check order in reasons.go.

// allowedTarget mirrors eci_runtime_artifact_path: three gate binaries only.
//
// Example: allowedTarget("eci-hook-helper") is true; allowedTarget("eci-verexec") is false (L1).
func allowedTarget(name string) bool {
	switch name {
	case "eci-command-plan", "eci-safe-import", "eci-hook-helper":
		return true
	}
	return false
}

// nameIsSafe is the ^[a-z0-9-]+$ name rule (L17).
//
// Example: nameIsSafe("eci.hook") is false.
func nameIsSafe(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// st_mode file-type values (S_IFMT and three of its types), so the decisions need no syscall package.
const (
	// modeTypeMask is S_IFMT.
	//
	// Example: mode&modeTypeMask == modeRegular for a regular file.
	modeTypeMask = 0o170000
	// modeRegular is S_IFREG.
	//
	// Example: 0o100755 is a regular file with mode 0755.
	modeRegular = 0o100000
	// modeSymlink is S_IFLNK.
	//
	// Example: directoryTrusted denies a chain component whose type is modeSymlink.
	modeSymlink = 0o120000
	// modeDir is S_IFDIR.
	//
	// Example: a directory descriptor (0o040755) denies L9 (reasonNotRegular).
	modeDir = 0o040000
)

// fileFacts is what the descriptor checks read from fstat.
//
// Example: fileFacts{mode: 0o100755, nlink: 1, uid: 1000} accepts for euid 1000.
type fileFacts struct {
	mode  uint32 // st_mode, type and permission bits
	nlink uint64
	uid   uint32
}

// descriptorReason applies the L9-L14 order (reasons.go) to an fstat record; "" accepts.
// modeReadable is false only in tests: the native mode is never unparsable text (L12).
//
// Example: a regular 0775 file with one link, owned by euid, gives reasonWritable.
func descriptorReason(f fileFacts, euid uint32, modeReadable bool) string {
	switch {
	case f.mode&modeTypeMask != modeRegular:
		return reasonNotRegular
	case f.nlink != 1:
		return reasonOutside
	case f.uid != euid:
		return reasonNotOwned
	case !modeReadable:
		return reasonUnreadableMode
	case f.mode&0o022 != 0:
		return reasonWritable
	case f.mode&0o111 == 0:
		return reasonNotExecutable
	}
	return ""
}

// directoryTrusted is the chain rule for one directory: owned by euid or root, not group/other-writable,
// and not a symlink.
//
// Example: directoryTrusted(0o041777, 0, 1000) is false: /tmp is other-writable.
func directoryTrusted(mode, uid, euid uint32) bool {
	if mode&modeTypeMask == modeSymlink {
		return false
	}
	return (uid == euid || uid == 0) && mode&0o022 == 0
}

// elfMatchesHost is the ELF rule of eci_runtime_binary_is_native: 20 header bytes, ELF magic, class, byte order
// and machine equal to the host's, and OS ABI SYSV or Linux.
//
// Example: an x86-64 header on an arm64 host is false (bytes 18-19 differ).
func elfMatchesHost(candidate, host []byte) bool {
	if len(candidate) < 20 || len(host) < 20 {
		return false
	}
	if candidate[0] != 0x7f || candidate[1] != 'E' || candidate[2] != 'L' || candidate[3] != 'F' {
		return false
	}
	for _, i := range [...]int{4, 5, 18, 19} {
		if candidate[i] != host[i] {
			return false
		}
	}
	return candidate[7] == 0 || candidate[7] == 3
}

// receiptDigest returns the hex digest from the receipt's first line when it is exactly
// "<64 lowercase hex>  <name>". A NUL anywhere in the line is malformed.
//
// Example: receiptDigest("eci-hook-helper", []byte(hex64+"  eci-hook-helper\n")) returns hex64, true.
func receiptDigest(name string, data []byte) (string, bool) {
	line := data
	for i, c := range data {
		if c == '\n' {
			line = data[:i]
			break
		}
	}
	if len(line) != 64+2+len(name) {
		return "", false
	}
	for _, c := range line[:64] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", false
		}
	}
	if line[64] != ' ' || line[65] != ' ' || string(line[66:]) != name {
		return "", false
	}
	return string(line[:64]), true
}

// receiptReason applies L17-L18 then L20 given the computed digest; "" accepts. Hashing (L19) happens between.
//
// Example: a valid receipt whose digest differs from the file's gives reasonMismatch.
func receiptReason(name string, data []byte, digest string) string {
	if !nameIsSafe(name) {
		return reasonNameNotAllowed
	}
	want, ok := receiptDigest(name, data)
	if !ok {
		return reasonMalformed
	}
	if digest != want {
		return reasonMismatch
	}
	return ""
}
