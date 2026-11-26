import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { matrixNotify, matrixSenderBinary, parseSenderOutput, type MatrixSender } from "../extensions/matrix-notify.ts";

type ToolResult = { isError: boolean; content: { text: string }[]; details: unknown };
type Tool = { name: string; parameters: unknown; execute: (...args: unknown[]) => Promise<ToolResult> };

// Records schema construction so the declared parameters can be asserted without a zod install.
const z = {
  object: (shape: unknown) => ({ object: shape }),
  enum: (values: readonly string[]) => ({ enum: [...values] }),
  string: () => ({ min: (n: number) => ({ string: { min: n } }) }),
};

function register(send: MatrixSender): Tool {
  let tool: Tool | undefined;
  const pi = { zod: z, registerTool: (t: Tool) => (tool = t) };
  matrixNotify(pi as unknown as ExtensionAPI, send);
  assert.ok(tool);
  return tool;
}

test("declares matrix_send with only the void/vickey alias and a message", () => {
  const tool = register(async () => ({ status: "error", error: "x" }));
  assert.equal(tool.name, "matrix_send");
  assert.deepEqual(tool.parameters, { object: { to: { enum: ["void", "vickey"] }, message: { string: { min: 1 } } } });
});

test("reports Matrix acceptance with event and room, not delivery", async () => {
  const calls: unknown[][] = [];
  const tool = register(async (to, message) => {
    calls.push([to, message]);
    return { status: "sent", room_id: "!r:x", event_id: "$e", target_membership: "join" };
  });
  const res = await tool.execute("id", { to: "void", message: "hi" });
  assert.deepEqual(calls, [["void", "hi"]]);
  assert.equal(res.isError, false);
  assert.match(res.content[0].text, /\$e.*!r:x/);
  assert.match(res.content[0].text, /not a delivery or read receipt/);
});

test("failures before the send say NOT sent", async () => {
  for (const result of [
    { status: "key_pending", room_id: "!r:x" },
    { status: "invite_pending", room_id: "!r:x" },
    { status: "error", error: "input" },
  ] as const) {
    const res = await register(async () => result).execute("id", { to: "vickey", message: "x" });
    assert.equal(res.isError, true, JSON.stringify(result));
    assert.match(res.content[0].text, /NOT sent/);
  }
});

test("an unknown outcome warns against blind retries and never claims NOT sent", async () => {
  const res = await register(async () => ({ status: "unknown", reason: "send_unknown" })).execute("id", {
    to: "void",
    message: "x",
  });
  assert.equal(res.isError, true);
  assert.match(res.content[0].text, /may or may not have been sent/);
  assert.doesNotMatch(res.content[0].text, /NOT sent/);
});

test("accepts only complete sender results; anything else is an unknown outcome", () => {
  assert.deepEqual(parseSenderOutput('{"status":"sent","room_id":"!r","event_id":"$e","target_membership":"join"}\n'), {
    status: "sent",
    room_id: "!r",
    event_id: "$e",
    target_membership: "join",
  });
  assert.deepEqual(parseSenderOutput('{"status":"error","error":"send_unknown"}'), {
    status: "unknown",
    reason: "send_unknown",
  });
  assert.deepEqual(parseSenderOutput('{"status":"error","error":"network"}'), { status: "error", error: "network" });
  for (const bad of [
    '{"status":"sent","room_id":"!r","target_membership":"join"}',
    '{"status":"sent","room_id":"!r","event_id":"","target_membership":"join"}',
    '{"status":"sent","room_id":"!r","event_id":"$e","target_membership":"leave"}',
    '{"status":"resolved","room_id":"!r"}',
    '{"status":"key_pending"}',
    "panic: boom",
    "",
  ]) {
    assert.equal(parseSenderOutput(bad).status, "unknown", bad);
  }
});

test("a cancelled sender run is an unknown outcome", async () => {
  const dir = mkdtempSync(join(tmpdir(), "mxn-"));
  const fake = join(dir, "slow");
  writeFileSync(fake, "#!/bin/sh\ncat >/dev/null\nsleep 5\n");
  chmodSync(fake, 0o700);
  const ctl = new AbortController();
  const pending = matrixSenderBinary(fake)("void", "x", ctl.signal);
  ctl.abort();
  assert.deepEqual(await pending, { status: "unknown", reason: "cancelled" });
});

test("runs the prebuilt sender directly with the body on stdin, and never builds it", async () => {
  const dir = mkdtempSync(join(tmpdir(), "mxn-"));
  const missing = await matrixSenderBinary(join(dir, "absent"))("void", "x");
  assert.equal(missing.status, "error");
  assert.ok(!existsSync(join(dir, "absent")));

  const fake = join(dir, "fake");
  writeFileSync(
    fake,
    `#!/bin/sh\nprintf '%s ' "$@" > ${dir}/args\ncat > ${dir}/stdin\necho '{"status":"sent","room_id":"!r:x","event_id":"$e","target_membership":"join"}'\n`,
  );
  chmodSync(fake, 0o700);
  const body = `line1\n$(touch ${dir}/pwned) 'q'`;
  const res = await matrixSenderBinary(fake)("vickey", body);
  assert.equal(res.status, "sent");
  assert.equal(readFileSync(join(dir, "args"), "utf8"), "-to vickey -deadline 120s ");
  assert.equal(readFileSync(join(dir, "stdin"), "utf8"), body);
  assert.ok(!existsSync(join(dir, "pwned")));

  writeFileSync(fake, `#!/bin/sh\necho 'panic: secret-ish internal detail' >&2\nexit 3\n`);
  const crash = await matrixSenderBinary(fake)("void", "x");
  assert.equal(crash.status, "unknown");
  assert.doesNotMatch(JSON.stringify(crash), /secret-ish/);
});
