package main

import "slices"

// ReasonCode names a recorded decision for a unit that has no omp counterpart, or that an omp implementation supersedes.
// Only the codes in recordedReasons are accepted, so a reason code outside the vocabulary fails the check.
type ReasonCode string

// Recorded reason codes. Each code is one decision made during the port; the evidence column of the manifest row
// cites where that decision is recorded.
const (
	// ReasonSourceLedger marks claude-port/README.md, a status ledger that exists only in the pre-migration source; its
	// named paths are absent at 575bb81.
	ReasonSourceLedger ReasonCode = "source-ledger"
	// ReasonConfigMoved marks config.toml, whose developer_instructions moved into agent/AGENTS.md.
	ReasonConfigMoved ReasonCode = "config-moved"
	// ReasonTestedToolUnported marks a test whose tested tool is not ported; TEST-DISPOSITION.md records it.
	ReasonTestedToolUnported ReasonCode = "tested-tool-unported"
	// ReasonSourceInstallStamp marks the install stamp of the pre-migration source, which has no omp reader.
	ReasonSourceInstallStamp ReasonCode = "source-install-stamp"
	// ReasonNeedsImageGenTool marks a system skill that needs an image generation tool and a home variable omp lacks.
	ReasonNeedsImageGenTool ReasonCode = "needs-image-gen-tool"
	// ReasonNeedsInstallerCatalog marks a system skill that installs into the provider home from an external catalog.
	ReasonNeedsInstallerCatalog ReasonCode = "needs-installer-catalog"
	// ReasonDuplicateOpenGap marks a system skill that duplicates an omp skill and keeps a gap with no omp counterpart.
	ReasonDuplicateOpenGap ReasonCode = "duplicate-open-gap"
	// ReasonNeedsOpenAIKey marks a system skill that fetches a manual available only in the pre-migration source and
	// needs an API key omp lacks.
	ReasonNeedsOpenAIKey ReasonCode = "needs-openai-key"
	// ReasonPythonUnrunnable marks a system skill whose Python scripts do not run on this host; an omp skill covers its purpose.
	ReasonPythonUnrunnable ReasonCode = "python-unrunnable"
	// ReasonOtherProviderToolMap marks another provider's tool map; omp keeps its own map.
	ReasonOtherProviderToolMap ReasonCode = "other-provider-tool-map"
	// ReasonSourceWrapperUnported marks documentation of a pre-migration source wrapper that omp does not port.
	ReasonSourceWrapperUnported ReasonCode = "source-wrapper-unported"
	// ReasonSourceOnlyLaneReminder marks a lane reminder wrapper that exists only in the pre-migration source.
	ReasonSourceOnlyLaneReminder ReasonCode = "source-only-lane-reminder"
	// ReasonSourceOnlyModelBridge marks a model bridge wrapper that exists only in the pre-migration source.
	ReasonSourceOnlyModelBridge ReasonCode = "source-only-model-bridge"
	// ReasonSourcePathDispatcher marks a PATH dispatcher wrapper that exists only in the pre-migration source.
	ReasonSourcePathDispatcher ReasonCode = "source-path-dispatcher"
	// ReasonObsolete marks a wrapper that its source calls obsolete.
	ReasonObsolete ReasonCode = "obsolete"
	// ReasonStopGateRetired marks the stop-gate wrapper of a retired stop gate; TEST-DISPOSITION.md records it.
	ReasonStopGateRetired ReasonCode = "stop-gate-retired"
	// ReasonNoPeerProviderRoot marks a peer-sync behavior that the port does not reproduce, because omp has no peer
	// provider root.
	ReasonNoPeerProviderRoot ReasonCode = "no-peer-provider-root"
	// ReasonSuperseded marks a unit that an omp implementation supersedes; the excluded row's omp path names it.
	ReasonSuperseded ReasonCode = "superseded"
	// ReasonSourceBoundUnused marks a wrapper bound to the pre-migration source whose omp config entry was removed.
	ReasonSourceBoundUnused ReasonCode = "source-bound-unused"
)

// recordedReasons lists every reason code the checker accepts. "unrecorded" is deliberately absent: a unit with no
// recorded disposition is in the unrecorded state, which fails the check.
var recordedReasons = []ReasonCode{
	ReasonSourceLedger,
	ReasonConfigMoved,
	ReasonTestedToolUnported,
	ReasonSourceInstallStamp,
	ReasonNeedsImageGenTool,
	ReasonNeedsInstallerCatalog,
	ReasonDuplicateOpenGap,
	ReasonNeedsOpenAIKey,
	ReasonPythonUnrunnable,
	ReasonOtherProviderToolMap,
	ReasonSourceWrapperUnported,
	ReasonSourceOnlyLaneReminder,
	ReasonSourceOnlyModelBridge,
	ReasonSourcePathDispatcher,
	ReasonObsolete,
	ReasonStopGateRetired,
	ReasonNoPeerProviderRoot,
	ReasonSuperseded,
	ReasonSourceBoundUnused,
}

// recorded reports whether the code is one of the recorded reason codes.
//
// Example: ReasonCode("unrecorded").recorded() is false, and ReasonSuperseded.recorded() is true.
func (code ReasonCode) recorded() bool {
	return slices.Contains(recordedReasons, code)
}
