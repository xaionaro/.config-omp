// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-hook-runner.test.ts
// (omp is a Bun build; no separate bun is needed). Not a hook factory: lib/ is not discovered.
import { afterEach, beforeEach, expect, test } from "bun:test";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, watch, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { on } from "node:events";
import { join } from "node:path";
import { BASH_GATE_DEADLINE_SEC, DRAIN_SEC, KILL_GRACE_SEC, advisoryContext, basePayload, bashGateDecision, bashHostCwd, bashPreToolVerdict, identityFor, preToolVerdict, prefixMarksSubagent, resolveHookBash, runEciHook } from "./eci-hook-runner";
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
		const decision = bashGateDecision(run);
		expect(decision.kind).toBe("unavailable");
		expect(JSON.stringify(decision)).not.toContain("eci_bash_gate_repair");
		expect(JSON.stringify(decision)).not.toContain("secret");
		const verdict = bashPreToolVerdict(run);
		expect(verdict?.block).toBe(true);
		expect(verdict?.reason).not.toContain(": eci-bash-gate-repair");
		expect(verdict?.reason).not.toContain("secret");
		expect(verdict?.reason).toContain("Repair: ");
		expect(verdict?.reason).toContain("validate-bash.sh");
	}
	expect(bashGateDecision({ outcome: exited(0), stdout: denial("bounded route"), stderr: "" })).toEqual({ kind: "deny", reason: "bounded route" });
	expect(bashGateDecision({ outcome: exited(0), stdout: "", stderr: "" })).toEqual({ kind: "allow" });
	expect(bashPreToolVerdict({ outcome: exited(0), stdout: denial("bounded route"), stderr: "" })?.reason).toBe("bounded route");
	expect(bashPreToolVerdict({ outcome: exited(0), stdout: "", stderr: "" })).toBeUndefined();
	expect(BASH_GATE_DEADLINE_SEC + KILL_GRACE_SEC + DRAIN_SEC).toBeLessThan(30);
});

test("native denial guidance exposes only recognized binary reasons", () => {
	const line = (reason: string) => `[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan is unavailable or unverified (${reason}); private trailing data`;
	const run = (reason: string) => ({ outcome: exited(2), stdout: "", stderr: line(reason) });
	expect(bashPreToolVerdict(run("has no sha256 receipt"))?.reason).toContain(": eci-bash-gate-repair");
	const unsafe = bashPreToolVerdict(run("is group- or other-writable"))?.reason ?? "";
	expect(unsafe).toContain("eci-command-plan");
	expect(unsafe).toContain("is group- or other-writable");
	expect(unsafe).not.toContain(": eci-bash-gate-repair");
	expect(unsafe).not.toContain("private trailing data");
	expect(unsafe).toContain("$HOME/.omp/agent/eci/bin, its parent directories and its binaries owned by you or root");
	expect(unsafe).not.toContain("restore or fix the broken file");
	for (const [reason, step] of [
		["temporary directory is missing", "set TMPDIR to a private directory you own"],
		["temporary directory is not trusted: owned by another user, or group/other-writable without a root-owned sticky bit", "set TMPDIR to a private directory you own"],
		["private copy directory cannot be created", "set TMPDIR to a private directory you own"],
		["host CPU type is not supported", "report the host as unsupported"],
		["not an allowed artifact name or HOME is not absolute", "set HOME to the account's absolute home directory"],
		["bin directory is missing", "build-native-binaries.sh"],
		["is not executable", "chmod u+x"],
		["cannot be hashed", "install shasum or sha256sum"],
	]) {
		const message = bashPreToolVerdict(run(reason))?.reason ?? "";
		expect(message).toContain(reason);
		expect(message).not.toContain(": eci-bash-gate-repair");
		expect(message).toContain(step);
		expect(message).not.toContain("restore or fix the broken file");
	}
	expect(bashPreToolVerdict(run("private attacker data"))?.reason).not.toContain("private attacker data");
});

test("recognized native failures offer fixed repair guidance", () => {
	for (const reason of ["is missing or a symlink", "is not an ELF binary for this host", "has no sha256 receipt", "has a malformed sha256 receipt", "sha256 does not match its receipt"]) {
		const stderr = `[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan is unavailable or unverified (${reason}); provision it`;
		expect(bashPreToolVerdict({ outcome: exited(2), stdout: "", stderr })?.reason).toContain(": eci-bash-gate-repair");
	}
});

test("Bash factory exceptions block with conditional repair guidance without exposing the exception", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; }, zod: { object: () => ({}) }, registerTool: () => {} } as never);
	expect(handler).toBeDefined();
	const event = { toolName: "bash", get input(): never { throw new Error("sensitive-value"); } };
	const verdict = await handler!(event, {});
	expect(verdict).toMatchObject({ block: true });
	expect(JSON.stringify(verdict)).not.toContain("eci_bash_gate_repair");
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
	for (const command of [
		`cd ""@/d && pwd`, `cd '@'/d && pwd`, String.raw`cd \@/d && pwd`,
		`cd "@/d" && pwd`, `cd ":"/d && pwd`, "cd /a/link/.. && pwd",
		"cd /a/../b; pwd", `cd "/p q" && pwd`, "cd ~/x && pwd",
	]) expect(bashHostCwd(undefined, command, session, noCDPath)).toBeUndefined();
	for (const command of ["cd /a/b && pwd", "cd /a/./b/ && pwd", "cd ../s && pwd", "cd - && pwd", " cd /x && pwd", "cdx /y"]) {
		expect(bashHostCwd(undefined, command, session, noCDPath)).toBe(session);
	}
	expect(bashHostCwd(undefined, "cd sub/child && pwd", session, { CDPATH: "/other" })).toBeUndefined();
});

test("Bash callback uses the session manager identity without inventing a transcript", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; }, zod: { object: () => ({}) }, registerTool: () => {} } as never);
	const savedHome = process.env.HOME;
	const hooks = join(dir, ".omp", "agent", "eci", "hooks");
	mkdirSync(hooks, { recursive: true });
	writeFileSync(join(hooks, "validate-bash.sh"), `pwd > '${join(dir, "hook-pwd")}'\nprintf '%s' "$ECI_HOOK_IS_SUBAGENT" > '${join(dir, "hook-role")}'\ncat > '${join(dir, "callback.json")}'\n`);
	try {
		process.env.HOME = dir;
		const verdict = await handler!({ toolName: "bash", input: { command: "true" } }, {
			cwd: dir,
			agent: { kind: "main" },
			sessionManager: { getSessionId: () => "native-session", getSessionFile: () => undefined },
		});
		expect(verdict).toBeUndefined();
		const payload = await Bun.file(join(dir, "callback.json")).json();
		expect(payload.session_id).toBe("native-session");
		expect(payload.cwd).toBe(dir);
		expect(payload.omp_agent_kind).toBe("main");
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
		const pendingTranscript = join(dir, "pending.jsonl");
		expect(await handler!({ toolName: "bash", input: { command: "true" } }, {
			cwd: dir,
			agent: { kind: "main" },
			sessionManager: { getSessionId: () => "native-session", getSessionFile: () => pendingTranscript },
		})).toBeUndefined();
		expect(readFileSync(join(dir, "hook-role"), "utf8")).toBe("0");
		const pendingPayload = await Bun.file(join(dir, "callback.json")).json();
		expect(pendingPayload.omp_agent_kind).toBe("main");
		expect(pendingPayload.transcript_path).toBe(pendingTranscript);
	} finally {
		if (savedHome === undefined) delete process.env.HOME;
		else process.env.HOME = savedHome;
	}
});

test("Bash recovery rewrites only exact requests with a native gate denial", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	eciBashGate({ on: (_name: string, callback: typeof handler) => { handler = callback; }, zod: { object: () => ({}) }, registerTool: () => {} } as never);
	const savedHome = process.env.HOME;
	const home = join(dir, "home 'quoted'");
	const hooks = join(home, ".omp/agent/eci/hooks");
	const script = join(hooks, "validate-bash.sh");
	mkdirSync(hooks, { recursive: true });
	const ctx = { cwd: dir, sessionManager: { getSessionId: () => "s", getSessionFile: () => undefined } };
	const call = (command: string) => handler!({ toolName: "bash", input: { command, cwd: "/wrong", timeout: 1, pty: true, async: true, name: "unsafe" } }, ctx);
	try {
		process.env.HOME = home;
		writeFileSync(script, "exit 2\n");
		expect(await call(": eci-bash-gate-repair")).toMatchObject({ block: true });
		for (const name of process.platform === "linux" ? ["eci-command-plan", "eci-verexec"] : ["eci-command-plan"]) {
			writeFileSync(script, `printf '%s\\n' '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary ${name} is unavailable or unverified (has no sha256 receipt); provision it' >&2\nexit 2\n`);
			for (const command of [": eci-bash-gate-repair; echo extra", ": eci-bash-gate-repair\ntrue", ":  eci-bash-gate-repair", "true"]) {
				expect(await call(command)).toMatchObject({ block: true });
			}
			const result = await call("  : eci-bash-gate-repair  ") as { input: Record<string, unknown> };
			expect(Object.keys(result.input).sort()).toEqual(["command", "cwd", "timeout"]);
			expect(result.input.cwd).toBe(hooks);
			expect(result.input.timeout).toBe(600);
			const quote = (s: string) => `'${s.replaceAll("'", "'\\''")}'`;
			const bash = resolveHookBash();
			if (!("bash" in bash)) throw new Error(bash.error);
			expect(result.input.command).toBe(`HOME=${quote(home)} ${quote(bash.bash)} ${quote(join(hooks, "build-native-binaries.sh"))} --gate-repair`);
		}
		writeFileSync(script, `printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"denied"}}'\n`);
		expect(await call(": eci-bash-gate-repair")).toEqual({ block: true, reason: "denied" });
		writeFileSync(script, "exit 0\n");
		expect(await call(": eci-bash-gate-repair")).toBeUndefined();
		writeFileSync(script, `echo malformed; echo '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan is unavailable or unverified (has no sha256 receipt); provision it' >&2; exit 2\n`);
		expect(await call(": eci-bash-gate-repair")).toMatchObject({ block: true });
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

test("deadline after leader exit still blocks inherited output", async () => {
	const pidFile = join(dir, "inherited-deadline.pid");
	const envelope = JSON.stringify({ hookSpecificOutput: { hookEventName: "SessionStart", additionalContext: "partial" } });
	const hook = script("inherited-deadline.sh", `printf '%s\\n' '${envelope}'\nsleep 30 &\necho $! > '${pidFile}'\nexit 0\n`);
	const run = await runEciHook(hook, {}, id, 1);
	expect(run.outcome).toEqual(deadline("graceful"));
	expect(preToolVerdict(run, "edit")?.reason).toContain("timed out");
	expect(bashPreToolVerdict(run)?.reason).toContain("deadline");
	expect(advisoryContext(run, "SessionStart")).toBeUndefined();
	expectProcessGone(pidFile);
}, 15000);

test("abort after leader exit while output remains open is cancellation", async () => {
	const controller = new AbortController();
	const ready = join(dir, "leader-exited");
	const pidFile = join(dir, "inherited-cancel.pid");
	const hook = script("inherited-cancel.sh", `parent=$$\nbash -c 'while kill -0 "$1" 2>/dev/null; do :; done; echo $$ > "$2"; exec sleep 30' _ "$parent" '${ready}' &\necho $! > '${pidFile}'\nexit 0\n`);
	const watcher = watch(dir);
	const changes = on(watcher, "change");
	try {
		const runPromise = runEciHook(hook, {}, id, 20, { cleanup: "owned-group", signal: controller.signal });
		while (!(await Bun.file(ready).exists())) await changes.next();
		controller.abort();
		const run = await runPromise;
		expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "cancel" }, cleanup: "graceful" });
		expect(bashPreToolVerdict(run)?.reason).toContain("cancel");
		expectProcessGone(pidFile);
	} finally {
		watcher.close();
	}
}, 15000);

test("abort after output closes does not change an exit during cleanup", async () => {
	const controller = new AbortController();
	const pidFile = join(dir, "post-close.pid");
	const ready = join(dir, "post-close.ready");
	const hook = script("post-close.sh", `bash -c 'trap "" TERM; echo ready > "$1"; exec sleep 30' _ '${ready}' </dev/null >/dev/null 2>&1 &\necho $! > '${pidFile}'\nwhile [ ! -f '${ready}' ]; do sleep 0.01; done\nexit 0\n`);
	const originalKill = process.kill;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0 && signal === "SIGTERM") controller.abort();
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const run = await runEciHook(hook, {}, id, 10, { cleanup: "owned-group", signal: controller.signal });
		expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "exit", code: 0 }, cleanup: "forced" });
		expectProcessGone(pidFile);
	} finally {
		process.kill = originalKill;
	}
}, 15000);

test("a deadline preceding an abort remains the stop cause and sends TERM once", async () => {
	const controller = new AbortController();
	const originalKill = process.kill;
	let terms = 0;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0 && signal === "SIGTERM") {
			terms++;
			controller.abort();
		}
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const run = await runEciHook(script("deadline-first.sh", "exec sleep 30\n"), {}, id, 1, { signal: controller.signal });
		expect(run.outcome).toEqual(deadline("graceful"));
		expect(terms).toBe(1);
	} finally {
		process.kill = originalKill;
	}
}, 15000);

test("an abort preceding a deadline remains the stop cause and sends TERM once", async () => {
	const controller = new AbortController();
	const pidFile = join(dir, "cancel-first.pid");
	const hook = script("cancel-first.sh", `trap '' TERM\necho $$ > '${pidFile}.tmp'\nmv '${pidFile}.tmp' '${pidFile}'\nexec sleep 30\n`);
	const watcher = watch(dir);
	const changes = on(watcher, "change");
	const originalKill = process.kill;
	let terms = 0;
	process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
		if (pid < 0 && signal === "SIGTERM") terms++;
		return originalKill(pid, signal);
	}) as typeof process.kill;
	try {
		const runPromise = runEciHook(hook, {}, id, 1, { signal: controller.signal });
		while (!(await Bun.file(pidFile).exists())) await changes.next();
		controller.abort();
		const run = await runPromise;
		expect(run.outcome).toEqual({ kind: "ran", stop: { kind: "cancel" }, cleanup: "forced" });
		expect(terms).toBe(1);
		expectProcessGone(pidFile);
	} finally {
		process.kill = originalKill;
		watcher.close();
	}
}, 15000);

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

test("Bash main identity defers transcript conflicts to the Go classifier", async () => {
	const transcript = join(dir, "pending.jsonl");
	const ctx = { cwd: dir, agent: { kind: "main" }, sessionManager: { getSessionFile: () => transcript } };
	const pending = identityFor(ctx, { useHostAgentKind: true });
	expect(pending.isSubagent).toBe(false);
	expect(basePayload(pending, "PreToolUse").omp_agent_kind).toBe("main");
	const run = await runEciHook(script("role.sh", 'printf "%s" "$ECI_HOOK_IS_SUBAGENT"\n'), {}, pending, 5);
	expect(run.stdout).toBe("0");
	writeFileSync(transcript, '{"type":"session","parentSession":"parent"}\n');
	expect(identityFor(ctx, { useHostAgentKind: true }).isSubagent).toBe(false);
	expect(identityFor({ ...ctx, sessionManager: { getSessionFile: () => dir } }, { useHostAgentKind: true }).isSubagent).toBe(false);
	expect(identityFor(ctx).isSubagent).toBe(true);
});

test("missing invalid and throwing agent identities preserve fail-closed detection", () => {
	const ctx = { cwd: dir, sessionManager: { getSessionFile: () => join(dir, "missing.jsonl") } };
	for (const agent of [undefined, null, {}, { kind: "Main" }, { kind: 1 }, { get kind() { throw new Error("unavailable"); } }]) {
		const identity = identityFor({ ...ctx, agent } as never, { useHostAgentKind: true });
		expect(identity.isSubagent).toBe(true);
		expect(basePayload(identity, "PreToolUse")).not.toHaveProperty("omp_agent_kind");
	}
	const identity = identityFor({ ...ctx, get agent() { throw new Error("unavailable"); } }, { useHostAgentKind: true });
	expect(identity.isSubagent).toBe(true);
	expect(basePayload(identity, "PreToolUse")).not.toHaveProperty("omp_agent_kind");
	expect(identityFor({ ...ctx, agent: { kind: "sub" } } as never, { useHostAgentKind: true }).isSubagent).toBe(true);
});

test("shared identity does not cache unreadable transcripts while Bash uses host kind", () => {
	if (process.getuid?.() === 0) return;
	const transcript = join(dir, "unreadable.jsonl");
	writeFileSync(transcript, '{"type":"session"}\n', { mode: 0o000 });
	const ctx = { cwd: dir, agent: { kind: "main" }, sessionManager: { getSessionFile: () => transcript } };
	try {
		expect(identityFor(ctx).isSubagent).toBe(true);
		expect(identityFor(ctx, { useHostAgentKind: true }).isSubagent).toBe(false);
	} finally {
		chmodSync(transcript, 0o600);
	}
	expect(identityFor(ctx).isSubagent).toBe(false);
	writeFileSync(transcript, '{"type":"session","parentSession":"parent"}\n');
	expect(identityFor(ctx).isSubagent).toBe(false);
});

test("existing resumed main advisor fork and child transcripts retain prefix classification", () => {
	for (const kind of ["main", "sub"]) {
		for (const [name, contents, worker] of [
			["main", '{"type":"session"}\n', false],
			["fork", '{"type":"session","parentSession":"parent"}\n', true],
			["malformed", "not json\n", true],
		] as const) {
			const transcript = join(dir, `${kind}-${name}.jsonl`);
			writeFileSync(transcript, contents);
			const ctx = { cwd: dir, agent: { kind }, sessionManager: { getSessionFile: () => transcript } };
			expect(identityFor(ctx).isSubagent).toBe(worker);
			expect(basePayload(identityFor(ctx), "PreToolUse").omp_agent_kind).toBe(kind);
			expect(identityFor(ctx, { useHostAgentKind: true }).isSubagent).toBe(kind === "sub");
		}
	}
	const noSession = { cwd: dir, agent: { kind: "main" }, sessionManager: { getSessionFile: () => undefined } };
	expect(identityFor(noSession).isSubagent).toBe(false);
	expect(identityFor(noSession, { useHostAgentKind: true }).isSubagent).toBe(false);
	expect(identityFor({ ...noSession, agent: { kind: "sub" } }, { useHostAgentKind: true }).isSubagent).toBe(true);
});

test("shared identity consumers retain missing-transcript fail-closed behavior", () => {
	const transcript = join(dir, "missing.jsonl");
	const ctx = { cwd: dir, agent: { kind: "main" }, sessionManager: { getSessionFile: () => transcript } };
	expect(identityFor(ctx).isSubagent).toBe(true);
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
