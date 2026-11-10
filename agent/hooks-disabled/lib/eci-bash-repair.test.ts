// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/eci-bash-repair.test.ts
import { expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, unlinkSync, watch, writeFileSync } from "node:fs";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { join } from "node:path";
import eciBashGate from "../pre/eci-bash-gate";

test("registers bounded repair with fixed gate binaries and probes the gate", async () => {
	const home = mkdtempSync(join(process.env.TMPDIR ?? "/tmp", "eci-bash-repair."));
	const previousHome = process.env.HOME;
	try {
		const hooks = join(home, ".omp/agent/eci/hooks");
		mkdirSync(hooks, { recursive: true });
		writeFileSync(join(hooks, "build-native-binaries.sh"), 'printf "%s\\n" "$@" > "$HOME/arguments"\n');
		writeFileSync(join(hooks, "validate-bash.sh"), 'cat >/dev/null\n');
		process.env.HOME = home;
		type RepairTool = { name: string; description: string; execute: (id: string, params: object, signal: AbortSignal | undefined, update: undefined, ctx: object) => Promise<{ isError?: boolean; content: { text: string }[] }> };
		let repair: RepairTool | undefined;
		const pi = { zod: { object: () => ({}) }, on() {}, registerTool(tool: RepairTool) { repair = tool; } };
		// The fixture supplies only the factory methods exercised by registration.
		eciBashGate(pi as unknown as ExtensionAPI);
		if (!repair) throw new Error("repair tool was not registered");
		expect(repair.name).toBe("eci_bash_gate_repair");
		expect(repair.description).toContain("shared");
		expect(repair.description).toContain("source");
		const ctx = { cwd: home, sessionManager: { getSessionId: () => "test", getSessionFile: () => undefined } };
		const healthy = await repair.execute("healthy", {}, undefined, undefined, ctx);
		expect(healthy.content[0].text).toContain("nothing was rebuilt");
		expect(healthy.isError).not.toBe(true);
		expect(existsSync(join(home, "arguments"))).toBe(false);
		writeFileSync(join(hooks, "validate-bash.sh"), '[ -f "$HOME/built" ] || exit 2\n');
		writeFileSync(join(hooks, "build-native-binaries.sh"), 'printf "%s\\n" "$@" > "$HOME/arguments"\ntouch "$HOME/built"\n');
		const result = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(readFileSync(join(home, "arguments"), "utf8").trim().split("\n")).toEqual(process.platform === "linux" ? ["eci-verexec", "eci-command-plan"] : ["eci-command-plan"]);
		expect(result.content[0].text).toContain("fixed true probe");
		expect(result.content[0].text).toContain("source status: unknown");
		expect(result.isError).not.toBe(true);
		mkdirSync(join(hooks, "lib/eci-command-plan-go"), { recursive: true });
		writeFileSync(join(hooks, "lib/eci-command-plan-go/unfinished.go"), "package main\n");
		const init = Bun.spawnSync(["git", "init", "-q", hooks]);
		expect(init.exitCode).toBe(0);
		unlinkSync(join(home, "built"));
		const dirty = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(dirty.isError).not.toBe(true);
		expect(dirty.content[0].text).toContain("unfinished.go");
		writeFileSync(join(hooks, "validate-bash.sh"), "exit 2\n");
		const unavailable = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(unavailable.content[0].text).toContain("did not succeed");
		expect(unavailable.isError).toBe(true);
		expect(unavailable.content[0].text).not.toContain("permissions");
		expect(unavailable.content[0].text).not.toContain("call it, then retry");
		expect(unavailable.content[0].text).not.toContain("will not change");
		writeFileSync(join(hooks, "build-native-binaries.sh"), "exit 1\n");
		writeFileSync(join(hooks, "validate-bash.sh"), 'echo probe >> "$HOME/probed"\nexit 2\n');
		const failed = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(failed.content[0].text).toContain("Repair failed");
		expect(failed.isError).toBe(true);
		expect(readFileSync(join(home, "probed"), "utf8")).toBe("probe\n");
		unlinkSync(join(home, "arguments"));
		const controller = new AbortController();
		controller.abort();
		const cancelled = await repair.execute("id", {}, controller.signal, undefined, ctx);
		expect(cancelled.content[0].text.toLowerCase()).toContain("cancel");
		expect(cancelled.isError).toBe(true);
		expect(existsSync(join(home, "arguments"))).toBe(false);
		writeFileSync(join(hooks, "validate-bash.sh"), "exit 2\n");
		writeFileSync(join(hooks, "build-native-binaries.sh"), 'echo started > "$HOME/build-started"\nsleep 30\n');
		const buildController = new AbortController();
		const buildWatcher = watch(home, (_event, name) => {
			if (name === "build-started") buildController.abort();
		});
		try {
			const interrupted = await repair.execute("id", {}, buildController.signal, undefined, ctx);
			expect(interrupted.isError).toBe(true);
			expect(interrupted.content[0].text).toContain("Repair cancelled");
		} finally {
			buildWatcher.close();
		}
		writeFileSync(join(hooks, "build-native-binaries.sh"), 'touch "$HOME/second-probe"\n');
		writeFileSync(join(hooks, "validate-bash.sh"), '[ -f "$HOME/second-probe" ] || exit 2\necho started > "$HOME/second-started"\nsleep 30\n');
		const secondController = new AbortController();
		const secondWatcher = watch(home, (_event, name) => {
			if (name === "second-started") secondController.abort();
		});
		try {
			const interrupted = await repair.execute("id", {}, secondController.signal, undefined, ctx);
			expect(interrupted.isError).toBe(true);
			expect(interrupted.content[0].text).toContain("probe cancelled");
		} finally {
			secondWatcher.close();
		}
		writeFileSync(join(hooks, "build-native-binaries.sh"), "exit 0\n");
		writeFileSync(join(hooks, "validate-bash.sh"), `printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"specific denial"}}'\n`);
		const denied = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(denied.isError).toBe(true);
		expect(denied.content[0].text).toContain("specific denial");
		expect(denied.content[0].text).toContain("Nothing was rebuilt");
		writeFileSync(join(hooks, "build-native-binaries.sh"), 'touch "$HOME/deny-after-build"\n');
		writeFileSync(join(hooks, "validate-bash.sh"), `[ -f "$HOME/deny-after-build" ] || exit 2\nprintf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"specific denial"}}'\n`);
		const deniedAfterBuild = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(deniedAfterBuild.isError).toBe(denied.isError);
		expect(deniedAfterBuild.content[0].text).toContain("specific denial");
		writeFileSync(join(hooks, "validate-bash.sh"), 'echo started > "$HOME/probe-started"\nsleep 30\n');
		const probeController = new AbortController();
		const watcher = watch(home, (_event, name) => {
			if (name === "probe-started") probeController.abort();
		});
		try {
			const interrupted = await repair.execute("id", {}, probeController.signal, undefined, ctx);
			expect(interrupted.isError).toBe(true);
			expect(interrupted.content[0].text).toContain("probe cancelled");
			expect(interrupted.content[0].text).not.toContain("permissions");
		} finally {
			watcher.close();
		}
	} finally {
		if (previousHome === undefined) delete process.env.HOME;
		else process.env.HOME = previousHome;
		rmSync(home, { recursive: true, force: true });
	}
}, 30000);

test("repair registration failure does not prevent Bash gate registration", async () => {
	let handler: ((event: unknown, ctx: unknown) => Promise<unknown>) | undefined;
	const pi = {
		zod: { object: () => ({}) },
		on(_event: string, callback: typeof handler) { handler = callback; },
		registerTool() { throw new Error("registration unavailable"); },
	};
	// The fixture supplies only the factory methods needed to observe registration.
	eciBashGate(pi as unknown as ExtensionAPI);
	expect(handler).toBeDefined();
	const event = { toolName: "bash", get input(): never { throw new Error("private data"); } };
	const verdict = await handler!(event, {});
	expect(verdict).toMatchObject({ block: true });
	expect(JSON.stringify(verdict)).not.toContain("private data");
});
