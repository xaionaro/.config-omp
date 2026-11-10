// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-hook-runner.test.ts
// (omp is a Bun build; no separate bun is needed). Not a hook factory: lib/ is not discovered.
import { afterEach, beforeEach, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { identityFor, preToolVerdict, prefixMarksSubagent, resolveHookBash, runEciHook } from "./eci-hook-runner";
import eciBashGate from "../pre/eci-bash-gate";

const id = { sessionId: "s", cwd: "/", isSubagent: false };
const savedPath = process.env.PATH;
const savedBash = process.env.ECI_BASH;
let dir = "";

/** A private scratch directory (0700, under $HOME, not /tmp). */
beforeEach(() => {
	dir = mkdtempSync(join(homedir(), ".eci-hook-runner-test."));
	chmodSync(dir, 0o700);
});

afterEach(() => {
	process.env.PATH = savedPath;
	if (savedBash === undefined) delete process.env.ECI_BASH;
	else process.env.ECI_BASH = savedBash;
	rmSync(dir, { recursive: true, force: true });
});

function script(name: string, body: string): string {
	const path = join(dir, name);
	writeFileSync(path, body, { mode: 0o700 });
	return path;
}

/** A PATH directory holding only bash and cat, so no `timeout` is reachable. */
function pathWithoutTimeout(): string {
	const bin = join(dir, "bin");
	const bash = Bun.which("bash", { PATH: savedPath ?? "" });
	const cat = Bun.which("cat", { PATH: savedPath ?? "" });
	if (!bash || !cat) throw new Error("bash and cat must be on the test PATH");
	Bun.spawnSync(["mkdir", "-m", "700", bin]);
	symlinkSync(bash, join(bin, "bash"));
	symlinkSync(cat, join(bin, "cat"));
	return bin;
}

test("runs hooks when no timeout binary is on PATH", async () => {
	const echo = script("echo.sh", "cat\n");
	process.env.PATH = pathWithoutTimeout();
	delete process.env.ECI_BASH;
	expect(Bun.which("timeout", { PATH: process.env.PATH })).toBeNull();
	const run = await runEciHook(echo, { k: "v" }, id, 10);
	expect(run.code).toBe(0);
	expect(run.ok).toBe(true);
	expect(JSON.parse(run.stdout)).toEqual({ k: "v" });
});

test("passes the script exit code through", async () => {
	const run = await runEciHook(script("exit3.sh", "exit 3\n"), {}, id, 10);
	expect(run.code).toBe(3);
	expect(run.ok).toBe(false);
});

test("PreToolUse: a nonzero exit blocks even with empty stdout; exit 0 with empty stdout allows", async () => {
	const failed = await runEciHook(script("die.sh", "echo boom >&2\nexit 1\n"), {}, id, 10);
	expect(failed.stdout).toBe("");
	expect(preToolVerdict(failed, "validate-bash")?.reason).toContain("exited 1: boom");
	const quiet = await runEciHook(script("quiet.sh", "exit 0\n"), {}, id, 10);
	expect(preToolVerdict(quiet, "validate-bash")).toBeUndefined();
});

test("Bash hook transport failures explain how to diagnose and retry without changing other hooks", () => {
	for (const run of [
		{ ok: false, code: 124, stdout: "", stderr: "" },
		{ ok: true, code: 0, stdout: "not json", stderr: "" },
		{ ok: true, code: 0, stdout: "{}", stderr: "" },
	]) {
		const verdict = preToolVerdict(run, "validate-bash");
		expect(verdict?.block).toBe(true);
		expect(verdict?.reason).toContain("validate-bash");
		expect(verdict?.reason).toContain("Inspect");
		expect(verdict?.reason).toContain("retry");
		expect(preToolVerdict(run, "pretooluse-edit-dispatch")?.reason).not.toContain("validate-bash");
	}
	const timeout = preToolVerdict({ ok: false, code: 124, stdout: "", stderr: "" }, "validate-bash");
	expect(timeout?.reason).toContain("process");
	expect(timeout?.reason).toContain("disk");
	const malformed = preToolVerdict({ ok: true, code: 0, stdout: "broken", stderr: "" }, "validate-bash");
	expect(malformed?.reason).toContain("outside the blocked Bash tool");
});

test("Bash factory exceptions block with external repair guidance without exposing the exception", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; } } as never);
	expect(handler).toBeDefined();
	const event = { toolName: "bash", get input(): never { throw new Error("sensitive-value"); } };
	const verdict = await handler!(event, {});
	expect(verdict).toMatchObject({ block: true });
	expect(JSON.stringify(verdict)).toContain("outside the blocked Bash tool");
	expect(JSON.stringify(verdict)).not.toContain("sensitive-value");
});

test("a deadline stops the hook with SIGTERM and reports 124", async () => {
	const started = Date.now();
	const run = await runEciHook(script("sleep.sh", "exec sleep 30\n"), {}, id, 1);
	expect(run.code).toBe(124);
	expect(Date.now() - started).toBeLessThan(5000);
	expect(preToolVerdict(run, "t")).toEqual({ block: true, reason: "ECI hook t timed out; blocking (fail-closed)." });
});

test("the deadline also stops children the hook started", async () => {
	const started = Date.now();
	const run = await runEciHook(script("child.sh", "sleep 30\necho done\n"), {}, id, 1);
	expect(run.code).toBe(124);
	expect(run.stdout).toBe("");
	expect(Date.now() - started).toBeLessThan(2500);
});

test("a hook that ignores SIGTERM is killed after the grace period and reports 137", async () => {
	const stubborn = script("stubborn.sh", "trap '' TERM\nwhile :; do sleep 1; done\n");
	const run = await runEciHook(stubborn, {}, id, 1);
	expect(run.code).toBe(137);
	expect(preToolVerdict(run, "t")?.block).toBe(true);
}, 15000);

test("ECI_BASH selects the shell", async () => {
	const marker = join(dir, "marker");
	const realBash = Bun.which("bash", { PATH: savedPath ?? "" }) ?? "";
	const wrapper = script("mybash", `#!${realBash}\n: > ${marker}\nexec ${realBash} "$@"\n`);
	process.env.ECI_BASH = wrapper;
	const run = await runEciHook(script("ok.sh", "exit 0\n"), {}, id, 10);
	expect(run.code).toBe(0);
	expect(await Bun.file(marker).exists()).toBe(true);
});

type CaseLine = string | { title: "slot" | "torn" | "short" } | { repeat: string; count: number } | { pad: number };
interface PrefixCase {
	name: string;
	lines: CaseLine[];
	tail?: string;
	want: "main" | "unknown" | "subagent";
}

/** Builds a case's transcript bytes exactly as hook_transcript_prefix_test.go does. */
function caseContent(c: PrefixCase): string {
	const head = '{"type":"title","v":1,"title":"t","pad":"';
	const out: string[] = [];
	for (const line of c.lines) {
		if (typeof line === "string") out.push(line);
		else if ("title" in line) {
			const slot = `${head}${" ".repeat(255 - head.length - 2)}"}`;
			out.push(line.title === "slot" ? slot : line.title === "torn" ? `${slot.slice(0, 200)}${" ".repeat(55)}` : slot.slice(0, 100));
		} else if ("repeat" in line) for (let i = 0; i < line.count; i++) out.push(line.repeat);
		else out.push(`{"type":"message","pad":"${"x".repeat(line.pad)}"}`);
	}
	return out.map(line => `${line}\n`).join("") + (c.tail ?? "");
}

test("the prefix rule matches the Go classifier on the shared cases", async () => {
	const table = join(import.meta.dir, "../../eci/hooks/lib/eci-command-plan-go/plan/testdata/transcript-prefix-cases.json");
	const { cases } = (await Bun.file(table).json()) as { cases: PrefixCase[] };
	expect(cases.length).toBeGreaterThan(10);
	for (const c of cases) {
		const marked = prefixMarksSubagent(new TextEncoder().encode(caseContent(c)).subarray(0, 64 << 10));
		expect({ name: c.name, marked }).toEqual({ name: c.name, marked: c.want !== "main" });
	}
});

test("subagent detection reads only the prefix of a large transcript", () => {
	const big = join(dir, "big.jsonl");
	const history = `{"type":"message","pad":"${"x".repeat(1 << 16)}"}\n`.repeat(64);
	writeFileSync(big, `{"type":"session","version":3,"id":"m"}\n${history}{"type":"session_init"}\n`);
	const ctx = { cwd: "/", sessionManager: { getSessionFile: () => big } };
	expect(identityFor(ctx).isSubagent).toBe(false);
	const missing = { cwd: "/", sessionManager: { getSessionFile: () => join(dir, "missing.jsonl") } };
	expect(identityFor(missing).isSubagent).toBe(true);
});

test("an unusable ECI_BASH or a PATH without bash fails closed", async () => {
	expect(resolveHookBash({ ECI_BASH: "bash" })).toEqual({ error: "ECI_BASH is not an absolute path: bash" });
	expect("error" in resolveHookBash({ ECI_BASH: join(dir, "missing") })).toBe(true);
	expect(resolveHookBash({ PATH: dir })).toEqual({ error: "bash not found on PATH" });
	process.env.ECI_BASH = "relative-bash";
	const run = await runEciHook(script("ok2.sh", "exit 0\n"), {}, id, 10);
	expect(run.code).toBe(-1);
	expect(preToolVerdict(run, "t")?.block).toBe(true);
});
