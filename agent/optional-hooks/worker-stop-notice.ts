// Optional advisory hook: when a subagent run ends aborted or errored, wake the main session with a notice so the
// Supervisor sees the stall even when the Worker's owner is parked. Subagent sessions reuse this factory in the same
// process, so the main session's API is shared through a module variable. A deliberate cancel also yields `aborted`;
// the notice says so. Not covered: subagents in another process, and runs that end normally but leave work stalled.
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

type AgentInfo = { kind?: unknown; id?: unknown; parentId?: unknown };
type AssistantMessage = { role?: unknown; stopReason?: unknown; errorMessage?: unknown };

const STOP_REASONS: Record<string, true> = { aborted: true, error: true };
let mainSession: ExtensionAPI | undefined;

// Notice text for a subagent run ending with stopReason, or undefined when the run ended normally.
export function workerStopNotice(agent: AgentInfo, messages: readonly unknown[], now: Date): string | undefined {
	let last: AssistantMessage | undefined;
	for (const message of messages as AssistantMessage[]) if (message?.role === "assistant") last = message;
	const reason = last?.stopReason;
	if (typeof reason !== "string" || !STOP_REASONS[reason]) return undefined;
	const id = String(agent.id ?? "unknown");
	const owner = typeof agent.parentId === "string" ? agent.parentId : undefined;
	const detail = typeof last?.errorMessage === "string" ? ` (${last.errorMessage})` : "";
	const ownerRoute = owner
		? ` Its owner ${owner} may be parked: read history://${owner} and proc://${owner}; if it has not acted, revive it with write agent://${owner}.`
		: "";
	return `Worker ${id} stopped at ${now.toISOString()} with stopReason=${reason}${detail}.${ownerRoute} Ignore if this was a deliberate cancel already handled.`;
}

export default function workerStopNoticeHook(pi: ExtensionAPI): void {
	pi.on("session_start", (_event, ctx) => {
		if ((ctx.agent as AgentInfo | undefined)?.kind === "main") mainSession = pi;
	});
	pi.on("agent_end", (event, ctx) => {
		const agent = ctx.agent as AgentInfo | undefined;
		if (agent?.kind !== "sub" || mainSession === undefined) return;
		const content = workerStopNotice(agent, event.messages, new Date());
		if (!content) return;
		try {
			mainSession.sendMessage(
				{ customType: "worker-stop-notice", content, display: true },
				{ deliverAs: "followUp", triggerTurn: true },
			);
		} catch (error) {
			console.error(`worker-stop-notice: delivery failed: ${String(error)}`);
		}
	});
}
