package main

import "slices"

// ReasonCode names a recorded decision for a unit that has no omp counterpart. Only the codes in recordedReasons are
// accepted, so a unit whose disposition was never recorded fails the check.
type ReasonCode string

// Recorded reason codes. Each code is one decision made during the port; the evidence column of the manifest row
// cites where that decision is recorded.
const (
	// ReasonCodexLedger marks claude-port/README.md, the Codex-only status ledger whose named paths are absent at 575bb81.
	ReasonCodexLedger ReasonCode = "codex-ledger"
	// ReasonConfigMoved marks config.toml, whose developer_instructions moved into agent/AGENTS.md.
	ReasonConfigMoved ReasonCode = "config-moved"
	// ReasonTestedToolUnported marks a Codex test whose tested tool is not ported; TEST-DISPOSITION.md records it.
	ReasonTestedToolUnported ReasonCode = "tested-tool-unported"
	// ReasonCodexInstallStamp marks the Codex install stamp, which has no omp reader.
	ReasonCodexInstallStamp ReasonCode = "codex-install-stamp"
	// ReasonNeedsCodexImageGen marks a system skill that needs the Codex image_gen tool and CODEX_HOME.
	ReasonNeedsCodexImageGen ReasonCode = "needs-codex-image-gen"
	// ReasonNeedsCodexInstaller marks a system skill that installs into CODEX_HOME from the openai/skills catalog.
	ReasonNeedsCodexInstaller ReasonCode = "needs-codex-installer"
	// ReasonDuplicateOpenGap marks a system skill that duplicates an omp skill and keeps a gap with no omp counterpart.
	ReasonDuplicateOpenGap ReasonCode = "duplicate-open-gap"
	// ReasonNeedsOpenAIKey marks a system skill that fetches the Codex-only manual and needs an OpenAI API key omp lacks.
	ReasonNeedsOpenAIKey ReasonCode = "needs-openai-key"
	// ReasonPythonUnrunnable marks a system skill whose Python scripts do not run on this host; an omp skill covers its purpose.
	ReasonPythonUnrunnable ReasonCode = "python-unrunnable"
	// ReasonOtherProviderToolMap marks another provider's tool map; omp keeps its own map.
	ReasonOtherProviderToolMap ReasonCode = "other-provider-tool-map"
	// ReasonCodexWrapperUnported marks documentation of a Codex wrapper that omp does not port.
	ReasonCodexWrapperUnported ReasonCode = "codex-wrapper-unported"
	// ReasonBehaviorNotPorted marks a build, refresh, or install behavior of a Codex bin wrapper that omp does not
	// reproduce; the evidence names the omp caller or the omp decision.
	ReasonBehaviorNotPorted ReasonCode = "behavior-not-ported"
)

// recordedReasons lists every reason code the checker accepts. "unrecorded" is deliberately absent: a unit with no
// recorded disposition must fail visibly instead of passing as reason-coded.
var recordedReasons = []ReasonCode{
	ReasonCodexLedger,
	ReasonConfigMoved,
	ReasonTestedToolUnported,
	ReasonCodexInstallStamp,
	ReasonNeedsCodexImageGen,
	ReasonNeedsCodexInstaller,
	ReasonDuplicateOpenGap,
	ReasonNeedsOpenAIKey,
	ReasonPythonUnrunnable,
	ReasonOtherProviderToolMap,
	ReasonCodexWrapperUnported,
	ReasonBehaviorNotPorted,
}

// recorded reports whether the code is one of the recorded reason codes.
//
// Example: ReasonCode("unrecorded").recorded() is false, and ReasonBehaviorNotPorted.recorded() is true.
func (code ReasonCode) recorded() bool {
	return slices.Contains(recordedReasons, code)
}
