// omp equivalent of Codex PreToolUse matcher `^Bash$` → validate-bash.sh (timeout 75s).
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { basePayload, identityFor, preToolVerdict, runCodexHook, scriptPath } from "../lib/codex-bridge.ts";

const SCRIPT = "validate-bash.sh";

export default function codexBashGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		if (event.toolName !== "bash") return undefined;
		const input = event.input as { command?: unknown };
		const command = typeof input.command === "string" ? input.command : "";
		const id = identityFor(ctx);
		const run = await runCodexHook(scriptPath(SCRIPT),
			{ ...basePayload(id, "PreToolUse"), tool_name: "Bash", tool_input: { command } },
			id,
			75,
		);
		return preToolVerdict(run, "validate-bash");
	});
}
