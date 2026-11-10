// omp PreToolUse gate for `edit` and `write` → pretooluse-edit-dispatch.sh (timeout 75s).
// `edit` targets include hashline patch headers and move destinations; `write` targets
// are `path`/`paths`. Each distinct target gets one dispatcher run with `tool_input.file_path`.
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
const MOVE_TARGET = /^MV (.+)$/;

function unquotePath(path: string): string {
	if (path.length >= 2 && (path[0] === '"' || path[0] === "'") && path.at(-1) === path[0]) {
		return path.slice(1, -1);
	}
	return path;
}

/** Distinct hashline patch source and move destination paths, in order. */
function editTargetPaths(input: Record<string, unknown>): string[] {
	if (typeof input.input !== "string") return [];
	const paths: string[] = [];
	for (const line of input.input.split("\n")) {
		const match = PATCH_HEADER.exec(line.trimEnd());
		if (match) paths.push(match[1]);
		else {
			const move = MOVE_TARGET.exec(line);
			if (move) paths.push(unquotePath(move[1]));
		}
	}
	return [...new Set(paths)];
}

export default function eciEditGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
			const input = event.input as Record<string, unknown>;
			const id = { ...identityFor(ctx, { useHostAgentKind: true }), sessionId: ctx.sessionManager.getSessionId() };
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
