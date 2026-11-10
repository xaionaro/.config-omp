// Native ECI hook runner: runs ECI hook scripts from ~/.omp/agent/eci/hooks on behalf of omp
// hook factories. Not discovered as a factory (lives outside pre/ and post/).
import { spawn } from "node:child_process";
import { closeSync, existsSync, openSync, readSync } from "node:fs";
import { constants as osConstants, homedir, userInfo } from "node:os";
import { isAbsolute, join, resolve } from "node:path";

/** Structural subset of omp's ExtensionContext used by the factories. */
export interface EciSessionContext {
	cwd: string;
	sessionManager: { getSessionFile(): string | undefined };
}

export interface EciIdentity {
	sessionId: string;
	transcriptPath?: string;
	cwd: string;
	isSubagent: boolean;
}

export type HookStop = { kind: "exit"; code: number } | { kind: "deadline" } | { kind: "cancel" };
export type HookOutcome =
	| { kind: "cancelled-before-start" }
	| { kind: "spawn-error"; message: string }
	| { kind: "ran"; stop: HookStop; cleanup: "graceful" | "forced" | "unconfirmed" };

export interface HookRun {
	outcome: HookOutcome;
	stdout: string;
	stderr: string;
}

export const eciHooksDir = (): string => {
	const home = process.env.HOME;
	const base = home && isAbsolute(home) ? home : userInfo().homedir;
	return join(base, ".omp", "agent", "eci", "hooks");
};

const TIMEOUT_EXIT = 124;
const KILL_EXIT = 137;

const subagentCache = new Map<string, boolean>();

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The transcript prefix rule of the Go classifier (hook_context.go hookTranscriptPrefix). */
const PREFIX_BYTES = 64 << 10;
const PREFIX_LINES = 16;
const TITLE_SLOT_BYTES = 256;

/**
 * Reports whether the classified records of a transcript prefix mark it as not
 * verifiably main, which forces the worker role (ECI_HOOK_IS_SUBAGENT=1). The rule
 * matches the Go classifier: only the complete lines among the first 64 KiB and 16
 * lines count, a torn line-zero 256-byte title slot is skipped, and any other record
 * that is not JSON, a missing session header, a parentSession key or a session_init
 * marker (`type` or `customType`) marks the transcript.
 *
 * Example: a title line, then {"type":"session","id":"x"} with no other signal is main (false).
 */
export function prefixMarksSubagent(prefix: Uint8Array): boolean {
	let start = 0;
	let header = false;
	const decoder = new TextDecoder();
	for (let index = 0; index < PREFIX_LINES; index++) {
		const end = prefix.indexOf(10, start);
		if (end < 0) break;
		const line = prefix.subarray(start, end);
		start = end + 1;
		let record: unknown;
		try {
			record = JSON.parse(decoder.decode(line));
		} catch {
			if (index === 0 && line.length + 1 === TITLE_SLOT_BYTES) continue;
			return true;
		}
		if (!isRecord(record)) continue;
		if (record.type === "session") header = true;
		if ("parentSession" in record) return true;
		if (record.type === "session_init" || record.customType === "session_init") return true;
	}
	return !header;
}

/**
 * Subagent detection from the bounded transcript prefix (prefixMarksSubagent). A
 * transcript that cannot be read marks the call (fail-closed) and is not cached, so a
 * transient error does not pin the session.
 */
function detectSubagent(transcriptPath: string | undefined): boolean {
	if (!transcriptPath) return false;
	const cached = subagentCache.get(transcriptPath);
	if (cached !== undefined) return cached;
	const prefix = new Uint8Array(PREFIX_BYTES);
	let length = 0;
	try {
		const fd = openSync(transcriptPath, "r");
		try {
			for (let n = 1; n > 0 && length < PREFIX_BYTES; length += n) {
				n = readSync(fd, prefix, length, PREFIX_BYTES - length, length);
			}
		} finally {
			closeSync(fd);
		}
	} catch {
		return true;
	}
	const found = prefixMarksSubagent(prefix.subarray(0, length));
	subagentCache.set(transcriptPath, found);
	return found;
}

export function identityFor(ctx: EciSessionContext): EciIdentity {
	const transcriptPath = ctx.sessionManager.getSessionFile() ?? undefined;
	return {
		sessionId: process.env.PI_SESSION_ID ?? "",
		transcriptPath,
		cwd: ctx.cwd,
		isSubagent: detectSubagent(transcriptPath),
	};
}

/** Hook-shaped stdin common fields. */
export function basePayload(id: EciIdentity, hookEventName: string): Record<string, unknown> {
	return {
		session_id: id.sessionId,
		...(id.transcriptPath ? { transcript_path: id.transcriptPath } : {}),
		cwd: id.cwd,
		hook_event_name: hookEventName,
	};
}

const HOST_PATH_SPACE = /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/;

/** Returns omp 18.18.6's Bash cwd only for spellings resolved exactly here.
 * Recheck host resolution when upgrading omp; undefined means unknown.
 */
export function bashHostCwd(requested: unknown, command: string, sessionCwd: string, env = process.env): string | undefined {
	if (requested === undefined || requested === "") {
		const word = /^cd[ \t]+([^\n]*?)[ \t]*(?:&&|;)/.exec(command)?.[1];
		if (word !== undefined && (word.includes(":") || /^['"]?@/.test(word) || HOST_PATH_SPACE.test(word) || !!env.CDPATH)) return undefined;
		return sessionCwd;
	}
	if (typeof requested !== "string" || /[:\\]/.test(requested) || requested.startsWith("@") || HOST_PATH_SPACE.test(requested)) return undefined;
	let path = requested;
	if (path === "~") path = homedir();
	else if (path.startsWith("~/")) path = homedir() + path.slice(1);
	else if (path.startsWith("~")) return undefined;
	if (/^\/+$/.test(path)) return sessionCwd;
	if (!isAbsolute(path)) return resolve(sessionCwd, path);
	return path.split("/").includes("..") ? undefined : resolve(path);
}

/** Seconds between group SIGTERM and a SIGKILL if the group remains. */
export const KILL_GRACE_SEC = 5;
/** Seconds after SIGKILL before abandoning held output and checking cleanup. */
export const DRAIN_SEC = 2;
/** Bash gate deadline leaves room for cleanup within omp's default 30-second budget. */
export const BASH_GATE_DEADLINE_SEC = 20;

/**
 * The bash that runs ECI hooks: ECI_BASH when set (it must be an absolute path to an
 * executable), else the first bash on PATH. Too-old versions are reported by the hook's
 * own bash guard (hooks/lib/eci-bash-guard.sh). Returns an error string when no bash is usable.
 */
export function resolveHookBash(env: Record<string, string | undefined> = process.env): { bash: string } | { error: string } {
	const override = env.ECI_BASH;
	if (override !== undefined && override !== "") {
		if (!isAbsolute(override)) return { error: `ECI_BASH is not an absolute path: ${override}` };
		if (!existsSync(override)) return { error: `ECI_BASH does not exist: ${override}` };
		return { bash: override };
	}
	const found = Bun.which("bash", { PATH: env.PATH ?? "" });
	return found ? { bash: found } : { error: "bash not found on PATH" };
}

/** Signals the hook process group, distinguishing absence from an unknown error. */
function signalGroup(pid: number, signal: NodeJS.Signals | 0): "present" | "absent" | "unknown" {
	try {
		process.kill(-pid, signal);
		return "present";
	} catch (err) {
		return isRecord(err) && err.code === "ESRCH" ? "absent" : "unknown";
	}
}

/** Selects whether ordinary exit also retires the hook's process group. */
export type HookCleanupPolicy = "deadline-only" | "owned-group";

/**
 * Runs the hook in its own process group. A deadline retires that group under
 * either policy; owned-group also retires members after an ordinary exit.
 * Unconfirmed cleanup takes precedence over deadline and exit outcomes. Group
 * cleanup cannot cover descendants that leave the group. Omp may stop waiting
 * before this runner finishes; the deadline and cleanup then continue.
 */
export async function runEciHook(
	script: string,
	stdin: Record<string, unknown>,
	id: EciIdentity,
	timeoutSec: number,
	options: { cleanup?: HookCleanupPolicy; signal?: AbortSignal; args?: readonly string[] } = {},
): Promise<HookRun> {
	if (options.signal?.aborted) return { outcome: { kind: "cancelled-before-start" }, stdout: "", stderr: "" };
	const retireOnExit = options.cleanup === "owned-group";
	if (!isAbsolute(script)) return { outcome: { kind: "spawn-error", message: "ECI hook path is not absolute" }, stdout: "", stderr: "" };
	const env: Record<string, string> = {};
	for (const [k, v] of Object.entries(process.env)) if (v !== undefined) env[k] = v;
	env.ECI_SESSION_ID = id.sessionId;
	env.ECI_THREAD_ID = id.sessionId;
	env.ECI_HOOK_IS_SUBAGENT = id.isSubagent ? "1" : "0";
	const resolved = resolveHookBash(env);
	if ("error" in resolved) return { outcome: { kind: "spawn-error", message: resolved.error }, stdout: "", stderr: "" };
	const { promise, resolve } = Promise.withResolvers<HookRun>();
	const stdout: Buffer[] = [];
	const stderr: Buffer[] = [];
	const timers: Timer[] = [];
	let stop: HookStop | undefined;
	let killed = false;
	let closed = false;
	let cleanupUnconfirmed = false;
	let finished = false;
	let retiring = false;
	let deadlineTimer: Timer | undefined;
	let exitCode: number | undefined;
	const child = spawn(resolved.bash, [script, ...(options.args ?? [])], {
		cwd: existsSync(id.cwd) ? id.cwd : undefined,
		env,
		stdio: ["pipe", "pipe", "pipe"],
		detached: true,
	});
	const finish = (run: HookRun): void => {
		if (finished) return;
		finished = true;
		options.signal?.removeEventListener("abort", onAbort);
		for (const timer of timers) clearTimeout(timer);
		child.stdout?.destroy();
		child.stderr?.destroy();
		resolve(run);
	};
	const result = (): HookRun => {
		const outcome: HookOutcome = { kind: "ran", stop: stop ?? { kind: "exit", code: -1 }, cleanup: cleanupUnconfirmed ? "unconfirmed" : killed ? "forced" : "graceful" };
		return { outcome, stdout: Buffer.concat(stdout).toString(), stderr: Buffer.concat(stderr).toString() };
	};
	const retireGroup = (pid: number): void => {
		if (retiring) return;
		retiring = true;
		if (signalGroup(pid, "SIGTERM") === "unknown") cleanupUnconfirmed = true;
		timers.push(setTimeout(() => {
			const group = signalGroup(pid, 0);
			if (group !== "absent") {
				if (group === "unknown") cleanupUnconfirmed = true;
				killed = true;
				if (signalGroup(pid, "SIGKILL") === "unknown") cleanupUnconfirmed = true;
			}
			if (closed && !killed) {
				finish(result());
				return;
			}
			timers.push(setTimeout(() => {
				if (signalGroup(pid, 0) !== "absent") cleanupUnconfirmed = true;
				finish(result());
			}, DRAIN_SEC * 1000));
		}, KILL_GRACE_SEC * 1000));
	};
	child.on("error", err => finish({ outcome: { kind: "spawn-error", message: String(err) }, stdout: "", stderr: "" }));
	child.stdout?.on("data", (chunk: Buffer) => stdout.push(chunk));
	child.stderr?.on("data", (chunk: Buffer) => stderr.push(chunk));
	child.on("exit", (code, signal) => {
		exitCode = code ?? (signal ? 128 + (osConstants.signals[signal] ?? 0) : -1);
		stop ??= { kind: "exit", code: exitCode };
	});
	child.on("close", () => {
		if (finished) return;
		closed = true;
		clearTimeout(deadlineTimer);
		// Bun emits error then close without exit for a failed spawn.
		if (exitCode === undefined) {
			finish({ outcome: { kind: "spawn-error", message: "hook process did not start" }, stdout: "", stderr: "" });
			return;
		}
		if (retiring) {
			const group = child.pid === undefined ? "unknown" : signalGroup(child.pid, 0);
			if (group === "unknown") cleanupUnconfirmed = true;
			if (group === "absent") finish(result());
			return;
		}
		if ((stop?.kind !== "exit" || retireOnExit) && child.pid !== undefined) {
			const group = signalGroup(child.pid, 0);
			if (group === "unknown") cleanupUnconfirmed = true;
			if (group !== "absent") {
				retireGroup(child.pid);
				return;
			}
		}
		finish(result());
	});
	child.stdin?.on("error", () => {});
	child.stdin?.end(JSON.stringify(stdin));
	const pid = child.pid;
	const onAbort = (): void => {
		if (finished || pid === undefined) return;
		stop ??= { kind: "cancel" };
		retireGroup(pid);
	};
	if (pid !== undefined) {
		options.signal?.addEventListener("abort", onAbort, { once: true });
		deadlineTimer = setTimeout(() => {
			stop ??= { kind: "deadline" };
			retireGroup(pid);
		}, timeoutSec * 1000);
		timers.push(deadlineTimer);
	}
	return promise;
}

export type PreToolVerdict = { block: true; reason: string } | undefined;

/**
 * PreToolUse translation. Fail-closed: spawn error, timeout, non-zero exit, non-JSON or
 * unrecognized output all block. Empty stdout with exit 0 allows.
 * Deny → block with the script's permissionDecisionReason.
 */
export function preToolVerdict(run: HookRun, label: string): PreToolVerdict {
	const fail = (why: string): PreToolVerdict => ({
		block: true,
		reason: `ECI hook ${label} ${why}; blocking (fail-closed).`,
	});
	const outcome = run.outcome;
	if (outcome.kind === "spawn-error") return fail(`could not run (${outcome.message.trim()})`);
	if (outcome.kind === "cancelled-before-start") return fail("was cancelled");
	const { stop, cleanup } = outcome;
	if (stop.kind === "deadline") return fail("timed out");
	if (stop.kind === "cancel") return fail("was cancelled");
	if (cleanup === "unconfirmed") return fail(`exited ${stop.code} without confirmed process cleanup`);
	if (cleanup === "forced") return fail(`exited ${stop.code} and needed forced process cleanup`);
	// Preserve the Edit gate's historical coreutils timeout status handling.
	if (stop.code === TIMEOUT_EXIT || stop.code === KILL_EXIT) return fail("timed out");
	if (stop.code !== 0) return fail(`exited ${stop.code}${run.stderr.trim() ? `: ${run.stderr.trim()}` : ""}`);
	const out = run.stdout.trim();
	if (out === "") return undefined;
	let parsed: unknown;
	try {
		parsed = JSON.parse(out);
	} catch {
		return fail("returned non-JSON output");
	}
	const hso = isRecord(parsed) && isRecord(parsed.hookSpecificOutput) ? parsed.hookSpecificOutput : undefined;
	if (hso?.hookEventName !== "PreToolUse") return fail("returned unrecognized output");
	if (hso.permissionDecision === "allow") return undefined;
	if (hso.permissionDecision === "deny") {
		const reason = typeof hso.permissionDecisionReason === "string" ? hso.permissionDecisionReason : `denied by ECI hook ${label}`;
		return { block: true, reason };
	}
	return fail("returned unsupported permissionDecision");
}

/** Blocks an unchecked Bash call without exposing callback data. */
export function bashGateUnavailable(detail: string): PreToolVerdict {
	return { block: true, reason: `ECI Bash gate unavailable (${detail}); the call is blocked. If eci_bash_gate_repair is available (top-level or xd://eci_bash_gate_repair), call it, then retry; if unavailable here or the gate stays unavailable, continue non-Bash work and report it to the parent.` };
}

/** Accepts only a complete Bash gate decision; failures never expose hook output. */
export function bashPreToolVerdict(run: HookRun): PreToolVerdict {
	// A resisting or unconfirmed group means a complete decision cannot be trusted.
	const outcome = run.outcome;
	if (outcome.kind === "spawn-error") return bashGateUnavailable("spawn");
	if (outcome.kind === "cancelled-before-start") return bashGateUnavailable("cancelled before start");
	const { stop, cleanup } = outcome;
	const stopped = stop.kind === "exit" ? `exit ${stop.code}` : stop.kind;
	if (cleanup === "unconfirmed") return bashGateUnavailable(`unconfirmed cleanup after ${stopped}`);
	if (cleanup === "forced") return bashGateUnavailable(`forced cleanup after ${stopped}`);
	if (stop.kind !== "exit") return bashGateUnavailable(stop.kind);
	if (stop.code !== 0) return bashGateUnavailable(`exit ${stop.code}`);
	const out = run.stdout.trim();
	if (out === "") return undefined;
	let parsed: unknown;
	try {
		parsed = JSON.parse(out);
	} catch {
		return bashGateUnavailable("invalid-json");
	}
	const hso = isRecord(parsed) && isRecord(parsed.hookSpecificOutput) ? parsed.hookSpecificOutput : undefined;
	if (hso?.hookEventName !== "PreToolUse") return bashGateUnavailable("invalid-output");
	if (hso.permissionDecision === "allow") return undefined;
	if (hso.permissionDecision !== "deny") return bashGateUnavailable("invalid-decision");
	if (typeof hso.permissionDecisionReason !== "string" || hso.permissionDecisionReason.trim() === "") {
		return bashGateUnavailable("invalid-denial");
	}
	return { block: true, reason: hso.permissionDecisionReason };
}

/** Advisory translation (SessionStart / UserPromptSubmit). Never throws; failures yield undefined. */
export function advisoryContext(run: HookRun, hookEventName: string): string | undefined {
	if (run.outcome.kind !== "ran" || run.outcome.stop.kind !== "exit" || run.outcome.stop.code !== 0 || run.outcome.cleanup !== "graceful") return undefined;
	const out = run.stdout.trim();
	if (out === "") return undefined;
	let parsed: unknown;
	try {
		parsed = JSON.parse(out);
	} catch {
		return undefined;
	}
	const hso = isRecord(parsed) && isRecord(parsed.hookSpecificOutput) ? parsed.hookSpecificOutput : undefined;
	if (hso?.hookEventName === hookEventName && typeof hso.additionalContext === "string" && hso.additionalContext.trim() !== "") {
		return hso.additionalContext;
	}
	return undefined;
}

export const scriptPath = (name: string): string => join(eciHooksDir(), name);
