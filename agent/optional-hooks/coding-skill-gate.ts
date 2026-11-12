// Optional hook: the shared Go helper is provisioned on first use.
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { resolveEditMode } from "@oh-my-pi/pi-coding-agent/utils/edit-mode";
import { editInspect } from "@oh-my-pi/pi-natives";
import { spawn } from "node:child_process";
import { join } from "node:path";
import { ensureHookBinary, hookAgentRoot, waitForProvision } from "../eci/native-hook-binary.ts";

type Description = { callTools: string[]; resultTools: string[]; gatedExtensions: Set<string>; gatedAgentPrefixes: string[] };
type HostEvent = { toolName: string; input: Record<string, unknown>; details?: unknown; isError?: boolean };
type AgentState = { state: unknown; initialized: boolean; tail: Promise<void> };
type Verdict = { block: true; reason: string } | undefined;

// Lowercase extension of a write target, ignoring hashline headers and read selectors; the engine owns which ones are code.
function extensionOf(path: string): string | undefined {
	return /(\.[^./\\#:\]\s]+)(?:#[a-z0-9]{4}\]\s*)?(?::(?:raw|conflicts))?$/i.exec(path)?.[1].toLowerCase();
}

// Every skill read may be required (coding, harness, ECI role modules); the Go policy decides which count.
function isSkillRead(path: string): boolean {
	return path.startsWith("skill://");
}

// Each invocation carries one complete JSON request; the engine owns its state format.
async function invoke(command: string, request?: unknown): Promise<unknown> {
	const binary = await waitForProvision(ensureHookBinary(), 10_000);
	const { promise, resolve, reject } = Promise.withResolvers<unknown>();
	const child = spawn(binary, ["coding-skill", command], { stdio: ["pipe", "pipe", "pipe"], signal: AbortSignal.timeout(10_000) });
	let stdout = "", stderr = "";
	child.stdout.setEncoding("utf8");
	child.stderr.setEncoding("utf8");
	child.stdout.on("data", (chunk: string) => { stdout += chunk; });
	child.stderr.on("data", (chunk: string) => { stderr += chunk; });
	child.on("error", reject);
	child.stdin.on("error", reject);
	child.on("close", code => {
		if (code !== 0) return reject(new Error(code === 2 && !stderr.trim() ? 'helper may not support coding-skill; rebuild with "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" eci-hook-helper and retry' : stderr.trim() || `engine exited ${code}`));
		try { resolve(JSON.parse(stdout)); } catch (error) { reject(error); }
	});
	child.stdin.end(request === undefined ? "" : JSON.stringify(request));
	return promise;
}

function description(value: unknown): Description {
	if (value === null || typeof value !== "object" || !("callTools" in value) || !("resultTools" in value) || !("gatedExtensions" in value) || !("gatedAgentPrefixes" in value) ||
		!Array.isArray(value.callTools) || !Array.isArray(value.resultTools) || !Array.isArray(value.gatedExtensions) || !Array.isArray(value.gatedAgentPrefixes) ||
		![...value.callTools, ...value.resultTools, ...value.gatedExtensions, ...value.gatedAgentPrefixes].every(item => typeof item === "string")) {
		throw new Error("invalid engine description");
	}
	return { callTools: value.callTools, resultTools: value.resultTools, gatedExtensions: new Set(value.gatedExtensions), gatedAgentPrefixes: value.gatedAgentPrefixes };
}

// Retain only read call/result pairs needed by the Go policy during hydration.
function skillReadHistory(ctx: ExtensionContext): unknown[] {
	const ids = new Set<string>();
	const history: unknown[] = [];
	for (const entry of ctx.sessionManager.getBranch()) {
		if (entry.type !== "message") continue;
		const message = entry.message;
		if (message.role === "assistant") {
			const calls = message.content.filter(part => part.type === "toolCall" && part.name === "read" &&
				typeof part.arguments.path === "string" && isSkillRead(part.arguments.path));
			for (const call of calls) if (call.type === "toolCall") ids.add(call.id);
			if (calls.length) history.push({type: "message", message: {role: "assistant", content: calls}});
		} else if (message.role === "toolResult" && ids.has(message.toolCallId)) {
			history.push({type: "message", message: {role: "toolResult", toolName: message.toolName,
				toolCallId: message.toolCallId, details: message.details, isError: message.isError}});
		}
	}
	return history;
}

// A failed helper description is retried after this window: provisioning stays off the per-call path, and a finished build or rebuild takes effect within a minute.
const describeRetryMs = 60_000;

export default function codingSkillGate(pi: ExtensionAPI): void {
	const root = hookAgentRoot();
	const binary = join(root, "eci", "bin", "eci-hook-helper");
	let cachedDescription: Promise<Description> | undefined;
	let retryAt = Infinity;
	function describe(): Promise<Description> {
		if (!cachedDescription || Date.now() >= retryAt) {
			cachedDescription = invoke("describe").then(description);
			retryAt = Infinity;
			cachedDescription.catch(() => { retryAt = Date.now() + describeRetryMs; });
		}
		return cachedDescription;
	}
	let describeWarned = false;
	const agents = new Map<string, AgentState>();
	let ownAgent: string | undefined;
	let generation = 0;
	const reset = () => { generation++; agents.clear(); };
	pi.on("session_start", (_event, ctx) => { reset(); describeWarned = false; ownAgent = ctx.agent?.id; });
	pi.on("session_switch", () => { reset(); describeWarned = false; });
	pi.on("session_tree", reset);
	pi.on("session_branch", reset);

	function enqueue<T>(ctx: ExtensionContext, operation: (slot: AgentState) => Promise<T>): Promise<T> {
		const key = JSON.stringify([ctx.sessionManager.getSessionId(), ctx.agent?.id]);
		let slot = agents.get(key);
		if (!slot) {
			slot = { state: null, initialized: false, tail: Promise.resolve() };
			agents.set(key, slot);
		}
		const active = slot;
		const next = active.tail.then(() => operation(active));
		active.tail = next.then(() => undefined, () => undefined);
		return next;
	}

	async function reduce(slot: AgentState, ctx: ExtensionContext, results: HostEvent[]): Promise<void> {
		const branch = !slot.initialized && ctx.agent?.id === ownAgent ? skillReadHistory(ctx) : undefined;
		const response = await invoke("reduce", { state: slot.state, branch, results });
		if (response === null || typeof response !== "object" || !("state" in response) || response.state === null || typeof response.state !== "object") throw new Error("invalid engine state");
		slot.state = response.state;
		slot.initialized = true;
	}

	pi.on("tool_result", async (event, ctx) => {
		if (event.toolName !== "read" || typeof event.input.path !== "string" || !isSkillRead(event.input.path)) return;
		const current = generation;
		try {
			// TODO: during describe backoff this drops skill-read results, so agents not hydrated from the transcript need a denial and re-read; reduce without describe(), since the filter above already selects skill reads, if that extra denial proves common.
			if (!(await describe()).resultTools.includes(event.toolName)) return;
			if (current !== generation) return;
			await enqueue(ctx, slot => current === generation ? reduce(slot, ctx, [{ toolName: event.toolName, input: event.input, details: event.details, isError: event.isError }]) : Promise.resolve());
		} catch (error) {
			console.error(`Coding skill gate could not record a result: ${String(error)}`);
		}
	});

	pi.on("tool_call", async (event, ctx): Promise<Verdict> => {
		const current = generation;
		let editTargets: string[] | undefined;
		// Set once the engine says this call is checked: by agent role, or by a gated write target. Only checked calls block on failure.
		let gated = false;
		// TODO: a write to conflict://<id> or conflict://* rewrites files whose markers an earlier read registered but names no file, so no path-based skill read is required, even for ~/.omp instruction files or code; fix by remembering the files of reads that register conflict ids and checking them as that write's targets.
		let paths = event.toolName === "write" && typeof event.input.path === "string" ? [event.input.path] : [];
		try {
			if (event.toolName === "edit") {
				const mode = resolveEditMode({settings: pi.pi.settings, getActiveModelString: () =>
					ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : undefined});
				const inspection = editInspect(mode, JSON.stringify(event.input));
				paths = [...inspection.paths, ...inspection.fileOps.flatMap(op => op.to === undefined ? [] : [op.to])];
				if (paths.length) editTargets = paths;
			}
			let described: Description;
			try {
				described = await describe();
			} catch (error) {
				if (!describeWarned) {
					describeWarned = true;
					const message = `Coding skill gate is not checking writes: the eci-hook-helper description is unavailable or stale (${String(error)}). Rebuild with: bash "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" eci-hook-helper`;
					console.error(message);
					ctx.ui?.notify(message, "warning");
				}
				return undefined;
			}
			if (!described.callTools.includes(event.toolName)) return undefined;
			const agentName = ctx.agent?.name;
			gated = (agentName !== undefined && described.gatedAgentPrefixes.some(prefix => agentName.startsWith(prefix))) ||
				paths.some(path => described.gatedExtensions.has(extensionOf(path) ?? ""));
			if (!gated) return undefined;
			if (current !== generation) throw new Error("session changed; retry the call");
			return await enqueue(ctx, async slot => {
				if (current !== generation) throw new Error("session changed; retry the call");
				if (!slot.initialized) await reduce(slot, ctx, []);
				if (current !== generation) throw new Error("session changed; retry the call");
				const response = await invoke("check", { state: slot.state, call: { toolName: event.toolName, input: event.input, editTargets, cwd: ctx.cwd, agentName } });
				if (current !== generation) throw new Error("session changed; retry the call");
				if (response === null || typeof response !== "object" || !("block" in response) || typeof response.block !== "boolean") throw new Error("invalid engine verdict");
				if (!response.block) return undefined;
				if (!("reason" in response) || typeof response.reason !== "string") throw new Error("invalid engine denial");
				return { block: true, reason: response.reason };
			});
		} catch (error) {
			if (!gated) return undefined;
			if (current !== generation) return { block: true, reason: "Coding skill gate: session changed; retry the call." };
			return { block: true, reason: `Coding skill gate unavailable (${String(error)}). Ensure Go and the native provisioner are available to build ${binary}, then retry; if it still fails, report this denial to your owner or the user.` };
		}
	});
}
