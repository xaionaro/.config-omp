/**
 * Block the `ask` tool while a goal is engaged.
 *
 * While a goal is active or budget-limited, the session runs unattended and no
 * user is watching to answer `ask`. The gate blocks `ask` with a model-facing
 * reason so the model decides from context and continues.
 *
 * Engagement comes from a module-level cache set by `goal_updated` (only from
 * the main agent), with a live `ctx.sessionModes` overlay for goals restored
 * before the cache is populated. The gate fails closed: if engagement cannot
 * be read, `ask` is blocked. No other tool is affected.
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

let goalEngaged = false;

export default function askGoalGate(pi: ExtensionAPI): void {
  pi.on("goal_updated", (event, ctx) => {
    if (ctx.agent.kind !== "main") return;
    goalEngaged = event.state?.enabled === true;
  });

  pi.on("session_switch", (_event, ctx) => {
    if (ctx.agent.kind !== "main") return;
    goalEngaged = false;
  });

  pi.on("tool_call", (event, ctx) => {
    if (event.toolName !== "ask") return;
    try {
      const engaged =
        goalEngaged || ctx.sessionModes?.getState().goal.status === "active";
      if (engaged) return { block: true, reason: GOAL_BLOCK_REASON };
      return undefined;
    } catch {
      return { block: true, reason: GOAL_UNREADABLE_REASON };
    }
  });
}
