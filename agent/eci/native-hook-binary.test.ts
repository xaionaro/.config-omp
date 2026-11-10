import { test } from "node:test";
import { strict as assert } from "node:assert";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ensureHookBinary } from "./native-hook-binary.ts";

test("concurrent missing-helper requests share provisioning and reuse the result", async () => {
	const root = mkdtempSync(join(tmpdir(), "hook-build-"));
	const hooks = join(root, "eci", "hooks", "lib");
	const bin = join(root, "eci", "bin");
	const marker = join(root, "builds");
	mkdirSync(hooks, { recursive: true });
	mkdirSync(bin, { recursive: true });
	writeFileSync(join(hooks, "eci-native-provision.sh"), `eci_native_provision() { printf x >> '${marker}'; printf binary > '${join(bin, "eci-hook-helper")}'; printf receipt > '${join(bin, "eci-hook-helper.sha256")}'; }\n`);
	try {
		const paths = await Promise.all(Array.from({ length: 8 }, () => ensureHookBinary(root)));
		assert.deepEqual(new Set(paths), new Set([join(bin, "eci-hook-helper")]));
		assert.equal(readFileSync(marker, "utf8"), "x");
		await ensureHookBinary(root);
		assert.equal(readFileSync(marker, "utf8"), "x");
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});
