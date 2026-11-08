package main

// Deny reasons. Each string is byte-identical to the one lib/eci-native-binary.sh prints for the same
// condition, and each appears exactly once in this module.
const (
	reasonNameOrHome     = "not an allowed artifact name or HOME is not absolute"
	reasonBinMissing     = "bin directory is missing"
	reasonChain          = "bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"
	reasonMissingOrLink  = "is missing or a symlink"
	reasonCannotOpen     = "cannot be opened"
	reasonFDUnresolved   = "descriptor cannot be resolved"
	reasonOutside        = "resolves outside the verified bin directory"
	reasonCannotStat     = "cannot be stat'ed"
	reasonNotRegular     = "is not a regular file"
	reasonNotOwned       = "is not owned by the current user"
	reasonUnreadableMode = "has an unreadable mode"
	reasonWritable       = "is group- or other-writable"
	reasonNotExecutable  = "is not executable"
	reasonNotELF         = "is not an ELF binary for this host"
	reasonNoReceipt      = "has no sha256 receipt"
	reasonNameNotAllowed = "is not an allowed artifact name"
	reasonMalformed      = "has a malformed sha256 receipt"
	reasonCannotHash     = "cannot be hashed"
	reasonMismatch       = "sha256 does not match its receipt"
)

// buildStep is literal: the bash loader prints $HOME unexpanded.
const buildStep = `$HOME/.omp/agent/eci/hooks/build-native-binaries.sh`

// selfName is the verifier's own artifact name; its self-check denies under this name.
const selfName = "eci-verexec"

func denialText(name, reason string) string {
	return "ECI native gate binary " + name + " is unavailable or unverified (" + reason + "); provision it with \"" + buildStep + "\" and retry\n"
}
