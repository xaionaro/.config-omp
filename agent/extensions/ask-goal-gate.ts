/**
 * Block the `ask` tool while a goal is engaged.
 *
 * While a goal is active or budget-limited, the session runs unattended and no
 * user is watching to answer `ask`. The gate blocks `ask` with a model-facing
 * reason so the model decides from context and continues.
 *
 * Engagement comes from a cache keyed by session id, set by `goal_updated`, with
 * a live `ctx.sessionModes` overlay for goals restored before the cache is
 * populated. One module instance serves every session in the process, so the
 * cache must never be shared across sessions. The gate fails closed: if
 * engagement cannot be read, `ask` is blocked until a clean `goal_updated`.
 *
 * Load: place in the active profile's extensions directory, or pass
 * `omp --extension ./ask-goal-gate.ts`.
 *
 * API reference:
 * https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

export const GOAL_BLOCK_REASON =
  "`ask` is disabled while a goal runs unattended; no user can answer it. Work autonomously. If you already read the matching skill this session, follow it. Otherwise, read `skill://<name>` and follow it. If no skill matches, follow the task's rules. Choose the most defensible option from context. State each assumption in your next message. If you cannot progress, do not call `ask`; state the exact blocker and needed input in your final message, then end your turn.";

export const GOAL_UNREADABLE_REASON = "`ask` is unavailable: goal state could not be read";

// Session ids whose goal is engaged according to goal_updated.
const engagedSessions = new Set<string>();
// Session ids whose last goal_updated could not be read. Only a clean goal_updated
// clears these; session_switch does not, so the block survives a switch.
const unreadableSessions = new Set<string>();

interface SessionContext {
  sessionManager: { getSessionId(): string };
}

// The one typed accessor for the session id. The installed ExtensionContext type
// is not verified here, so the cast lives in this function only.
function sessionIdOf(ctx: unknown): string {
  const typed = ctx as SessionContext;
  return typed.sessionManager.getSessionId();
}

export default function askGoalGate(pi: ExtensionAPI): void {
  // Intended behavior for kind !== "main": a non-main agent never writes this cache.
  // A subagent context never writes the engaged set: its goal_updated returns at the
  // kind check. It writes the unreadable set only if reading its kind throws. Its ask
  // is blocked if its session id is engaged or unreadable, or if its sessionModes
  // overlay reports an active goal. The overlay check is not kind-gated, so the overlay
  // can block a subagent. Overlay behavior for a subagent context is unverified in the
  // installed build.
  pi.on("goal_updated", (event, ctx) => {
    let id: string | undefined;
    try {
      id = sessionIdOf(ctx);
      // Decision: if the kind read throws, the catch marks this session unreadable, so
      // its ask stays blocked until a clean goal_updated from that session. A subagent
      // never reaches a clean update, so its ask stays blocked for the life of the
      // process (fail closed).
      if (ctx.agent.kind !== "main") return;
      // An absent state means engagement cannot be read, so throw into the catch,
      // which marks the session unreadable.
      if (event.state === undefined) throw new Error("goal_updated carries no state");
      if (event.state.enabled === true) engagedSessions.add(id);
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
      engagedSessions.delete(sessionIdOf(ctx));
    } catch {
      // Nothing to reset; tool_call still fails closed if the id is unreadable.
    }
  });

  pi.on("tool_call", (event, ctx) => {
    if (event.toolName !== "ask") return undefined;
    try {
      const id = sessionIdOf(ctx);
      if (unreadableSessions.has(id)) return { block: true, reason: GOAL_UNREADABLE_REASON };
      // Accepted residual: the overlay type declares only inactive | active | paused,
      // so only "active" is checked here and budget-limited is not. A restored
      // budget-limited goal is covered only if its goal_updated reached the cache.
      // Not verified against the installed build.
      const overlayStatus = ctx.sessionModes?.getState().goal.status;
      const engaged = engagedSessions.has(id) || overlayStatus === "active";
      if (engaged) return { block: true, reason: GOAL_BLOCK_REASON };
      return undefined;
    } catch {
      return { block: true, reason: GOAL_UNREADABLE_REASON };
    }
  });
}
