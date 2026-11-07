// omp UserPromptSubmit reminder → prompt-task-reminder.sh. Its additionalContext becomes
// a hidden custom message for this turn via before_agent_start. Advisory: cannot block.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { advisoryContext, basePayload, identityFor, runEciHook, scriptPath } from "../lib/eci-hook-runner.ts";

const SCRIPT = "prompt-task-reminder.sh";

export default function eciPromptReminder(pi: ExtensionAPI): void {
	pi.on("before_agent_start", async (event, ctx) => {
		try {
			if (event.reason === "revive") return undefined;
			const id = identityFor(ctx);
			const run = await runEciHook(scriptPath(SCRIPT),
				{ ...basePayload(id, "UserPromptSubmit"), prompt: event.prompt },
				id,
				30,
			);
			const content = advisoryContext(run, "UserPromptSubmit");
			if (content === undefined) return undefined;
			return { message: { customType: "eci-prompt-reminder", content, display: false, attribution: "agent" } };
		} catch {
			return undefined;
		}
	});
}
