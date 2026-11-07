package main

import "slices"

// ReasonCode names a recorded decision for a unit that has no omp counterpart, or that an omp implementation supersedes.
// Only the codes in recordedReasons are accepted, so a reason code outside the vocabulary fails the check.
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
	// ReasonCodexOnlyLaneReminder marks the Codex-only lane reminder wrapper, which omp does not port.
	ReasonCodexOnlyLaneReminder ReasonCode = "codex-only-lane-reminder"
	// ReasonCodexOnlyModelBridge marks the Codex-only model bridge wrapper, which omp does not port.
	ReasonCodexOnlyModelBridge ReasonCode = "codex-only-model-bridge"
	// ReasonCodexPathDispatcher marks the Codex PATH dispatcher wrapper, which omp does not port.
	ReasonCodexPathDispatcher ReasonCode = "codex-path-dispatcher"
	// ReasonObsolete marks a wrapper the Codex source itself calls obsolete.
	ReasonObsolete ReasonCode = "obsolete"
	// ReasonStopGateRetired marks the stop-gate wrapper of a retired stop gate; TEST-DISPOSITION.md records it.
	ReasonStopGateRetired ReasonCode = "stop-gate-retired"
	// ReasonNoPeerProviderRoot marks a peer-sync behavior that the Codex-free port does not reproduce, because omp has no
	// peer provider root.
	ReasonNoPeerProviderRoot ReasonCode = "no-peer-provider-root"
	// ReasonSuperseded marks a codex unit that an omp implementation supersedes; the excluded row's omp path names it.
	ReasonSuperseded ReasonCode = "superseded"
	// ReasonCodexBoundUnused marks a Codex-bound wrapper that omp does not use; its omp config entry was removed.
	ReasonCodexBoundUnused ReasonCode = "codex-bound-unused"
)

// recordedReasons lists every reason code the checker accepts. "unrecorded" is deliberately absent: a unit with no
// recorded disposition is in the unrecorded state, which fails the check.
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
	ReasonCodexOnlyLaneReminder,
	ReasonCodexOnlyModelBridge,
	ReasonCodexPathDispatcher,
	ReasonObsolete,
	ReasonStopGateRetired,
	ReasonNoPeerProviderRoot,
	ReasonSuperseded,
	ReasonCodexBoundUnused,
}

// recorded reports whether the code is one of the recorded reason codes.
//
// Example: ReasonCode("unrecorded").recorded() is false, and ReasonSuperseded.recorded() is true.
func (code ReasonCode) recorded() bool {
	return slices.Contains(recordedReasons, code)
}
