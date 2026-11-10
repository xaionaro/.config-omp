// omp PreToolUse gate for `edit` and `write` → pretooluse-edit-dispatch.sh.
// Edit targets use the host hashline inspector, including move destinations.
// Write targets are path/paths. Each distinct target gets one dispatcher run.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { editInspect } from "@oh-my-pi/pi-natives";
import { BASH_GATE_DEADLINE_SEC, basePayload, identityFor, preToolVerdict, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "pretooluse-edit-dispatch.sh";

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
			const deadline = performance.now() + BASH_GATE_DEADLINE_SEC * 1000;
			const input = event.input as Record<string, unknown>;
			const id = { ...identityFor(ctx, { useHostAgentKind: true }), sessionId: ctx.sessionManager.getSessionId() };
			const toolName = event.toolName === "edit" ? "Edit" : "Write";
			let paths: string[];
			if (event.toolName === "edit") {
				try {
					paths = editTargetPaths(input);
				} catch (err) {
					return { block: true, reason: `ECI edit target inspection failed: ${String(err)}. Correct the patch and retry, or read the exact file and use Write for the intended content; Write remains subject to the same target checks.` };
				}
				if (paths.length === 0) return { block: true, reason: "ECI edit target inspection found no targets. Correct the patch and retry, or read the exact file and use Write for the intended content; Write remains subject to the same target checks." };
			} else {
				paths = targetPaths(input);
			}
			const toolInputs = paths.length > 0 ? paths.map((filePath) => ({ ...input, file_path: filePath })) : [input];
			for (const toolInput of toolInputs) {
				const remaining = (deadline - performance.now()) / 1000;
				if (remaining <= 0) return { block: true, reason: "ECI edit target checks timed out; retry the same operation, or split it into smaller edits within the same session." };
				const run = await runEciHook(scriptPath(SCRIPT),
					{ ...basePayload(id, "PreToolUse"), tool_name: toolName, tool_input: toolInput },
					id,
					remaining,
					{ cleanup: "owned-group" },
				);
				if (run.outcome.kind === "ran" && run.outcome.stop.kind === "deadline") {
					return { block: true, reason: "ECI edit target checks timed out; retry the same operation, or split it into smaller edits within the same session." };
				}
				const verdict = preToolVerdict(run, "pretooluse-edit-dispatch");
				if (verdict) return verdict;
			}
			return undefined;
		} catch (err) {
			return { block: true, reason: "ECI hook error: " + String(err) };
		}
	});
}
