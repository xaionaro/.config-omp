// Optional hook: after each Bash call, tell the model which processes that call left running.
// The Go helper owns the scan and the notice text; any failure means no notice.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { ensureHookBinary, waitForProvision } from "../eci/native-hook-binary.ts";

const exec = promisify(execFile);
let warned = false;

export default function bashSurvivors(pi: ExtensionAPI): void {
	pi.on("tool_result", async event => {
		const details = event.details as { async?: { state?: unknown }; service?: unknown } | undefined;
		if (event.toolName !== "bash" || details?.async?.state === "running" || details?.service !== undefined) return undefined;
		try {
			const binary = await waitForProvision(ensureHookBinary(), 2000);
			const { stdout } = await exec(binary, ["call-survivors", event.toolCallId], { timeout: 2000 });
			const notice = stdout.trim();
			return notice ? { additionalContext: notice } : undefined;
		} catch (error) {
			if (!warned) {
				warned = true;
				console.error(`Bash survivor notice unavailable (${String(error)}). Rebuild with: bash ~/.omp/agent/eci/hooks/build-native-binaries.sh eci-hook-helper`);
			}
			return undefined;
		}
	});
}
