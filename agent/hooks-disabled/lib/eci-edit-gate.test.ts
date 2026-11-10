// Run with BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-edit-gate.test.ts
import { expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import gate from "../pre/eci-edit-gate";

test("dispatches each edit source and move destination with host session identity", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-adapter-"));
 const home = process.env.HOME;
 const oldSession = process.env.PI_SESSION_ID;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 const capture = join(dir, "calls");
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), `cat >> '${capture}'\nprintf '\\n' >> '${capture}'\n`);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  process.env.PI_SESSION_ID = "stale-environment";
  const ctx = { cwd: dir, agent: { kind: "main" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const input = "*** Begin Patch\n[source.txt#1234]\nMV \"other session/dest.txt\"\n[other.txt#ABCD]\nCUT 1.=1\n*** End Patch";
  expect(await handler({ toolName: "edit", input: { input } }, ctx)).toBeUndefined();
  const calls = readFileSync(capture, "utf8").trim().split("\n").map(line => JSON.parse(line));
  expect(calls.map(call => call.tool_input.file_path)).toEqual(["source.txt", "other session/dest.txt", "other.txt"]);
  expect(calls.every(call => call.session_id === "host-session")).toBe(true);
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  if (oldSession === undefined) delete process.env.PI_SESSION_ID; else process.env.PI_SESSION_ID = oldSession;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("blocks a move when the dispatcher denies its destination", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-deny-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), `target=$(jq -r .tool_input.file_path)\nif [ "$target" = protected ]; then printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected destination"}}'; fi\n`);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const verdict = await handler({ toolName: "edit", input: { input: "*** Begin Patch\n[ordinary#1234]\nMV protected\n*** End Patch" } }, ctx);
  expect(verdict).toEqual({ block: true, reason: "protected destination" });
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});
