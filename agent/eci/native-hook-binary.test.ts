import { test } from "node:test";
import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ensureHookBinary, waitForProvision } from "./native-hook-binary.ts";

test("concurrent missing-helper requests share provisioning and reuse the result", async () => {
	const home = mkdtempSync(join(tmpdir(), "hook-build-"));
	const root = join(home, ".omp", "agent");
	const hooks = join(root, "eci", "hooks", "lib");
	const bin = join(root, "eci", "bin");
	const marker = join(home, "builds");
	const previous = process.env.HOME;
	mkdirSync(hooks, { recursive: true });
	mkdirSync(bin, { recursive: true });
	writeFileSync(join(hooks, "eci-native-provision.sh"), `eci_native_prepare_hook() { printf x >> '${marker}'; printf binary > '${join(bin, "eci-hook-helper")}'; printf receipt > '${join(bin, "eci-hook-helper.sha256")}'; printf verifier > '${join(bin, "eci-verexec")}'; printf receipt > '${join(bin, "eci-verexec.sha256")}'; }\n`);
	try {
		process.env.HOME = home;
		const paths = await Promise.all(Array.from({ length: 8 }, () => ensureHookBinary()));
		assert.deepEqual(new Set(paths), new Set([join(bin, "eci-hook-helper")]));
		assert.equal(readFileSync(marker, "utf8"), "x");
		await ensureHookBinary();
		assert.equal(readFileSync(marker, "utf8"), "x");
	} finally {
		if (previous === undefined) delete process.env.HOME; else process.env.HOME = previous;
		rmSync(home, { recursive: true, force: true });
	}
});

test("existing helper is not upgraded automatically", async () => {
	const home = mkdtempSync(join(tmpdir(), "hook-existing-"));
	const bin = join(home, ".omp", "agent", "eci", "bin");
	const binary = join(bin, "eci-hook-helper");
	const previous = process.env.HOME;
	mkdirSync(bin, { recursive: true });
	writeFileSync(binary, "old");
	writeFileSync(`${binary}.sha256`, "old receipt");
	writeFileSync(join(bin, "eci-verexec"), "verifier");
	writeFileSync(join(bin, "eci-verexec.sha256"), "receipt");
	try {
		process.env.HOME = home;
		assert.equal(await ensureHookBinary(), binary);
		assert.equal(readFileSync(binary, "utf8"), "old");
	} finally {
		if (previous === undefined) delete process.env.HOME; else process.env.HOME = previous;
		rmSync(home, { recursive: true, force: true });
	}
});

test("provisions a missing Linux verifier without replacing an existing helper", { skip: process.platform !== "linux" }, async () => {
	const home = mkdtempSync(join(tmpdir(), "hook-verifier-"));
	const root = join(home, ".omp", "agent", "eci");
	const bin = join(root, "bin");
	const hooks = join(root, "hooks", "lib");
	const binary = join(bin, "eci-hook-helper");
	const previous = process.env.HOME;
	mkdirSync(bin, { recursive: true });
	mkdirSync(hooks, { recursive: true });
	writeFileSync(binary, "old");
	writeFileSync(`${binary}.sha256`, "receipt");
	writeFileSync(join(hooks, "eci-native-provision.sh"), `eci_native_prepare_hook() { printf verifier > '${join(bin, "eci-verexec")}'; printf receipt > '${join(bin, "eci-verexec.sha256")}'; }\n`);
	try {
		process.env.HOME = home;
		assert.equal(await ensureHookBinary(), binary);
		assert.equal(readFileSync(join(bin, "eci-verexec"), "utf8"), "verifier");
		assert.equal(readFileSync(binary, "utf8"), "old");
	} finally {
		if (previous === undefined) delete process.env.HOME; else process.env.HOME = previous;
		rmSync(home, { recursive: true, force: true });
	}
});

test("resolves missing Go caches using the account home under Bun", async () => {
	const home = mkdtempSync(join(tmpdir(), "hook-cache-"));
	const bin = join(home, ".omp", "agent", "eci", "bin");
	const hooks = join(home, ".omp", "agent", "eci", "hooks", "lib");
	const tools = join(home, "tools");
	const log = join(home, "go-env");
	const saved = { ...process.env };
	const source = join(import.meta.dir, "hooks", "lib", "eci-native-provision.sh");
	const portable = join(import.meta.dir, "hooks", "tests", "portable-test.sh");
	const accountHome = execFileSync("bash", ["-c", 'source "$1"; eci_user_home', "bash", portable], { encoding: "utf8" }).trim();
	mkdirSync(bin, { recursive: true });
	mkdirSync(hooks, { recursive: true });
	mkdirSync(tools);
	writeFileSync(join(tools, "go"), `#!/bin/sh\nprintf '%s|%s|%s|%s|%s\\n' "$HOME" "$PWD" "$GOTOOLCHAIN" "$GOFLAGS" "$2" >> '${log}'\ncase "$2" in GOCACHE) echo "\${FAKE_GOCACHE:-/global/cache}";; GOMODCACHE) echo /global/modules;; esac\n`);
	chmodSync(join(tools, "go"), 0o755);
	writeFileSync(join(hooks, "eci-native-provision.sh"), `source '${source}'\neci_native_provision() { printf '%s|%s' "$GOCACHE" "$GOMODCACHE" > '${join(home, "caches")}'; printf x > '${join(bin, "eci-hook-helper")}'; printf x > '${join(bin, "eci-hook-helper.sha256")}'; printf x > '${join(bin, "eci-verexec")}'; printf x > '${join(bin, "eci-verexec.sha256")}'; }\n`);
	try {
		const env = { ...saved, HOME: home, PATH: `${tools}:${saved.PATH}`, BUN_BE_BUN: "1" };
		delete env.GOCACHE;
		delete env.GOMODCACHE;
		const args = ["-e", `import { ensureHookBinary } from ${JSON.stringify(join(import.meta.dir, "native-hook-binary.ts"))}; await ensureHookBinary();`];
		execFileSync(process.execPath, args, { env });
		assert.equal(readFileSync(join(home, "caches"), "utf8"), "/global/cache|/global/modules");
		assert.deepEqual(readFileSync(log, "utf8").split("\n").filter(Boolean), [`${accountHome}|/|local| |GOCACHE`, `${accountHome}|/|local| |GOMODCACHE`]);
		for (const file of ["eci-hook-helper", "eci-hook-helper.sha256"]) rmSync(join(bin, file));
		writeFileSync(log, "");
		env.GOCACHE = "/inherited/cache";
		execFileSync(process.execPath, args, { env });
		assert.equal(readFileSync(join(home, "caches"), "utf8"), "/inherited/cache|/global/modules");
		assert.deepEqual(readFileSync(log, "utf8").split("\n").filter(Boolean), [`${accountHome}|/|local| |GOMODCACHE`]);
		for (const file of ["eci-hook-helper", "eci-hook-helper.sha256"]) rmSync(join(bin, file));
		delete env.GOCACHE;
		writeFileSync(log, "");
		env.GOMODCACHE = "/inherited/modules";
		execFileSync(process.execPath, args, { env });
		assert.equal(readFileSync(join(home, "caches"), "utf8"), "/global/cache|/inherited/modules");
		assert.deepEqual(readFileSync(log, "utf8").split("\n").filter(Boolean), [`${accountHome}|/|local| |GOCACHE`]);
		for (const file of ["eci-hook-helper", "eci-hook-helper.sha256"]) rmSync(join(bin, file));
		delete env.GOMODCACHE;
		env.FAKE_GOCACHE = "off";
		assert.throws(() => execFileSync(process.execPath, args, { env, stdio: "pipe" }), /go env GOCACHE did not return an absolute path/);
		assert.equal(existsSync(join(bin, "eci-hook-helper")), false);
	} finally {
		rmSync(home, { recursive: true, force: true });
	}
});

test("provisioning timeout leaves the shared work running", async () => {
	const { promise: work, resolve: finish } = Promise.withResolvers<void>();
	await assert.rejects(waitForProvision(work, 1), /retry/);
	finish();
	await work;
});
