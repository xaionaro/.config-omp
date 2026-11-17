// Optional hook: moves subagent files that nobody wrote for longer than the idle threshold out of the session store into
// ~/tmp/backup/agent/<session-id>/, keeping their names. Runs in main sessions only, at session start and then hourly.
//
// Setting: ~/.omp/agent/subagent-archive.json, `{ "idleHours": <positive number> }` (hours, fractions allowed;
// shipped value 28). Without the file the threshold is DEFAULT_IDLE_HOURS (28 hours). An unreadable or invalid file
// stops archiving and is reported once. Changes apply at the next sweep.
//
// Unit: a subagent is a top-level <Name>.jsonl in a session directory (sessions/<project>/<timestamp>_<session-id>/)
// whose session header names that session's transcript as parentSession, together with its siblings <Name>.md and
// <Name>/ (nested children). Nothing else is ever moved: changes.jsonl, advisors, tool logs, local/, blobs/, orphan .md
// files, and shrinker backups (*.shrink-*.bak); a unit containing a shrinker backup anywhere stays whole. A unit moves
// whole or not at all.
//
// "Not used": every file and directory in the unit was last written more than the threshold ago, and no transcript in
// it (nested ones included) is lease-held, i.e. run by an omp process (`omp session lease --json`). A lease reported
// unknown keeps the unit; a failing probe moves nothing. Parked agents of a live main session do move once idle. Reads
// (agent://, history://) leave no reliable trace (relatime), so they do not count. An existing destination is never
// overwritten.
//
// Restore: an archived agent is gone from agent://, history:// and the Agent Hub until restored. The backup keeps the
// session-relative names, so one move of the unit's entries (those present of <Name>.jsonl, <Name>.md, <Name>/)
// restores it:
//   cd ~/tmp/backup/agent/<session-id> && mv <Name>.jsonl <Name>.md <Name> ~/.omp/agent/sessions/<project>/<timestamp>_<session-id>/
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { execFile } from "node:child_process";
import { cp, lstat, mkdir, open, readdir, readFile, rename, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { basename, join } from "node:path";
import { promisify } from "node:util";

export const DEFAULT_IDLE_HOURS = 28;
const HOUR_MS = 3_600_000;
const SWEEP_INTERVAL_MS = HOUR_MS;
const LEASE_TIMEOUT_MS = 30_000;
// The session header is the first or second record; 16 KiB covers a title record plus the header.
const HEADER_BYTES = 16_384;
// Session directories are named <timestamp>_<session-id>.
const SESSION_DIR = /^[^_]+_(.+)$/;
const SHRINK_BACKUP = /\.shrink-[^/]*\.bak$/;

export type LeaseState = { held: string[]; unknown: string[] };
export type SweepOptions = {
	sessionsRoot: string;
	backupRoot: string;
	idleMs: number;
	now: number;
	// Lease state of the given session ids; a test seam for `omp session lease --json`.
	leases: (ids: string[]) => Promise<LeaseState>;
};
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

type SessionHeader = { id: string; parentSession?: string };

// The `type:"session"` record among a transcript's first records, or undefined when there is none.
async function readHeader(path: string): Promise<SessionHeader | undefined> {
	const handle = await open(path, "r");
	let text: string;
	try {
		const buffer = Buffer.alloc(HEADER_BYTES);
		const { bytesRead } = await handle.read(buffer, 0, HEADER_BYTES, 0);
		text = buffer.toString("utf8", 0, bytesRead);
	} finally {
		await handle.close();
	}
	for (const line of text.split("\n").slice(0, 2)) {
		let record: unknown;
		try {
			record = JSON.parse(line);
		} catch (error) {
			if (error instanceof SyntaxError) continue;
			throw error;
		}
		if (record === null || typeof record !== "object" || !("type" in record) || record.type !== "session") continue;
		if (!("id" in record) || typeof record.id !== "string") return undefined;
		const parent = "parentSession" in record && typeof record.parentSession === "string" ? record.parentSession : undefined;
		return { id: record.id, parentSession: parent };
	}
	return undefined;
}

// Newest write time over a unit, whether it holds a shrinker backup, and the session ids of its transcripts.
type UnitScan = { newestMtimeMs: number; hasShrinkBackup: boolean; ids: string[] };

async function scanUnitEntry(path: string, scan: UnitScan): Promise<void> {
	const info = await lstat(path);
	scan.newestMtimeMs = Math.max(scan.newestMtimeMs, info.mtimeMs);
	if (SHRINK_BACKUP.test(path)) scan.hasShrinkBackup = true;
	if (info.isFile() && path.endsWith(".jsonl")) {
		const header = await readHeader(path);
		if (header) scan.ids.push(header.id);
	}
	if (!info.isDirectory()) return;
	for (const entry of await readdir(path)) await scanUnitEntry(join(path, entry), scan);
}

async function scanUnit(dir: string, entries: string[]): Promise<UnitScan> {
	const scan: UnitScan = { newestMtimeMs: 0, hasShrinkBackup: false, ids: [] };
	for (const entry of entries) await scanUnitEntry(join(dir, entry), scan);
	return scan;
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

type Unit = { dir: string; sessionId: string; name: string; entries: string[] };
type IdleUnit = Unit & { ids: string[] };

const isIdle = (scan: UnitScan, options: SweepOptions) =>
	!scan.hasShrinkBackup && options.now - scan.newestMtimeMs > options.idleMs;

// Idle subagent units of one session directory, not yet lease-checked.
async function findIdleUnits(dir: string, sessionId: string, options: SweepOptions): Promise<IdleUnit[]> {
	const names = new Set(await readdir(dir));
	const ownTranscript = `${basename(dir)}.jsonl`;
	const units: IdleUnit[] = [];
	for (const file of names) {
		if (!file.endsWith(".jsonl")) continue;
		const header = await readHeader(join(dir, file));
		if (!header?.parentSession || basename(header.parentSession) !== ownTranscript) continue;
		const name = file.slice(0, -".jsonl".length);
		const entries = [file, `${name}.md`, name].filter(entry => names.has(entry));
		const scan = await scanUnit(dir, entries);
		if (isIdle(scan, options)) units.push({ dir, sessionId, name, entries, ids: scan.ids });
	}
	return units;
}

// Moves a unit whole into the backup; false when it was written meanwhile or a destination exists. On a failure
// part-way the entries already moved go back before the error propagates.
async function archiveUnit(unit: Unit, options: SweepOptions): Promise<boolean> {
	const { dir, entries } = unit;
	// A revived agent writes its transcript, so a fresh scan catches revival since the lease probe.
	if (!isIdle(await scanUnit(dir, entries), options)) return false;
	const target = join(options.backupRoot, unit.sessionId);
	const taken = await Promise.all(entries.map(entry => pathExists(join(target, entry))));
	if (taken.includes(true)) return false;
	await mkdir(target, { recursive: true });
	const done: string[] = [];
	try {
		for (const entry of entries) {
			await move(join(dir, entry), join(target, entry));
			done.push(entry);
		}
	} catch (error) {
		for (const entry of done.reverse()) await move(join(target, entry), join(dir, entry));
		throw error;
	}
	return true;
}

// One pass over every session directory under sessionsRoot with one lease probe. A failing session or unit (a file
// vanishing under a concurrent sweep in another omp process, permissions) is skipped and reported in errors; the
// others still run. A failing lease probe throws and moves nothing.
export async function sweep(options: SweepOptions): Promise<{ moved: MovedGroup[]; errors: string[] }> {
	const moved: MovedGroup[] = [];
	const errors: string[] = [];
	const units: IdleUnit[] = [];
	for (const project of await readdir(options.sessionsRoot, { withFileTypes: true })) {
		if (!project.isDirectory()) continue;
		const projectDir = join(options.sessionsRoot, project.name);
		for (const session of await readdir(projectDir, { withFileTypes: true })) {
			const match = SESSION_DIR.exec(session.name);
			if (!session.isDirectory() || !match) continue;
			try {
				units.push(...(await findIdleUnits(join(projectDir, session.name), match[1], options)));
			} catch (error) {
				errors.push(`${project.name}/${session.name}: ${String(error)}`);
			}
		}
	}
	if (units.length === 0) return { moved, errors };
	const lease = await options.leases([...new Set(units.flatMap(unit => unit.ids))]);
	const inUse = new Set([...lease.held, ...lease.unknown]);
	for (const unit of units) {
		if (unit.ids.some(id => inUse.has(id))) continue;
		try {
			if (await archiveUnit(unit, options)) {
				moved.push({ sessionId: unit.sessionId, name: unit.name, entries: unit.entries });
			}
		} catch (error) {
			errors.push(`${unit.dir}/${unit.name}: ${String(error)}`);
		}
	}
	return { moved, errors };
}

// Lease state from `omp session lease --json`, which reports which sessions a running omp owns without taking them.
export async function probeLeases(ids: string[]): Promise<LeaseState> {
	const { stdout } = await promisify(execFile)("omp", ["session", "lease", "--json", "--", ...ids], {
		timeout: LEASE_TIMEOUT_MS,
		maxBuffer: 16 * 1024 * 1024,
	});
	const state: unknown = JSON.parse(stdout);
	if (
		state === null ||
		typeof state !== "object" ||
		!("held" in state) ||
		!("unknown" in state) ||
		!Array.isArray(state.held) ||
		!Array.isArray(state.unknown)
	) {
		throw new Error(`omp session lease: unexpected output ${stdout.slice(0, 200)}`);
	}
	return { held: state.held.map(String), unknown: state.unknown.map(String) };
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
				leases: probeLeases,
			});
			for (const error of errors) console.error(`subagent-archive: ${error}`);
		} catch (error) {
			if (warned) return;
			warned = true;
			const message = `Subagent archiving failed: ${String(error)}`;
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
