import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import {
  ALIASES,
  CATEGORIES,
  RUNBOOK,
  matrixNotify,
  matrixSenderBinary,
  parseSenderOutput,
  type MatrixSendResult,
  type MatrixSender,
} from "../extensions/matrix-notify.ts";

type ToolResult = { isError: boolean; content: { text: string }[]; details: unknown };
type Tool = { name: string; parameters: unknown; execute: (...args: unknown[]) => Promise<ToolResult> };

// The Go→TS result contract, shared with agent/cmd/matrix-notify/main_test.go.
const WIRE = JSON.parse(
  readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../cmd/matrix-notify/testdata/wire.json"), "utf8"),
) as { categories: string[]; aliases: string[]; cases: Record<string, string> };

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

async function textFor(result: MatrixSendResult): Promise<ToolResult> {
  return register(async () => result).execute("id", { to: "void", message: "x" });
}

test("declares matrix_send with only the void/vickey alias and a message", () => {
  const tool = register(async () => ({ status: "not_built" }));
  assert.equal(tool.name, "matrix_send");
  assert.deepEqual(tool.parameters, { object: { to: { enum: ["void", "vickey"] }, message: { string: { min: 1 } } } });
});

// T3: every fixture line parses to the expected result; names, categories and aliases match the Go side.
test("parses every wire fixture shape exactly", () => {
  const expected: Record<string, MatrixSendResult> = {
    sent_join: { status: "sent", room_id: "!r:hs", event_id: "$e", recipient_membership: "join" },
    sent_invite: { status: "sent", room_id: "!r:hs", event_id: "$e", recipient_membership: "invite" },
    sent_cleanup_error: {
      status: "sent",
      room_id: "!r:hs",
      event_id: "$e",
      recipient_membership: "join",
      cleanup_error: "state",
    },
    invite_pending: { status: "invite_pending", room_id: "!r:hs" },
    key_pending: { status: "key_pending", room_id: "!r:hs" },
    unknown: { status: "unknown", reason: "no answer to send" },
    error: { status: "error", error: "auth" },
    resolved: { status: "unknown", reason: "unexpected sender output" },
    absent: { status: "unknown", reason: "unexpected sender output" },
  };
  assert.deepEqual(Object.keys(expected).sort(), Object.keys(WIRE.cases).sort());
  for (const [name, line] of Object.entries(WIRE.cases)) {
    assert.deepEqual(parseSenderOutput(line + "\n"), expected[name], name);
  }
  assert.deepEqual([...CATEGORIES], WIRE.categories);
  assert.deepEqual([...ALIASES].sort(), [...WIRE.aliases].sort());
});

test("rejects incomplete or unknown shapes as an unknown outcome", () => {
  for (const bad of [
    '{"status":"sent","room_id":"!r","recipient_membership":"join"}',
    '{"status":"sent","room_id":"!r","event_id":"$e","recipient_membership":"leave"}',
    '{"status":"key_pending"}',
    '{"status":"error","error":"send_unknown"}',
    '{"status":"sent","room_id":"!r","event_id":"$e","recipient_membership":"join","cleanup_error":"bogus"}',
    "panic: boom",
    "",
  ]) {
    assert.deepEqual(parseSenderOutput(bad), { status: "unknown", reason: "unexpected sender output" }, bad);
  }
});

test("reports Matrix acceptance with event and room, not delivery", async () => {
  const calls: unknown[][] = [];
  const tool = register(async (to, message) => {
    calls.push([to, message]);
    return { status: "sent", room_id: "!r:x", event_id: "$e", recipient_membership: "join" };
  });
  const res = await tool.execute("id", { to: "void", message: "hi" });
  assert.deepEqual(calls, [["void", "hi"]]);
  assert.equal(res.isError, false);
  assert.match(res.content[0].text, /\$e.*!r:x/);
  assert.match(res.content[0].text, /not a delivery or read receipt/);
  // T7: no runbook pointer on a clean success.
  assert.ok(!res.content[0].text.includes(RUNBOOK));
});

// T1 (TS side): a cleanup failure after an accepted send stays a success with a warning.
test("a cleanup failure after a send keeps the success and warns", async () => {
  const res = await textFor({
    status: "sent",
    room_id: "!r:x",
    event_id: "$e",
    recipient_membership: "join",
    cleanup_error: "state",
  });
  assert.equal(res.isError, false);
  assert.match(res.content[0].text, /\$e/);
  assert.match(res.content[0].text, /cleanup failed \(category: state\)/);
  assert.ok(res.content[0].text.includes(RUNBOOK));
  assert.doesNotMatch(res.content[0].text, /NOT sent/);
});

test("failures before the send say NOT sent and point to the runbook", async () => {
  for (const result of [
    { status: "key_pending", room_id: "!r:x" },
    { status: "invite_pending", room_id: "!r:x" },
    { status: "error", error: "input" },
  ] as const) {
    const res = await textFor(result);
    assert.equal(res.isError, true, JSON.stringify(result));
    assert.match(res.content[0].text, /NOT sent/);
  }
  // T7: error texts route to the runbook.
  const err = await textFor({ status: "error", error: "identity" });
  assert.ok(err.content[0].text.includes(RUNBOOK));
});

// T2 (TS side): an unknown outcome warns against blind retries and never claims NOT sent.
test("an unknown outcome says it may or may not have been sent", async () => {
  const res = await textFor({ status: "unknown", reason: "no answer to send" });
  assert.equal(res.isError, true);
  assert.match(res.content[0].text, /may or may not have been sent/);
  assert.doesNotMatch(res.content[0].text, /NOT sent/);
});

// T6, T7: a missing or non-executable sender is not built: NOT sent, with the build hint and runbook pointer.
test("a missing or non-executable sender is reported as not built", async () => {
  const dir = mkdtempSync(join(tmpdir(), "mxn-"));
  const noExec = join(dir, "noexec");
  writeFileSync(noExec, "#!/bin/sh\necho '{}'\n");
  chmodSync(noExec, 0o600);
  for (const path of [join(dir, "absent"), noExec]) {
    const result = await matrixSenderBinary(path)("void", "x");
    assert.deepEqual(result, { status: "not_built" }, path);
    const res = await textFor(result);
    assert.equal(res.isError, true);
    assert.match(res.content[0].text, /NOT sent to void: the sender is not built/);
    assert.match(res.content[0].text, /go build -tags goolm/);
    assert.ok(res.content[0].text.includes(RUNBOOK));
    assert.doesNotMatch(res.content[0].text, /may or may not/);
  }
  assert.ok(!existsSync(join(dir, "absent")));
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
  const fake = join(dir, "fake");
  writeFileSync(
    fake,
    `#!/bin/sh\nprintf '%s ' "$@" > ${dir}/args\ncat > ${dir}/stdin\necho '${WIRE.cases.sent_join}'\n`,
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
