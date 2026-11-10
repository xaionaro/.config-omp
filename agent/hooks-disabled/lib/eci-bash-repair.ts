import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { BASH_GATE_DEADLINE_SEC, basePayload, bashPreToolVerdict, identityFor, runEciHook, scriptPath } from "./eci-hook-runner.ts";

/** Registers a fixed repair of the native binaries required by the Bash gate. */
export function registerBashGateRepair(pi: ExtensionAPI): void {
	pi.registerTool({
		name: "eci_bash_gate_repair",
		label: "Repair ECI Bash gate",
		description: "Rebuild the native Bash gate binaries and check whether the gate can decide.",
		parameters: pi.zod.object({}),
		async execute(_callId, _params, signal, _onUpdate, ctx) {
			const id = { ...identityFor(ctx), sessionId: ctx.sessionManager.getSessionId() };
			const args = process.platform === "linux" ? ["eci-verexec", "eci-command-plan"] : ["eci-command-plan"];
			const build = await runEciHook(scriptPath("build-native-binaries.sh"), {}, id, 600, { cleanup: "owned-group", args, signal });
			const output = `${build.stdout}${build.stderr}`.slice(-8192);
			if (build.outcome.kind === "cancelled-before-start" || (build.outcome.kind === "ran" && build.outcome.stop.kind === "cancel")) {
				return { isError: true, content: [{ type: "text", text: `Repair cancelled; retry to finish.\n${output}` }], details: {} };
			}
			if (build.outcome.kind !== "ran" || build.outcome.stop.kind !== "exit" || build.outcome.stop.code !== 0 || build.outcome.cleanup !== "graceful") {
				return { isError: true, content: [{ type: "text", text: `Repair failed (${JSON.stringify(build.outcome)}); gate readiness is unknown.\n${output}` }], details: {} };
			}
			const probe = await runEciHook(scriptPath("validate-bash.sh"), { ...basePayload(id, "PreToolUse"), tool_name: "Bash", tool_input: { command: "true" } }, id, BASH_GATE_DEADLINE_SEC, { cleanup: "owned-group", signal });
			if (probe.outcome.kind === "cancelled-before-start" || (probe.outcome.kind === "ran" && probe.outcome.stop.kind === "cancel")) {
				return { isError: true, content: [{ type: "text", text: `Repair probe cancelled; gate readiness is unknown.\n${output}` }], details: {} };
			}
			const verdict = bashPreToolVerdict(probe);
			if (probe.outcome.kind !== "ran" || probe.outcome.stop.kind !== "exit" || probe.outcome.stop.code !== 0 || probe.outcome.cleanup !== "graceful" || verdict !== undefined) {
				return { isError: true, content: [{ type: "text", text: `Repair finished, but the Bash gate is not ready (${JSON.stringify(probe.outcome)}).${verdict ? ` ${verdict.reason}` : ""}\n${output}\n${probe.stderr.slice(-8192)}` }], details: {} };
			}
			return { content: [{ type: "text", text: `Bash gate accepted the fixed true probe.\n${output}` }], details: {} };
		},
	});
}
