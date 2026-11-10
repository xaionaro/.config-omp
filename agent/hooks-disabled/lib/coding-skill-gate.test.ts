import { test } from "node:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import gate from "../pre/coding-skill-gate.ts";

type Event = { toolName: string; input: Record<string, unknown>; isError?: boolean; details?: unknown; content?: { type: string; text: string }[] };
type Context = { cwd: string; agent: { id: string; kind: string }; sessionManager: { getSessionId(): string } };
type Verdict = { block: boolean; reason: string } | undefined;
function setup() {
 const handlers: Record<string, (event: Event, ctx: Context) => Verdict> = {};
 // This fixture supplies only event registration.
 gate({ on(name: string, handler: (event: Event, ctx: Context) => Verdict) { handlers[name] = handler; } } as unknown as ExtensionAPI);
 const ctx = (session = "one", id = "Main") => ({ cwd: "/tmp", agent: { id, kind: "main" }, sessionManager: { getSessionId: () => session } });
 const call = (toolName: string, input: Record<string, unknown>, c = ctx()) => handlers.tool_call({ toolName, input }, c);
 const read = (path: string, c = ctx(), isError = false) => handlers.tool_result({ toolName: "read", input: { path }, isError, content: [{type: "text", text: "skill"}] }, c);
 const result = (toolName: string, input: Record<string, unknown>, details: unknown, c = ctx(), isError = false) => handlers.tool_result({ toolName, input, details, isError }, c);
 return { call, read, result, ctx };
}

test("requires both successful skill reads per agent and session", () => {
 const { call, read, ctx } = setup();
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common:1-2");
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common", ctx(), true);
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common");
 assert.match(call("write", {path: "a.go"})!.reason, /coding-go/);
 read("skill://coding-go");
 assert.equal(call("write", {path: "a.go"}), undefined);
 assert.match(call("write", {path: "b.go"}, ctx("two"))!.reason, /coding-common/);
 assert.match(call("write", {path: "b.go"}, ctx("one", "advisor"))!.reason, /coding-common/);
 assert.match(call("write", {path: "b.py"})!.reason, /coding-python/);
 assert.equal(call("write", {path: "a.txt"}), undefined);
});

test("recognizes patch, shell, eval and pending structural edits", () => {
 const { call, result } = setup();
 for (const [tool, input] of [
  ["edit", {input: "*** Begin Patch\n[a.go#1234]\nCUT 1.=1\n*** End Patch"}],
  ["bash", {command: "printf hi > a.go"}],
  ["eval", {code: "write('a.go', 'x')"}],
 ] as const) assert.match(call(tool, input)!.reason, /coding-common/);
 assert.equal(call("ast_edit", {paths: ["a.go"], ops: []}), undefined);
 result("ast_edit", {paths: ["src/"], ops: []}, {files: ["src/a.go"]});
 assert.match(call("write", {path: "xd://resolve"})!.reason, /coding-common/);
 assert.equal(call("bash", {command: "cat a.go"}), undefined);
 assert.equal(call("bash", {command: "printf '%s' 'example > a.go'"}), undefined);
 assert.equal(call("eval", {code: "print(\"write('a.go', 'x')\")"}), undefined);
 assert.match(call("bash", {command: "tee a.go"})!.reason, /coding-common/);
 assert.match(call("bash", {command: "cp a.txt b.py"})!.reason, /coding-common/);
 assert.match(call("edit", {path: "a.txt", edits: [{rename: "b.py"}]})!.reason, /coding-common/);
 assert.match(call("edit", {input: "*** Begin Patch\n[a.txt#1234]\nMV b.go\n*** End Patch"})!.reason, /coding-common/);
 assert.equal(call("eval", {code: "print('a.go')"}), undefined);
});
