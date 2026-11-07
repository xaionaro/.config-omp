// omp PreToolUse gate for `edit` and `write` → pretooluse-edit-dispatch.sh (timeout 75s).
// `edit` targets are the `[path#TAG]` header lines of its hashline patch (`input.input`); `write`
// targets are `path`/`paths`. Each distinct target gets one dispatcher run with `tool_input.file_path`.
// With no target, one run gets the input unchanged.
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

const PATCH_HEADER = /^\[(.+)#[0-9A-Za-z]{4}\]\s*$/;

/** Distinct paths named by `[path#TAG]` header lines of an edit hashline patch, in order. */
function editTargetPaths(input: Record<string, unknown>): string[] {
	if (typeof input.input !== "string") return [];
	const paths: string[] = [];
	for (const line of input.input.split("\n")) {
		const match = PATCH_HEADER.exec(line.trimEnd());
		if (match) paths.push(match[1]);
	}
	return [...new Set(paths)];
}

export default function eciEditGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
			const input = event.input as Record<string, unknown>;
			const id = identityFor(ctx);
			const toolName = event.toolName === "edit" ? "Edit" : "Write";
			const paths = event.toolName === "edit" ? editTargetPaths(input) : targetPaths(input);
			const toolInputs = paths.length > 0 ? paths.map((filePath) => ({ ...input, file_path: filePath })) : [input];
			for (const toolInput of toolInputs) {
				const run = await runEciHook(scriptPath(SCRIPT),
					{ ...basePayload(id, "PreToolUse"), tool_name: toolName, tool_input: toolInput },
					id,
					75,
				);
				const verdict = preToolVerdict(run, "pretooluse-edit-dispatch");
				if (verdict) return verdict;
			}
			return undefined;
		} catch (err) {
			return { block: true, reason: "ECI hook error: " + String(err) };
		}
	});
}
