package main

// Deny reasons. Each string is byte-identical to the text the former bash loader printed for the same
// condition (the macOS bash path in lib/eci-native-binary.sh still prints every one it shares), and each
// appears exactly once in this module.
//
// Check order. The first failing check wins; the self-check runs L4 and L6-L20 on the running image
// under the name eci-verexec before the target's L4.
//
//	label  check                                                              reason constant
//	L1     name is one of the three gate binaries, and HOME is absolute        reasonNameOrHome
//	L2     $HOME/.omp/agent/eci/bin resolves (realpath -e)                     reasonBinMissing
//	L3     / to bin, opened O_PATH|O_NOFOLLOW: each owned by euid or root,     reasonChain
//	       none group/other-writable, none a symlink
//	L4     fstatat(bin, name, NOFOLLOW) is a regular file, not a symlink       reasonMissingOrLink
//	L5     openat(bin, name, O_RDONLY|O_NOFOLLOW|O_NONBLOCK) succeeds          reasonCannotOpen
//	L6     fstat(fd) fails with EBADF                                          reasonFDUnresolved
//	L7     fd (dev, ino) equals the L4 (dev, ino)                              reasonOutside
//	L8     fstat(fd) fails otherwise                                           reasonCannotStat
//	L9     fd is a regular file                                                reasonNotRegular
//	L10    link count is 1                                                     reasonOutside
//	L11    owner is euid                                                       reasonNotOwned
//	L12    mode is readable (unreachable natively; kept for order)             reasonUnreadableMode
//	L13    mode & 022 == 0                                                     reasonWritable
//	L14    mode & 0111 != 0                                                    reasonNotExecutable
//	L15    20 header bytes: ELF magic, class, byte order and machine of the    reasonNotELF
//	       host, OS ABI 0 or 3
//	L16    bin/name.sha256 is a regular file, not a symlink                    reasonNoReceipt
//	L17    name matches ^[a-z0-9-]+$ (unreachable after L1; kept for order)   reasonNameNotAllowed
//	L18    first receipt line is exactly "<64 lowercase hex>  <name>"          reasonMalformed
//	L19    pread hash of [0, st_size) succeeds                                 reasonCannotHash
//	L20    digest equals the receipt                                           reasonMismatch
const (
	// reasonNameOrHome is L1.
	//
	// Example: `eci-verexec --check --deny-fd 3 eci-worker-git` denies with it.
	reasonNameOrHome = "not an allowed artifact name or HOME is not absolute"
	// reasonBinMissing is L2.
	//
	// Example: a HOME with no .omp/agent/eci/bin denies with it.
	reasonBinMissing = "bin directory is missing"
	// reasonChain is L3.
	//
	// Example: a 0775 $HOME/.omp denies with it.
	reasonChain = "bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"
	// reasonMissingOrLink is L4, and the shim's reason for a missing or symlinked eci-verexec.
	//
	// Example: bin/eci-hook-helper as a symlink denies with it.
	reasonMissingOrLink = "is missing or a symlink"
	// reasonCannotOpen is L5.
	//
	// Example: a 0311 target, which the owner cannot read, denies with it.
	reasonCannotOpen = "cannot be opened"
	// reasonFDUnresolved is L6.
	//
	// Example: verifyFD on a closed descriptor number denies with it.
	reasonFDUnresolved = "descriptor cannot be resolved"
	// reasonOutside is L7 and L10.
	//
	// Example: a target with a second hard link in bin/ denies with it.
	reasonOutside = "resolves outside the verified bin directory"
	// reasonCannotStat is L8.
	//
	// Example: an fstat failure other than EBADF denies with it.
	reasonCannotStat = "cannot be stat'ed"
	// reasonNotRegular is L9.
	//
	// Example: verifyFD on a directory descriptor denies with it.
	reasonNotRegular = "is not a regular file"
	// reasonNotOwned is L11.
	//
	// Example: a root-owned binary bind-mounted over the target denies with it.
	reasonNotOwned = "is not owned by the current user"
	// reasonUnreadableMode is L12; only the decision tests reach it.
	//
	// Example: descriptorReason(f, euid, false) returns it.
	reasonUnreadableMode = "has an unreadable mode"
	// reasonWritable is L13.
	//
	// Example: a 0757 target denies with it.
	reasonWritable = "is group- or other-writable"
	// reasonNotExecutable is L14.
	//
	// Example: a 0644 target denies with it.
	reasonNotExecutable = "is not executable"
	// reasonNotELF is L15.
	//
	// Example: a #! script with a matching receipt denies with it.
	reasonNotELF = "is not an ELF binary for this host"
	// reasonNoReceipt is L16.
	//
	// Example: a target with no bin/<name>.sha256 denies with it.
	reasonNoReceipt = "has no sha256 receipt"
	// reasonNameNotAllowed is L17.
	//
	// Example: receiptReason("eci.hook", data, digest) returns it.
	reasonNameNotAllowed = "is not an allowed artifact name"
	// reasonMalformed is L18.
	//
	// Example: a receipt with one space between digest and name denies with it.
	reasonMalformed = "has a malformed sha256 receipt"
	// reasonCannotHash is L19.
	//
	// Example: hashFD on a descriptor that fails pread denies with it.
	reasonCannotHash = "cannot be hashed"
	// reasonMismatch is L20.
	//
	// Example: a target with one byte appended after its receipt was written denies with it.
	reasonMismatch = "sha256 does not match its receipt"
)

// buildStep is literal: the deny text names $HOME unexpanded, as the bash loader always has.
//
// Example: every deny line ends in `provision it with "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" and retry`.
const buildStep = `$HOME/.omp/agent/eci/hooks/build-native-binaries.sh`

// selfName is the verifier's own artifact name; its self-check denies under this name.
//
// Example: a group-writable bin/eci-verexec denies "ECI native gate binary eci-verexec is unavailable ...".
const selfName = "eci-verexec"

// denialText is the full deny line for name and reason, with its trailing newline.
//
// Example: denialText("eci-hook-helper", reasonNotExecutable) is
// "ECI native gate binary eci-hook-helper is unavailable or unverified (is not executable); provision it with \"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry\n".
func denialText(name, reason string) string {
	return "ECI native gate binary " + name + " is unavailable or unverified (" + reason + "); provision it with \"" + buildStep + "\" and retry\n"
}
