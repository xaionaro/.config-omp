// omp PreToolUse gate for `bash` → validate-bash.sh.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { join } from "node:path";
import { BASH_GATE_DEADLINE_SEC, basePayload, bashGateRepairable, bashGateUnavailable, bashHostCwd, bashPreToolVerdict, eciHome, identityFor, resolveHookBash, runEciHook } from "../lib/eci-hook-runner.ts";
import { registerBashGateRepair } from "../lib/eci-bash-repair.ts";

const SCRIPT = "validate-bash.sh";
const REPAIR_COMMAND = ": eci-bash-gate-repair";

/** Quotes one fixed path for the shell, including paths containing apostrophes. */
function shellQuote(value: string): string {
	return `'${value.replaceAll("'", "'\\''")}'`;
}

export default function eciBashGate(pi: ExtensionAPI): void {
	pi.on("tool_call", async (event, ctx) => {
		try {
			if (event.toolName !== "bash") return undefined;
			const input = event.input as { command?: unknown; cwd?: unknown };
			const command = typeof input.command === "string" ? input.command : "";
			const cwd = bashHostCwd(input.cwd, command, ctx.cwd);
			const id = { ...identityFor(ctx, { useHostAgentKind: true }), sessionId: ctx.sessionManager.getSessionId() };
			const home = eciHome();
			const root = join(home, ".omp", "agent", "eci");
			const hooks = join(root, "hooks");
			const run = await runEciHook(join(hooks, SCRIPT),
				{ ...basePayload(id, "PreToolUse"), ...(cwd === undefined ? { cwd: undefined, cwd_unknown: true } : { cwd }), tool_name: "Bash", tool_input: { command } },
				id,
				BASH_GATE_DEADLINE_SEC,
				{ cleanup: "owned-group" },
			);
			if (command.trim() === REPAIR_COMMAND && bashGateRepairable(run)) {
				const bash = resolveHookBash();
				if ("bash" in bash) {
					return { input: {
						command: `HOME=${shellQuote(home)} ${shellQuote(bash.bash)} ${shellQuote(join(hooks, "build-native-binaries.sh"))} --gate-repair`,
						cwd: hooks,
						timeout: 600,
					} };
				}
			}
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
