// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-hook-runner.test.ts
// (omp is a Bun build; no separate bun is needed). Not a hook factory: lib/ is not discovered.
import { afterEach, beforeEach, expect, test } from "bun:test";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, watch, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { on } from "node:events";
import { join } from "node:path";
import { BASH_GATE_DEADLINE_SEC, DRAIN_SEC, KILL_GRACE_SEC, advisoryContext, bashHostCwd, bashPreToolVerdict, identityFor, preToolVerdict, prefixMarksSubagent, resolveHookBash, runEciHook } from "./eci-hook-runner";
import eciBashGate from "../pre/eci-bash-gate";
import eciSessionStart from "../post/eci-session-start";

const id = { sessionId: "s", cwd: "/", isSubagent: false };
const savedPath = process.env.PATH;
const exited = (code: number) => ({ kind: "ran" as const, stop: { kind: "exit" as const, code }, cleanup: "graceful" as const });
const deadline = (cleanup: "graceful" | "forced" | "unconfirmed") => ({ kind: "ran" as const, stop: { kind: "deadline" as const }, cleanup });
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

/** Verifies that a recorded child PID is absent, not merely unsignalable. */
function expectProcessGone(pidFile: string): void {
	const pid = Number(readFileSync(pidFile, "utf8").trim());
	expect(Number.isSafeInteger(pid) && pid > 0).toBe(true);
	let failure: unknown;
	try {
		process.kill(pid, 0);
	} catch (err) {
		failure = err;
	}
	expect(failure).toMatchObject({ code: "ESRCH" });
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
	expect(run.outcome).toEqual(exited(0));
	expect(JSON.parse(run.stdout)).toEqual({ k: "v" });
});

test("passes the script exit code through", async () => {
	const run = await runEciHook(script("exit3.sh", "exit 3\n"), {}, id, 10);
	expect(run.outcome).toEqual(exited(3));
});

test("PreToolUse: a nonzero exit blocks even with empty stdout; exit 0 with empty stdout allows", async () => {
	const failed = await runEciHook(script("die.sh", "echo boom >&2\nexit 1\n"), {}, id, 10);
	expect(failed.stdout).toBe("");
	expect(preToolVerdict(failed, "validate-bash")?.reason).toContain("exited 1: boom");
	const quiet = await runEciHook(script("quiet.sh", "exit 0\n"), {}, id, 10);
	expect(preToolVerdict(quiet, "validate-bash")).toBeUndefined();
});

test("advisory context is delivered only for a successful matching envelope", () => {
	const stdout = JSON.stringify({ hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: "snapshot" } });
	expect(advisoryContext({ outcome: exited(0), stdout, stderr: "" }, "SessionStart")).toBe("snapshot");
	expect(advisoryContext({ outcome: exited(0), stdout, stderr: "" }, "UserPromptSubmit")).toBeUndefined();
	for (const outcome of [
		exited(1),
		deadline("graceful"),
		{ kind: "spawn-error" as const, message: "unavailable" },
		{ kind: "ran" as const, stop: { kind: "exit" as const, code: 0 }, cleanup: "unconfirmed" as const },
	]) {
		expect(advisoryContext({ outcome, stdout, stderr: "" }, "SessionStart")).toBeUndefined();
	}
});

test("session-start factory emits a hidden advisory and suppresses failed hooks", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	const messages: unknown[] = [];
	eciSessionStart({ on: (_name: string, callback: typeof handler) => { handler = callback; }, sendMessage: (...args: unknown[]) => { messages.push(args); } } as never);
	const savedHome = process.env.HOME;
	const hooks = join(dir, ".omp", "agent", "eci", "hooks");
	mkdirSync(hooks, { recursive: true });
	const hook = join(hooks, "session-snapshot.sh");
	const envelope = JSON.stringify({ hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: "snapshot" } });
	const ctx = { cwd: dir, sessionManager: { getSessionFile: () => undefined } };
	try {
		process.env.HOME = dir;
		writeFileSync(hook, `printf '%s\\n' '${envelope}'\n`);
		await handler!({}, ctx);
		expect(messages).toEqual([[{ customType: "eci-session-snapshot", content: "snapshot", display: false, attribution: "agent" }, { deliverAs: "nextTurn" }]]);
		writeFileSync(hook, "exit 1\n");
		await handler!({}, ctx);
		expect(messages).toHaveLength(1);
	} finally {
		if (savedHome === undefined) delete process.env.HOME;
		else process.env.HOME = savedHome;
	}
});

test("Bash failures block with fixed diagnostics without exposing hook data", () => {
	const denial = (reason: unknown) => JSON.stringify({ hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: reason } });
	for (const run of [
		{ outcome: { kind: "spawn-error" as const, message: "secret" }, stdout: "", stderr: "" },
		{ outcome: deadline("graceful"), stdout: "", stderr: "secret" },
		{ outcome: exited(2), stdout: "", stderr: "secret" },
		{ outcome: { kind: "ran" as const, stop: { kind: "exit" as const, code: 0 }, cleanup: "forced" as const }, stdout: "", stderr: "secret" },
		{ outcome: deadline("unconfirmed"), stdout: "", stderr: "secret" },
		{ outcome: exited(0), stdout: "secret", stderr: "" },
		{ outcome: exited(0), stdout: "{}", stderr: "" },
		...([undefined, 42, "", "  "].map(reason => ({ outcome: exited(0), stdout: denial(reason), stderr: "" }))),
	]) {
		const verdict = bashPreToolVerdict(run);
		expect(verdict?.block).toBe(true);
		expect(verdict?.reason).toContain("If eci_bash_gate_repair is available");
		expect(verdict?.reason).toContain("if unavailable here");
		expect(verdict?.reason).not.toContain("secret");
	}
	expect(bashPreToolVerdict({ outcome: exited(0), stdout: denial("bounded route"), stderr: "" })?.reason).toBe("bounded route");
	expect(bashPreToolVerdict({ outcome: exited(0), stdout: "", stderr: "" })).toBeUndefined();
	expect(BASH_GATE_DEADLINE_SEC + KILL_GRACE_SEC + DRAIN_SEC).toBeLessThan(30);
});

test("Bash factory exceptions block with conditional repair guidance without exposing the exception", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; }, zod: { object: () => ({}) }, registerTool: () => {} } as never);
	expect(handler).toBeDefined();
	const event = { toolName: "bash", get input(): never { throw new Error("sensitive-value"); } };
	const verdict = await handler!(event, {});
	expect(verdict).toMatchObject({ block: true });
	expect(JSON.stringify(verdict)).toContain("If eci_bash_gate_repair is available");
	expect(JSON.stringify(verdict)).not.toContain("sensitive-value");
});

test("Bash cwd only claims host spellings it can resolve exactly", () => {
	const session = "/session/base";
	const noCDPath = { CDPATH: "" };
	for (const [requested, expected] of [
		[undefined, session], ["", session], ["/", session], ["//", session],
		["~", homedir()], ["~/a", join(homedir(), "a")],
		["sub", "/session/base/sub"], ["../x", "/session/x"],
		["/tmp/", "/tmp"], ["/a/./b", "/a/b"],
	] as const) {
		expect(bashHostCwd(requested, "pwd", session, noCDPath)).toBe(expected);
	}
	for (const requested of [
		"/a/../b", ":/t", "@/t", "file:///t", "local://x", "local:/x",
		"/p/local://x", "https://e", "C:\\x", "a\u00a0b", "~user", 42,
	]) {
		expect(bashHostCwd(requested, "pwd", session, noCDPath)).toBeUndefined();
	}
	expect(bashHostCwd(undefined, "cd /abs && pwd", session, noCDPath)).toBe(session);
	expect(bashHostCwd(undefined, "cd @/x && pwd", session, noCDPath)).toBeUndefined();
	expect(bashHostCwd(undefined, "cd sub/child && pwd", session, { CDPATH: "/other" })).toBeUndefined();
});

test("Bash callback uses the session manager identity without inventing a transcript", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; }, zod: { object: () => ({}) }, registerTool: () => {} } as never);
	const savedHome = process.env.HOME;
	const hooks = join(dir, ".omp", "agent", "eci", "hooks");
	mkdirSync(hooks, { recursive: true });
	writeFileSync(join(hooks, "validate-bash.sh"), `pwd > '${join(dir, "hook-pwd")}'\ncat > '${join(dir, "callback.json")}'\n`);
	try {
		process.env.HOME = dir;
		const verdict = await handler!({ toolName: "bash", input: { command: "true" } }, {
			cwd: dir,
			sessionManager: { getSessionId: () => "native-session", getSessionFile: () => undefined },
		});
		expect(verdict).toBeUndefined();
		const payload = await Bun.file(join(dir, "callback.json")).json();
		expect(payload.session_id).toBe("native-session");
		expect(payload.cwd).toBe(dir);
		const dependency = join(dir, "dependency");
		mkdirSync(dependency);
		for (const requested of ["dependency", dependency, "local://repository"]) {
			const command = "git -C nested status";
			const next = await handler!({ toolName: "bash", input: { command, cwd: requested } }, {
				cwd: dir,
				sessionManager: { getSessionId: () => "native-session", getSessionFile: () => undefined },
			});
			expect(next).toBeUndefined();
			const callback = await Bun.file(join(dir, "callback.json")).json();
			expect(callback.cwd).toBe(requested.startsWith("local://") ? undefined : dependency);
			expect(callback.cwd_unknown).toBe(requested.startsWith("local://") ? true : undefined);
			expect(readFileSync(join(dir, "hook-pwd"), "utf8").trim()).toBe(dir);
			expect(callback.tool_input.command).toBe(command);
		}
		expect(payload.transcript_path).toBeUndefined();
	} finally {
		if (savedHome === undefined) delete process.env.HOME;
		else process.env.HOME = savedHome;
	}
});

test("Bash wrapper blocks when its verifier or planner is unavailable without a marker", async () => {
	const source = join(import.meta.dir, "../../eci/hooks");
	const hooks = join(dir, "hooks");
	mkdirSync(join(hooks, "lib"), { recursive: true });
	writeFileSync(join(hooks, "validate-bash.sh"), readFileSync(join(source, "validate-bash.sh")));
	writeFileSync(join(hooks, "lib", "eci-bash-guard.sh"), readFileSync(join(source, "lib", "eci-bash-guard.sh")));
	const savedHome = process.env.HOME;
	try {
		process.env.HOME = dir;
		for (const withVerifier of [false, true]) {
			if (withVerifier) writeFileSync(join(hooks, "lib", "eci-native-binary.sh"), readFileSync(join(source, "lib", "eci-native-binary.sh")));
			const run = await runEciHook(join(hooks, "validate-bash.sh"), { tool_name: "Bash", tool_input: { command: "true" } }, id, 5);
			expect(run.outcome).toEqual(exited(2));
			expect(run.stdout).toBe("");
			expect(run.stderr).toContain("ECI_HOOK_NATIVE_GATE_UNAVAILABLE");
		}
		writeFileSync(join(hooks, "lib", "eci-native-binary.sh"), "eci_runtime_verexec() { printf 'partial output'; return 2; }\n");
		const failedPlanner = await runEciHook(join(hooks, "validate-bash.sh"), {}, id, 5);
		expect(failedPlanner.outcome).toEqual(exited(2));
		expect(failedPlanner.stdout).toBe("");
		expect(failedPlanner.stderr.trim().split("\n")).toHaveLength(1);
	} finally {
		if (savedHome === undefined) delete process.env.HOME;
		else process.env.HOME = savedHome;
	}
});

test("a deadline stops the hook with SIGTERM", async () => {
	const started = Date.now();
	const run = await runEciHook(script("sleep.sh", "exec sleep 30\n"), {}, id, 1);
	expect(run.outcome).toEqual(deadline("graceful"));
	expect(Date.now() - started).toBeLessThan(5000);
	expect(preToolVerdict(run, "t")).toEqual({ block: true, reason: "ECI hook t timed out; blocking (fail-closed)." });
});

for (const policy of ["deadline-only", "owned-group"] as const) {
	test(`${policy}: deadline retires the hook's child within the cleanup bound`, async () => {
		const pidFile = join(dir, "child.pid");
		const started = Date.now();
		const run = await runEciHook(script("child.sh", `sleep 30 &\necho $! > '${pidFile}'\nwait\n`), {}, id, 1, { cleanup: policy });
		expect(run.outcome).toEqual(deadline("graceful"));
		expect(run.stdout).toBe("");
		expect(preToolVerdict(run, "edit-consumer")?.block).toBe(true);
		expect(bashPreToolVerdict(run)?.reason).toContain("deadline");
		expect(Date.now() - started).toBeLessThan((1 + KILL_GRACE_SEC + DRAIN_SEC + 2) * 1000);
		expectProcessGone(pidFile);
	}, 15000);

	test(`${policy}: redirected TERM-resistant child cannot outlive cleanup`, async () => {
		const pidFile = join(dir, "stubborn-child.pid");
		const run = await runEciHook(script("redirected.sh", `bash -c 'trap "" TERM; exec sleep 30' </dev/null >/dev/null 2>&1 &\necho $! > '${pidFile}'\nwait\n`), {}, id, 1, { cleanup: policy });
		expect(run.outcome).toEqual(deadline("forced"));
		expect(preToolVerdict(run, "edit-consumer")?.block).toBe(true);
		expectProcessGone(pidFile);
	}, 15000);
}

test("normal hook exit retires a redirected child in its process group", async () => {
	const pidFile = join(dir, "normal-child.pid");
	const run = await runEciHook(script("normal-child.sh", `sleep 30 </dev/null >/dev/null 2>&1 &\necho $! > '${pidFile}'\nexit 0\n`), {}, id, 10, { cleanup: "owned-group" });
	expect(run.outcome).toEqual(exited(0));
	expect(bashPreToolVerdict(run)).toBeUndefined();
	expectProcessGone(pidFile);
}, 15000);

test("normal-exit forced cleanup reports cleanup and preserves the hook exit", async () => {
	const pidFile = join(dir, "forced-child.pid");
	const readyFile = join(dir, "forced-child.ready");
	const run = await runEciHook(script("forced-child.sh", `bash -c 'trap "" TERM; echo ready > "$1"; exec sleep 30' _ '${readyFile}' </dev/null >/dev/null 2>&1 &\necho $! > '${pidFile}'\nwhile [ ! -f '${readyFile}' ]; do sleep 0.01; done\nexit 0\n`), {}, id, 10, { cleanup: "owned-group" });
	expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "exit", code: 0 }, cleanup: "forced" });
	expect(bashPreToolVerdict(run)?.reason).toContain("forced cleanup after exit 0");
	expectProcessGone(pidFile);
}, 15000);

for (const code of [124, 137]) {
	test(`ordinary hook exit ${code} is not a deadline`, async () => {
		const run = await runEciHook(script(`exit-${code}.sh`, `exit ${code}\n`), {}, id, 5, { cleanup: "owned-group" });
		expect(run.outcome).toEqual(exited(code));
		expect(bashPreToolVerdict(run)?.reason).toContain(`exit ${code}`);
		expect(preToolVerdict(run, "edit")?.reason).toContain("timed out");
	});
}

test("a hook that ignores SIGTERM is killed after the grace period", async () => {
	const stubborn = script("stubborn.sh", "trap '' TERM\nwhile :; do sleep 1; done\n");
	const run = await runEciHook(stubborn, {}, id, 1);
	expect(run.outcome).toEqual(deadline("forced"));
	expect(preToolVerdict(run, "t")?.block).toBe(true);
}, 15000);

test("unknown process-group probe errors remain an explicit cleanup failure", async () => {
	const originalKill = process.kill;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0 && signal === 0) throw Object.assign(new Error("private diagnostic"), { code: "EPERM" });
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const run = await runEciHook(script("probe-error.sh", "exec sleep 30\n"), {}, id, 1, { cleanup: "owned-group" });
		expect(run.outcome).toEqual(deadline("unconfirmed"));
		expect(bashPreToolVerdict(run)?.reason).toContain("cleanup");
		expect(bashPreToolVerdict(run)?.reason).not.toContain("private diagnostic");
	} finally {
		process.kill = originalKill;
	}
}, 15000);

test("deadline-only cleanup reports unknown group probes without hiding the Edit timeout", async () => {
	const originalKill = process.kill;
	let attemptedKill = false;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0 && signal === 0) throw Object.assign(new Error("private diagnostic"), { code: "EPERM" });
		if (pid < 0 && signal === "SIGKILL") attemptedKill = true;
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const run = await runEciHook(script("probe-default.sh", "exec sleep 30\n"), {}, id, 1);
		expect(run.outcome).toEqual(deadline("unconfirmed"));
		expect(attemptedKill).toBe(true);
		expect(preToolVerdict(run, "edit")?.reason).toContain("timed out");
	} finally {
		process.kill = originalKill;
	}
}, 15000);

for (const failedSignal of ["SIGTERM", "SIGKILL"] as const) {
	test(`unknown ${failedSignal} errors remain an explicit cleanup failure`, async () => {
		const originalKill = process.kill;
		let probes = 0;
		process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
			if (pid >= 0) return originalKill(pid, signal);
			if (signal === 0) {
				probes++;
				if (probes === 1 || (failedSignal === "SIGKILL" && probes === 2)) return true;
				throw Object.assign(new Error("absent"), { code: "ESRCH" });
			}
			if (signal === failedSignal) throw Object.assign(new Error("private diagnostic"), { code: "EPERM" });
			return true;
		}) as typeof process.kill;
		try {
			const run = await runEciHook(script("signal-error.sh", "exit 0\n"), {}, id, 5, { cleanup: "owned-group" });
			expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "exit", code: 0 }, cleanup: "unconfirmed" });
			expect(bashPreToolVerdict(run)?.reason).toContain("unconfirmed cleanup after exit 0");
			expect(bashPreToolVerdict(run)?.reason).toContain("cleanup");
			expect(bashPreToolVerdict(run)?.reason).not.toContain("private diagnostic");
		} finally {
			process.kill = originalKill;
		}
	}, 15000);
}

test("a group still present after forced cleanup is not reported as settled", async () => {
	const originalKill = process.kill;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0) return true;
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const run = await runEciHook(script("unsettled.sh", "exit 0\n"), {}, id, 5, { cleanup: "owned-group" });
		expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "exit", code: 0 }, cleanup: "unconfirmed" });
		expect(bashPreToolVerdict(run)?.reason).toContain("cleanup");
	} finally {
		process.kill = originalKill;
	}
}, 15000);

test("other hook consumers retain normal-exit behavior", async () => {
	const originalKill = process.kill;
	process.kill = (() => { throw new Error("unexpected group probe"); }) as typeof process.kill;
	try {
		const run = await runEciHook(script("other-hook.sh", "exit 0\n"), {}, id, 5);
		expect(run.outcome).toEqual(exited(0));
	} finally {
		process.kill = originalKill;
	}
});

test("pre-aborted runs never spawn", async () => {
	const controller = new AbortController();
	controller.abort();
	const marker = join(dir, "must-not-exist");
	const run = await runEciHook(script("pre-abort.sh", `touch '${marker}'\n`), {}, id, 5, { signal: controller.signal });
	expect(run.outcome).toEqual({ kind: "cancelled-before-start" });
	expect(await Bun.file(marker).exists()).toBe(false);
});

test("fixed script arguments pass through verbatim", async () => {
	const run = await runEciHook(script("arguments.sh", `printf '%s\\n' "$@"\n`), {}, id, 5, { args: ["one value", "--literal"] });
	expect(run.outcome).toEqual(exited(0));
	expect(run.stdout).toBe("one value\n--literal\n");
});

test("cancellation retires a running hook group before returning", async () => {
	const controller = new AbortController();
	const pidFile = join(dir, "cancel-child.pid");
	const hook = script("cancel.sh", `sleep 30 &\necho $! > '${pidFile}.tmp'\nmv '${pidFile}.tmp' '${pidFile}'\nwait\n`);
	const watcher = watch(dir);
	const changes = on(watcher, "change");
	try {
		const runPromise = runEciHook(hook, {}, id, 20, { cleanup: "owned-group", signal: controller.signal });
		while (!(await Bun.file(pidFile).exists())) await changes.next();
		controller.abort();
		const run = await runPromise;
		expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "cancel" }, cleanup: "graceful" });
		expectProcessGone(pidFile);
	} finally {
		watcher.close();
	}
}, 15000);

test("ECI_BASH selects the shell", async () => {
	const marker = join(dir, "marker");
	const realBash = Bun.which("bash", { PATH: savedPath ?? "" }) ?? "";
	const wrapper = script("mybash", `#!${realBash}\n: > ${marker}\nexec ${realBash} "$@"\n`);
	process.env.ECI_BASH = wrapper;
	const run = await runEciHook(script("ok.sh", "exit 0\n"), {}, id, 10);
	expect(run.outcome).toEqual(exited(0));
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
	expect(run.outcome).toEqual({ kind: "spawn-error", message: "ECI_BASH is not an absolute path: relative-bash" });
	expect(preToolVerdict(run, "t")?.block).toBe(true);
});

test("a non-executable ECI_BASH is a spawn error, not a hook exit", async () => {
	const shell = join(dir, "not-executable");
	writeFileSync(shell, "#!/bin/sh\n", { mode: 0o600 });
	process.env.ECI_BASH = shell;
	const run = await runEciHook(script("unused.sh", "exit 0\n"), {}, id, 5);
	expect(run.outcome.kind).toBe("spawn-error");
	expect(run.stdout).toBe("");
	expect(run.stderr).toBe("");
	expect(preToolVerdict(run, "edit")?.reason).toContain("could not run");
	expect(bashPreToolVerdict(run)?.reason).not.toContain(shell);
});
