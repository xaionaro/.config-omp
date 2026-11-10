// omp PreToolUse gate for `bash` → validate-bash.sh.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { BASH_GATE_DEADLINE_SEC, basePayload, bashGateUnavailable, bashPreToolVerdict, identityFor, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "validate-bash.sh";

export default function eciBashGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "bash") return undefined;
			const input = event.input as { command?: unknown };
			const command = typeof input.command === "string" ? input.command : "";
			const id = { ...identityFor(ctx), sessionId: ctx.sessionManager.getSessionId() };
			const run = await runEciHook(scriptPath(SCRIPT),
				{ ...basePayload(id, "PreToolUse"), tool_name: "Bash", tool_input: { command } },
				id,
				BASH_GATE_DEADLINE_SEC,
			);
			return bashPreToolVerdict(run);
		} catch {
			return bashGateUnavailable("adapter", -1);
		}
	});
}
