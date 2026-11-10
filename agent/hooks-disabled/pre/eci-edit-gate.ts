// omp PreToolUse gate for `edit` and `write`. It resolves every target the way the host
// does, then makes one call to eci-edit-gate.sh, whose verified eci-hook-helper runs
// pretooluse-edit-dispatch.sh once per distinct target. All targets share one deadline.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { unwrapHashlineHeaderPath } from "@oh-my-pi/pi-coding-agent/tools/hashline-header-path";
import { expandPath } from "@oh-my-pi/pi-coding-agent/tools/path-utils";
import { type EditInspection, editInspect } from "@oh-my-pi/pi-natives";
import { advisoryContext, basePayload, type EciIdentity, identityFor, preToolVerdict, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";
import { ensureHookBinary, hookAgentRoot } from "../../eci/native-hook-binary.ts";

const SCRIPT = "eci-edit-gate.sh";
// With the runner's KILL_GRACE_SEC + DRAIN_SEC group cleanup this ends within 27 seconds,
// inside omp's default 30-second tool_call handler budget (extensionHandlers.toolCallTimeoutMs).
// A lower configured budget makes the host block the call first.
const EDIT_GATE_DEADLINE_SEC = 20;
const INSPECTION_RECOVERY = "Correct the edit and retry, or read the exact file and use Write for the intended content; Write remains subject to the same target checks.";

/** A target as requested and as the host resolves it; Go checks file_path and reports path. */
type GateTarget = { path: string; file_path: string };

/** Paths the host hands to native edit resolution: expandPath semantics (colon, `@`, `~`). */
function inspectedTarget(path: string): GateTarget {
	return { path, file_path: expandPath(path) };
}

/** A whole tool `path` argument: the host also unwraps a `[path#TAG]` hashline header. */
function pathArgumentTarget(path: string): GateTarget {
	return { path, file_path: expandPath(unwrapHashlineHeaderPath(path)) };
}

/**
 * Edit payload shapes select the inspection grammar, as the host's ACP permission gate
 * does: `edits` is patch mode, `input` is hashline, apply_patch or sloppy (`*** Edit File:`;
 * the three grammars do not overlap), and a bare `path` is replace mode.
 */
function editModes(input: Record<string, unknown>): string[] {
	if (Array.isArray(input.edits)) return ["patch"];
	if (typeof input.input === "string") return ["hashline", "apply_patch", "sloppy"];
	return [];
}

/** Every edit source and move destination, in payload order. */
function editTargets(input: Record<string, unknown>): { targets: GateTarget[]; errors: string[] } {
	const targets: GateTarget[] = [];
	const errors: string[] = [];
	const pathArgument = typeof input.path === "string" && input.path !== "" ? input.path : undefined;
	if (pathArgument !== undefined) targets.push(pathArgumentTarget(pathArgument));
	const argsJson = JSON.stringify(input);
	for (const mode of editModes(input)) {
		let inspection: EditInspection;
		try {
			inspection = editInspect(mode, argsJson);
		} catch (err) {
			errors.push(`${mode}: ${String(err)}`);
			continue;
		}
		for (const path of inspection.paths) if (path !== pathArgument) targets.push(inspectedTarget(path));
		for (const op of inspection.fileOps) if (op.kind === "move" && op.to !== undefined) targets.push(inspectedTarget(op.to));
	}
	return { targets, errors };
}

function shellQuote(value: string): string {
	return `'${value.replaceAll("'", "'\\''")}'`;
}

/** Timeout recovery naming the exact request and identity of the timed-out call. */
function timeoutReason(request: Record<string, unknown>, id: EciIdentity): string {
	const session = shellQuote(id.sessionId);
	const env = `ECI_SESSION_ID=${session} ECI_THREAD_ID=${session} ECI_HOOK_IS_SUBAGENT=${id.isSubagent ? "1" : "0"}`;
	return [
		`ECI edit target checks timed out after ${EDIT_GATE_DEADLINE_SEC} seconds. Retry in the same session, or split a multi-target edit.`,
		`If one target still times out, reproduce the call with Bash: printf '%s\\n' ${shellQuote(JSON.stringify(request))} | ${env} bash "$HOME/.omp/agent/eci/hooks/${SCRIPT}".`,
		`To trace one target, pipe {session_id, cwd, hook_event_name:"PreToolUse", tool_name, tool_input:{file_path}} with the same environment to bash -x "$HOME/.omp/agent/eci/hooks/pretooluse-edit-dispatch.sh"; it starts validate-edit-write.sh and eci-active-gate.sh as separate processes that bash -x does not trace, so trace a slow guard the same way.`,
		"Repair the cause within your owned scope; if it lies outside, report it to its owner.",
	].join(" ");
}

export default function eciEditGate(pi: ExtensionAPI): void {
	// Start a cold build at hook initialization, before the per-call gate budget.
	ensureHookBinary(hookAgentRoot()).catch(() => {});
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
			// A failed startup build may be retried after its cause is repaired.
			await ensureHookBinary(hookAgentRoot());
			const deadline = performance.now() + EDIT_GATE_DEADLINE_SEC * 1000;
			const input = event.input as Record<string, unknown>;
			const id = { ...identityFor(ctx, { useHostAgentKind: true }), sessionId: ctx.sessionManager.getSessionId() };
			let targets: GateTarget[];
			if (event.toolName === "edit") {
				const inspected = editTargets(input);
				if (inspected.targets.length === 0) {
					const why = inspected.errors.length > 0 ? `failed (${inspected.errors.join("; ")})` : "found no targets";
					return { block: true, reason: `ECI edit target inspection ${why}. ${INSPECTION_RECOVERY}` };
				}
				targets = inspected.targets;
			} else {
				// The host write schema has one required `path`; without it the host rejects the call.
				if (typeof input.path !== "string" || input.path === "") return undefined;
				targets = [pathArgumentTarget(input.path)];
			}
			const request = { ...basePayload(id, "PreToolUse"), tool_name: event.toolName === "edit" ? "Edit" : "Write", targets };
			const remaining = (deadline - performance.now()) / 1000;
			if (remaining <= 0) return { block: true, reason: timeoutReason(request, id) };
			const run = await runEciHook(scriptPath(SCRIPT), request, id, remaining, { cleanup: "owned-group" });
			if (run.outcome.kind === "ran" && run.outcome.stop.kind === "deadline") return { block: true, reason: timeoutReason(request, id) };
			const verdict = preToolVerdict(run, "eci-edit-gate");
			if (verdict) return verdict;
			const warning = advisoryContext(run, "PreToolUse");
			return warning === undefined ? undefined : { additionalContext: warning };
		} catch (err) {
			return { block: true, reason: "ECI hook error: " + String(err) };
		}
	});
}
