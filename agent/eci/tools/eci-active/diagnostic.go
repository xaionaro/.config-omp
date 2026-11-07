package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Gate denials use the shape of hooks/lib/eci-diagnostic.sh (omp copy): a stable code, phase, operation, subject,
// reason, and remediation on one line.

var (
	diagExplicitCodeRe = regexp.MustCompile(`^\[(ECI_[A-Z0-9_]+)\]`)
	diagWrapperCodes   = map[string]bool{
		"ECI_COMMAND_IDENTITY_UNSUPPORTED": true, "ECI_GATE_DENIED": true, "ECI_REVIEW_GATE_DENIED": true,
		"ECI_STOP_GATE_DENIED": true, "ECI_DIAGNOSTIC_DENIED": true, "ECI_EDIT_ROUTE_DENIED": true,
	}
	diagNewlineEscaper = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)
	diagUnderscoreRun  = regexp.MustCompile(`_+`)
)

// diagRule maps the first matching glob alternative to a stable code. Patterns use only `*` wildcards.
type diagRule struct {
	patterns []string
	code     string
}

// diagRules mirrors the case statement of eci_diagnostic_code_for_reason, in order.
var diagRules = []diagRule{
	{[]string{"*command identity*", "*unsupported wrapper*", "*unrecognized command form*"}, "ECI_COMMAND_IDENTITY_INVALID"},
	{[]string{"*pre-reviewer*", "*pre reviewer*"}, "ECI_PRE_REVIEWER_SKILL_OR_DELEGATION_REQUIRED"},
	{[]string{"*external-review*", "*external review*"}, "ECI_STOP_REVIEW_VIOLATION_BLOCKED"},
	{[]string{"*git-mutation*", "*git mutation*"}, "ECI_GIT_MUTATION_APPROVAL_REQUIRED"},
	{[]string{"*agent-dispatch*", "*Agent envelope*"}, "ECI_FIRST_AGENT_DISPATCH_REQUIRED"},
	{[]string{"*'ECI_STOP_STATE_UNAVAILABLE'*"}, "ECI_STOP_STATE_UNAVAILABLE"},
	{[]string{"*'LOOP DETECTED'*"}, "ECI_STOP_LOOP_DETECTED"},
	{[]string{"*'active marker'*", "*marker*unresolved*", "*marker*invalid*"}, "ECI_STOP_MARKER_INVALID"},
	{[]string{"*review gate*usage*", "Usage: *eci-review-gate*"}, "ECI_REVIEW_USAGE_DENIED"},
	{[]string{"*proof root*"}, "ECI_REVIEW_PROOF_ROOT_DENIED"},
	{[]string{"*session director*", "*session directory*"}, "ECI_REVIEW_SESSION_DIR_DENIED"},
	{[]string{"*manifest*"}, "ECI_REVIEW_MANIFEST_DENIED"},
	{[]string{"*schema*"}, "ECI_REVIEW_SCHEMA_DENIED"},
	{[]string{"*binding*"}, "ECI_REVIEW_BINDING_DENIED"},
	{[]string{"*artifact*"}, "ECI_REVIEW_ARTIFACT_DENIED"},
	{[]string{"*target*"}, "ECI_REVIEW_TARGET_DENIED"},
	{[]string{"*diff*"}, "ECI_REVIEW_DIFF_DENIED"},
	{[]string{"*critic*"}, "ECI_REVIEW_CRITIC_DENIED"},
	{[]string{"*ledger*"}, "ECI_REVIEW_LEDGER_DENIED"},
	{[]string{"*transaction*"}, "ECI_REVIEW_TRANSACTION_DENIED"},
	{[]string{"*anchor*"}, "ECI_REVIEW_ANCHOR_DENIED"},
	{[]string{"*receipt*"}, "ECI_REVIEW_RECEIPT_DENIED"},
	{[]string{"*acceptance_version*", "*acceptance version*"}, "ECI_REVIEW_ACCEPTANCE_VERSION_DENIED"},
	{[]string{"*version*"}, "ECI_REVIEW_VERSION_DENIED"},
	{[]string{"*git push*"}, "ECI_GIT_PUSH_DENIED"},
	{[]string{"*no-progress*", "*no progress*", "*no terminal output*", "*blocked on 3 times*"}, "ECI_NO_PROGRESS_BLOCK_DENIED"},
	{[]string{"*lock*"}, "ECI_REVIEW_LOCK_DENIED"},
	{[]string{"*could not*", "*cannot*", "*unable*"}, "ECI_REVIEW_IO_DENIED"},
	{[]string{"*unsafe *path*", "*unsafe resolved*path*"}, "ECI_UNSAFE_PATH_DENIED"},
	{[]string{"*requires a current session*", "*current session id*"}, "ECI_SESSION_REQUIRED"},
	{[]string{"*belongs to session*", "*allowed sessions*"}, "ECI_SESSION_OWNERSHIP_DENIED"},
	{[]string{"*ownership check failed*"}, "ECI_OWNERSHIP_CHECK_FAILED"},
	{[]string{"*plan files*", "*docs/plans*", "*superpowers/plans*"}, "ECI_PLAN_PATH_DENIED"},
	{[]string{"*vendor*", "*third-party*", "*thirdparty*"}, "ECI_VENDOR_PATH_DENIED"},
	{[]string{"*git submodule*"}, "ECI_SUBMODULE_PATH_DENIED"},
	{[]string{"*local relative replace*", "*local relative*go.mod*"}, "ECI_GOMOD_LOCAL_REPLACE_DENIED"},
	{[]string{"*finite memory cap*", "*uncapped make*"}, "ECI_MAKE_CAP_REQUIRED"},
	{[]string{"*-count=1*", "*-count 1*", "*test cache*"}, "ECI_GO_TEST_CACHE_DENIED"},
	{[]string{"*go test output*", "*captured to a file*"}, "ECI_GO_TEST_OUTPUT_DENIED"},
	{[]string{"*git reset*"}, "ECI_GIT_RESET_DENIED"},
}

// globMatch reports whether s matches pat, where `*` matches any run of bytes and everything else is literal.
func globMatch(pat, s string) bool {
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return s == pat
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, last)
}

// diagValue escapes line breaks and bounds one diagnostic field to 4096 characters.
func diagValue(v string) string {
	v = diagNewlineEscaper.Replace(v)
	if r := []rune(v); len(r) > 4096 {
		v = string(r[:4093]) + "..."
	}
	return v
}

// diagSubject is diagValue with the 512-character subject bound.
func diagSubject(v string) string {
	v = diagNewlineEscaper.Replace(v)
	if r := []rune(v); len(r) > 512 {
		v = string(r[:509]) + "..."
	}
	return v
}

// diagStripExplicit removes a leading "[CODE]" wrapper and the separators that follow it.
func diagStripExplicit(reason, code string) string {
	reason = strings.TrimPrefix(reason, "["+code+"]")
	reason = strings.TrimPrefix(reason, " ")
	reason = strings.TrimPrefix(reason, ":")
	return strings.TrimPrefix(reason, " ")
}

// diagCodeForReason classifies a denial reason into a stable ECI code.
func diagCodeForReason(reason string) string {
	if m := diagExplicitCodeRe.FindStringSubmatch(reason); m != nil {
		if !diagWrapperCodes[m[1]] {
			return m[1]
		}
		reason = diagStripExplicit(reason, m[1])
	}
	for _, rule := range diagRules {
		for _, pat := range rule.patterns {
			if globMatch(pat, reason) {
				return rule.code
			}
		}
	}
	normalized := []rune(strings.ToUpper(reason))
	for i, r := range normalized {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			normalized[i] = '_'
		}
	}
	collapsed := strings.Trim(diagUnderscoreRun.ReplaceAllString(string(normalized), "_"), "_")
	if collapsed == "" {
		collapsed = "UNSPECIFIED"
	}
	if r := []rune(collapsed); len(r) > 48 {
		collapsed = string(r[:48])
	}
	return "ECI_GATE_FAILURE_" + collapsed
}

// diagReason renders one denial line. A wrapper code is replaced by the code its reason classifies to.
func diagReason(code, phase, operation, subject, reason, remediation string) string {
	if code == "" {
		code = "ECI_DIAGNOSTIC_CODE_MISSING"
	}
	if strings.HasPrefix(code, "[") && strings.HasSuffix(code, "]") {
		code = code[1 : len(code)-1]
	}
	if m := diagExplicitCodeRe.FindStringSubmatch(reason); m != nil && diagWrapperCodes[m[1]] {
		reason = diagStripExplicit(reason, m[1])
	}
	if diagWrapperCodes[code] {
		code = diagCodeForReason(reason)
	}
	return fmt.Sprintf("[%s] ECI gate denied (phase=%s, operation=%s, subject=%s); reason: %s; remediation: %s",
		code, phase, operation, diagSubject(subject), diagValue(reason), diagValue(remediation))
}
