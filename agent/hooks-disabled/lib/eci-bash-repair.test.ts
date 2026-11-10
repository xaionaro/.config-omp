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
		type RepairTool = { name: string; execute: (id: string, params: object, signal: AbortSignal | undefined, update: undefined, ctx: object) => Promise<{ isError?: boolean; content: { text: string }[] }> };
		let repair: RepairTool | undefined;
		const pi = { zod: { object: () => ({}) }, on() {}, registerTool(tool: RepairTool) { repair = tool; } };
		// The fixture supplies only the factory methods exercised by registration.
		eciBashGate(pi as unknown as ExtensionAPI);
		if (!repair) throw new Error("repair tool was not registered");
		expect(repair.name).toBe("eci_bash_gate_repair");
		const ctx = { cwd: home, sessionManager: { getSessionId: () => "test", getSessionFile: () => undefined } };
		const result = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(readFileSync(join(home, "arguments"), "utf8").trim().split("\n")).toEqual(process.platform === "linux" ? ["eci-verexec", "eci-command-plan"] : ["eci-command-plan"]);
		expect(result.content[0].text).toBe("Bash gate accepted the fixed true probe.\n");
		expect(result.isError).not.toBe(true);
		writeFileSync(join(hooks, "validate-bash.sh"), "exit 2\n");
		const unavailable = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(unavailable.content[0].text).toContain("not ready");
		expect(unavailable.isError).toBe(true);
		expect(unavailable.content[0].text).not.toContain("permissions");
		writeFileSync(join(hooks, "build-native-binaries.sh"), "exit 1\n");
		writeFileSync(join(hooks, "validate-bash.sh"), 'echo unexpected > "$HOME/probed"\n');
		const failed = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(failed.content[0].text).toContain("Repair failed");
		expect(failed.isError).toBe(true);
		expect(existsSync(join(home, "probed"))).toBe(false);
		unlinkSync(join(home, "arguments"));
		const controller = new AbortController();
		controller.abort();
		const cancelled = await repair.execute("id", {}, controller.signal, undefined, ctx);
		expect(cancelled.content[0].text.toLowerCase()).toContain("cancel");
		expect(cancelled.isError).toBe(true);
		expect(existsSync(join(home, "arguments"))).toBe(false);
		writeFileSync(join(hooks, "build-native-binaries.sh"), "exit 0\n");
		writeFileSync(join(hooks, "validate-bash.sh"), `printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"specific denial"}}'\n`);
		const denied = await repair.execute("id", {}, undefined, undefined, ctx);
		expect(denied.isError).toBe(true);
		expect(denied.content[0].text).toContain("specific denial");
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
}, 15000);
