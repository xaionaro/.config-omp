// omp PreToolUse gate for `bash` → validate-bash.sh (timeout 75s).
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { basePayload, identityFor, preToolVerdict, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "validate-bash.sh";

export default function eciBashGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "bash") return undefined;
			const input = event.input as { command?: unknown };
			const command = typeof input.command === "string" ? input.command : "";
			const id = identityFor(ctx);
			const run = await runEciHook(scriptPath(SCRIPT),
				{ ...basePayload(id, "PreToolUse"), tool_name: "Bash", tool_input: { command } },
				id,
				75,
			);
			return preToolVerdict(run, "validate-bash");
		} catch (err) {
			return { block: true, reason: "ECI hook error: " + String(err) };
		}
	});
}
