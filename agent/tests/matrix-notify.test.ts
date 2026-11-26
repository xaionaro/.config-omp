import { test } from "node:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { matrixNotify, parseHermesLauncher, type HelperRunner } from "../extensions/matrix-notify.ts";

type ToolResult = { isError: boolean; content: { text: string }[] };
type Tool = { name: string; parameters: unknown; execute: (...args: unknown[]) => Promise<ToolResult> };

// Records schema construction so tests can assert the declared recipient enum without a zod install.
const z = {
  object: (shape: unknown) => ({ object: shape }),
  enum: (values: string[]) => ({ enum: values }),
  string: () => ({ min: (n: number) => ({ string: { min: n } }) }),
  boolean: () => ({ optional: () => ({ boolean: "optional" }) }),
};

function register(run: HelperRunner): Tool {
  let tool: Tool | undefined;
  const pi = { zod: z, registerTool: (t: Tool) => (tool = t) };
  matrixNotify(pi as unknown as ExtensionAPI, run);
  assert.ok(tool);
  return tool;
}

test("maps void and vickey to their Matrix IDs and forwards message and dry_run", async () => {
  const calls: unknown[][] = [];
  const tool = register(async (mxid, message, dryRun) => {
    calls.push([mxid, message, dryRun]);
    return { ok: true, room_id: "!r:x" };
  });
  assert.equal(tool.name, "matrix_notify");
  const sent = await tool.execute("id", { recipient: "void", message: "hi" });
  await tool.execute("id", { recipient: "vickey", message: "yo", dry_run: true });
  assert.deepEqual(calls, [
    ["@xaionaro:matrix.dx.center", "hi", false],
    ["@danusya:matrix.dx.center", "yo", true],
  ]);
  assert.equal(sent.isError, false);
  assert.match(sent.content[0].text, /Sent to void .* in !r:x/);
});

test("declares only void and vickey and reports helper failures as errors", async () => {
  const tool = register(async () => ({ ok: false, error: "Matrix connect failed" }));
  assert.deepEqual(tool.parameters, {
    object: { recipient: { enum: ["void", "vickey"] }, message: { string: { min: 1 } }, dry_run: { boolean: "optional" } },
  });
  const res = await tool.execute("id", { recipient: "void", message: "x" });
  assert.equal(res.isError, true);
  assert.match(res.content[0].text, /Matrix connect failed/);
});

test("parses interpreter and source root from the hermes launcher", () => {
  const launcher = `#!/bin/sh\nexec /opt/py/bin/python3 -I -c 'import os\nsys.path.insert(0, '"'"'/src/hermes-agent'"'"')\n' "$@"`;
  assert.deepEqual(parseHermesLauncher(launcher), { python: "/opt/py/bin/python3", root: "/src/hermes-agent" });
  assert.equal(parseHermesLauncher("#!/bin/sh\nexec foo"), undefined);
});
