import { afterAll, beforeAll, expect, mock, test } from "bun:test";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type gateFactory from "./coding-skill-gate.ts";

let root: string;
let home: string;
const originalHome = process.env.HOME;
let gate: typeof gateFactory;
let editMode = "hashline";

beforeAll(async () => {
	home = mkdtempSync(join(tmpdir(), "coding-skill-bridge-"));
	root = join(home, ".omp", "agent");
	process.env.HOME = home;
	mkdirSync(join(root, "eci", "bin"), {recursive: true});
	const engine = join(root, "eci", "bin", "eci-hook-helper");
	writeFileSync(engine, `#!/usr/bin/env node
const fs = require('fs');
const mode = process.argv[3];
if (mode === 'describe') {
 process.stdout.write(JSON.stringify({callTools:['write','edit'],resultTools:['read']}));
} else {
 let data=''; process.stdin.on('data',chunk=>data+=chunk); process.stdin.on('end',()=>{
  const req=JSON.parse(data); fs.appendFileSync(${JSON.stringify(join(root, "requests.jsonl"))},JSON.stringify({mode,...req})+'\\n');
  const state=req.state || {reads:[]};
  if (mode === 'reduce') {
   if (Array.isArray(req.branch) && req.branch.length) state.reads.push('branch');
   for (const result of req.results) if (!result.isError) state.reads.push(result.input.path);
   process.stdout.write(JSON.stringify({state}));
  } else if (mode === 'check') {
   process.stdout.write(JSON.stringify({block:!state.reads.includes('skill://coding-common') || !state.reads.includes('skill://coding-go'),reason:'missing skill'}));
  } else process.exit(2);
 });
}
`);
	writeFileSync(`${engine}.sha256`, "stub");
	chmodSync(engine, 0o755);
	mock.module("@oh-my-pi/pi-natives", () => ({ editInspect: (mode: string) => ({paths: [mode === "sloppy" ? "notes.txt" : "a.go"], fileOps: []}) }));
	mock.module("@oh-my-pi/pi-coding-agent/utils/edit-mode", () => ({ resolveEditMode: () => editMode }));
	// Load after replacing the host-bundled modules.
	gate = (await import("./coding-skill-gate.ts")).default;
});

afterAll(() => {
	mock.restore();
	if (originalHome === undefined) delete process.env.HOME; else process.env.HOME = originalHome;
	rmSync(home, {recursive: true, force: true});
});

test("serializes parallel read reductions before the next write check", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "one", getBranch: () => []}};
	await handlers.session_start({}, ctx);
	const result = (path: string) => handlers.tool_result({toolName: "read", input: {path}, isError: false}, ctx);
	await Promise.all([result("skill://coding-common"), result("skill://coding-go")]);
	expect(await handlers.tool_call({toolName: "write", input: {path: "a.go"}}, ctx)).toBeUndefined();
	const requests = readFileSync(join(root, "requests.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
	const reductions = requests.filter(request => request.mode === "reduce");
	expect(reductions).toHaveLength(2);
	expect(reductions[1].state.reads).toContain("skill://coding-common");
});

test("does not hydrate an advisor from its parent's transcript", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "two", getBranch: () => [{type: "message"}]}};
	await handlers.session_start({}, ctx);
	await handlers.tool_result({toolName: "read", input: {path: "skill://coding-go"}, isError: false}, {...ctx, agent: {id: "advisor"}});
	const requests = readFileSync(join(root, "requests.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
	const last = requests.filter(request => request.mode === "reduce").at(-1);
	expect(last.branch ?? []).toEqual([]);
});

test("sends only skill read history to the policy engine", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const branch = [
		{type: "message", message: {role: "assistant", content: [{type: "toolCall", id: "skill", name: "read", arguments: {path: "skill://coding-common"}}, {type: "toolCall", id: "other", name: "read", arguments: {path: "large.txt"}}]}},
		{type: "message", message: {role: "toolResult", toolCallId: "other", toolName: "read", content: [{type: "text", text: "unrelated large data"}]}},
		{type: "message", message: {role: "toolResult", toolCallId: "skill", toolName: "read", details: {totalLines: 1}}},
	];
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "history", getBranch: () => branch}};
	await handlers.session_start({}, ctx);
	await handlers.tool_call({toolName: "write", input: {path: "a.go"}}, ctx);
	const requests = readFileSync(join(root, "requests.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
	const history = requests.filter(request => request.mode === "reduce").at(-1).branch;
	expect(JSON.stringify(history)).not.toContain("large.txt");
	expect(JSON.stringify(history)).not.toContain("unrelated large data");
	expect(JSON.stringify(history)).toContain("skill://coding-common");
});

test("does not send unrelated reads to the policy engine", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "unrelated", getBranch: () => []}};
	await handlers.session_start({}, ctx);
	const before = readFileSync(join(root, "requests.jsonl"), "utf8");
	await handlers.tool_result({toolName: "read", input: {path: "large.txt"}, isError: false}, ctx);
	expect(readFileSync(join(root, "requests.jsonl"), "utf8")).toBe(before);
});

test("uses the active edit mode when inspecting targets", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({pi: {settings: {}}, on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "sloppy", getBranch: () => []}};
	editMode = "sloppy";
	try {
		await handlers.session_start({}, ctx);
		await handlers.tool_call({toolName: "edit", input: {input: "*** Edit File: notes.txt\n*** Find\n[a.go#ABCD]\n*** Replace\n[b.go#1234]"}}, ctx);
		const requests = readFileSync(join(root, "requests.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
		expect(requests.filter(request => request.mode === "check").at(-1).call.editTargets).toEqual(["notes.txt"]);
	} finally {
		editMode = "hashline";
	}
});

test("does not credit pending same-batch reads and resets on navigation", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "three", getBranch: () => []}};
	await handlers.session_start({}, ctx);
	const write = () => handlers.tool_call({toolName: "write", input: {path: "a.go"}}, ctx);
	const read = (path: string) => handlers.tool_result({toolName: "read", input: {path}, isError: false}, ctx);
	const first = write();
	const results = [read("skill://coding-common"), read("skill://coding-go")];
	expect(await first).toEqual({block: true, reason: "missing skill"});
	await Promise.all(results);
	expect(await write()).toBeUndefined();
	await handlers.session_tree({}, ctx);
	expect(await write()).toEqual({block: true, reason: "missing skill"});
});

test("does not carry an in-flight old-branch result across navigation", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "four", getBranch: () => []}};
	await handlers.session_start({}, ctx);
	const pending = handlers.tool_result({toolName: "read", input: {path: "skill://coding-common"}, isError: false}, ctx);
	await handlers.session_tree({}, ctx);
	await pending;
	await handlers.tool_result({toolName: "read", input: {path: "skill://coding-go"}, isError: false}, ctx);
	expect(await handlers.tool_call({toolName: "write", input: {path: "a.go"}}, ctx)).toEqual({block: true, reason: "missing skill"});
});

test("reports an actionable error when the provisioned engine disappears", async () => {
	const handlers: Record<string, (event: unknown, ctx: unknown) => unknown> = {};
	gate({pi: {settings: {}}, on(name: string, handler: (event: unknown, ctx: unknown) => unknown) { handlers[name] = handler; }} as never);
	const ctx = {cwd: root, agent: {id: "Main"}, sessionManager: {getSessionId: () => "five", getBranch: () => []}};
	await handlers.session_start({}, ctx);
	await handlers.tool_result({toolName: "read", input: {path: "skill://coding-common"}, isError: false}, ctx);
	const engine = join(root, "eci", "bin", "eci-hook-helper");
	renameSync(engine, `${engine}.held`);
	try {
		const result = await handlers.tool_call({toolName: "write", input: {path: "a.go"}}, ctx);
		expect(result).toMatchObject({block: true, reason: expect.stringContaining("Coding skill gate unavailable")});
		expect(await handlers.tool_call({toolName: "write", input: {path: "[a.go#ABCD]"}}, ctx)).toMatchObject({block: true});
		expect(await handlers.tool_call({toolName: "write", input: {path: "notes.txt"}}, ctx)).toBeUndefined();
		editMode = "sloppy";
		expect(await handlers.tool_call({toolName: "edit", input: {input: "*** Edit File: notes.txt"}}, ctx)).toBeUndefined();
		editMode = "hashline";
	} finally {
		renameSync(`${engine}.held`, engine);
	}
});
