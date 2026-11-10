// omp PreToolUse gate for `bash` → validate-bash.sh.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { BASH_GATE_DEADLINE_SEC, basePayload, bashGateUnavailable, bashHostCwd, bashPreToolVerdict, identityFor, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";
import { registerBashGateRepair } from "../lib/eci-bash-repair.ts";

const SCRIPT = "validate-bash.sh";

export default function eciBashGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "bash") return undefined;
			const input = event.input as { command?: unknown; cwd?: unknown };
			const command = typeof input.command === "string" ? input.command : "";
			const cwd = bashHostCwd(input.cwd, command, ctx.cwd);
			const id = { ...identityFor(ctx), sessionId: ctx.sessionManager.getSessionId() };
			const run = await runEciHook(scriptPath(SCRIPT),
				{ ...basePayload(id, "PreToolUse"), ...(cwd === undefined ? { cwd: undefined, cwd_unknown: true } : { cwd }), tool_name: "Bash", tool_input: { command } },
				id,
				BASH_GATE_DEADLINE_SEC,
				{ cleanup: "owned-group" },
			);
			return bashPreToolVerdict(run);
		} catch {
			return bashGateUnavailable("adapter");
		}
	});
	try {
		registerBashGateRepair(pi);
	} catch {
		console.error("ECI Bash gate repair tool registration failed; Bash gate remains active.");
	}
}
