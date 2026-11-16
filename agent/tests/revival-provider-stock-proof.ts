import assert from "node:assert/strict";
import * as fs from "node:fs/promises";
import * as path from "node:path";
import * as os from "node:os";

// Run against the installed omp: bun revival-provider-stock-proof.ts /absolute/path/to/omp
// ECI_CONTROL: role-chain, wildcard, literal, literal-carry, auto, auto-carry,
// normalization, routing, ceiling, legacy, unset, exact-empty, child-empty, fallback-off,
// offline, pin, hook-error, install-error, first-write, metadata, model-append, effort-append, alias, enabled-current.
// A nonzero exit means qualification failed; leave the extension disabled.

const binary = process.argv[2];
assert(binary && path.isAbsolute(binary), "Usage: bun revival-provider-stock-proof.ts /absolute/path/to/omp");
const extension = path.resolve(import.meta.dir, "../extensions/revival-provider.ts");
const observer = path.join(import.meta.dir, "revival-provider-stock-observer.ts");
const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "revival-provider-")));
const home = path.join(root, "home");
const agentDir = path.join(home, ".omp", "agent");
const cwd = path.join(home, "project");
const log = path.join(root, "events.jsonl");
await fs.mkdir(path.join(agentDir, "agents"), { recursive: true });
await fs.mkdir(cwd, { recursive: true });
const requests: Array<{ provider: string; child: boolean; component: boolean; scenario: string; at: string; monotonicMs: number; body: { model?: string; provider?: { only?: string[] }; reasoning?: { effort?: string }; reasoning_effort?: string; messages?: Array<{ role?: string; content?: unknown }> } }> = [];
let scenario = "";
let parentTurn = 0;
let responseMode = "task";
let wakeChildTurns = 0;
let childEnd = Promise.withResolvers<void>();
let proofFailure = Promise.withResolvers<never>();
interface CaseOptions {
	pin?: string;
	retry?: Record<string, string[]>;
	control?: string;
	modelFallback?: boolean;
	offline?: boolean;
}
let options: CaseOptions = {};
const model = { id: "probe", name: "probe", reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 131072, maxTokens: 1024 };
function frames(reply: { text: string } | { tool: string; args: unknown }): string {
	const base = { id: "test", object: "chat.completion.chunk", created: 0, model: "probe" };
	const delta = "text" in reply ? { role: "assistant", content: reply.text } : { role: "assistant", tool_calls: [{ index: 0, id: `call_${parentTurn}`, type: "function", function: { name: reply.tool, arguments: JSON.stringify(reply.args) } }] };
	return [ { ...base, choices: [{ index: 0, delta, finish_reason: null }] }, { ...base, choices: [{ index: 0, delta: {}, finish_reason: "text" in reply ? "stop" : "tool_calls" }] }, { ...base, choices: [], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } } ].map(x => `data: ${JSON.stringify(x)}\n\n`).join("") + "data: [DONE]\n\n";
}
const server = Bun.serve({ hostname: "127.0.0.1", port: 0, async fetch(request) {
	const url = new URL(request.url);
	if (url.pathname === "/child-end" && request.method === "POST") {
		childEnd.resolve();
		return new Response("ok");
	}
	if (url.pathname === "/proof-failure" && request.method === "POST") {
		proofFailure.reject(new Error(`SDK component failure: ${await request.text()}`));
		return new Response("recorded");
	}
	if (!url.pathname.endsWith("/chat/completions")) return new Response("not found", { status: 404 });
	const body = await request.json() as { model?: string; provider?: { only?: string[] }; reasoning?: { effort?: string }; reasoning_effort?: string; messages?: Array<{ role?: string; content?: unknown }> };
	const messages = body.messages ?? [];
	const child = messages.some(m => (m.role === "system" || m.role === "developer") && JSON.stringify(m.content).includes("ECI_PROBE_CHILD"));
	const component = messages.some(m => JSON.stringify(m.content).includes("SDK_COMPONENT"));
	const provider = url.pathname.split("/")[1] ?? "";
	requests.push({ provider, child, component, scenario, at: new Date().toISOString(), monotonicMs: performance.now(), body });
	if (child && ((provider === "enabled" && (component || ["role-chain", "wildcard", "literal", "literal-carry", "auto", "auto-carry", "normalization"].includes(options.control ?? ""))) || (provider === "openrouter" && ["routing", "alias"].includes(options.control ?? ""))) && responseMode === "wake") {
		const status = 401;
		const error = { message: "Invalid API key provided", type: "invalid_request_error", code: "invalid_api_key" };
		return new Response(JSON.stringify({ error }), { status, headers: { "content-type": "application/json", "x-should-retry": "false" } });
	}
	let reply: { text: string } | { tool: string; args: unknown };
	if (component) {
		reply = { text: "SDK integration complete" };
	} else if (child) {
		reply = responseMode === "wake" && wakeChildTurns++ === 0
			? { tool: "write", args: { path: "agent://Main", content: "same probe answered" } }
			: { tool: "yield", args: { data: `${scenario}:answer` } };
	} else if (messages.some(m => m.role === "user" && JSON.stringify(m.content).includes("run stock probe"))) {
		parentTurn++;
		if (parentTurn === 1 && responseMode === "task") reply = { tool: "task", args: { context: "probe", tasks: [{ name: "Probe", agent: "eci-preset-probe", ...(options.pin ? { model: options.pin } : {}), task: "answer", solutionSpace: "one result" }] } };
		else if (parentTurn === 1 && responseMode === "wake") reply = { tool: "write", args: { path: "agent://Probe", content: "resume same probe and reply" } };
		else reply = { text: "parent complete" };
	} else reply = { text: "side request" };
	return new Response(frames(reply), { headers: { "content-type": "text/event-stream" } });
} });

async function writeConfig(disabled: string[], config: CaseOptions) {
	const providers = Object.fromEntries(["primary", "enabled", "backup", "wrong", "driver", ...(["routing", "alias"].includes(config.control ?? "") ? ["openrouter"] : [])].map(p => [p, { baseUrl: `${server.url.origin}/${p}/v1`, api: "openai-completions", auth: "none", models: p === "enabled" && config.control === "literal" ? [model, { ...model, id: "probe:high" }] : p === "enabled" && config.control === "literal-carry" ? [model, { ...model, id: "probe:max" }] : p === "enabled" && config.control === "wildcard" ? [model, { ...model, id: "alternate" }] : p === "enabled" && config.control === "normalization" ? [{ ...model, thinking: { mode: "effort", efforts: ["low"] } }] : p === "enabled" && config.control === "effort-append" ? [{ ...model, thinking: { mode: "effort", efforts: ["low", "high"], defaultLevel: "high" } }] : [model] }]));
	await Bun.write(path.join(agentDir, "models.yml"), JSON.stringify({ providers }));
	const fallbackChains = config.retry ?? (config.control === "alias" && responseMode === "task" ? { default: ["pi/proof-target:medium", "backup/probe:high"] } : { default: ["wrong/probe:low"] });
	await Bun.write(path.join(agentDir, "config.yml"), JSON.stringify({ modelRoles: { default: "driver/probe", ...(config.control === "alias" ? { "proof-target": "openrouter/probe@upstream" } : {}) }, disabledProviders: disabled, models: { offline: !!config.offline }, retry: { enabled: true, modelFallback: config.modelFallback !== false, maxRetries: 2, baseDelayMs: 20, maxDelayMs: 100, fallbackOn: "any", fallbackChains }, async: { enabled: false }, task: { agentIdleTtlMs: 0, perCallModel: "on" }, compaction: { enabled: false }, advisor: { enabled: false } }));
	const preset = config.control === "alias" ? "primary/probe:low" : ["primary/probe:low", "enabled/probe:medium", "backup/probe:high"];
	await Bun.write(path.join(agentDir, "agents", "eci-preset-probe.md"), `---\nname: eci-preset-probe\ndescription: test ECI role\nstructured: true\nmodel: ${JSON.stringify(preset)}\n---\nECI_PROBE_CHILD\n`);
}
async function run(mode: string, disabled: string[], filter: boolean, session?: string, label?: string, config: CaseOptions = {}) {
	scenario = label ?? `${mode}:${disabled.join(",")}:${filter}`; parentTurn = 0; responseMode = mode; wakeChildTurns = 0; options = config;
	childEnd = Promise.withResolvers<void>();
	proofFailure = Promise.withResolvers<never>();
	await writeConfig(disabled, config);
	const proc = Bun.spawn([binary!, "--no-title", "--no-lsp", "--no-extensions", "--extension", observer, "--extension", extension, "--mode", "rpc", ...(session ? ["--resume", session] : [])], { cwd, env: { PATH: Bun.env.PATH ?? "", HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: agentDir, ECI_PROBE_LOG: log, ECI_PROBE_SCENARIO: scenario, ECI_PROBE_NOTIFY: server.url.origin, NO_COLOR: "1" }, stdin: "pipe", stdout: "pipe", stderr: "pipe" });
	const stderr = new Response(proc.stderr).text();
	const reader = proc.stdout.getReader();
	let output = "";
	let failure: unknown;
	let completed = false;
	try {
		proc.stdin.write(`${JSON.stringify({ id: "probe", type: "prompt", message: "run stock probe" })}\n`);
		const controller = AbortSignal.timeout(30_000);
		const abort = new Promise<never>((_, reject) => controller.addEventListener("abort", () => reject(new Error("RPC probe timed out")), { once: true }));
		const read = (async () => {
			const decoder = new TextDecoder();
			let requestedState = false;
			for (;;) {
				const part = await reader.read();
				if (part.done) throw new Error("RPC closed before state response");
				output += decoder.decode(part.value, { stream: true });
				if (!requestedState && output.includes('"type":"agent_end"')) {
					requestedState = true;
					proc.stdin.write(`${JSON.stringify({ id: "probe-state", type: "get_state" })}\n`);
				}
				const stateReady = requestedState && output.slice(0, output.lastIndexOf("\n") + 1).includes('"id":"probe-state"');
				const needsFailureNotice = mode === "wake" && ["exact-empty", "child-empty", "fallback-off", "offline", "install-error", "first-write", "metadata"].includes(config.control ?? "");
				if (stateReady && (!needsFailureNotice || output.includes("Wake turn failed: No model configured"))) return;
			}
		})();
		await Promise.race([read, abort, proofFailure.promise]);
		if (mode === "wake" && ["role-chain", "wildcard", "literal", "literal-carry", "auto", "auto-carry", "normalization", "routing", "alias", "model-append", "effort-append", "ceiling", "legacy", "unset", "hook-error", "enabled-current"].includes(config.control ?? "")) await Promise.race([childEnd.promise, abort, proofFailure.promise]);
		completed = true;
	} catch (error) {
		failure = error;
	} finally {
		if (completed) {
			proc.stdin.end();
			const drain = (async () => { while (!(await reader.read()).done) {} })();
			const exited = await Promise.race([proc.exited.then(() => true), Bun.sleep(10_000).then(() => false)]);
			if (!exited) {
				console.error(`Graceful RPC shutdown timed out for ${proc.pid}; terminating owned process`);
				proc.kill();
				await proc.exited;
			}
			await drain.catch(() => {});
		} else {
			proc.kill();
			await proc.exited;
			await reader.cancel().catch(() => {});
		}
		reader.releaseLock();
	}
	const errors = await stderr;
	if (failure) throw new Error(`${String(failure)}\nstdout=${output}\nstderr=${errors}`);
	const state = output.slice(0, output.lastIndexOf("\n") + 1).split("\n").filter(Boolean).map(line => JSON.parse(line)).find(frame => frame.id === "probe-state" && frame.type === "response");
	return { scenario, output, stderr: errors, sessionFile: state?.data?.sessionFile as string | undefined };
}
const control = process.env.ECI_CONTROL ?? "role-chain";
assert(["role-chain", "wildcard", "literal", "literal-carry", "auto", "auto-carry", "normalization", "routing", "alias", "ceiling", "legacy", "unset", "exact-empty", "child-empty", "fallback-off", "offline", "pin", "hook-error", "install-error", "first-write", "metadata", "model-append", "effort-append", "enabled-current"].includes(control), "unknown ECI_CONTROL");
const results: Array<{ scenario: string; output?: string; stderr?: string; error?: string; sessionFile?: string }> = [];
let succeeded = false;
try {
	const seedConfig: CaseOptions = { control, ...(control === "pin" ? { pin: "primary/probe:low" } : {}) };
	const seed = await run("task", [], false, undefined, `${control}-seed`, seedConfig);
	results.push(seed);
	assert(seed.sessionFile, "seed has no persisted parent session");
	assert(seed.output?.includes('status=\\"completed\\"') || seed.output?.includes('status="completed"'), "seed did not complete");
	{
	const wakeConfig: CaseOptions = {
		control,
		...(control === "fallback-off" ? { modelFallback: false } : {}),
		...(control === "offline" ? { offline: true } : {}),
		...(control === "exact-empty" ? { retry: { "primary/probe:low": [], default: ["wrong/probe:low"] } } : {}),
		...(control === "wildcard" ? { retry: { "primary/probe:low": ["enabled/alternate:medium", "backup/*"], default: ["wrong/probe:low"] } } : {}),
		...(control === "literal" ? { retry: { "primary/probe:low": ["enabled/probe:high", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "auto" ? { retry: { "primary/probe:low": ["enabled/probe:auto", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "literal-carry" ? { retry: { "primary/probe:low": ["enabled/probe:max", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "auto-carry" ? { retry: { "primary/probe:low": ["enabled/probe", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "normalization" ? { retry: { "primary/probe:low": ["enabled/probe:high", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "routing" ? { retry: { "primary/probe:low": ["openrouter/probe@upstream:medium", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
		...(control === "effort-append" ? { retry: { "primary/probe:low": ["enabled/probe", "backup/probe:high"], default: ["wrong/probe:low"] } } : {}),
	};
	const wake = await run("wake", control === "enabled-current" ? [] : ["primary"], false, seed.sessionFile, `${control}-wake`, wakeConfig);
	results.push(wake);
	let second;
	if (control === "role-chain") {
		assert(wake.sessionFile, "first wake has no parent session");
		second = await run("wake", ["primary"], false, wake.sessionFile, `${control}-second-wake`, wakeConfig);
		results.push(second);
	}
	const events = (await Bun.file(log).exists()) ? await Bun.file(log).text() : "";
	const rows = Bun.JSONL.parse(events) as Array<{ scenario: string; detail: { event: string; sessionId?: string; agent?: { id?: string }; entries?: Array<{ type: string }>; [key: string]: unknown } }>;
	const findEvent = (phase: string, event: string) => rows.find(row => row.scenario === `${control}-${phase}` && row.detail.event === event && (event !== "session_start" || row.detail.agent?.id === "Probe"))?.detail;
	const before = findEvent("seed", "session_start");
	const after = findEvent("wake", "session_start");
	const childRequests = requests.filter(request => request.child && !request.component && request.scenario === `${control}-wake`);
	if (control !== "enabled-current") assert(!childRequests.some(request => request.provider === "primary"), "disabled primary received a child request");
	assert(!childRequests.some(request => request.provider === "wrong" || request.provider === "driver"), "child used the default/parent chain");
	if (control === "pin") {
		assert.equal(childRequests.length, 0, "pinned child was substituted");
		assert.match(wake.output ?? "", /per-call model.*no longer available|per-call worker revives only on its exact saved model/i, "cold pinned refusal was not observed");
	} else {
		assert(before?.sessionId && before.sessionId === after?.sessionId, "cold wake changed child session identity");
		const seedHistory = findEvent("seed", "probe_model_history")?.entries?.find(entry => entry.type === "session_init");
		const wakeHistory = findEvent("wake", "probe_model_history")?.entries?.find(entry => entry.type === "session_init");
		assert(seedHistory && wakeHistory && JSON.stringify(seedHistory) === JSON.stringify(wakeHistory), "cold wake changed the persisted session initialization");
		if (control === "enabled-current") {
			const prior = findEvent("wake", "current_before");
			const later = findEvent("wake", "current_after");
			assert(prior?.model, "revival did not restore a current model");
			assert(later, "child did not start");
			assert.deepEqual({ model: later.model, effort: later.effort, roles: later.roles, entries: later.entries }, { model: prior.model, effort: prior.effort, roles: prior.roles, entries: prior.entries });
			assert.equal(childRequests[0]?.provider, "primary");
		} else if (control === "ceiling" || control === "legacy" || control === "unset") {
			const component = requests.filter(request => request.component && request.scenario === `${control}-wake`);
			const selected = findEvent("wake", "component_selected");
			const settled = findEvent("wake", "component_settled");
			assert(selected?.model && JSON.stringify(selected.model).includes('"provider":"enabled"'), "SDK recovery did not select enabled");
			assert(settled?.model && JSON.stringify(settled.model).includes('"provider":"backup"'), "SDK fallback did not settle on backup");
			assert.deepEqual(settled.fallbackEvents, ["retry_fallback_applied", "retry_fallback_succeeded"]);
			assert(JSON.stringify(settled.assistant).includes("SDK integration complete"), "SDK assistant did not complete");
			assert(findEvent("wake", "owners_before") && findEvent("wake", "owners_after_create") && findEvent("wake", "owners_after_dispose"), "owner preservation evidence missing");
			assert.equal(findEvent("wake", "child_success")?.token, `${control}-wake:answer`, "original Probe did not finish after SDK disposal");
			assert(childRequests.some(request => request.provider === "enabled"), "original Probe did not resume independently");
			assert.deepEqual(component.map(request => request.provider), ["enabled", "backup"]);
			if (control === "ceiling") {
				assert.equal(findEvent("wake", "component_selected")?.effort, "low");
				assert.equal(findEvent("wake", "component_settled")?.effort, "low");
				assert(component.every(request => request.body.reasoning_effort === "low"), "SDK session exceeded its ceiling");
			} else if (control === "unset") {
				assert(findEvent("wake", "component_before"), "unset pre-state missing");
				assert.equal(findEvent("wake", "component_before")?.effort, undefined);
				assert.equal(findEvent("wake", "component_selected")?.effort, undefined);
				assert.equal(component[0]?.body.reasoning_effort, undefined);
			}
		} else if (control === "hook-error") {
			const failure = findEvent("wake", "child_extension_error");
			assert(failure, "child extension error was not observed");
			assert.match(JSON.stringify(failure.error), /revival-provider\.ts.*before_agent_start.*child Probe.*enabled\/probe:medium.*model application.*requested effort medium.*observed model enabled\/probe.*observed effort medium.*injected post-application failure/);
			const injection = findEvent("wake", "application_injection");
			assert(injection, "post-application injection was not executed");
			assert.deepEqual(failure.model, injection.model);
			assert.equal(failure.effort, injection.effort);
			assert(failure.model, "selected model residue missing");
			assert.equal(childRequests[0]?.provider, "enabled", "native execution did not continue after the hook error");
			assert.equal(findEvent("wake", "child_success")?.token, `${control}-wake:answer`);
		} else if (control === "install-error") {
			const failure = findEvent("wake", "child_extension_error");
			const prior = findEvent("wake", "failure_before");
			assert(failure && prior, "policy failure snapshots missing");
			assert.match(JSON.stringify(failure.error), /revival-provider\.ts.*before_agent_start.*child Probe.*enabled\/probe:medium.*installation.*settings may have changed.*injected second settings write failure/);
			assert.notDeepEqual(failure.roles, prior.roles, "first override did not persist");
			assert.deepEqual(failure.chains, prior.chains, "failed second override changed chain");
			assert.deepEqual(failure.model, prior.model);
			assert.equal(failure.effort, prior.effort);
			assert.equal(findEvent("wake", "first_policy_write")?.primary, "enabled/probe:medium");
			assert.equal(findEvent("wake", "first_policy_write")?.sameScope, true);
			assert.equal(childRequests.length, 0);
		} else if (["first-write", "metadata", "model-append", "effort-append"].includes(control)) {
			const prior = findEvent("wake", "seam_before");
			const failure = findEvent("wake", "seam_error");
			assert(prior && failure, "seam snapshots missing");
			assert.equal(failure.fired, 1, "injection did not fire exactly once");
			const messages: Record<string, string> = { "first-write": "injected first policy write failure", metadata: "injected metadata refresh failure", "model-append": "injected model history failure", "effort-append": "injected thinking history failure" };
			const stage = control === "first-write" ? "policy installation" : control === "effort-append" ? "effort application" : "model application";
			const candidate = control === "effort-append" ? "enabled/probe" : "enabled/probe:medium";
			const error = JSON.stringify(failure.error);
			assert(error.includes("revival-provider.ts") && error.includes("before_agent_start") && error.includes("child Probe") && error.includes(candidate) && error.includes(stage) && error.includes(messages[control]), "contextual seam error missing");
			const requested = control === "effort-append" ? "low" : "medium";
			const observedModel = ["model-append", "effort-append"].includes(control) ? "enabled/probe" : "none";
			assert(error.includes(`requested effort ${requested}`) && error.includes(`observed model ${observedModel}`) && error.includes(`observed effort ${String(failure.effort)}`), "requested or observed seam state missing");
			const beforeRoles = prior.roles as Record<string, unknown>;
			const afterRoles = failure.roles as Record<string, unknown>;
			const beforeChains = prior.chains as Record<string, unknown>;
			const afterChains = failure.chains as Record<string, unknown>;
			const otherEntries = (values: Record<string, unknown>) => Object.fromEntries(Object.entries(values).filter(([key]) => key !== "subagent:Probe"));
			assert.deepEqual(otherEntries(afterRoles), otherEntries(beforeRoles), "unrelated roles changed");
			assert.deepEqual(otherEntries(afterChains), otherEntries(beforeChains), "unrelated chains changed");
			if (control === "first-write") {
				assert.deepEqual(failure.roles, prior.roles);
				assert.deepEqual(failure.chains, prior.chains);
				assert.deepEqual(failure.model, prior.model);
				assert.equal(failure.effort, prior.effort);
				assert.deepEqual(failure.history, prior.history);
				assert.equal(childRequests.length, 0);
			} else {
				assert.equal(afterRoles["subagent:Probe"], candidate, "installed primary missing");
				assert.deepEqual(afterChains["subagent:Probe"], ["backup/probe:high"], "remaining chain changed");
				if (control === "metadata") {
					assert.deepEqual(failure.model, prior.model);
					assert.deepEqual(failure.history, prior.history);
					assert.equal(childRequests.length, 0);
				} else {
					assert(JSON.stringify(failure.model).includes('"provider":"enabled"'), "selected model residue missing");
					assert.equal(findEvent("wake", "child_success")?.token, `${control}-wake:answer`, "native continuation did not finish");
					if (control === "model-append") assert.deepEqual(failure.history, prior.history);
					else {
						const afterApply = findEvent("wake", "seam_after_apply");
						assert(afterApply && afterApply.effort === "high", "real application did not set high effort");
						assert.equal(failure.effort, "low");
						assert.deepEqual(failure.history, afterApply.history, "failed final history append changed the branch");
					}
				}
			}
		} else if (!["role-chain", "wildcard", "literal", "literal-carry", "auto", "auto-carry", "normalization", "routing", "alias"].includes(control)) {
			assert((wake.output ?? "").includes("Wake turn failed: No model configured"), "negative child terminal failure missing");
			assert.equal(childRequests.length, 0, `${control} dispatched a child request`);
		} else {
			assert.equal(childRequests[0]?.provider, ["routing", "alias"].includes(control) ? "openrouter" : "enabled", "first child target was not authorized");
			if (control === "routing" || control === "alias") {
				assert.equal(childRequests[0]?.body.model, "probe", "routed model identity changed");
				assert.deepEqual(childRequests[0]?.body.provider?.only, ["upstream"], "upstream routing was lost");
				assert.equal(childRequests[0]?.body.reasoning?.effort, "medium", "OpenRouter wire effort mismatch");
				if (control === "alias") {
					assert(JSON.stringify(seedHistory).includes("pi/proof-target:medium"), "raw inherited alias was not persisted");
					const installed = findEvent("wake", "alias_role");
					assert.equal(installed?.primary, "pi/proof-target:medium", "raw child role was not retained");
					assert.deepEqual(installed?.chain, ["backup/probe:high"], "alias continuation chain changed");
				}
			} else if (control === "auto" || control === "auto-carry") {
				assert(rows.some(row => row.scenario === `${control}-wake` && row.detail.event === "provider_request" && row.detail.effort === "auto" && JSON.stringify(row.detail.model).includes('"provider":"enabled"')), "configured auto effort was not retained");
			} else {
				assert.equal(childRequests[0]?.body.reasoning_effort, control === "literal" ? "high" : ["literal-carry", "normalization"].includes(control) ? "low" : "medium", "enabled wire effort mismatch");
			}
			if (control === "literal") assert.equal(childRequests[0]?.body.model, "probe:high", "literal level-shaped model id was parsed as effort");
			if (control === "literal-carry" || control === "auto-carry") {
				const effort = control === "literal-carry" ? "low" : "auto";
				assert.equal(findEvent("wake", "pre_recovery_effort")?.effort, effort);
				assert(rows.some(row => row.scenario === `${control}-wake` && row.detail.event === "provider_request" && row.detail.effort === effort && JSON.stringify(row.detail.model).includes('"provider":"enabled"')), "carried configured effort missing");
				if (control === "literal-carry") assert.equal(childRequests[0]?.body.model, "probe:max");
			}
			const backup = childRequests.find(request => request.provider === "backup");
			assert(backup, "failed enabled target did not reach the child backup");
			assert.equal(backup.body.reasoning_effort, control === "wildcard" ? "medium" : "high", "backup wire effort mismatch");
			if (control === "wildcard") assert.equal(backup.body.model, "probe", "remaining wildcard lost original selector identity");
			assert(rows.some(row => row.scenario === `${control}-wake` && row.detail.event === "after_provider_response" &&
				row.detail.agent?.id === "Probe" && row.detail.provider === "backup" && row.detail.status === 200), "backup response was not observed");
			assert.equal(findEvent("wake", "child_success")?.token, `${control}-wake:answer`, "wake lacks its successful yield");
			if (control === "role-chain") {
				const secondStart = findEvent("second-wake", "session_start");
				assert.equal(secondStart?.sessionId, before.sessionId, "second cold wake changed child identity");
				const secondHistory = findEvent("second-wake", "probe_model_history")?.entries?.find(entry => entry.type === "session_init");
				assert.deepEqual(secondHistory, seedHistory, "second cold wake changed persisted session initialization");
				assert.equal(findEvent("second-wake", "child_success")?.token, `${control}-second-wake:answer`, "second wake lacks its own successful yield");
				const secondRequests = requests.filter(request => request.child && request.scenario === `${control}-second-wake`);
				assert(!secondRequests.some(request => ["primary", "driver", "wrong"].includes(request.provider)), "second wake used disabled or parent/default route");
				assert(secondRequests.some(request => request.provider === "backup" && request.body.reasoning_effort === "high"), "second wake did not reach backup:high");
				assert(second, "second wake missing");
			}
		}
	}
	console.log(JSON.stringify({ control, results, requests, events }, null, 2));
	succeeded = true;
	}
} catch (error) {
	const events = (await Bun.file(log).exists()) ? await Bun.file(log).text() : "";
	console.log(JSON.stringify({ control, root, results, requests, events, error: String(error) }, null, 2));
	process.exitCode = 1;
} finally {
	server.stop(true);
	if (succeeded) await fs.rm(root, { recursive: true, force: true });
}
