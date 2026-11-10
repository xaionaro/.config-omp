// HOME=<scratch> ECI_EDIT_GATE_CHAIN_HOME=<same scratch> BUN_BE_BUN=1 omp test --preserve-symlinks agent/hooks-disabled/lib/eci-edit-gate.test.ts
// Requires resolvable @oh-my-pi/pi-natives (with its platform addon) and @oh-my-pi/pi-coding-agent.
// The chain tests build the real launcher, verifier and helper into HOME, so they run only when
// ECI_EDIT_GATE_CHAIN_HOME names the launch HOME; the host resolves `~` against the launch HOME.
import { afterAll, beforeAll, describe, expect, spyOn, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { homedir, tmpdir, userInfo } from "node:os";
import { join, resolve } from "node:path";
import gate from "../pre/eci-edit-gate";

type Handler = (event: unknown, ctx: unknown) => Promise<unknown>;
type Request = { session_id: string; cwd: string; hook_event_name: string; tool_name: string; omp_agent_kind?: string; targets: { path: string; file_path: string }[] };

const HOME = homedir();

function loadGate(): Handler {
	let handler: Handler = async () => undefined;
	gate({ on: (_name: string, fn: Handler) => { handler = fn; } } as never);
	return handler;
}

function context(cwd: string, kind = "sub") {
	return { cwd, agent: { kind }, sessionManager: { getSessionFile: () => undefined, getSessionId: () => "host-session" } };
}

/** Runs body against a stub launcher that records its request, then runs script. */
async function withStubLauncher(script: string, body: (handler: Handler, dir: string, request: () => Request) => Promise<void>): Promise<void> {
	const dir = mkdtempSync(join(tmpdir(), "eci-edit-gate-"));
	const home = process.env.HOME;
	const hooks = join(dir, ".omp/agent/eci/hooks");
	mkdirSync(hooks, { recursive: true });
	const capture = join(dir, "request.json");
	writeFileSync(join(hooks, "eci-edit-gate.sh"), `cat > '${capture}'\n${script}\n`);
	try {
		process.env.HOME = dir;
		await body(loadGate(), dir, () => JSON.parse(readFileSync(capture, "utf8")) as Request);
	} finally {
		if (home === undefined) delete process.env.HOME; else process.env.HOME = home;
		rmSync(dir, { recursive: true, force: true });
	}
}

describe("adapter", () => {
	test("sends every hashline source and move destination, normalized as the host resolves them", async () => {
		await withStubLauncher("", async (handler, dir, request) => {
			const input = "[:~/a.txt#1234]\nMV \":~/b.txt\"\n[:foo#ABCD]\nCUT 1.=1\n[rel/c.txt#ABCD]\nCUT 1.=1\n[~/d.txt#ABCD]\nCUT 1.=1";
			expect(await handler({ toolName: "edit", input: { input } }, context(dir, "main"))).toBeUndefined();
			expect(request()).toEqual({
				session_id: "host-session",
				omp_agent_kind: "main",
				cwd: dir,
				hook_event_name: "PreToolUse",
				tool_name: "Edit",
				targets: [
					{ path: ":~/a.txt", file_path: `${HOME}/a.txt` },
					{ path: ":foo", file_path: ":foo" },
					{ path: "rel/c.txt", file_path: "rel/c.txt" },
					{ path: "~/d.txt", file_path: `${HOME}/d.txt` },
					{ path: ":~/b.txt", file_path: `${HOME}/b.txt` },
				],
			});
		});
	});

	test("uses host parsing for whitespace, CRLF, untagged headers and quoted moves", async () => {
		await withStubLauncher("", async (handler, dir, request) => {
			const input = "*** Begin Patch\r\n[ untagged ]\r\nMV \"protected path\"\r\n[other#ABCD]\r\nCUT 1.=1\r\n*** End Patch";
			expect(await handler({ toolName: "edit", input: { input } }, context(dir))).toBeUndefined();
			expect(request().targets.map(target => target.file_path).sort()).toEqual(["other", "protected path", "untagged"]);
		});
	});

	test("selects the inspection grammar from the payload shape", async () => {
		const cases: [Record<string, unknown>, [string, string][]][] = [
			[{ path: "[:~/r.txt#ABCD]", old_string: "a", new_string: "b" }, [["[:~/r.txt#ABCD]", `${HOME}/r.txt`]]],
			[{ path: ":~/p.txt", edits: [{ op: "update", rename: ":~/q.txt", diff: "@@\n-a\n+b" }] }, [[":~/p.txt", `${HOME}/p.txt`], [":~/q.txt", `${HOME}/q.txt`]]],
			[{ input: "*** Begin Patch\n*** Update File: :~/ap.txt\n*** Move to: :../ap2.txt\n@@\n-a\n+b\n*** End Patch" }, [[":~/ap.txt", `${HOME}/ap.txt`], [":../ap2.txt", "../ap2.txt"]]],
			[{ input: "[:~/s.txt#ABCD]\nPUT 1.=1:\n+b" }, [[":~/s.txt", `${HOME}/s.txt`]]],
		];
		for (const [input, targets] of cases) {
			await withStubLauncher("", async (handler, dir, request) => {
				expect(await handler({ toolName: "edit", input }, context(dir))).toBeUndefined();
				expect(request().targets).toEqual(targets.map(([path, file_path]) => ({ path, file_path })));
			});
		}
	});

	test("resolves write paths as the host does, including hashline headers", async () => {
		const cases: [string, string][] = [["[~/w.txt#ABCD]", `${HOME}/w.txt`], [":~/w.txt", `${HOME}/w.txt`], [":foo", ":foo"], [":../up.txt", "../up.txt"], ["plain.txt", "plain.txt"]];
		for (const [path, file_path] of cases) {
			await withStubLauncher("", async (handler, dir, request) => {
				expect(await handler({ toolName: "write", input: { path, content: "text" } }, context(dir))).toBeUndefined();
				expect(request()).toMatchObject({ tool_name: "Write", targets: [{ path, file_path }] });
			});
		}
	});

	test("maps the launcher decision", async () => {
		const deny = `printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected"}}'`;
		const warn = `printf '%s\\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","additionalContext":"guard warning"}}'`;
		const write = { toolName: "write", input: { path: "file", content: "text" } };
		await withStubLauncher(deny, async (handler, dir) => {
			expect(await handler(write, context(dir))).toEqual({ block: true, reason: "protected" });
		});
		await withStubLauncher(warn, async (handler, dir) => {
			expect(await handler(write, context(dir))).toEqual({ additionalContext: "guard warning" });
		});
		await withStubLauncher("printf 'not json\\n'", async (handler, dir) => {
			expect(await handler(write, context(dir))).toEqual({ block: true, reason: "ECI hook eci-edit-gate returned non-JSON output; blocking (fail-closed)." });
		});
		await withStubLauncher("printf '%s\\n' '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] helper missing' >&2; exit 2", async (handler, dir) => {
			expect(await handler(write, context(dir))).toEqual({ block: true, reason: "ECI hook eci-edit-gate exited 2: [ECI_HOOK_NATIVE_GATE_UNAVAILABLE] helper missing; blocking (fail-closed)." });
		});
		await withStubLauncher("", async (handler, dir) => {
			rmSync(join(dir, ".omp/agent/eci/hooks/eci-edit-gate.sh"));
			const verdict = await handler(write, context(dir)) as { block: boolean; reason: string };
			expect(verdict.block).toBe(true);
			expect(verdict.reason).toContain("fail-closed");
		});
	});

	test("blocks an edit with no inspectable target without running the launcher", async () => {
		await withStubLauncher("", async (handler, dir) => {
			const verdict = await handler({ toolName: "edit", input: { input: "" } }, context(dir)) as { block: boolean; reason: string };
			expect(verdict.block).toBe(true);
			expect(verdict.reason).toContain("found no targets");
			expect(verdict.reason).toMatch(/write/i);
			expect(existsSync(join(dir, "request.json"))).toBe(false);
		});
	});

	test("blocks when the shared budget is spent before the launcher starts", async () => {
		const clock = spyOn(performance, "now").mockReturnValueOnce(0).mockReturnValue(21_000);
		try {
			await withStubLauncher("", async (handler, dir) => {
				const verdict = await handler({ toolName: "write", input: { path: "file", content: "text" } }, context(dir)) as { block: boolean; reason: string };
				expect(verdict.block).toBe(true);
				expect(verdict.reason).toContain("timed out");
				expect(existsSync(join(dir, "request.json"))).toBe(false);
			});
		} finally {
			clock.mockRestore();
		}
	});

	test("blocks a launcher that exceeds the remaining budget with an exact reproduction", async () => {
		const clock = spyOn(performance, "now").mockReturnValueOnce(0).mockReturnValue(19_980);
		try {
			await withStubLauncher("sleep 10", async (handler, dir) => {
				const verdict = await handler({ toolName: "write", input: { path: "it's.txt", content: "text" } }, context(dir)) as { block: boolean; reason: string };
				expect(verdict.block).toBe(true);
				expect(verdict.reason).toContain("timed out after 20 seconds");
				expect(verdict.reason).toContain(`"file_path":"it'\\''s.txt"`);
				expect(verdict.reason).toContain(`ECI_SESSION_ID='host-session' ECI_THREAD_ID='host-session' ECI_HOOK_IS_SUBAGENT=1 bash "$HOME/.omp/agent/eci/hooks/eci-edit-gate.sh"`);
				expect(verdict.reason).toContain("bash -x does not trace");
				expect(verdict.reason).toContain("report it to its owner");
			});
		} finally {
			clock.mockRestore();
		}
	}, 12_000);
});

const chainHome = process.env.ECI_EDIT_GATE_CHAIN_HOME;
const chain = chainHome !== undefined && resolve(chainHome) === HOME;

describe.skipIf(!chain)("real launcher, helper and dispatcher", () => {
	const eci = join(HOME, ".omp/agent/eci");
	const bin = join(eci, "bin");
	const repo = join(HOME, "repo");
	const other = join(HOME, ".agent/context/sessions/other");
	const sourceHooks = join(import.meta.dir, "../../eci/hooks");
	const proof = join(HOME, "proof");
	const savedProof = process.env.ECI_PROOF_ROOT;

	/** Go caches come from the account home, never the scratch HOME. */
	function goEnv(): Record<string, string> {
		const env: Record<string, string> = {};
		for (const [k, v] of Object.entries(process.env)) if (v !== undefined) env[k] = v;
		for (const name of ["GOCACHE", "GOMODCACHE"]) {
			if (env[name]) continue;
			const value = Bun.spawnSync(["go", "env", name], { env: { ...env, HOME: userInfo().homedir } }).stdout.toString().trim();
			if (!value.startsWith("/")) throw new Error(`go env ${name} is not absolute: ${value}`);
			env[name] = value;
		}
		return env;
	}

	function run(argv: string[], options: { cwd?: string; env?: Record<string, string> } = {}): string {
		const result = Bun.spawnSync(argv, { cwd: options.cwd, env: options.env, stderr: "pipe", stdout: "pipe" });
		if (result.exitCode !== 0) throw new Error(`${argv.join(" ")} exited ${result.exitCode}: ${result.stderr.toString()}`);
		return result.stdout.toString();
	}

	beforeAll(() => {
		// The verifier rejects group- or other-writable bin ancestors.
		mkdirSync(eci, { recursive: true, mode: 0o700 });
		mkdirSync(join(repo, "sub"), { recursive: true });
		mkdirSync(other, { recursive: true });
		mkdirSync(proof, { recursive: true });
		symlinkSync(other, join(repo, "link"));
		run(["cp", "-a", `${sourceHooks}/.`, join(eci, "hooks")]);
		run(["bash", join(eci, "hooks/build-native-binaries.sh"), "eci-verexec", "eci-hook-helper"], { env: goEnv() });
		process.env.ECI_PROOF_ROOT = proof;
	}, 300_000);

	afterAll(() => {
		if (savedProof === undefined) delete process.env.ECI_PROOF_ROOT; else process.env.ECI_PROOF_ROOT = savedProof;
	});

	const write = (path: string) => ({ toolName: "write", input: { path, content: "text" } });

	test("denies a colon-prefixed foreign-session target and keeps the requested spelling", async () => {
		const verdict = await loadGate()(write(":~/.agent/context/sessions/other/x.txt"), context(repo)) as { block: boolean; reason: string };
		expect(verdict.block).toBe(true);
		expect(verdict.reason).toContain(`(requested as ":~/.agent/context/sessions/other/x.txt", checked as "${other}/x.txt")`);
	}, 30_000);

	test("resolves tilde, relative and symlinked targets and keeps ':foo' literal", async () => {
		const handler = loadGate();
		for (const path of ["~/.agent/context/sessions/other/x.txt", "link/x.txt", "sub/../link/x.txt", "[~/.agent/context/sessions/other/x.txt#ABCD]"]) {
			expect(((await handler(write(path), context(repo))) as { block?: boolean } | undefined)?.block).toBe(true);
		}
		for (const path of [":~/.agent/context/sessions/host-session/x.txt", ":foo", "ordinary.txt", ":~/ordinary.txt"]) {
			expect(await handler(write(path), context(repo))).toBeUndefined();
		}
	}, 60_000);

	test("checks every edit target in order, including a move destination", async () => {
		const handler = loadGate();
		const move = { toolName: "edit", input: { input: "[ordinary.txt#1234]\nMV \":~/.agent/context/sessions/other/moved.txt\"" } };
		expect(((await handler(move, context(repo))) as { block?: boolean } | undefined)?.block).toBe(true);
		const ordinary = { toolName: "edit", input: { input: "[a.txt#1234]\nMV \"b.txt\"\n[c.txt#ABCD]\nCUT 1.=1" } };
		expect(await handler(ordinary, context(repo))).toBeUndefined();
	}, 60_000);

	test("a failed guard denies protected targets and warns with a rerun command on ordinary ones", async () => {
		const guard = join(eci, "hooks/validate-edit-write.sh");
		const saved = readFileSync(guard);
		writeFileSync(guard, "printf '%s\\n' 'synthetic guard failure' >&2\nexit 17\n");
		try {
			const handler = loadGate();
			const protectedVerdict = await handler(write(":~/.agent/context/sessions/other/x.txt"), context(repo)) as { block: boolean; reason: string };
			expect(protectedVerdict.block).toBe(true);
			expect(protectedVerdict.reason).toContain("[ECI_EDIT_GATE_FAILED] validate-edit-write.sh exited 17: synthetic guard failure");
			const ordinary = await handler(write("ordinary.txt"), context(repo)) as { additionalContext: string };
			expect(ordinary.additionalContext).toContain("ECI edit guard validate-edit-write.sh failed while checking the allowed target \"ordinary.txt\"");
			expect(ordinary.additionalContext).toContain(`ECI_SESSION_ID='host-session' ECI_THREAD_ID='host-session' ECI_HOOK_IS_SUBAGENT='1' bash -x "$HOME/.omp/agent/eci/hooks/validate-edit-write.sh"`);
		} finally {
			writeFileSync(guard, saved);
		}
	}, 60_000);

	test("blocks with the rebuild route when the helper is missing or predates edit-gate", async () => {
		const helper = join(bin, "eci-hook-helper");
		const receipt = `${helper}.sha256`;
		renameSync(helper, `${helper}.saved`);
		renameSync(receipt, `${receipt}.saved`);
		try {
			const missing = await loadGate()(write("ordinary.txt"), context(repo)) as { block: boolean; reason: string };
			expect(missing.block).toBe(true);
			expect(missing.reason).toContain("[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-hook-helper is unavailable or unverified");
			expect(missing.reason).toContain("build-native-binaries.sh");

			// 112c614 is the last helper source without the edit-gate mode.
			const stale = mkdtempSync(join(HOME, "stale-"));
			const sourceRoot = run(["git", "-C", sourceHooks, "rev-parse", "--show-toplevel"]).trim();
			run(["bash", "-c", `git -C '${sourceRoot}' archive 112c614 agent/eci/hooks/lib/eci-hook-helper-go | tar -x -C '${stale}'`]);
			run(["go", "build", "-o", helper, "."], { cwd: join(stale, "agent/eci/hooks/lib/eci-hook-helper-go"), env: goEnv() });
			chmodSync(helper, 0o755);
			const digest = run(["sha256sum", helper]).split(" ")[0];
			writeFileSync(receipt, `${digest}  eci-hook-helper\n`);
			const outdated = await loadGate()(write("ordinary.txt"), context(repo)) as { block: boolean; reason: string };
			expect(outdated.block).toBe(true);
			expect(outdated.reason).toContain("the verified eci-hook-helper does not support edit-gate");
			expect(outdated.reason).toContain("build-native-binaries.sh\" eci-hook-helper");
		} finally {
			renameSync(`${helper}.saved`, helper);
			renameSync(`${receipt}.saved`, receipt);
		}
	}, 120_000);
});
