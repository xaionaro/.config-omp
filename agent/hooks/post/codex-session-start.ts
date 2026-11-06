// omp equivalent of Codex SessionStart → session-snapshot.sh. Its additionalContext is delivered
// as a hidden custom message on the next turn without starting a turn. Advisory: never blocks.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { advisoryContext, basePayload, identityFor, runCodexHook, scriptPath } from "../lib/codex-bridge.ts";

const SCRIPT = "session-snapshot.sh";

export default function codexSessionStart(pi: ExtensionAPI): void {
	pi.on("session_start", async (_event, ctx) => {
		const id = identityFor(ctx);
		const run = await runCodexHook(scriptPath(SCRIPT), basePayload(id, "SessionStart"), id, 60);
		const additionalContext = advisoryContext(run, "SessionStart");
		if (additionalContext === undefined) return;
		pi.sendMessage(
			{ customType: "codex-session-snapshot", content: additionalContext, display: false, attribution: "agent" },
			{ deliverAs: "nextTurn" },
		);
	});
}
