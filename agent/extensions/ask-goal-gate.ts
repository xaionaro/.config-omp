/**
 * Block the `ask` tool while a goal is engaged.
 *
 * While a goal is active or budget-limited, the session runs unattended and no
 * user is watching to answer `ask`. The gate blocks `ask` with a model-facing
 * reason so the model decides from context and continues.
 *
 * Engagement comes from a cache keyed by session id, set by `goal_updated` (only
 * from the main agent), with a live `ctx.sessionModes` overlay for goals restored
 * before the cache is populated. One module instance serves every session in the
 * process, so the cache must never be shared across sessions. The gate fails
 * closed: if engagement cannot be read, `ask` is blocked. No other tool is affected.
 *
 * Load: place in the active profile's extensions directory, or pass
 * `omp --extension ./ask-goal-gate.ts`.
 *
 * API reference:
 * https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

export const GOAL_BLOCK_REASON =
  "Blocked: a goal is running unattended, so no user can answer ask. Decide from the available context, state the assumption in your next message, and continue. The user can pause or drop the goal to re-enable ask.";

export const GOAL_UNREADABLE_REASON = "ask is unavailable: goal state could not be read";

// Overlay statuses that mean a goal is engaged. Budget-limited is listed in case
// the runtime reports it even though the declared overlay type omits it.
const ENGAGED_OVERLAY_STATUSES: Record<string, true> = { active: true, "budget-limited": true };

// Session ids whose goal is engaged according to goal_updated.
const engagedSessions = new Set<string>();
// Session ids whose last goal_updated could not be read; ask stays blocked for them.
const unreadableSessions = new Set<string>();

interface SessionContext {
  sessionManager: { getSessionId(): string };
}

export default function askGoalGate(pi: ExtensionAPI): void {
  pi.on("goal_updated", (event, ctx) => {
    let id: string | undefined;
    try {
      if (ctx.agent.kind !== "main") return;
      id = (ctx as unknown as SessionContext).sessionManager.getSessionId();
      if (event.state?.enabled === true) engagedSessions.add(id);
      else engagedSessions.delete(id);
      unreadableSessions.delete(id);
    } catch {
      // Mark the session unreadable so its ask stays blocked (fail closed).
      if (id !== undefined) unreadableSessions.add(id);
    }
  });

  pi.on("session_switch", (_event, ctx) => {
    try {
      if (ctx.agent.kind !== "main") return;
      const id = (ctx as unknown as SessionContext).sessionManager.getSessionId();
      engagedSessions.delete(id);
      unreadableSessions.delete(id);
    } catch {
      // Nothing to reset; tool_call still fails closed if the id is unreadable.
    }
  });

  pi.on("tool_call", (event, ctx) => {
    if (event.toolName !== "ask") return;
    try {
      const id = (ctx as unknown as SessionContext).sessionManager.getSessionId();
      if (unreadableSessions.has(id)) return { block: true, reason: GOAL_UNREADABLE_REASON };
      const overlayStatus = ctx.sessionModes?.getState().goal.status;
      const engaged =
        engagedSessions.has(id) ||
        (overlayStatus !== undefined && ENGAGED_OVERLAY_STATUSES[overlayStatus] === true);
      if (engaged) return { block: true, reason: GOAL_BLOCK_REASON };
      return undefined;
    } catch {
      return { block: true, reason: GOAL_UNREADABLE_REASON };
    }
  });
}
