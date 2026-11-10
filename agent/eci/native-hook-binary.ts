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

export async function ensureHookBinary(): Promise<string> {
	const root = hookAgentRoot();
	const binary = join(root, "eci", "bin", "eci-hook-helper");
	const verifier = join(root, "eci", "bin", "eci-verexec");
	if (existsSync(binary) && existsSync(`${binary}.sha256`) &&
		(process.platform !== "linux" || (existsSync(verifier) && existsSync(`${verifier}.sha256`)))) return binary;
	let build = builds.get(root);
	if (!build) {
		build = (async () => {
			const env = { ...process.env };
			const missing = ["GOCACHE", "GOMODCACHE"].filter(name => !env[name]);
			if (missing.length) {
				const { stdout } = await exec("go", ["env", "-json", ...missing], {
					cwd: "/", env: { ...env, HOME: userInfo().homedir, GOTOOLCHAIN: "local", GOFLAGS: " " },
				});
				const caches = JSON.parse(stdout) as Record<string, string>;
				for (const name of missing) {
					if (!isAbsolute(caches[name] ?? "")) throw new Error(`go env ${name} did not return an absolute path`);
					env[name] = caches[name];
				}
			}
			const provisioner = join(root, "eci", "hooks", "lib", "eci-native-provision.sh");
			await exec("bash", ["-c", 'source "$1" && eci_native_prepare_hook', "bash", provisioner], { env });
		})();
		builds.set(root, build);
		build.finally(() => builds.delete(root)).catch(() => {});
	}
	await build;
	if (!existsSync(binary) || !existsSync(`${binary}.sha256`)) throw new Error(`native provisioner did not create ${binary} and its receipt`);
	return binary;
}

// A timed-out hook call releases the host while its shared build continues.
export async function waitForProvision<T>(work: Promise<T>, milliseconds: number): Promise<T> {
	const { promise, reject } = Promise.withResolvers<never>();
	const timer = setTimeout(() => reject(new Error("native helper build is still running; retry shortly")), milliseconds);
	try {
		return await Promise.race([work, promise]);
	} finally {
		clearTimeout(timer);
	}
}
