// Native ECI hook runner: runs ECI hook scripts from ~/.omp/agent/eci/hooks on behalf of omp
// hook factories. Not discovered as a factory (lives outside pre/ and post/).
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

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

export const eciHooksDir = (): string => join(process.env.HOME ?? homedir(), ".omp", "agent", "eci", "hooks");

const TIMEOUT_EXIT = 124;
const KILL_EXIT = 137;

const subagentCache = new Map<string, boolean>();

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Subagent detection: a transcript containing a `session_init` record marks a subagent.
 * [INFERENCE: exact record location of `agent`; no omp transcript currently contains a
 * session_init record.]
 */
function detectSubagent(transcriptPath: string | undefined): boolean {
	if (!transcriptPath) return false;
	const cached = subagentCache.get(transcriptPath);
	if (cached !== undefined) return cached;
	let text: string;
	try {
		text = readFileSync(transcriptPath, "utf8");
	} catch {
		return false;
	}
	let found = false;
	for (const line of text.split("\n")) {
		if (!line.includes("session_init")) continue;
		let record: unknown;
		try {
			record = JSON.parse(line);
		} catch {
			continue;
		}
		if (!isRecord(record)) continue;
		if (record.type !== "session_init" && record.customType !== "session_init") continue;
		found = true;
		break;
	}
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

/**
 * Spawns the hook script with Bun.spawn: explicit argv, JSON piped to stdin, and a deadline.
 * The `timeout` binary enforces the deadline (exit 124, or 137 after kill-after); a JS-side
 * kill timer is a backstop. Any spawn error returns code -1 (fail-closed for PreToolUse).
 */
export async function runEciHook(
	script: string,
	stdin: Record<string, unknown>,
	id: EciIdentity,
	timeoutSec: number,
): Promise<HookRun> {
	const env: Record<string, string> = {};
	for (const [k, v] of Object.entries(process.env)) if (v !== undefined) env[k] = v;
	env.ECI_SESSION_ID = id.sessionId;
	env.ECI_THREAD_ID = id.sessionId;
	env.ECI_HOOK_IS_SUBAGENT = id.isSubagent ? "1" : "0";
	try {
		const proc = Bun.spawn(
			["timeout", "--kill-after=5", String(timeoutSec), "bash", script],
			{
				cwd: existsSync(id.cwd) ? id.cwd : undefined,
				env,
				stdin: "pipe",
				stdout: "pipe",
				stderr: "pipe",
			},
		);
		const backstop = setTimeout(() => proc.kill("SIGKILL"), (timeoutSec + 10) * 1000);
		proc.stdin.write(JSON.stringify(stdin));
		await proc.stdin.end();
		const [stdout, stderr, code] = await Promise.all([
			new Response(proc.stdout).text(),
			new Response(proc.stderr).text(),
			proc.exited,
		]);
		clearTimeout(backstop);
		return { ok: code === 0, code, stdout, stderr };
	} catch (err) {
		return { ok: false, code: -1, stdout: "", stderr: String(err) };
	}
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
