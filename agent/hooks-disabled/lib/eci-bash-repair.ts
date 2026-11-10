import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { execFile } from "node:child_process";
import { dirname, join } from "node:path";
import { BASH_GATE_DEADLINE_SEC, basePayload, bashGateDecision, identityFor, runEciHook, scriptPath } from "./eci-hook-runner.ts";

/** Reports changes in the fixed modules without making Git status a repair prerequisite. */
function sourceStatus(names: readonly string[], signal: AbortSignal | undefined): Promise<string> {
	const lib = join(dirname(scriptPath("build-native-binaries.sh")), "lib");
	const { promise, resolve } = Promise.withResolvers<string>();
	execFile("git", ["--no-optional-locks", "-C", lib, "status", "--porcelain=v1", "--untracked-files=all", "--", ...names.map(name => `${name}-go`)],
		{ timeout: 5000, maxBuffer: 65536, signal }, (error, stdout) => {
			resolve(error ? "unknown" : stdout.trim() || "none");
		});
	return promise;
}

/** Registers a fixed repair of the native binaries required by the Bash gate. */
export function registerBashGateRepair(pi: ExtensionAPI): void {
	pi.registerTool({
		name: "eci_bash_gate_repair",
		label: "Repair ECI Bash gate",
		description: "If the gate is unavailable, compile eci-command-plan and, on Linux, eci-verexec from the current ~/.omp working-tree source, including uncommitted changes, and replace the shared ~/.omp/agent/eci/bin binaries and receipts for all sessions. Does not reset or edit source.",
		parameters: pi.zod.object({}),
		async execute(_callId, _params, signal, _onUpdate, ctx) {
			const id = { ...identityFor(ctx), sessionId: ctx.sessionManager.getSessionId() };
			const names = process.platform === "linux" ? ["eci-verexec", "eci-command-plan"] : ["eci-command-plan"];
			const payload = { ...basePayload(id, "PreToolUse"), tool_name: "Bash", tool_input: { command: "true" } };
			const probe = () => runEciHook(scriptPath("validate-bash.sh"), payload, id, BASH_GATE_DEADLINE_SEC, { cleanup: "owned-group", signal });
			const first = await probe();
			const firstDecision = bashGateDecision(first);
			if (firstDecision.kind === "allow") {
				return { content: [{ type: "text", text: "The fixed true probe was accepted; nothing was rebuilt. Retry the blocked Bash call once; if blocked again, continue non-Bash work and report it." }], details: {} };
			}
			if (firstDecision.kind === "deny") {
				return { content: [{ type: "text", text: `The fixed true probe was denied: ${firstDecision.reason} Nothing was rebuilt; retry the blocked Bash call once, then continue non-Bash work and report if it remains blocked.` }], details: {} };
			}
			if (first.outcome.kind === "ran" && first.outcome.cleanup !== "graceful") {
				return { isError: true, content: [{ type: "text", text: `Probe cleanup was ${first.outcome.cleanup}; nothing was rebuilt. Continue non-Bash work and report it.` }], details: {} };
			}
			if (signal?.aborted) {
				return { isError: true, content: [{ type: "text", text: "Repair probe cancelled; nothing was rebuilt." }], details: {} };
			}
			const source = await sourceStatus(names, signal);
			if (signal?.aborted) {
				return { isError: true, content: [{ type: "text", text: "Repair cancelled before rebuilding." }], details: {} };
			}
			const build = await runEciHook(scriptPath("build-native-binaries.sh"), {}, id, 600, { cleanup: "owned-group", args: names, signal });
			const output = `${build.stdout}${build.stderr}`.slice(-8192);
			const summary = `source status: ${source}\n${output}`;
			if (build.outcome.kind === "cancelled-before-start" || (build.outcome.kind === "ran" && build.outcome.stop.kind === "cancel")) {
				return { isError: true, content: [{ type: "text", text: `Repair cancelled; retry to finish.\n${summary}` }], details: {} };
			}
			if (build.outcome.kind !== "ran" || build.outcome.stop.kind !== "exit" || build.outcome.stop.code !== 0 || build.outcome.cleanup !== "graceful") {
				return { isError: true, content: [{ type: "text", text: `Repair failed (${JSON.stringify(build.outcome)}); gate readiness is unknown.\n${summary}` }], details: {} };
			}
			const second = await probe();
			if (second.outcome.kind === "cancelled-before-start" || (second.outcome.kind === "ran" && second.outcome.stop.kind === "cancel")) {
				return { isError: true, content: [{ type: "text", text: `Repair probe cancelled; gate readiness is unknown.\n${summary}` }], details: {} };
			}
			const decision = bashGateDecision(second);
			if (decision.kind !== "allow") {
				const reason = decision.kind === "deny" ? decision.reason : `${decision.detail}\n${second.stderr.slice(-8192)}`;
				return { isError: true, content: [{ type: "text", text: `The fixed true probe did not succeed: ${reason}\nCalling eci_bash_gate_repair again will not change this result until the installation or source changes. Continue non-Bash work and report this result to the parent or user.\n${summary}` }], details: {} };
			}
			return { content: [{ type: "text", text: `Bash gate accepted the fixed true probe.\n${summary}` }], details: {} };
		},
	});
}
