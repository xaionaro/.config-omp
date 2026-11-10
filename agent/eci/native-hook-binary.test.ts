import { test } from "node:test";
import { strict as assert } from "node:assert";
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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

test("resolves missing Go caches outside the calling module with the local toolchain", async () => {
	const home = mkdtempSync(join(tmpdir(), "hook-cache-"));
	const root = join(home, ".omp", "agent", "eci");
	const bin = join(root, "bin");
	const hooks = join(root, "hooks", "lib");
	const tools = join(home, "tools");
	const saved = { ...process.env };
	mkdirSync(bin, { recursive: true });
	mkdirSync(hooks, { recursive: true });
	mkdirSync(tools);
	writeFileSync(join(tools, "go"), `#!/bin/sh\n[ "$PWD" = / ] && [ "$GOTOOLCHAIN" = local ] && [ "$GOFLAGS" = ' ' ] || exit 1\nprintf '{"GOCACHE":"/global/cache","GOMODCACHE":"/global/modules"}\\n'\n`);
	chmodSync(join(tools, "go"), 0o755);
	writeFileSync(join(hooks, "eci-native-provision.sh"), `eci_native_prepare_hook() { [ "$GOCACHE" = /global/cache ] && [ "$GOMODCACHE" = /global/modules ] || return 1; printf x > '${join(bin, "eci-hook-helper")}'; printf x > '${join(bin, "eci-hook-helper.sha256")}'; printf x > '${join(bin, "eci-verexec")}'; printf x > '${join(bin, "eci-verexec.sha256")}'; }\n`);
	try {
		process.env.HOME = home;
		process.env.PATH = `${tools}:${saved.PATH}`;
		delete process.env.GOCACHE;
		delete process.env.GOMODCACHE;
		await ensureHookBinary();
	} finally {
		process.env.HOME = saved.HOME;
		process.env.PATH = saved.PATH;
		if (saved.GOCACHE === undefined) delete process.env.GOCACHE; else process.env.GOCACHE = saved.GOCACHE;
		if (saved.GOMODCACHE === undefined) delete process.env.GOMODCACHE; else process.env.GOMODCACHE = saved.GOMODCACHE;
		rmSync(home, { recursive: true, force: true });
	}
});

test("provisioning timeout leaves the shared work running", async () => {
	const { promise: work, resolve: finish } = Promise.withResolvers<void>();
	await assert.rejects(waitForProvision(work, 1), /retry/);
	finish();
	await work;
});
