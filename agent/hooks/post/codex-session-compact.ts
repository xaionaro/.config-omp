// omp equivalent of Codex PostCompact → eci-post-compact-refresh.sh. Its stdout is always `{}`,
// so nothing is returned to omp. Failures are non-blocking (session_compact cannot block).
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { basePayload, identityFor, runCodexHook, scriptPath } from "../lib/codex-bridge.ts";

const SCRIPT = "eci-post-compact-refresh.sh";

export default function codexSessionCompact(pi: ExtensionAPI): void {
	pi.on("session_compact", async (_event, ctx) => {
		const id = identityFor(ctx);
		await runCodexHook(scriptPath(SCRIPT), basePayload(id, "PostCompact"), id, 30);
	});
}
