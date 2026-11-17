import { afterEach, beforeEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join } from "node:path";
import { DEFAULT_IDLE_HOURS, readIdleMs, sweep } from "./subagent-archive.ts";

const HOUR = 3_600_000;
const NOW = Date.parse("2026-11-17T12:00:00Z");
const IDLE = 28 * HOUR;
const SESSION = "2026-11-10T00-00-00-000Z_01a1aaaa-0000-7000-8000-000000000001";
const SESSION_ID = "01a1aaaa-0000-7000-8000-000000000001";
let root: string;
let held: string[];
let unknown: string[];
let queried: string[][];
let sessionsRoot: string;
let backupRoot: string;
let sessionDir: string;

beforeEach(() => {
	held = [];
	unknown = [];
	queried = [];
	mkdirSync(join(homedir(), "tmp"), { recursive: true });
	root = mkdtempSync(join(homedir(), "tmp", "subagent-archive-test-"));
	sessionsRoot = join(root, "sessions");
	backupRoot = join(root, "backup", "agent");
	sessionDir = join(sessionsRoot, "-project", SESSION);
	mkdirSync(sessionDir, { recursive: true });
	// The main transcript is fresh: a live main session does not protect its idle parked subagents.
	writeFileSync(join(sessionsRoot, "-project", `${SESSION}.jsonl`), "{}\n");
});

afterEach(() => rmSync(root, { recursive: true, force: true }));

// Transcript text as omp writes it: a title record, then the session header with the agent's id and its parent's path.
function transcriptOf(name: string, parent: string): string {
	const header = { type: "session", version: 3, id: `id-${basename(name)}`, parentSession: parent };
	return `{"type":"title","v":1}\n${JSON.stringify(header)}\n`;
}

// Creates a file (and parents) last written ageHours before NOW. A .jsonl defaults to a subagent transcript whose
// parent is the main session for top-level files and the owning transcript for nested ones.
function file(relative: string, ageHours: number, content?: string): void {
	const path = join(sessionDir, relative);
	mkdirSync(dirname(path), { recursive: true });
	const parent = relative.includes("/") ? `${join(sessionDir, dirname(relative))}.jsonl` : `${sessionDir}.jsonl`;
	writeFileSync(path, content ?? (relative.endsWith(".jsonl") ? transcriptOf(relative.slice(0, -6), parent) : relative));
	age(relative, ageHours);
}

function age(relative: string, ageHours: number): void {
	const time = new Date(NOW - ageHours * HOUR);
	utimesSync(join(sessionDir, relative), time, time);
}

const run = () =>
	sweep({
		sessionsRoot,
		backupRoot,
		idleMs: IDLE,
		now: NOW,
		leases: async ids => {
			queried.push(ids);
			return { held: ids.filter(id => held.includes(id)), unknown: ids.filter(id => unknown.includes(id)) };
		},
	});
const inBackup = (relative: string) => join(backupRoot, SESSION_ID, relative);
const inSession = (relative: string) => join(sessionDir, relative);

test("moves an idle subagent's transcript, report and children with contents, keeps everything else", async () => {
	file("Old.jsonl", 30);
	file("Old.md", 40);
	file("Old/Old.Child.jsonl", 29);
	age("Old", 29);
	file("Fresh.jsonl", 27);
	file("12.bash.log", 100);
	file("local/note.md", 100);

	const { moved, errors } = await run();

	expect(errors).toEqual([]);
	expect(moved).toEqual([{ sessionId: SESSION_ID, name: "Old", entries: ["Old.jsonl", "Old.md", "Old"] }]);
	expect(readFileSync(inBackup("Old.jsonl"), "utf8")).toBe(transcriptOf("Old", `${sessionDir}.jsonl`));
	expect(existsSync(inBackup("Old/Old.Child.jsonl"))).toBe(true);
	expect(existsSync(inBackup("Old.md"))).toBe(true);
	for (const gone of ["Old.jsonl", "Old.md", "Old"]) expect(existsSync(inSession(gone))).toBe(false);
	for (const kept of ["Fresh.jsonl", "12.bash.log", "local/note.md"]) expect(existsSync(inSession(kept))).toBe(true);
	expect(queried).toEqual([["id-Old", "id-Old.Child"]]);
});

test("restore is one move of the unit's entries back into the session directory", async () => {
	file("Old.jsonl", 30);
	file("Old.md", 30);
	file("Old/Old.Child.jsonl", 30);
	age("Old", 30);

	await run();
	for (const entry of ["Old.jsonl", "Old.md", "Old"]) renameSync(inBackup(entry), inSession(entry));

	for (const entry of ["Old.jsonl", "Old.md", "Old/Old.Child.jsonl"]) expect(existsSync(inSession(entry))).toBe(true);
});

test("a recent write anywhere in the unit keeps the whole unit", async () => {
	file("Parent.jsonl", 50);
	file("Parent/Parent.Child.jsonl", 1);
	age("Parent", 50);
	file("Reported.jsonl", 50);
	file("Reported.md", 2);

	const { moved } = await run();

	expect(moved).toEqual([]);
	for (const kept of ["Parent.jsonl", "Parent/Parent.Child.jsonl", "Reported.jsonl", "Reported.md"]) {
		expect(existsSync(inSession(kept))).toBe(true);
	}
	expect(queried).toEqual([]);
});

test("a lease-held transcript anywhere in the unit keeps it; unknown lease state keeps it too", async () => {
	file("Running.jsonl", 100);
	file("Owner.jsonl", 100);
	file("Owner/Owner.Child.jsonl", 100);
	age("Owner", 100);
	file("Unsure.jsonl", 100);
	file("Parked.jsonl", 100);
	held = ["id-Running", "id-Owner.Child"];
	unknown = ["id-Unsure"];

	const { moved } = await run();

	expect(moved.map(unit => unit.name)).toEqual(["Parked"]);
	for (const kept of ["Running.jsonl", "Owner.jsonl", "Owner/Owner.Child.jsonl", "Unsure.jsonl"]) {
		expect(existsSync(inSession(kept))).toBe(true);
	}
});

test("a failing lease probe moves nothing", async () => {
	file("Parked.jsonl", 100);

	const result = sweep({
		sessionsRoot,
		backupRoot,
		idleMs: IDLE,
		now: NOW,
		leases: async () => {
			throw new Error("omp missing");
		},
	});

	await expect(result).rejects.toThrow("omp missing");
	expect(existsSync(inSession("Parked.jsonl"))).toBe(true);
});

test("moves only subagent transcripts of this session; shrinker backups and other files stay", async () => {
	file("Shrunk.jsonl", 100);
	file("Shrunk.jsonl.shrink-2026-11-10T00-00-00-000Z.bak", 100);
	file("Nested.jsonl", 100);
	file("Nested/Nested.Child.jsonl", 100);
	file("Nested/Nested.Child.jsonl.shrink-2026-11-10T00-00-00-000Z.bak", 100);
	age("Nested", 100);
	file("__advisor.rules.jsonl", 100, '{"type":"title"}\n{"type":"session","version":3,"id":"advisor"}\n');
	file("changes.jsonl", 100, '{"type":"call","agent":"X"}\n');
	file("Foreign.jsonl", 100, transcriptOf("Foreign", "/elsewhere/2026-01-01T00-00-00-000Z_other.jsonl"));
	file("Broken.jsonl", 100, "not json\n");
	file("Orphan.md", 100);
	file("Plain.jsonl", 100);

	const { moved, errors } = await run();

	expect(errors).toEqual([]);
	expect(moved.map(unit => unit.name).sort()).toEqual(["Plain", "Shrunk"]);
	const kept = [
		"Shrunk.jsonl.shrink-2026-11-10T00-00-00-000Z.bak",
		"Nested.jsonl",
		"Nested/Nested.Child.jsonl.shrink-2026-11-10T00-00-00-000Z.bak",
		"__advisor.rules.jsonl",
		"changes.jsonl",
		"Foreign.jsonl",
		"Broken.jsonl",
		"Orphan.md",
	];
	for (const entry of kept) expect(existsSync(inSession(entry))).toBe(true);
});

test("idleness is strict: exactly the threshold stays", async () => {
	file("Edge.jsonl", 28);
	file("Past.jsonl", 28.01);

	const { moved } = await run();

	expect(moved.map(unit => unit.name)).toEqual(["Past"]);
	expect(existsSync(inSession("Edge.jsonl"))).toBe(true);
});

test("never overwrites an existing backup and leaves the unit in place", async () => {
	file("Dup.jsonl", 30);
	file("Dup.md", 30);
	mkdirSync(join(backupRoot, SESSION_ID), { recursive: true });
	writeFileSync(inBackup("Dup.md"), "old");

	const { moved } = await run();

	expect(moved).toEqual([]);
	expect(readFileSync(inBackup("Dup.md"), "utf8")).toBe("old");
	expect(existsSync(inBackup("Dup.jsonl"))).toBe(false);
	expect(existsSync(inSession("Dup.jsonl"))).toBe(true);
});

test("a unit written after the lease probe stays", async () => {
	file("Revived.jsonl", 100);

	const { moved } = await sweep({
		sessionsRoot,
		backupRoot,
		idleMs: IDLE,
		now: NOW,
		leases: async () => {
			age("Revived.jsonl", 0);
			return { held: [], unknown: [] };
		},
	});

	expect(moved).toEqual([]);
	expect(existsSync(inSession("Revived.jsonl"))).toBe(true);
});

test("ignores directories that are not sessions", async () => {
	mkdirSync(join(sessionsRoot, "-project", "nounderscore"));
	writeFileSync(join(sessionsRoot, "-project", "nounderscore", "X.jsonl"), transcriptOf("X", "/x/nounderscore.jsonl"));
	utimesSync(join(sessionsRoot, "-project", "nounderscore", "X.jsonl"), new Date(0), new Date(0));

	const { moved } = await run();

	expect(moved).toEqual([]);
	expect(existsSync(join(sessionsRoot, "-project", `${SESSION}.jsonl`))).toBe(true);
	expect(existsSync(join(sessionsRoot, "-project", "nounderscore", "X.jsonl"))).toBe(true);
});

test("idle threshold: default when unset, configured hours otherwise, invalid rejected", async () => {
	const config = join(root, "subagent-archive.json");
	expect(DEFAULT_IDLE_HOURS).toBe(28);
	expect(await readIdleMs(config)).toBe(28 * HOUR);
	writeFileSync(config, JSON.stringify({ idleHours: 1.5 }));
	expect(await readIdleMs(config)).toBe(1.5 * HOUR);
	for (const bad of [{ idleHours: 0 }, { idleHours: -1 }, { idleHours: "28" }, {}]) {
		writeFileSync(config, JSON.stringify(bad));
		await expect(readIdleMs(config)).rejects.toThrow("idleHours");
	}
	writeFileSync(config, "{");
	await expect(readIdleMs(config)).rejects.toThrow();
});

test("the shipped setting is 28 hours", async () => {
	expect(await readIdleMs(join(import.meta.dir, "..", "subagent-archive.json"))).toBe(28 * HOUR);
});
