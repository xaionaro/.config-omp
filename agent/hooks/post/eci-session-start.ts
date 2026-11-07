// omp session_start snapshot → session-snapshot.sh. Its additionalContext is delivered
// as a hidden custom message on the next turn without starting a turn. Advisory: never blocks.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { advisoryContext, basePayload, identityFor, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "session-snapshot.sh";

export default function eciSessionStart(pi: ExtensionAPI): void {
	pi.on("session_start", async (_event, ctx) => {
		try {
			const id = identityFor(ctx);
			const run = await runEciHook(scriptPath(SCRIPT), basePayload(id, "SessionStart"), id, 60);
			const additionalContext = advisoryContext(run, "SessionStart");
			if (additionalContext === undefined) return;
			pi.sendMessage(
				{ customType: "eci-session-snapshot", content: additionalContext, display: false, attribution: "agent" },
				{ deliverAs: "nextTurn" },
			);
		} catch {
			return undefined;
		}
	});
}
