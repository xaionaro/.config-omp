import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { helperAt, matrixNotify, type HelperRunner } from "../extensions/matrix-notify.ts";

type ToolResult = { isError: boolean; content: { text: string }[]; details: unknown };
type Tool = { name: string; description: string; parameters: unknown; execute: (...args: unknown[]) => Promise<ToolResult> };

// Records schema construction so the declared parameters can be asserted without a zod install.
const z = {
  object: (shape: unknown) => ({ object: shape }),
  enum: (values: string[]) => ({ enum: values }),
  string: () => ({ min: (n: number) => ({ string: { min: n } }) }),
};

function register(run: HelperRunner): Tool {
  let tool: Tool | undefined;
  const pi = { zod: z, registerTool: (t: Tool) => (tool = t) };
  matrixNotify(pi as unknown as ExtensionAPI, run);
  assert.ok(tool);
  return tool;
}

test("declares matrix_send with only the void/vickey alias and a message", () => {
  const tool = register(async () => ({ status: "sent" }));
  assert.equal(tool.name, "matrix_send");
  assert.deepEqual(tool.parameters, { object: { to: { enum: ["void", "vickey"] }, message: { string: { min: 1 } } } });
  assert.doesNotMatch(tool.description, /resolve|dry/i);
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
  assert.match(res.content[0].text, /accepted.*\$e.*!r:x/);
  assert.match(res.content[0].text, /not a delivery or read receipt/);
  assert.doesNotMatch(res.content[0].text, /\b(was|been) (delivered|read)\b/i);
});

test("rejects whitespace-only messages before calling the helper", async () => {
  let called = false;
  const tool = register(async () => {
    called = true;
    return { status: "sent", event_id: "$e" };
  });
  const res = await tool.execute("id", { to: "void", message: " \n\t" });
  assert.equal(res.isError, true);
  assert.equal(called, false);
});

test("pending, failed and eventless results are errors, never success", async () => {
  for (const result of [
    { status: "key_pending", room_id: "!r:x" },
    { status: "invite_pending", room_id: "!r:x" },
    { status: "error", error: "network" },
    { status: "sent", room_id: "!r:x" },
  ]) {
    const res = await register(async () => result).execute("id", { to: "vickey", message: "x" });
    assert.equal(res.isError, true, JSON.stringify(result));
    assert.match(res.content[0].text, /not sent/i);
  }
});

test("runs the prebuilt helper directly with the body on stdin, and never builds it", async () => {
  const dir = mkdtempSync(join(tmpdir(), "mxn-"));
  const missing = await helperAt(join(dir, "absent"))("void", "x");
  assert.equal(missing.status, "error");
  assert.match(String(missing.error), /go build -tags goolm/);

  const fake = join(dir, "fake");
  writeFileSync(fake, `#!/bin/sh\nprintf '%s ' "$@" > ${dir}/args\ncat > ${dir}/stdin\necho '{"status":"sent","room_id":"!r:x","event_id":"$e"}'\n`);
  chmodSync(fake, 0o700);
  const body = "line1\n$(touch ${dir}/pwned) 'q'";
  const res = await helperAt(fake)("vickey", body);
  assert.equal(res.event_id, "$e");
  assert.equal(readFileSync(join(dir, "args"), "utf8"), "-to vickey ");
  assert.equal(readFileSync(join(dir, "stdin"), "utf8"), body);

  writeFileSync(fake, `#!/bin/sh\necho 'panic: secret-ish internal detail' >&2\nexit 3\n`);
  const crash = await helperAt(fake)("void", "x");
  assert.equal(crash.status, "error");
  assert.doesNotMatch(String(crash.error), /secret-ish/);
});
