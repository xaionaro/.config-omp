import { afterEach, beforeEach, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { DEFAULT_IDLE_HOURS, readIdleMs, sweep } from "./subagent-archive.ts";

const HOUR = 3_600_000;
const NOW = Date.parse("2026-11-17T12:00:00Z");
const IDLE = 28 * HOUR;
const SESSION = "2026-11-10T00-00-00-000Z_01a1aaaa-0000-7000-8000-000000000001";
const SESSION_ID = "01a1aaaa-0000-7000-8000-000000000001";
let root: string;
let sessionsRoot: string;
let backupRoot: string;
let sessionDir: string;

beforeEach(() => {
	mkdirSync(join(homedir(), "tmp"), { recursive: true });
	root = mkdtempSync(join(homedir(), "tmp", "subagent-archive-test-"));
	sessionsRoot = join(root, "sessions");
	backupRoot = join(root, "backup", "agent");
	sessionDir = join(sessionsRoot, "-project", SESSION);
	mkdirSync(sessionDir, { recursive: true });
	writeFileSync(join(sessionsRoot, "-project", `${SESSION}.jsonl`), "{}\n");
});

afterEach(() => rmSync(root, { recursive: true, force: true }));

// Creates a file (and parents) last written ageHours before NOW.
function file(relative: string, ageHours: number, content = relative): void {
	const path = join(sessionDir, relative);
	mkdirSync(dirname(path), { recursive: true });
	writeFileSync(path, content);
	age(relative, ageHours);
}

function age(relative: string, ageHours: number): void {
	const time = new Date(NOW - ageHours * HOUR);
	utimesSync(join(sessionDir, relative), time, time);
}

const run = () => sweep({ sessionsRoot, backupRoot, idleMs: IDLE, now: NOW });
const inBackup = (relative: string) => join(backupRoot, SESSION_ID, relative);
const inSession = (relative: string) => join(sessionDir, relative);

test("moves an idle subagent's transcript, report and children with contents, keeps everything else", async () => {
	file("Old.jsonl", 30, "transcript");
	file("Old.md", 40);
	file("Old/Old.Child.jsonl", 29, "child");
	age("Old", 29);
	file("Fresh.jsonl", 27);
	file("12.bash.log", 100);
	file("local/note.md", 100);

	const { moved, errors } = await run();

	expect(errors).toEqual([]);
	expect(moved).toEqual([{ sessionId: SESSION_ID, name: "Old", entries: ["Old.jsonl", "Old.md", "Old"] }]);
	expect(readFileSync(inBackup("Old.jsonl"), "utf8")).toBe("transcript");
	expect(readFileSync(inBackup("Old/Old.Child.jsonl"), "utf8")).toBe("child");
	expect(existsSync(inBackup("Old.md"))).toBe(true);
	for (const gone of ["Old.jsonl", "Old.md", "Old"]) expect(existsSync(inSession(gone))).toBe(false);
	for (const kept of ["Fresh.jsonl", "12.bash.log", "local/note.md"]) expect(existsSync(inSession(kept))).toBe(true);
	expect(existsSync(inBackup("Fresh.jsonl"))).toBe(false);
});

test("a recent write anywhere in the group keeps the whole group", async () => {
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
});

test("idleness is strict: exactly the threshold stays", async () => {
	file("Edge.jsonl", 28);
	file("Past.jsonl", 28.01);

	const { moved } = await run();

	expect(moved.map(group => group.name)).toEqual(["Past"]);
	expect(existsSync(inSession("Edge.jsonl"))).toBe(true);
});

test("never overwrites an existing backup and leaves the group in place", async () => {
	file("Dup.jsonl", 30, "new");
	file("Dup.md", 30);
	mkdirSync(join(backupRoot, SESSION_ID), { recursive: true });
	writeFileSync(inBackup("Dup.md"), "old");

	const { moved } = await run();

	expect(moved).toEqual([]);
	expect(readFileSync(inBackup("Dup.md"), "utf8")).toBe("old");
	expect(existsSync(inBackup("Dup.jsonl"))).toBe(false);
	expect(existsSync(inSession("Dup.jsonl"))).toBe(true);
});

test("ignores session transcripts and directories that are not sessions", async () => {
	age(".", 100);
	mkdirSync(join(sessionsRoot, "-project", "nounderscore"));
	writeFileSync(join(sessionsRoot, "-project", "nounderscore", "X.jsonl"), "");
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
