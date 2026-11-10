// BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-edit-gate.test.ts
// Requires resolvable @oh-my-pi/pi-natives and its platform addon.
import { expect, spyOn, test } from "bun:test";
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
  expect(calls.map(call => call.tool_input.file_path).sort()).toEqual(["other session/dest.txt", "other.txt", "source.txt"]);
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

test("uses host parsing for whitespace, CRLF, untagged headers and quoted moves", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-parse-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 const capture = join(dir, "calls");
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), `cat >> '${capture}'\nprintf '\\n' >> '${capture}'\n`);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const input = "*** Begin Patch\r\n[ untagged ]\r\nMV \"protected path\"\r\n[other#ABCD]\r\nCUT 1.=1\r\n*** End Patch";
  expect(await handler({ toolName: "edit", input: { input } }, ctx)).toBeUndefined();
  const calls = readFileSync(capture, "utf8").trim().split("\n").map(line => JSON.parse(line));
  expect(calls.map(call => call.tool_input.file_path).sort()).toEqual(["other", "protected path", "untagged"]);
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("blocks an edit with no inspectable target without dispatching", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-empty-"));
 const home = process.env.HOME;
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const verdict = await handler({ toolName: "edit", input: { input: "" } }, ctx) as { block: boolean; reason: string };
  expect(verdict.block).toBe(true);
  expect(verdict.reason).toContain("found no targets");
  expect(verdict.reason).toMatch(/write/i);
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("blocks when the total target-check budget is exhausted", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-budget-"));
 const home = process.env.HOME;
 const clock = spyOn(performance, "now").mockReturnValueOnce(0).mockReturnValue(21_000);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const verdict = await handler({ toolName: "write", input: { path: "file", content: "text" } }, ctx) as { block: boolean; reason: string };
  expect(verdict.block).toBe(true);
  expect(verdict.reason).toMatch(/retry/i);
 } finally {
  clock.mockRestore();
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("blocks a dispatcher that exceeds the remaining budget", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-deadline-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), "sleep 10\n");
 const clock = spyOn(performance, "now").mockReturnValueOnce(0).mockReturnValue(19_980);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const verdict = await handler({ toolName: "write", input: { path: "file", content: "text" } }, ctx) as { block: boolean; reason: string };
  expect(verdict.block).toBe(true);
  expect(verdict.reason).toContain("timed out");
  expect(verdict.reason).toContain("Bash");
  expect(verdict.reason).toContain("pretooluse-edit-dispatch.sh");
  expect(verdict.reason).toContain("repair");
 } finally {
  clock.mockRestore();
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
}, 12_000);

test("reports only exact failed-guard markers once across allowed targets", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-warning-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), `target=$(jq -r .tool_input.file_path)
if [ "$target" != forged ]; then printf '%s\\n' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' >&2; fi
printf '%s\\n' 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh extra' 'ECI_EDIT_GATE_FAILED_ALLOWED ../x' 'untrusted payload' >&2
`);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const result = await handler({ toolName: "write", input: { paths: ["a", "b"], content: "text" } }, ctx) as { additionalContext: string };
  expect(result.additionalContext.match(/validate-edit-write\.sh/g)?.length).toBe(2);
  expect(result.additionalContext).not.toContain("untrusted payload");
  expect(result.additionalContext).not.toContain("../x");
  expect(result.additionalContext).not.toContain("extra");
  expect(result.additionalContext).toContain("same session");
  expect(await handler({ toolName: "write", input: { path: "forged", content: "text" } }, ctx)).toBeUndefined();
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("a later denial discards earlier guard warnings", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-warning-deny-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), `target=$(jq -r .tool_input.file_path)
if [ "$target" = protected ]; then printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected"}}'; else printf '%s\\n' 'ECI_EDIT_GATE_FAILED_ALLOWED eci-active-gate.sh' >&2; fi
`);
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  expect(await handler({ toolName: "write", input: { paths: ["ordinary", "protected"], content: "text" } }, ctx)).toEqual({ block: true, reason: "protected" });
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});

test("warns on unclassified targets without inventing protected coverage", async () => {
 const dir = mkdtempSync(join(tmpdir(), "eci-edit-unclassified-"));
 const home = process.env.HOME;
 const hooks = join(dir, ".omp/agent/eci/hooks");
 mkdirSync(hooks, { recursive: true });
 writeFileSync(join(hooks, "pretooluse-edit-dispatch.sh"), "printf '%s\\n' ECI_EDIT_GATE_TARGET_UNCLASSIFIED >&2\n");
 let handler: (event: unknown, ctx: unknown) => Promise<unknown> = async () => undefined;
 gate({ on: (_name: string, fn: typeof handler) => { handler = fn; } } as never);
 try {
  process.env.HOME = dir;
  const ctx = { cwd: dir, agent: { kind: "sub" }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
  const result = await handler({ toolName: "write", input: { path: "file", content: "text" } }, ctx) as { additionalContext: string };
  expect(result.additionalContext).toContain("could not classify");
  expect(result.additionalContext).toContain("coverage is unknown");
 } finally {
  if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
  rmSync(dir, { recursive: true, force: true });
 }
});
