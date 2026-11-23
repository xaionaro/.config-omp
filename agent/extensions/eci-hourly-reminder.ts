import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";

// The user asked for this reminder in one retained ECI Supervisor session; every other session loading this global extension stays inert.
const ROOT_SESSION_ID = "01a1bbe9-3693-7088-a9d7-5142c31c7ba0";
const HOUR_MS = 60 * 60 * 1000;
const REMINDER = `Most important:
Remember: you are an ECI supervisor. Do not forget to always immediatelly update the project ledger (keep in mind that it is present simple, and you must do that always, not just when I send this message). Read ECI, project ledger and coding skills, and strictly follow them. Never forget that you are not just proxy, you actually must critically and proactively ensure the project is progressing forward. Continously reflect on any violations of the skills mentioned and correct for the mistakes.

Just to verify if you follow the "Most important" part correctly:
Print me the full list of violations that you found and what are you doing to correct for the violations. Do not repeat previously mentioned violations, look only for genuenly new ones. Give me the current roster (recursively, including grandchildren; only active agents, include model&effort and who works on what). List the top priority issues/tasks and which agents work on them. List the top priority issues/tasks and which agents work on them. Is there any work that can be unstuck?

When I give to you this prompt, you tend to just start making a verification report. But I'm RE-EMPHASIZING: THE MOST IMPORTANT IS THE FIRST PARAGRAPH, DO MOST OF YOUR WORK THERE. The second is just verification AFTER the first.`;

export default function eciHourlyReminder(pi: ExtensionAPI) {
  let cancel: (() => void) | undefined;

  function stop() {
    cancel?.();
    cancel = undefined;
  }

  function sync(_event: unknown, ctx: ExtensionContext) {
    if (ctx.sessionManager.getSessionId() !== ROOT_SESSION_ID) {
      stop();
      return;
    }
    if (cancel !== undefined) return;
    const timer = ctx.setInterval(() => {
      if (ctx.sessionManager.getSessionId() === ROOT_SESSION_ID) {
        // The user's prompt, verbatim with default user attribution; "aside" lands at the next step boundary of a live run instead of waiting for it to end.
        return pi.sendUserMessage(REMINDER, { deliverAs: "aside" });
      }
    }, HOUR_MS);
    cancel = () => ctx.clearTimer(timer);
  }

  pi.on("session_start", sync);
  pi.on("session_switch", sync);
  pi.on("session_branch", sync);
  pi.on("session_shutdown", stop);
}
