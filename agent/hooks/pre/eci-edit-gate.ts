// omp PreToolUse gate for `edit` and `write` → pretooluse-edit-dispatch.sh (timeout 75s).
// omp `edit`/`write` tool calls are sent as `Edit`/`Write` with `tool_input.file_path`, one
// dispatcher run per distinct target path.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { basePayload, identityFor, preToolVerdict, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "pretooluse-edit-dispatch.sh";

function targetPaths(input: Record<string, unknown>): string[] {
	const paths: string[] = [];
	if (typeof input.path === "string" && input.path !== "") paths.push(input.path);
	if (Array.isArray(input.paths)) {
		for (const p of input.paths) if (typeof p === "string" && p !== "") paths.push(p);
	}
	return [...new Set(paths)];
}

export default function eciEditGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
		const input = event.input as Record<string, unknown>;
		const id = identityFor(ctx);
		const toolName = event.toolName === "edit" ? "Edit" : "Write";
		for (const filePath of targetPaths(input)) {
			const run = await runEciHook(scriptPath(SCRIPT),
				{ ...basePayload(id, "PreToolUse"), tool_name: toolName, tool_input: { ...input, file_path: filePath } },
				id,
				75,
			);
			const verdict = preToolVerdict(run, "pretooluse-edit-dispatch");
			if (verdict) return verdict;
		}
		return undefined;
	});
}
