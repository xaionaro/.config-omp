// Native ECI hook runner: runs ECI hook scripts from ~/.omp/agent/eci/hooks on behalf of omp
// hook factories. Not discovered as a factory (lives outside pre/ and post/).
import { spawn } from "node:child_process";
import { closeSync, existsSync, openSync, readSync } from "node:fs";
import { constants as osConstants, userInfo } from "node:os";
import { isAbsolute, join } from "node:path";

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

export interface HookRun {
	ok: boolean;
	code: number;
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

/** Seconds between the deadline's SIGTERM and the SIGKILL that follows it. */
const KILL_GRACE_SEC = 5;
/** Seconds after the SIGKILL before output still held open by an escaped process is abandoned. */
const DRAIN_SEC = 2;

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

/** Signals every process in the hook's process group; false when none is left. */
function signalGroup(pid: number, signal: NodeJS.Signals | 0): boolean {
	try {
		process.kill(-pid, signal);
		return true;
	} catch {
		return false;
	}
}

/**
 * Spawns the hook script as `bash script` in its own process group, with the JSON
 * payload on stdin and a deadline enforced here, so no external `timeout` is needed.
 * Like coreutils timeout, the deadline sends SIGTERM to the whole group and, when any
 * member is still alive KILL_GRACE_SEC later, SIGKILL; the run then reports 124
 * (stopped by SIGTERM) or 137 (needed SIGKILL). Output still held open DRAIN_SEC after
 * the SIGKILL (a process that left the group) is abandoned. A process ended by a signal
 * outside the deadline reports 128 plus the signal number. Any spawn or
 * bash-resolution error returns code -1 (fail-closed for PreToolUse).
 */
export async function runEciHook(
	script: string,
	stdin: Record<string, unknown>,
	id: EciIdentity,
	timeoutSec: number,
): Promise<HookRun> {
	if (!isAbsolute(script)) return { ok: false, code: -1, stdout: "", stderr: "ECI hook path is not absolute" };
	const env: Record<string, string> = {};
	for (const [k, v] of Object.entries(process.env)) if (v !== undefined) env[k] = v;
	env.ECI_SESSION_ID = id.sessionId;
	env.ECI_THREAD_ID = id.sessionId;
	env.ECI_HOOK_IS_SUBAGENT = id.isSubagent ? "1" : "0";
	const resolved = resolveHookBash(env);
	if ("error" in resolved) return { ok: false, code: -1, stdout: "", stderr: resolved.error };
	const { promise, resolve } = Promise.withResolvers<HookRun>();
	const stdout: Buffer[] = [];
	const stderr: Buffer[] = [];
	const timers: Timer[] = [];
	let timedOut = false;
	let killed = false;
	let exitCode: number | undefined;
	const child = spawn(resolved.bash, [script], {
		cwd: existsSync(id.cwd) ? id.cwd : undefined,
		env,
		stdio: ["pipe", "pipe", "pipe"],
		detached: true,
	});
	const finish = (run: HookRun): void => {
		for (const timer of timers) clearTimeout(timer);
		child.stdout?.destroy();
		child.stderr?.destroy();
		resolve(run);
	};
	const result = (): HookRun => {
		const code = killed ? KILL_EXIT : timedOut ? TIMEOUT_EXIT : (exitCode ?? -1);
		return { ok: code === 0, code, stdout: Buffer.concat(stdout).toString(), stderr: Buffer.concat(stderr).toString() };
	};
	child.on("error", err => finish({ ok: false, code: -1, stdout: "", stderr: String(err) }));
	child.stdout?.on("data", (chunk: Buffer) => stdout.push(chunk));
	child.stderr?.on("data", (chunk: Buffer) => stderr.push(chunk));
	child.on("exit", (code, signal) => {
		exitCode = code ?? (signal ? 128 + (osConstants.signals[signal] ?? 0) : -1);
	});
	child.on("close", () => finish(result()));
	child.stdin?.on("error", () => {});
	child.stdin?.end(JSON.stringify(stdin));
	const pid = child.pid;
	if (pid !== undefined) {
		timers.push(
			setTimeout(() => {
				timedOut = true;
				signalGroup(pid, "SIGTERM");
				timers.push(
					setTimeout(() => {
						if (signalGroup(pid, 0)) {
							killed = true;
							signalGroup(pid, "SIGKILL");
						}
						timers.push(setTimeout(() => finish(result()), DRAIN_SEC * 1000));
					}, KILL_GRACE_SEC * 1000),
				);
			}, timeoutSec * 1000),
		);
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
		reason: `ECI hook ${label} ${why}; blocking (fail-closed).${label === "validate-bash" ? " Inspect the hook stderr and $HOME/.omp/agent/eci/hooks/validate-bash.sh, repair the reported runtime or output failure, then retry the same command." : ""}`,
	});
	if (run.code === -1) return fail(`could not run (${run.stderr.trim()})`);
	if (run.code === TIMEOUT_EXIT || run.code === KILL_EXIT) return fail("timed out");
	if (!run.ok) return fail(`exited ${run.code}${run.stderr.trim() ? `: ${run.stderr.trim()}` : ""}`);
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

/** Advisory translation (SessionStart / UserPromptSubmit). Never throws; failures yield undefined. */
export function advisoryContext(run: HookRun, hookEventName: string): string | undefined {
	if (!run.ok) return undefined;
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
