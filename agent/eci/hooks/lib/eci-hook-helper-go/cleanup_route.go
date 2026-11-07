package main

// Port of the former python3 program in hooks/lib/eci-cleanup-route.sh.
//
// Mode: cleanup-route <command> <allow-private-session-child: true|false>
//
// Prints "ok" and returns 0 when the literal cleanup command is approved.
// Otherwise prints "coordinator-cleanup-route command=<command> reason=<reason>"
// and returns 1. Usage errors return statusRejected.
//
// Shell words are split with the subset of Python shlex.split(posix=True) that
// applies here: the caller has already rejected quotes, `$`, backticks and
// newlines, so only whitespace splitting and backslash escapes remain.
// Path semantics mirror posixpath (normpath, realpath, relpath) so that the
// canonical-spelling checks match the original program.

import (
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
)

const cleanupRouteMaxPaths = 64

var (
	cleanupSessionTempName = regexp.MustCompile(`^eci-[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	cleanupRunnerFile      = regexp.MustCompile(`^\.eci-runner-test\.[A-Za-z0-9._-]+$`)
	cleanupGeneratedName   = regexp.MustCompile(`^eci-generated-cleanup-[A-Za-z0-9._-]+$`)

	cleanupRootFiles = map[string]bool{"config-new.toml": true, "migrations-effort.json": true}
	cleanupRootDirs  = map[string]bool{
		"cron": true, "search-index": true, "workspace-trust": true,
		"bin/__pycache__": true, "bin/tests/__pycache__": true, "hooks/tests/__pycache__": true,
	}
)

// runCleanupRoute implements the cleanup-route mode. args is
// [command, allow-private-session-child].
func runCleanupRoute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		io.WriteString(stderr, "usage: cleanup-route <command> <true|false>\n")
		return statusRejected
	}
	command := args[0]
	reason := cleanupRouteCheck(command, args[1] == "true")
	if reason != "" {
		io.WriteString(stdout, "coordinator-cleanup-route command="+command+" reason="+reason+"\n")
		return 1
	}
	io.WriteString(stdout, "ok\n")
	return statusOK
}

// cleanupRouteCheck returns "" when the command is an approved cleanup, else
// the reason text that follows "reason=".
func cleanupRouteCheck(command string, allowPrivate bool) string {
	if strings.ContainsAny(command, "\n\r$`'\"") {
		return "literal cleanup command contains quoting, substitution, or newlines"
	}
	tokens, ok := cleanupShellSplit(command)
	if !ok {
		return "shell quoting is unbalanced"
	}

	var mode string
	var paths []string
	temporaryUnqualifiedFileRemove := false
	switch {
	case len(tokens) > 0 && tokens[0] == "mv":
		if len(tokens) != 4 || tokens[1] != "--" {
			return "expected exactly mv -- <approved-artifact> <quarantine-destination>"
		}
		mode = "mv"
		paths = tokens[2:]
	case len(tokens) >= 3 && tokens[0] == "rm" && tokens[1] == "-f":
		mode = "-f"
		if tokens[2] == "--" {
			paths = tokens[3:]
		} else {
			paths = tokens[2:]
			temporaryUnqualifiedFileRemove = true
		}
		if reason := cleanupPathListReason(paths); reason != "" {
			return reason
		}
	case len(tokens) >= 4 && tokens[0] == "rm" && tokens[2] == "--":
		mode = tokens[1]
		if mode != "-f" && mode != "-rf" {
			return "rm option must be exactly -f for files or -rf for directories"
		}
		paths = tokens[3:]
		if reason := cleanupPathListReason(paths); reason != "" {
			return reason
		}
	default:
		return "expected rm -f -- <files>, rm -rf -- <directories>, or mv -- <artifact> <quarantine-destination>"
	}
	if len(paths) == 0 {
		return "cleanup target list is empty"
	}
	if temporaryUnqualifiedFileRemove {
		return cleanupTemporaryRemoveReason(paths, allowPrivate)
	}

	homes, reason := cleanupProviderHomes()
	if reason != "" {
		return reason
	}
	if mode == "mv" {
		return cleanupMoveReason(paths[0], paths[1], homes)
	}
	for _, path := range paths {
		matched, kind := cleanupApprovedPath(path, homes)
		if !matched {
			return "path=" + path + " reason=" + kind
		}
		if kind == cleanupUnapproved {
			return "path=" + path + " reason=path is not an approved generated artifact"
		}
		if pyIsLink(path) {
			return "path=" + path + " reason=symlink targets are not cleanup-eligible"
		}
		if mode == "-f" {
			if kind != "file" {
				return "path=" + path + " reason=-f requires an approved regular file"
			}
			if !pyIsFile(path) || pyIsLink(path) {
				return "path=" + path + " reason=approved file is missing or not a canonical regular file"
			}
		} else {
			if kind != "directory" {
				return "path=" + path + " reason=-rf requires an approved generated directory"
			}
			if !pyIsDir(path) || pyIsLink(path) {
				return "path=" + path + " reason=approved directory is missing or not canonical"
			}
		}
	}
	return ""
}

func cleanupPathListReason(paths []string) string {
	if len(paths) > cleanupRouteMaxPaths {
		return "cleanup target list exceeds the 64-path bound"
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			return "cleanup target list contains duplicate paths"
		}
		seen[path] = true
	}
	return ""
}

// cleanupShellSplit reproduces shlex.split(s, posix=True) for input without
// quotes: whitespace separates words and a backslash escapes the next byte.
// A trailing backslash is an error, as in Python.
func cleanupShellSplit(s string) ([]string, bool) {
	var tokens []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\r', '\n':
			if inWord {
				tokens = append(tokens, word.String())
				word.Reset()
				inWord = false
			}
		case '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			word.WriteByte(s[i])
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		tokens = append(tokens, word.String())
	}
	return tokens, true
}

// cleanupTemporaryRemoveReason validates `rm -f <absolute-temp-files>`.
func cleanupTemporaryRemoveReason(paths []string, allowPrivate bool) string {
	home := os.Getenv("HOME")
	canonicalHome := ""
	if home != "" {
		canonicalHome = pyRealpath(home)
	}

	temporaryRoots := map[string]bool{}
	for _, raw := range []string{
		os.Getenv("ECI_TMPDIR"),
		os.Getenv("TMPDIR"),
		pyIfNonEmpty(home, func() string { return pyJoin(home, "tmp") }),
	} {
		if canonical := cleanupCanonicalDirectory(raw, home, canonicalHome); canonical != "" {
			temporaryRoots[canonical] = true
		}
	}
	if len(temporaryRoots) == 0 {
		return "no canonical temporary directory is available"
	}

	for _, path := range paths {
		if !pyIsAbs(path) || pyNormpath(path) != path || strings.ContainsAny(path, "*?[]") {
			return "path=" + path + " reason=temporary cleanup requires canonical absolute paths"
		}
		parent := pyDirname(path)
		canonicalParent := pyRealpath(parent)
		homeTmpAlias := pyIfNonEmpty(home, func() string { return pyJoin(home, "tmp") })
		parentIsHomeTmpAlias := parent == homeTmpAlias &&
			pyIsLink(parent) &&
			temporaryRoots[canonicalParent] &&
			cleanupExactHomeTmpAlias(parent, canonicalParent, home, canonicalHome)
		if parent != canonicalParent && !parentIsHomeTmpAlias {
			return "path=" + path + " reason=temporary cleanup parent must use its canonical root spelling"
		}
		root := ""
		for candidate := range temporaryRoots {
			if canonicalParent == candidate || pyDirname(canonicalParent) == candidate {
				if cleanupLongerRoot(candidate, root) {
					root = candidate
				}
			}
		}
		if root == "" {
			return "path=" + path + " reason=unqualified rm -f is limited to canonical temporary roots"
		}
		if canonicalParent != root {
			if !allowPrivate || len(paths) != 1 || !cleanupPrivateSessionTempParent(root, parent) {
				return "path=" + path + " reason=temporary cleanup permits one regular leaf only under a private direct eci session directory"
			}
		}
		if pyIsLink(path) {
			return "path=" + path + " reason=symlink targets are not cleanup-eligible"
		}
		if pyLexists(path) && (!pyIsFile(path) || pyRealpath(path) != path) {
			return "path=" + path + " reason=temporary cleanup requires a canonical regular file"
		}
		ancestor := parent
		for !pyLexists(ancestor) && ancestor != pyDirname(ancestor) {
			ancestor = pyDirname(ancestor)
		}
		canonicalAncestor := pyRealpath(ancestor)
		inside := false
		for candidate := range temporaryRoots {
			if canonicalAncestor == candidate || strings.HasPrefix(canonicalAncestor, candidate+"/") {
				inside = true
				break
			}
		}
		if !inside {
			return "path=" + path + " reason=temporary cleanup parent escapes canonical temporary roots"
		}
	}
	return ""
}

// cleanupLongerRoot orders candidates by (len, value), as Python's max() did.
func cleanupLongerRoot(candidate, current string) bool {
	if current == "" {
		return true
	}
	if len(candidate) != len(current) {
		return len(candidate) > len(current)
	}
	return candidate > current
}

func cleanupCanonicalDirectory(raw, home, canonicalHome string) string {
	if raw == "" || !pyIsAbs(raw) || pyNormpath(raw) != raw {
		return ""
	}
	canonical := pyRealpath(raw)
	if !pyIsAbs(canonical) || pyNormpath(canonical) != canonical ||
		!pyIsDir(canonical) || pyIsLink(canonical) || pyRealpath(canonical) != canonical {
		return ""
	}
	if canonical == "/tmp" || strings.HasPrefix(canonical, "/tmp/") {
		return ""
	}
	homeScoped := canonicalHome != "" &&
		(canonical == canonicalHome || strings.HasPrefix(canonical, canonicalHome+"/"))
	if pyIsLink(raw) && !homeScoped && !cleanupExactHomeTmpAlias(raw, canonical, home, canonicalHome) {
		return ""
	}
	return canonical
}

func cleanupExactHomeTmpAlias(raw, canonical, home, canonicalHome string) bool {
	if raw != pyJoin(home, "tmp") || !pyIsLink(raw) {
		return false
	}
	if home == "" || !pyIsAbs(home) || pyNormpath(home) != home ||
		!pyIsDir(home) || pyIsLink(home) ||
		canonicalHome == "" || canonicalHome != home ||
		pyRealpath(canonicalHome) != canonicalHome {
		return false
	}
	return canonical != "/tmp" && !strings.HasPrefix(canonical, "/tmp/")
}

// cleanupPrivateSessionTempParent accepts only a direct, private, 0700
// eci-<name> directory owned by the effective user under root.
func cleanupPrivateSessionTempParent(root, parent string) bool {
	if parent == root || pyDirname(parent) != root {
		return false
	}
	if !pyIsDir(parent) || pyIsLink(parent) || pyRealpath(parent) != parent ||
		!cleanupSessionTempName.MatchString(pyBasename(parent)) {
		return false
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return info.IsDir() &&
		st.Uid == uint32(os.Geteuid()) &&
		uint32(st.Mode)&07777 == 0o700
}

type cleanupHome struct {
	root      string
	canonical string
}

// cleanupProviderHomes returns the canonical ECI home. A non-empty
// reason means no home is usable.
func cleanupProviderHomes() ([]cleanupHome, string) {
	home := os.Getenv("HOME")
	var homes []cleanupHome
	eciHome := pyJoin(pyJoin(pyJoin(home, ".omp"), "agent"), "eci")
	for _, pair := range [][2]string{
		{os.Getenv("ECI_HOME"), eciHome},
	} {
		root := pair[0]
		if root == "" {
			root = pair[1]
		}
		if root == "" || !pyIsAbs(root) || pyNormpath(root) != root {
			continue
		}
		if !pyIsDir(root) || pyIsLink(root) {
			continue
		}
		canonicalRoot := pyRealpath(root)
		if !pyIsAbs(canonicalRoot) || pyNormpath(canonicalRoot) != canonicalRoot ||
			!pyIsDir(canonicalRoot) || pyIsLink(canonicalRoot) ||
			pyRealpath(canonicalRoot) != canonicalRoot {
			continue
		}
		if canonicalRoot != root {
			canonicalHome := ""
			if home != "" {
				canonicalHome = pyRealpath(home)
			}
			if home == "" || !pyIsAbs(home) || pyNormpath(home) != home ||
				!pyIsDir(home) || pyIsLink(home) ||
				canonicalHome == "" || pyRealpath(canonicalHome) != canonicalHome ||
				pyJoin(pyJoin(pyJoin(home, ".omp"), "agent"), "eci") != root ||
				pyJoin(pyJoin(pyJoin(canonicalHome, ".omp"), "agent"), "eci") != canonicalRoot {
				continue
			}
		}
		homes = append(homes, cleanupHome{root: root, canonical: canonicalRoot})
	}
	if len(homes) == 0 {
		return nil, "no canonical ECI home is available"
	}
	return homes, ""
}

const cleanupUnapproved = "unapproved generated path"

// cleanupApprovedPath classifies path against the provider homes. matched is
// false when path lies outside every home; kind is then the reason text. When
// matched, kind is "file", "directory", cleanupUnapproved, or the escape reason.
func cleanupApprovedPath(path string, homes []cleanupHome) (bool, string) {
	if !pyIsAbs(path) || pyNormpath(path) != path {
		return false, "target path must be canonical absolute"
	}
	for _, h := range homes {
		if path != h.root && !strings.HasPrefix(path, h.root+"/") {
			continue
		}
		canonicalPath := pyRealpath(path)
		if canonicalPath != h.canonical && !strings.HasPrefix(canonicalPath, h.canonical+"/") {
			return true, "resolved target escapes canonical provider home"
		}
		relative := pyRelpath(canonicalPath, h.canonical)
		if cleanupRootFiles[relative] {
			return true, "file"
		}
		if cleanupRootDirs[relative] {
			return true, "directory"
		}
		if d := pyDirname(relative); (d == "" || d == ".") && cleanupRunnerFile.MatchString(pyBasename(relative)) {
			if pyIsDir(path) {
				return true, "directory"
			}
			if pyIsFile(path) {
				return true, "file"
			}
			return true, cleanupUnapproved
		}
		return true, cleanupUnapproved
	}
	return false, "target is outside canonical ECI home"
}

// cleanupMoveReason validates `mv -- <approved-artifact> <quarantine-destination>`.
func cleanupMoveReason(source, destination string, homes []cleanupHome) string {
	matched, kind := cleanupApprovedPath(source, homes)
	if !matched {
		return "path=" + source + " reason=" + kind
	}
	if kind == cleanupUnapproved {
		return "path=" + source + " reason=path is not an approved generated artifact"
	}
	if kind != "file" && kind != "directory" {
		return "path=" + source + " reason=" + kind
	}
	if pyIsLink(source) {
		return "path=" + source + " reason=symlink targets are not cleanup-eligible"
	}
	if (kind == "file" && (!pyIsFile(source) || pyIsLink(source))) ||
		(kind == "directory" && (!pyIsDir(source) || pyIsLink(source))) {
		return "path=" + source + " reason=approved artifact is missing or not canonical"
	}

	home := os.Getenv("HOME")
	configuredTmpdir := os.Getenv("TMPDIR")
	if configuredTmpdir == "" {
		configuredTmpdir = pyJoin(home, "tmp")
	}
	if !pyIsAbs(configuredTmpdir) || pyNormpath(configuredTmpdir) != configuredTmpdir {
		return "configured TMPDIR must be an absolute normalized path"
	}
	realTmpdir := pyRealpath(configuredTmpdir)
	if !pyIsAbs(realTmpdir) || pyNormpath(realTmpdir) != realTmpdir ||
		!pyIsDir(realTmpdir) || pyIsLink(realTmpdir) || pyRealpath(realTmpdir) != realTmpdir {
		return "configured TMPDIR does not resolve to a canonical directory"
	}
	canonicalHome := ""
	if home != "" {
		canonicalHome = pyRealpath(home)
	}
	homeScopedTmp := canonicalHome != "" &&
		(realTmpdir == canonicalHome || strings.HasPrefix(realTmpdir, canonicalHome+"/"))
	if pyIsLink(configuredTmpdir) && !homeScopedTmp {
		return "configured TMPDIR symlink must resolve within the home-scoped temporary tree"
	}
	if realTmpdir == "/tmp" || strings.HasPrefix(realTmpdir, "/tmp/") {
		return "configured TMPDIR must resolve to a non-system temporary directory"
	}
	destinationParent := pyDirname(destination)
	destinationParentReal := pyRealpath(destinationParent)
	if !pyIsAbs(destination) || pyNormpath(destination) != destination ||
		pyIsLink(destinationParent) || destinationParentReal != realTmpdir {
		return "destination must be a direct child of the canonical non-system TMPDIR or home-scoped temporary root; configured_tmpdir=" +
			configuredTmpdir + "; real_tmpdir=" + realTmpdir + "; destination_parent=" + destinationParent +
			"; allowed_temp_roots=" + realTmpdir
	}
	if !cleanupGeneratedName.MatchString(pyBasename(destination)) {
		return "destination basename must match eci-generated-cleanup-<literal-safe-name>"
	}
	if pyLexists(destination) {
		return "destination must not already exist"
	}
	return ""
}

// ---- posixpath-equivalent helpers ----

func pyIfNonEmpty(s string, f func() string) string {
	if s == "" {
		return ""
	}
	return f()
}

func pyIsAbs(p string) bool { return strings.HasPrefix(p, "/") }

// pyNormpath mirrors posixpath.normpath, including the two-leading-slash rule.
func pyNormpath(p string) string {
	if p == "" {
		return "."
	}
	initial := 0
	if strings.HasPrefix(p, "/") {
		initial = 1
		if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
			initial = 2
		}
	}
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		if c != ".." || (initial == 0 && len(out) == 0) || (len(out) > 0 && out[len(out)-1] == "..") {
			out = append(out, c)
		} else if len(out) > 0 {
			out = out[:len(out)-1]
		}
	}
	res := strings.Join(out, "/")
	if initial > 0 {
		res = strings.Repeat("/", initial) + res
	}
	if res == "" {
		return "."
	}
	return res
}

func pySplit(p string) (string, string) {
	i := strings.LastIndexByte(p, '/') + 1
	head, tail := p[:i], p[i:]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head, tail
}

func pyDirname(p string) string {
	head, _ := pySplit(p)
	return head
}

func pyBasename(p string) string {
	_, tail := pySplit(p)
	return tail
}

// pyJoin mirrors posixpath.join for two components.
func pyJoin(a, b string) string {
	if strings.HasPrefix(b, "/") {
		return b
	}
	if a == "" || strings.HasSuffix(a, "/") {
		return a + b
	}
	return a + "/" + b
}

func pyAbspath(p string) string {
	if !pyIsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			wd = ""
		}
		p = pyJoin(wd, p)
	}
	return pyNormpath(p)
}

// pyRealpath mirrors posixpath.realpath (non-strict).
func pyRealpath(p string) string {
	path, _ := pyJoinRealpath("", p, map[string]*string{})
	return pyAbspath(path)
}

func pyJoinRealpath(path, rest string, seen map[string]*string) (string, bool) {
	if pyIsAbs(rest) {
		rest = rest[1:]
		path = "/"
	}
	for rest != "" {
		var name string
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name, rest = rest[:i], rest[i+1:]
		} else {
			name, rest = rest, ""
		}
		if name == "" || name == "." {
			continue
		}
		if name == ".." {
			if path != "" {
				var last string
				path, last = pySplit(path)
				if last == ".." {
					path = pyJoin(pyJoin(path, ".."), "..")
				}
			} else {
				path = ".."
			}
			continue
		}
		newpath := pyJoin(path, name)
		if !pyIsLink(newpath) {
			path = newpath
			continue
		}
		if v, ok := seen[newpath]; ok {
			if v != nil {
				path = *v
				continue
			}
			return pyJoin(newpath, rest), false
		}
		seen[newpath] = nil
		target, err := os.Readlink(newpath)
		if err != nil {
			return pyJoin(newpath, rest), false
		}
		resolved, ok := pyJoinRealpath(path, target, seen)
		if !ok {
			return pyJoin(resolved, rest), false
		}
		path = resolved
		seen[newpath] = &resolved
	}
	return path, true
}

// pyRelpath mirrors posixpath.relpath(p, start).
func pyRelpath(p, start string) string {
	start = pyAbspath(start)
	p = pyAbspath(p)
	startParts := cleanupNonEmptyParts(start)
	pathParts := cleanupNonEmptyParts(p)
	i := 0
	for i < len(startParts) && i < len(pathParts) && startParts[i] == pathParts[i] {
		i++
	}
	var rel []string
	for k := i; k < len(startParts); k++ {
		rel = append(rel, "..")
	}
	rel = append(rel, pathParts[i:]...)
	if len(rel) == 0 {
		return "."
	}
	return strings.Join(rel, "/")
}

func cleanupNonEmptyParts(p string) []string {
	var parts []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			parts = append(parts, c)
		}
	}
	return parts
}

func pyLexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func pyIsLink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func pyIsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func pyIsFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
