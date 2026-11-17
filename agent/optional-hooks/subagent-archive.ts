// Optional hook: moves subagent files that nobody wrote for longer than the idle threshold out of the session store into
// ~/tmp/backup/agent/<session-id>/, keeping their names. Runs in main sessions only, at session start and then hourly.
//
// Setting: ~/.omp/agent/subagent-archive.json, `{ "idleHours": <positive number> }` (hours, fractions allowed;
// shipped value 28). Without the file the threshold is DEFAULT_IDLE_HOURS (28 hours). An unreadable or invalid file
// disables archiving and is reported once. Changes apply at the next sweep.
//
// A subagent is one top-level <Name>.jsonl transcript in a session directory
// (sessions/<project>/<timestamp>_<session-id>/) together with its siblings <Name>.md and <Name>/ (nested children).
// "Not used" means not written: the group moves only when the newest modification time over every file and directory
// in it is older than the threshold. A running agent keeps appending to its transcript, so it is never idle; reads
// (agent://, history://) leave no reliable trace (relatime), so they do not count. Tool logs, local/ and other
// session files stay. Moving makes agent:// and history:// for that agent unavailable; move the files back to restore.
// An existing destination is never overwritten: that group stays in place.
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { cp, lstat, mkdir, readdir, readFile, rename, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";

export const DEFAULT_IDLE_HOURS = 28;
const HOUR_MS = 3_600_000;
const SWEEP_INTERVAL_MS = HOUR_MS;
// Session directories are named <timestamp>_<session-id>.
const SESSION_DIR = /^[^_]+_(.+)$/;

export type SweepOptions = { sessionsRoot: string; backupRoot: string; idleMs: number; now: number };
export type MovedGroup = { sessionId: string; name: string; entries: string[] };

function errnoCode(error: unknown): unknown {
	return error instanceof Error && "code" in error ? error.code : undefined;
}

// Idle threshold in milliseconds from the settings file, or DEFAULT_IDLE_HOURS when the file does not exist.
export async function readIdleMs(configPath: string): Promise<number> {
	let text: string;
	try {
		text = await readFile(configPath, "utf8");
	} catch (error) {
		if (errnoCode(error) === "ENOENT") return DEFAULT_IDLE_HOURS * HOUR_MS;
		throw error;
	}
	const config: unknown = JSON.parse(text);
	const hours = config !== null && typeof config === "object" && "idleHours" in config ? config.idleHours : undefined;
	if (typeof hours !== "number" || !Number.isFinite(hours) || hours <= 0) {
		throw new Error(`${configPath}: idleHours must be a positive number of hours, got ${JSON.stringify(hours)}`);
	}
	return hours * HOUR_MS;
}

async function newestMtimeMs(path: string): Promise<number> {
	const info = await lstat(path);
	let newest = info.mtimeMs;
	if (!info.isDirectory()) return newest;
	for (const entry of await readdir(path)) newest = Math.max(newest, await newestMtimeMs(join(path, entry)));
	return newest;
}

async function pathExists(path: string): Promise<boolean> {
	try {
		await lstat(path);
		return true;
	} catch (error) {
		if (errnoCode(error) === "ENOENT") return false;
		throw error;
	}
}

// rename, or copy then delete when the backup lies on another filesystem.
async function move(from: string, to: string): Promise<void> {
	try {
		await rename(from, to);
	} catch (error) {
		if (errnoCode(error) !== "EXDEV") throw error;
		await cp(from, to, { recursive: true, preserveTimestamps: true, errorOnExist: true, force: false });
		await rm(from, { recursive: true });
	}
}

async function sweepSession(dir: string, sessionId: string, options: SweepOptions): Promise<MovedGroup[]> {
	const names = new Set(await readdir(dir));
	const target = join(options.backupRoot, sessionId);
	const moved: MovedGroup[] = [];
	for (const file of names) {
		if (!file.endsWith(".jsonl")) continue;
		const name = file.slice(0, -".jsonl".length);
		const entries = [file, `${name}.md`, name].filter(entry => names.has(entry));
		let newest = 0;
		for (const entry of entries) newest = Math.max(newest, await newestMtimeMs(join(dir, entry)));
		if (options.now - newest <= options.idleMs) continue;
		const taken = await Promise.all(entries.map(entry => pathExists(join(target, entry))));
		if (taken.includes(true)) continue;
		await mkdir(target, { recursive: true });
		for (const entry of entries) await move(join(dir, entry), join(target, entry));
		moved.push({ sessionId, name, entries });
	}
	return moved;
}

// One pass over every session directory under sessionsRoot. A failing session (a file vanishing under a concurrent
// sweep in another omp process, permissions) is skipped and reported in errors; the others still run.
export async function sweep(options: SweepOptions): Promise<{ moved: MovedGroup[]; errors: string[] }> {
	const moved: MovedGroup[] = [];
	const errors: string[] = [];
	for (const project of await readdir(options.sessionsRoot, { withFileTypes: true })) {
		if (!project.isDirectory()) continue;
		const projectDir = join(options.sessionsRoot, project.name);
		for (const session of await readdir(projectDir, { withFileTypes: true })) {
			const match = SESSION_DIR.exec(session.name);
			if (!session.isDirectory() || !match) continue;
			try {
				moved.push(...(await sweepSession(join(projectDir, session.name), match[1], options)));
			} catch (error) {
				errors.push(`${project.name}/${session.name}: ${String(error)}`);
			}
		}
	}
	return { moved, errors };
}

export default function subagentArchive(pi: ExtensionAPI): void {
	const agentDir = join(homedir(), ".omp", "agent");
	let sweeping = false;
	let warned = false;

	async function archive(ctx: ExtensionContext): Promise<void> {
		if (sweeping) return;
		sweeping = true;
		try {
			const { errors } = await sweep({
				sessionsRoot: join(agentDir, "sessions"),
				backupRoot: join(homedir(), "tmp", "backup", "agent"),
				idleMs: await readIdleMs(join(agentDir, "subagent-archive.json")),
				now: Date.now(),
			});
			for (const error of errors) console.error(`subagent-archive: ${error}`);
		} catch (error) {
			if (warned) return;
			warned = true;
			const message = `Subagent archiving disabled: ${String(error)}`;
			console.error(message);
			ctx.ui?.notify(message, "warning");
		} finally {
			sweeping = false;
		}
	}

	pi.on("session_start", (_event, ctx) => {
		if (ctx.agent?.kind !== "main") return;
		void archive(ctx);
		ctx.setInterval(() => archive(ctx), SWEEP_INTERVAL_MS);
	});
}
