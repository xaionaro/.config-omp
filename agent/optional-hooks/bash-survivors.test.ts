import { afterAll, beforeAll, beforeEach, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

let root: string;
let home: string;
let helper: string;
const originalHome = process.env.HOME;
const originalError = console.error;
let errors: string[] = [];
type Handler = (event: unknown, ctx: unknown) => Promise<unknown>;
type Instance = { handlers: Record<string, Handler>; sent: unknown[][]; notices: unknown[][]; ctx: unknown };

function writeHelper(body: string): void {
	writeFileSync(helper, `#!/usr/bin/env node
require('fs').appendFileSync(${JSON.stringify(join(root, "calls.jsonl"))}, JSON.stringify(process.argv.slice(2)) + '\\n');
${body}
`);
	chmodSync(helper, 0o755);
}

function calls(): string[][] {
	const path = join(root, "calls.jsonl");
	return existsSync(path) ? readFileSync(path, "utf8").trim().split("\n").map(line => JSON.parse(line)) : [];
}

type Factory = (pi: unknown) => void;

// Fresh module instance per test: the warn-once flag is module state.
async function loadFactory(): Promise<Factory> {
	return (await import(`./bash-survivors.ts?${Math.random()}`)).default;
}

function instantiate(factory: Factory): Instance {
	const instance: Instance = { handlers: {}, sent: [], notices: [], ctx: undefined };
	instance.ctx = { ui: { notify: (...args: unknown[]) => { instance.notices.push(args); } } };
	factory({
		on(name: string, handler: Handler) { instance.handlers[name] = handler; },
		sendMessage(...args: unknown[]) { instance.sent.push(args); },
	});
	return instance;
}

async function load(): Promise<Instance> {
	return instantiate(await loadFactory());
}

const bash = (details?: unknown, isError = false, toolCallId = "call-1") => ({ toolName: "bash", toolCallId, input: { command: "x" }, details, isError });
const running = (jobId: string, toolCallId = "call-1") => bash({ async: { state: "running", jobId, type: "bash" } }, false, toolCallId);
const waitResult = (jobs: unknown) => ({ toolName: "wait", toolCallId: "w", input: {}, details: { op: "wait", jobs }, isError: false });
const asyncResult = (jobs: unknown, role = "custom", customType = "async-result") => ({ message: { role, customType, details: { jobs } } });

async function result(instance: Instance, event: unknown): Promise<unknown> {
	return instance.handlers.tool_result(event, instance.ctx);
}

async function messageEnd(instance: Instance, event: unknown): Promise<unknown> {
	return instance.handlers.message_end(event, instance.ctx);
}

beforeAll(() => {
	home = mkdtempSync(join(tmpdir(), "bash-survivors-"));
	root = join(home, ".omp", "agent");
	process.env.HOME = home;
	mkdirSync(join(root, "eci", "bin"), { recursive: true });
	mkdirSync(join(root, "eci", "hooks", "lib"), { recursive: true });
	writeFileSync(join(root, "eci", "hooks", "lib", "eci-native-provision.sh"), "eci_native_prepare_hook() { :; }\n");
	helper = join(root, "eci", "bin", "eci-hook-helper");
	writeFileSync(`${helper}.sha256`, "stub");
	writeFileSync(join(root, "eci", "bin", "eci-verexec"), "stub");
	writeFileSync(join(root, "eci", "bin", "eci-verexec.sha256"), "stub");
	console.error = (...args: unknown[]) => { errors.push(args.map(String).join(" ")); };
});

beforeEach(() => {
	rmSync(join(root, "calls.jsonl"), { force: true });
	errors = [];
	writeHelper("process.stdout.write('notice text\\n');");
});

afterAll(() => {
	console.error = originalError;
	if (originalHome === undefined) delete process.env.HOME; else process.env.HOME = originalHome;
	rmSync(home, { recursive: true, force: true });
});

test("returns the helper notice as additionalContext", async () => {
	const instance = await load();
	expect(await result(instance, bash())).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("scans errored bash results", async () => {
	const instance = await load();
	expect(await result(instance, bash(undefined, true))).toEqual({ additionalContext: "notice text" });
});

test("empty helper output returns nothing", async () => {
	writeHelper("");
	const instance = await load();
	expect(await result(instance, bash())).toBeUndefined();
	expect(calls()).toHaveLength(1);
});

test("skips non-bash and service results", async () => {
	const instance = await load();
	expect(await result(instance, { ...bash(), toolName: "read" })).toBeUndefined();
	expect(await result(instance, bash({ service: { name: "svc" } }))).toBeUndefined();
	expect(calls()).toEqual([]);
});

test("T1 running async job is scanned when wait reports it settled", async () => {
	const instance = await load();
	expect(await result(instance, running("bg_1"))).toBeUndefined();
	expect(calls()).toEqual([]);
	expect(await result(instance, waitResult([{ id: "bg_1", type: "bash", status: "completed" }]))).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("T2 a settled job is claimed once", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	await result(instance, waitResult([{ id: "bg_1", status: "completed" }]));
	expect(await result(instance, waitResult([{ id: "bg_1", status: "completed" }]))).toBeUndefined();
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", status: "completed" }]));
	expect(calls()).toHaveLength(1);
	expect(instance.sent).toEqual([]);
});

test("T3 a still-running job in a wait result stays remembered", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	expect(await result(instance, waitResult([{ id: "bg_1", status: "running" }]))).toBeUndefined();
	expect(calls()).toEqual([]);
	expect(await result(instance, waitResult([{ id: "bg_1", status: "failed" }]))).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("T4 unremembered jobs, non-array jobs and peer-message waits do not scan", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	expect(await result(instance, waitResult([{ id: "bg_9", status: "completed" }]))).toBeUndefined();
	expect(await result(instance, waitResult({ id: "bg_1" }))).toBeUndefined();
	expect(await result(instance, { toolName: "wait", toolCallId: "w", input: {}, details: { op: "wait", messages: [] }, isError: false })).toBeUndefined();
	await messageEnd(instance, asyncResult([{ jobId: "bg_9", status: "completed" }]));
	await messageEnd(instance, asyncResult("bg_1"));
	expect(calls()).toEqual([]);
	expect(instance.sent).toEqual([]);
});

test("T5 async-result message sends the notice as a hidden aside", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", type: "bash", status: "completed" }]));
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
	expect(instance.sent).toEqual([[{ customType: "bash-survivors", content: "notice text", display: false }, { deliverAs: "aside" }]]);
});

test("T6 other messages and empty notices send nothing", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", status: "completed" }], "assistant"));
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", status: "completed" }], "custom", "other"));
	expect(calls()).toEqual([]);
	writeHelper("");
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", status: "completed" }]));
	expect(calls()).toHaveLength(1);
	expect(instance.sent).toEqual([]);
});

test("T7 two settled jobs share one scan", async () => {
	const instance = await load();
	await result(instance, running("bg_1", "call-1"));
	await result(instance, running("bg_2", "call-2"));
	expect(await result(instance, waitResult([{ id: "bg_1", status: "completed" }, { id: "bg_2", status: "cancelled" }]))).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1", "call-2"]]);
});

test("T8 remembered jobs belong to one hook instance", async () => {
	const factory = await loadFactory();
	const a = instantiate(factory);
	const b = instantiate(factory);
	await result(a, running("bg_1"));
	await messageEnd(b, asyncResult([{ jobId: "bg_1", status: "completed" }]));
	expect(calls()).toEqual([]);
	expect(b.sent).toEqual([]);
});

test("T11 session changes forget remembered jobs", async () => {
	for (const event of ["session_start", "session_switch", "session_tree", "session_branch"]) {
		const instance = await load();
		await result(instance, running("bg_1"));
		await instance.handlers[event]?.({}, instance.ctx);
		expect(await result(instance, waitResult([{ id: "bg_1", status: "completed" }]))).toBeUndefined();
	}
	expect(calls()).toEqual([]);
});

const killResult = (jobs: unknown) => ({ toolName: "write", toolCallId: "k", input: { path: "proc://bg_1/kill", content: "" }, details: { proc: { op: "cancel", jobs, cancelled: [{ id: "bg_1", status: "cancelled" }] } }, isError: false });

test("T12 a killed job is scanned at the kill result", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	expect(await result(instance, killResult([{ id: "bg_1", type: "bash", status: "cancelled" }]))).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("T13 a killed job is claimed once", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	await result(instance, killResult([{ id: "bg_1", type: "bash", status: "cancelled" }]));
	expect(await result(instance, killResult([{ id: "bg_1", status: "cancelled" }]))).toBeUndefined();
	expect(await result(instance, waitResult([{ id: "bg_1", status: "completed" }]))).toBeUndefined();
	await messageEnd(instance, asyncResult([{ jobId: "bg_1", status: "completed" }]));
	expect(calls()).toHaveLength(1);
	expect(instance.sent).toEqual([]);
});

test("T14 a kill result reporting an already finished job scans it", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	expect(await result(instance, killResult([{ id: "bg_1", status: "completed" }]))).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("T15 other write results do not scan", async () => {
	const instance = await load();
	await result(instance, running("bg_1"));
	const write = (details: unknown, isError = false) => ({ toolName: "write", toolCallId: "k", input: { path: "a.txt", content: "x" }, details, isError });
	expect(await result(instance, write(undefined))).toBeUndefined();
	expect(await result(instance, write({}))).toBeUndefined();
	expect(await result(instance, write({ proc: { action: "stop", daemon: { name: "svc" } } }))).toBeUndefined();
	expect(await result(instance, write({ proc: { op: "cancel", jobs: [], cancelled: [{ id: "A1", status: "cancelled" }] } }))).toBeUndefined();
	expect(await result(instance, killResult([{ id: "bg_9", status: "cancelled" }]))).toBeUndefined();
	expect(await result(instance, write(undefined, true))).toBeUndefined();
	expect(calls()).toEqual([]);
});

test("T9 a helper that outlives the timeout yields nothing", async () => {
	// Real hang: exercises the hook's 2 s execFile timeout against an actual child.
	writeHelper("setTimeout(() => process.stdout.write('late notice'), 5000);");
	const instance = await load();
	expect(await result(instance, bash())).toBeUndefined();
}, 10_000);

test("T10 helper failures warn once on stderr and in the UI", async () => {
	writeHelper("process.exit(2);");
	const instance = await load();
	expect(await result(instance, bash())).toBeUndefined();
	rmSync(helper);
	expect(await result(instance, bash())).toBeUndefined();
	expect(errors).toHaveLength(1);
	expect(errors[0]).toContain('build-native-binaries.sh" eci-hook-helper');
	expect(instance.notices).toHaveLength(1);
	expect(instance.notices[0][0]).toContain('build-native-binaries.sh" eci-hook-helper');
	expect(instance.notices[0][1]).toBe("warning");
});
