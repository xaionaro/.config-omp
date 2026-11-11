import { afterAll, beforeAll, beforeEach, expect, mock, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

let root: string;
let home: string;
let helper: string;
const originalHome = process.env.HOME;
const originalError = console.error;
let errors: string[] = [];
type Handler = (event: unknown) => Promise<unknown>;

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

async function load(): Promise<Handler> {
	const handlers: Record<string, Handler> = {};
	// Fresh module instance per test: the warn-once flag is module state.
	const factory = (await import(`./bash-survivors.ts?${Math.random()}`)).default;
	factory({ on(name: string, handler: Handler) { handlers[name] = handler; } } as never);
	return handlers.tool_result;
}

const bash = (details?: unknown, isError = false) => ({ toolName: "bash", toolCallId: "call-1", input: { command: "x" }, details, isError });

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
});

afterAll(() => {
	console.error = originalError;
	mock.restore();
	if (originalHome === undefined) delete process.env.HOME; else process.env.HOME = originalHome;
	rmSync(home, { recursive: true, force: true });
});

test("returns the helper notice as additionalContext", async () => {
	writeHelper("process.stdout.write('notice text\\n');");
	const onResult = await load();
	expect(await onResult(bash())).toEqual({ additionalContext: "notice text" });
	expect(calls()).toEqual([["call-survivors", "call-1"]]);
});

test("scans errored bash results", async () => {
	writeHelper("process.stdout.write('notice');");
	const onResult = await load();
	expect(await onResult(bash(undefined, true))).toEqual({ additionalContext: "notice" });
});

test("empty helper output returns nothing", async () => {
	writeHelper("");
	const onResult = await load();
	expect(await onResult(bash())).toBeUndefined();
	expect(calls()).toHaveLength(1);
});

test("skips non-bash, running async, and service results", async () => {
	writeHelper("process.stdout.write('notice');");
	const onResult = await load();
	expect(await onResult({ ...bash(), toolName: "read" })).toBeUndefined();
	expect(await onResult(bash({ async: { state: "running", jobId: "bg_1" } }))).toBeUndefined();
	expect(await onResult(bash({ service: { name: "svc" } }))).toBeUndefined();
	expect(calls()).toEqual([]);
});

test("helper failures return nothing and warn once", async () => {
	writeHelper("process.exit(2);");
	const onResult = await load();
	expect(await onResult(bash())).toBeUndefined();
	// Real hang: exercises the hook's 2 s execFile timeout against an actual child.
	writeHelper("setTimeout(() => {}, 10000);");
	expect(await onResult(bash())).toBeUndefined();
	rmSync(helper);
	expect(await onResult(bash())).toBeUndefined();
	expect(errors).toHaveLength(1);
	expect(errors[0]).toContain("build-native-binaries.sh eci-hook-helper");
}, 15_000);
