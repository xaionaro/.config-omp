// Both hook bridges use the canonical receipt-backed helper. Provision only when absent;
// the native provisioner serializes builds and coordinates publication across processes.
import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { userInfo } from "node:os";
import { isAbsolute, join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const builds = new Map<string, Promise<void>>();

// Native tools belong to the HOME runtime, independent of the active profile.
export function hookAgentRoot(): string {
	const home = process.env.HOME;
	return join(home && isAbsolute(home) ? home : userInfo().homedir, ".omp", "agent");
}

export async function ensureHookBinary(root: string): Promise<string> {
	const binary = join(root, "eci", "bin", "eci-hook-helper");
	if (existsSync(binary) && existsSync(`${binary}.sha256`)) return binary;
	let build = builds.get(root);
	if (!build) {
		build = (async () => {
			const env = { ...process.env };
			if (!env.GOCACHE) {
				const { stdout } = await exec("go", ["env", "GOCACHE"], { env: { ...env, HOME: userInfo().homedir } });
				const cache = stdout.trim();
				if (!cache.startsWith("/")) throw new Error("go env GOCACHE did not return an absolute path");
				env.GOCACHE = cache;
			}
			const provisioner = join(root, "eci", "hooks", "lib", "eci-native-provision.sh");
			await exec("bash", ["-c", 'source "$1" && eci_native_gate_set=eci-hook-helper && eci_native_receipt_only= && { [ "$eci_runtime_platform" != Linux ] || eci_native_receipt_only=eci-verexec; } && eci_native_provision', "bash", provisioner], { env });
		})();
		builds.set(root, build);
		build.finally(() => builds.delete(root)).catch(() => {});
	}
	await build;
	if (!existsSync(binary) || !existsSync(`${binary}.sha256`)) throw new Error(`native provisioner did not create ${binary} and its receipt`);
	return binary;
}
