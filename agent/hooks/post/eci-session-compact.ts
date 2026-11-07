// omp session_compact refresh → eci-post-compact-refresh.sh. Its stdout is always `{}`,
// so nothing is returned to omp. Failures are non-blocking (session_compact cannot block).
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { basePayload, identityFor, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "eci-post-compact-refresh.sh";

export default function eciSessionCompact(pi: ExtensionAPI): void {
	pi.on("session_compact", async (_event, ctx) => {
		const id = identityFor(ctx);
		await runEciHook(scriptPath(SCRIPT), basePayload(id, "PostCompact"), id, 30);
	});
}
