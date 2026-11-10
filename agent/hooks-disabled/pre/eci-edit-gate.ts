// omp PreToolUse gate for `edit` and `write` → pretooluse-edit-dispatch.sh.
// Edit targets use the host hashline inspector, including move destinations.
// Write targets are path/paths. Each distinct target gets one dispatcher run.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { editInspect } from "@oh-my-pi/pi-natives";
import { basePayload, identityFor, preToolVerdict, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "pretooluse-edit-dispatch.sh";
// The host gives handlers 30 seconds; reserve seven seconds for child cleanup.
const EDIT_GATE_DEADLINE_SEC = 20;
const TIMEOUT_REASON = "ECI edit target checks timed out; retry the same operation, or split it into smaller edits within the same session.";
const INSPECTION_RECOVERY = "Correct the patch and retry, or read the exact file and use Write for the intended content; Write remains subject to the same target checks.";
const FAILED_GUARD = /^ECI_EDIT_GATE_FAILED_ALLOWED (validate-edit-write\.sh|eci-active-gate\.sh|validate-apply-patch\.sh)$/;

function failedGuardContext(guards: Set<string>): string {
	return [...guards].map(guard =>
		`ECI edit guard ${guard} failed while checking an allowed edit. Protected-target coverage may be incomplete. In the same session, set SESSION_ID and TARGET to the actual session ID and edited path, then diagnose with: jq -n --arg session "$SESSION_ID" --arg cwd "$PWD" --arg path "$TARGET" '{session_id:$session,cwd:$cwd,tool_name:"Edit",tool_input:{file_path:$path}}' | bash -x "$HOME/.omp/agent/eci/hooks/${guard}". This minimal callback omits the original transcript and host role and may not reproduce the failure; preserve that context when needed. Inspect the ordinary command output, repair the guard, and report the fix.`,
	).join("\n");
}

function targetPaths(input: Record<string, unknown>): string[] {
	const paths: string[] = [];
	if (typeof input.path === "string" && input.path !== "") paths.push(input.path);
	if (Array.isArray(input.paths)) {
		for (const p of input.paths) if (typeof p === "string" && p !== "") paths.push(p);
	}
	return [...new Set(paths)];
}

/** Inspect with the host parser rather than maintaining a separate grammar. */
function editTargetPaths(input: Record<string, unknown>): string[] {
	const inspection = editInspect("hashline", JSON.stringify(input));
	const paths = [...inspection.paths];
	for (const op of inspection.fileOps) if (op.kind === "move" && op.to !== undefined) paths.push(op.to);
	return [...new Set(paths)];
}

export default function eciEditGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
			const deadline = performance.now() + EDIT_GATE_DEADLINE_SEC * 1000;
			const input = event.input as Record<string, unknown>;
			const id = { ...identityFor(ctx, { useHostAgentKind: true }), sessionId: ctx.sessionManager.getSessionId() };
			const toolName = event.toolName === "edit" ? "Edit" : "Write";
			let paths: string[];
			if (event.toolName === "edit") {
				try {
					paths = editTargetPaths(input);
				} catch (err) {
					return { block: true, reason: `ECI edit target inspection failed: ${String(err)}. ${INSPECTION_RECOVERY}` };
				}
				if (paths.length === 0) return { block: true, reason: `ECI edit target inspection found no targets. ${INSPECTION_RECOVERY}` };
			} else {
				paths = targetPaths(input);
			}
			const toolInputs = paths.length > 0 ? paths.map((filePath) => ({ ...input, file_path: filePath })) : [input];
			const failedGuards = new Set<string>();
			let targetUnclassified = false;
			for (const toolInput of toolInputs) {
				const remaining = (deadline - performance.now()) / 1000;
				if (remaining <= 0) return { block: true, reason: TIMEOUT_REASON };
				const run = await runEciHook(scriptPath(SCRIPT),
					{ ...basePayload(id, "PreToolUse"), tool_name: toolName, tool_input: toolInput },
					id,
					remaining,
					{ cleanup: "owned-group" },
				);
				if (run.outcome.kind === "ran" && run.outcome.stop.kind === "deadline") {
					return { block: true, reason: TIMEOUT_REASON };
				}
				const verdict = preToolVerdict(run, "pretooluse-edit-dispatch");
				if (verdict) return verdict;
				for (const line of run.stderr.split("\n")) {
					const match = FAILED_GUARD.exec(line);
					if (match) failedGuards.add(match[1]);
					if (line === "ECI_EDIT_GATE_TARGET_UNCLASSIFIED") targetUnclassified = true;
				}
			}
			const warnings = [];
			if (failedGuards.size > 0) warnings.push(failedGuardContext(failedGuards));
			if (targetUnclassified) warnings.push("ECI edit gate could not classify an allowed target; protected-target coverage is unknown. Inspect the target and repair the classifier in the same session.");
			return warnings.length > 0 ? { additionalContext: warnings.join("\n") } : undefined;
		} catch (err) {
			return { block: true, reason: "ECI hook error: " + String(err) };
		}
	});
}
