import { test } from "node:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import eciHourlyReminder from "../extensions/eci-hourly-reminder.ts";

const REMINDER_SESSION_ID = "01a1bbe9-3693-7088-a9d7-5142c31c7ba0";
type Handler = (event: unknown, ctx: unknown) => void;

test("delivers hourly only in the retained Supervisor session", async () => {
  const handlers = new Map<string, Handler>();
  const timers = new Map<number, () => unknown>();
  const deliveries: { text: string; options: unknown }[] = [];
  let nextTimer = 0;
  let sessionId = "unrelated";
  const ctx = {
    sessionManager: { getSessionId: () => sessionId },
    setInterval(tick: () => unknown, delay: number) {
      assert.equal(delay, 3_600_000);
      timers.set(++nextTimer, tick);
      return nextTimer;
    },
    clearTimer(timer: number) {
      assert.ok(timers.delete(timer));
    },
  };
  const pi = {
    on(event: string, handler: Handler) {
      handlers.set(event, handler);
    },
    async sendUserMessage(text: string, options: unknown) {
      deliveries.push({ text, options });
    },
  };
  eciHourlyReminder(pi as unknown as ExtensionAPI);

  function emit(event: string) {
    const handler = handlers.get(event);
    assert.ok(handler, `expected a handler for ${event}`);
    handler({}, ctx);
  }

  emit("session_start");
  assert.equal(timers.size, 0);

  sessionId = REMINDER_SESSION_ID;
  emit("session_switch");
  emit("session_start");
  assert.equal(timers.size, 1);
  assert.equal(nextTimer, 1);
  assert.equal(deliveries.length, 0);

  const tick = timers.get(nextTimer);
  assert.ok(tick);
  await tick();
  assert.equal(deliveries.length, 1);
  assert.deepEqual(deliveries[0].options, { deliverAs: "aside" });
  assert.ok(deliveries[0].text.startsWith("Most important:\n"));

  sessionId = `${REMINDER_SESSION_ID}-child`;
  await tick();
  assert.equal(deliveries.length, 1);
  emit("session_switch");
  assert.equal(timers.size, 0);

  sessionId = REMINDER_SESSION_ID;
  emit("session_switch");
  assert.equal(timers.size, 1);
  emit("session_shutdown");
  assert.equal(timers.size, 0);
});
