// Optional hook: install only after separately provisioning its Go policy engine.
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { resolveEditMode } from "@oh-my-pi/pi-coding-agent/utils/edit-mode";
import { editInspect } from "@oh-my-pi/pi-natives";
import { getAgentDir } from "@oh-my-pi/pi-utils";
import { spawn } from "node:child_process";
import { join } from "node:path";

type Description = { callTools: string[]; resultTools: string[] };
type HostEvent = { toolName: string; input: Record<string, unknown>; details?: unknown; isError?: boolean };
type AgentState = { state: unknown; initialized: boolean; tail: Promise<void> };
type Verdict = { block: true; reason: string } | undefined;

// Each invocation carries one complete JSON request; the engine owns its state format.
function invoke(binary: string, command: string, request?: unknown): Promise<unknown> {
	const { promise, resolve, reject } = Promise.withResolvers<unknown>();
	const child = spawn(binary, [command], { stdio: ["pipe", "pipe", "pipe"], signal: AbortSignal.timeout(10_000) });
	let stdout = "", stderr = "";
	child.stdout.setEncoding("utf8");
	child.stderr.setEncoding("utf8");
	child.stdout.on("data", (chunk: string) => { stdout += chunk; });
	child.stderr.on("data", (chunk: string) => { stderr += chunk; });
	child.on("error", reject);
	child.stdin.on("error", reject);
	child.on("close", code => {
		if (code !== 0) return reject(new Error(stderr.trim() || `engine exited ${code}`));
		try { resolve(JSON.parse(stdout)); } catch (error) { reject(error); }
	});
	child.stdin.end(request === undefined ? "" : JSON.stringify(request));
	return promise;
}

function description(value: unknown): Description {
	if (value === null || typeof value !== "object" || !("callTools" in value) || !("resultTools" in value) ||
		!Array.isArray(value.callTools) || !Array.isArray(value.resultTools) ||
		!value.callTools.every(tool => typeof tool === "string") || !value.resultTools.every(tool => typeof tool === "string")) {
		throw new Error("invalid engine description");
	}
	return { callTools: value.callTools, resultTools: value.resultTools };
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
				typeof part.arguments.path === "string" && part.arguments.path.startsWith("skill://coding-"));
			for (const call of calls) if (call.type === "toolCall") ids.add(call.id);
			if (calls.length) history.push({type: "message", message: {role: "assistant", content: calls}});
		} else if (message.role === "toolResult" && ids.has(message.toolCallId)) {
			history.push({type: "message", message: {role: "toolResult", toolName: message.toolName,
				toolCallId: message.toolCallId, details: message.details, isError: message.isError}});
		}
	}
	return history;
}

export default function codingSkillGate(pi: ExtensionAPI): void {
	const root = getAgentDir();
	const binary = join(root, "optional-hooks", "coding-skill-gate");
	const describe = invoke(binary, "describe").then(description);
	// Report startup failures even if no callback arrives.
	describe.catch(error => console.error(`Coding skill gate unavailable: ${String(error)}`));
	const agents = new Map<string, AgentState>();
	let ownAgent: string | undefined;
	let generation = 0;
	const reset = () => { generation++; agents.clear(); };
	pi.on("session_start", (_event, ctx) => { reset(); ownAgent = ctx.agent?.id; });
	pi.on("session_switch", reset);
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
		const response = await invoke(binary, "reduce", { state: slot.state, branch, results });
		if (response === null || typeof response !== "object" || !("state" in response) || response.state === null || typeof response.state !== "object") throw new Error("invalid engine state");
		slot.state = response.state;
		slot.initialized = true;
	}

	pi.on("tool_result", async (event, ctx) => {
		if (event.toolName !== "read" || typeof event.input.path !== "string" || !event.input.path.startsWith("skill://coding-")) return;
		const current = generation;
		try {
			if (!(await describe).resultTools.includes(event.toolName)) return;
			if (current !== generation) return;
			await enqueue(ctx, slot => current === generation ? reduce(slot, ctx, [{ toolName: event.toolName, input: event.input, details: event.details, isError: event.isError }]) : Promise.resolve());
		} catch (error) {
			console.error(`Coding skill gate could not record a result: ${String(error)}`);
		}
	});

	pi.on("tool_call", async (event, ctx): Promise<Verdict> => {
		const current = generation;
		let editTargets: string[] | undefined;
		let paths = event.toolName === "write" && typeof event.input.path === "string" ? [event.input.path] : [];
		try {
			if (event.toolName === "edit") {
				const mode = resolveEditMode({settings: pi.pi.settings, getActiveModelString: () =>
					ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : undefined});
				const inspection = editInspect(mode, JSON.stringify(event.input));
				paths = [...inspection.paths, ...inspection.fileOps.flatMap(op => op.to === undefined ? [] : [op.to])];
				if (paths.length) editTargets = paths;
			}
			if (!(await describe).callTools.includes(event.toolName)) return undefined;
			if (current !== generation) throw new Error("session changed; retry the call");
			return await enqueue(ctx, async slot => {
				if (current !== generation) throw new Error("session changed; retry the call");
				if (!slot.initialized) await reduce(slot, ctx, []);
				if (current !== generation) throw new Error("session changed; retry the call");
				const response = await invoke(binary, "check", { state: slot.state, call: { toolName: event.toolName, input: event.input, editTargets } });
				if (current !== generation) throw new Error("session changed; retry the call");
				if (response === null || typeof response !== "object" || !("block" in response) || typeof response.block !== "boolean") throw new Error("invalid engine verdict");
				if (!response.block) return undefined;
				if (!("reason" in response) || typeof response.reason !== "string") throw new Error("invalid engine denial");
				return { block: true, reason: response.reason };
			});
		} catch (error) {
			if (!paths.some(path => /\.(?:go|py)(?:#[a-f0-9]{4}\])?(?::(?:raw|conflicts))?$/i.test(path))) return undefined;
			if (current !== generation) return { block: true, reason: "Coding skill gate: session changed; retry the call." };
			return { block: true, reason: `Coding skill gate unavailable (${String(error)}). Restore the separately provisioned engine at ${binary} and restart, or remove the optional hook and restart.` };
		}
	});
}
