import { describe, test } from "node:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import askGoalGate, { GOAL_BLOCK_REASON, GOAL_UNREADABLE_REASON } from "./ask-goal-gate.ts";

type Handler = (event: unknown, ctx: unknown) => unknown;

let sessionCounter = 0;
let currentId = "";

function setup() {
  const handlers: Record<string, Handler[]> = {};
  const pi = {
    on(name: string, handler: Handler) {
      (handlers[name] ??= []).push(handler);
    },
  };
  const fire = (name: string, event: object, ctx: object): unknown => {
    let result: unknown;
    for (const handler of handlers[name] ?? []) result = handler(event, ctx);
    return result;
  };
  askGoalGate(pi as unknown as ExtensionAPI);
  // Each test gets a fresh session id, so cache state never crosses tests.
  currentId = `session-${++sessionCounter}`;
  return { fire };
}

const sessionCtx = (id: string, agent: "main" | "sub", sessionModes?: unknown) => ({
  agent: { kind: agent },
  sessionManager: { getSessionId: () => id },
  sessionModes,
});
const main = (sessionModes?: unknown) => sessionCtx(currentId, "main", sessionModes);
const sub = (sessionModes?: unknown) => sessionCtx(currentId, "sub", sessionModes);

const askCall = { type: "tool_call", toolCallId: "call-1", toolName: "ask", input: {} };
const bashCall = { type: "tool_call", toolCallId: "call-2", toolName: "bash", input: {} };

// Mirrors the goal_updated payload: `goal` is the goal (null when cleared) and
// `state` is GoalModeState, which carries `enabled` and the goal.
function goalUpdated(enabled: boolean, status: string) {
  const goal = { objective: "test goal", status };
  return { type: "goal_updated", goal, state: { enabled, goal } };
}

function overlay(status: string) {
  return { getState: () => ({ goal: { status } }), set: async () => ({}) };
}

describe("ask goal gate", () => {
  test("blocks ask when goal is active", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), main());
    assert.deepEqual(fire("tool_call", askCall, main()), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  test("blocks ask when goal is budget-limited", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "budget-limited"), main());
    assert.deepEqual(fire("tool_call", askCall, main()), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  for (const status of ["paused", "complete", "dropped"]) {
    test(`allows ask after goal_updated with enabled false (${status})`, () => {
      const { fire } = setup();
      fire("goal_updated", goalUpdated(true, "active"), main());
      fire("goal_updated", goalUpdated(false, status), main());
      assert.equal(fire("tool_call", askCall, main()), undefined);
    });
  }

  test("allows ask when no goal_updated fired and sessionModes is undefined", () => {
    const { fire } = setup();
    assert.equal(fire("tool_call", askCall, main(undefined)), undefined);
  });

  test("blocks ask when live overlay reports active but cache is false (restore case)", () => {
    const { fire } = setup();
    assert.deepEqual(fire("tool_call", askCall, main(overlay("active"))), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  test("blocks ask when live overlay reports budget-limited but cache is false (restore case)", () => {
    const { fire } = setup();
    assert.deepEqual(fire("tool_call", askCall, main(overlay("budget-limited"))), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  test("fails closed with reason when goal state cannot be read", () => {
    const { fire } = setup();
    const throwing = {
      getState: () => {
        throw new Error("boom");
      },
    };
    let result: unknown;
    assert.doesNotThrow(() => {
      result = fire("tool_call", askCall, main(throwing));
    });
    assert.deepEqual(result, { block: true, reason: GOAL_UNREADABLE_REASON });
  });

  test("blocks ask with unreadable reason when goal_updated cannot be read, until a clean update", () => {
    const { fire } = setup();
    const unreadable = {
      type: "goal_updated",
      goal: null,
      get state(): never {
        throw new Error("boom");
      },
    };
    assert.doesNotThrow(() => fire("goal_updated", unreadable, main()));
    assert.deepEqual(fire("tool_call", askCall, main()), {
      block: true,
      reason: GOAL_UNREADABLE_REASON,
    });
    fire("goal_updated", goalUpdated(false, "complete"), main());
    assert.equal(fire("tool_call", askCall, main()), undefined);
  });

  test("leaves non-ask tools untouched and never reads sessionModes", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), main());
    let reads = 0;
    const counting = {
      getState: () => {
        reads += 1;
        return { goal: { status: "active" } };
      },
    };
    assert.equal(fire("tool_call", bashCall, main(counting)), undefined);
    assert.equal(reads, 0);
  });

  test("goal_updated from a sub context does not change the cache", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), sub());
    assert.equal(fire("tool_call", askCall, main(undefined)), undefined);
  });

  test("session_switch resets the cache so ask is allowed in the new session", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), main());
    fire("session_switch", { type: "session_switch" }, main());
    assert.equal(fire("tool_call", askCall, main(undefined)), undefined);
  });

  test("session_switch from a sub context does not reset the cache", () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), main());
    fire("session_switch", { type: "session_switch" }, sub());
    assert.deepEqual(fire("tool_call", askCall, main(undefined)), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  test("goal engaged in one session does not gate ask in another session", () => {
    const { fire } = setup();
    const a = sessionCtx("session-a", "main");
    const b = sessionCtx("session-b", "main");
    fire("goal_updated", goalUpdated(true, "active"), a);
    assert.equal(fire("tool_call", askCall, b), undefined);
    assert.deepEqual(fire("tool_call", askCall, a), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  test("session_switch in another session does not clear this session's goal", () => {
    const { fire } = setup();
    const a = sessionCtx("session-a", "main");
    const b = sessionCtx("session-b", "main");
    fire("goal_updated", goalUpdated(true, "active"), a);
    fire("session_switch", { type: "session_switch" }, b);
    assert.deepEqual(fire("tool_call", askCall, a), {
      block: true,
      reason: GOAL_BLOCK_REASON,
    });
  });

  // Characterization: whatever sessionModes reports during budget-limited,
  // the cache keeps the gate closed.
  for (const reported of ["active", "paused", "inactive"]) {
    test(`blocks during budget-limited when sessionModes reports ${reported}`, () => {
      const { fire } = setup();
      fire("goal_updated", goalUpdated(true, "budget-limited"), main());
      assert.deepEqual(fire("tool_call", askCall, main(overlay(reported))), {
        block: true,
        reason: GOAL_BLOCK_REASON,
      });
    });
  }

  test("pausing via sessionModes re-enables ask", async () => {
    const { fire } = setup();
    fire("goal_updated", goalUpdated(true, "active"), main());
    let status = "active";
    const modes = {
      getState: () => ({ goal: { status } }),
      set: async (request: { mode: string; action: string }) => {
        if (request.mode === "goal" && request.action === "pause") {
          status = "paused";
          // The runtime emits goal_updated with enabled false on pause.
          fire("goal_updated", goalUpdated(false, "paused"), main(modes));
          return { ok: true };
        }
        return { ok: false };
      },
    };
    await modes.set({ mode: "goal", action: "pause" });
    assert.equal(fire("tool_call", askCall, main(modes)), undefined);
  });
});
