import { appendFile } from "node:fs/promises";
import { appendFileSync } from "node:fs";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { AgentRegistry } from "@oh-my-pi/pi-coding-agent/registry/agent-registry";
import { AgentLifecycleManager } from "@oh-my-pi/pi-coding-agent/registry/agent-lifecycle";
import { isLocalAgentSession } from "@oh-my-pi/pi-coding-agent/session/agent-session-handle";
import { lookup } from "@oh-my-pi/pi-coding-agent/config/registry";
import { installRetryFallbackRole } from "@oh-my-pi/pi-coding-agent/session/retry-fallback-chains";
import { createAgentSession } from "@oh-my-pi/pi-coding-agent";
import { Settings } from "@oh-my-pi/pi-coding-agent/config/settings";
import { SessionManager } from "@oh-my-pi/pi-coding-agent/session/session-manager";
import { resolveModelOverride } from "@oh-my-pi/pi-coding-agent/config/model-resolver";
import { getRetryFallbackChains, resolveRetryFallbackChainKey, findRetryFallbackCandidates } from "@oh-my-pi/pi-coding-agent/session/retry-fallback-chains";
import { recoverModelLessRevival } from "../extensions/revival-provider.ts";
import { Effort } from "@oh-my-pi/pi-ai";

export default function observe(pi: ExtensionAPI) {
  const record = async (detail: unknown) => appendFile(process.env.ECI_PROBE_LOG!, `${JSON.stringify({ scenario: process.env.ECI_PROBE_SCENARIO, detail })}\n`);
  const signalFailure = async (error: unknown) => {
    const detail = { event: "component_failure", error: error instanceof Error ? error.stack ?? error.message : String(error), causes: error instanceof AggregateError ? error.errors.map(cause => cause instanceof Error ? cause.stack ?? cause.message : String(cause)) : undefined };
    await record(detail);
    await fetch(`${process.env.ECI_PROBE_NOTIFY}/proof-failure`, { method: "POST", body: JSON.stringify(detail) });
  };
  pi.on("session_start", async (_event, ctx) => {
    await record({ event: "session_start", agent: ctx.agent, sessionId: ctx.sessionManager.getSessionId(), model: ctx.model });
  });
  pi.on("before_agent_start", async (_event, ctx) => {
    if (ctx.agent.id !== "Probe") return;
    const proofSession = AgentRegistry.global().get(ctx.agent.id)?.session;
    const scenario = process.env.ECI_PROBE_SCENARIO;
    if (!proofSession || !isLocalAgentSession(proofSession)) {
      const error = new Error("proof child session unavailable");
      if (scenario === "ceiling-wake" || scenario === "legacy-wake" || scenario === "unset-wake") await signalFailure(error);
      throw error;
    }
    if (scenario === "ceiling-wake" || scenario === "legacy-wake" || scenario === "unset-wake") try {
      const settings = Settings.isolated({
        "disabledProviders": ["primary"],
        "retry.modelFallback": true,
        "retry.fallbackOn": "any",
        "retry.maxRetries": 2,
        "retry.baseDelayMs": 20,
        "retry.maxDelayMs": 100,
        "compaction.enabled": false,
      });
      const componentId = "ceiling";
      const componentRegistry = new AgentRegistry();
      const role = `subagent:${componentId}`;
      const chain = [scenario === "unset-wake" ? "enabled/probe" : "enabled/probe:high", "backup/probe:high"];
      installRetryFallbackRole(settings, role, { primary: "primary/probe:low", chain });
      const registry = AgentRegistry.global();
      const mainRef = registry.get("Main");
      const probeRef = registry.get(ctx.agent.id);
      assert(mainRef && probeRef, "component owner refs are unavailable");
      const lifecycle = AgentLifecycleManager.global();
      const mainSession = mainRef.session;
      const probeSession = probeRef.session;
      assert(mainSession && probeSession && isLocalAgentSession(mainSession) && isLocalAgentSession(probeSession), "owner sessions are unavailable");
      assert(lifecycle.manages(registry) && lifecycle.has(ctx.agent.id, probeRef), "Probe lifecycle ownership is unavailable");
      const snapshot = () => ({
        main: { generation: mainRef.generation, status: mainRef.status, sessionId: mainSession.sessionManager.getSessionId(), sessionFile: mainSession.sessionFile, disposed: mainSession.isDisposed, roles: mainSession.settings.getModelRoles(), chains: lookup("retry.fallbackChains")?.get(mainSession.settings), disabled: lookup("disabledProviders")?.get(mainSession.settings) },
        probe: { generation: probeRef.generation, status: probeRef.status, sessionId: probeSession.sessionManager.getSessionId(), sessionFile: probeSession.sessionFile, disposed: probeSession.isDisposed, roles: probeSession.settings.getModelRoles(), chains: lookup("retry.fallbackChains")?.get(probeSession.settings), disabled: lookup("disabledProviders")?.get(probeSession.settings) },
      });
      const before = structuredClone(snapshot());
      assert.equal(before.main.disposed, false);
      assert.equal(before.probe.disposed, false);
      const verifyOwners = () => {
        assert.equal(AgentRegistry.global(), registry);
        assert.equal(AgentLifecycleManager.global(), lifecycle);
        assert(lifecycle.manages(registry) && lifecycle.has(ctx.agent.id, probeRef));
        assert.equal(registry.get("Main"), mainRef);
        assert.equal(registry.get(ctx.agent.id), probeRef);
        assert.equal(mainRef.session, mainSession);
        assert.equal(probeRef.session, probeSession);
        assert.deepEqual(snapshot(), before);
      };
      await record({ event: "owners_before", ...before });
      const { session } = await createAgentSession({
        cwd: ctx.cwd,
        settings,
        modelRegistry: proofSession.modelRegistry,
        sessionManager: SessionManager.inMemory(ctx.cwd),
        agentRegistry: componentRegistry,
        agentId: componentId,
        parentTaskPrefix: componentId,
        taskDepth: ctx.agent.depth + 1,
        parentAgentId: ctx.agent.id,
        bindProcessState: false,
        modelPattern: "missing/no-such-model",
        thinkingLevelCeiling: scenario === "ceiling-wake" ? Effort.Low : undefined,
        systemPrompt: "ECI_PROBE_CHILD SDK_COMPONENT",
        disableExtensionDiscovery: true,
        toolNames: [],
        cacheWarming: false,
      });
      let primaryFailure: unknown;
      try {
        verifyOwners();
        const componentRef = componentRegistry.get(componentId);
        assert(componentRef && componentRef.kind === "sub" && componentRef.parentId === ctx.agent.id && componentRef.session === session, "component registration is not isolated");
        assert.equal(registry.get(componentId), undefined);
        await record({ event: "owners_after_create", ...snapshot(), component: { kind: componentRef.kind, parentId: componentRef.parentId } });
        if (session.model) throw new Error("ceiling integration expected a model-less SDK session");
        if (scenario === "unset-wake") {
          session.setThinkingLevel(undefined);
          await record({ event: "component_before", effort: session.configuredThinkingLevel() });
        }
        const context = { agent: { id: componentId }, sessionManager: session.sessionManager };
        const init = scenario === "legacy-wake"
          ? { resolvedModel: "primary/probe:low", modelRole: role }
          : { retryFallback: { primary: "primary/probe:low", chain } };
        const api = {
          session: () => componentRegistry.get(componentId)?.session,
          local: isLocalAgentSession,
          setting: (name: string, scope: Settings) => {
            const handle = lookup(name);
            if (!handle) throw new Error(`ceiling proof setting unavailable: ${name}`);
            return handle.get(scope);
          },
          chains: getRetryFallbackChains,
          chainKey: resolveRetryFallbackChainKey,
          candidates: findRetryFallbackCandidates,
          resolve: (selector: string) => resolveModelOverride([selector], session.modelRegistry, settings),
          install: installRetryFallbackRole,
        };
        await recoverModelLessRevival(context, init, api as never);
        const fallbackEvents: string[] = [];
        const unsubscribe = session.subscribe(event => {
          if (event.type === "retry_fallback_applied" || event.type === "retry_fallback_succeeded") fallbackEvents.push(event.type);
        });
        await record({ event: "component_selected", model: session.model, effort: session.configuredThinkingLevel() });
        await session.prompt("SDK integration");
        await session.waitForIdle();
        unsubscribe();
        const last = session.messages.findLast(message => message.role === "assistant");
        assert(last && last.stopReason === "stop" && JSON.stringify(last.content).includes("SDK integration complete"), "SDK assistant did not complete successfully");
        await record({ event: "component_settled", model: session.model, effort: session.configuredThinkingLevel(), fallbackEvents, assistant: last.content });
      } catch (error) {
        primaryFailure = error;
        throw error;
      } finally {
        try {
          await session.dispose();
          verifyOwners();
          assert.equal(componentRegistry.get(componentId), undefined, "SDK disposal retained component registration");
          await record({ event: "owners_after_dispose", ...snapshot() });
        } catch (cleanupFailure) {
          if (primaryFailure !== undefined) throw new AggregateError([primaryFailure, cleanupFailure], "SDK component and cleanup both failed");
          throw cleanupFailure;
        }
      }
    } catch (error) {
      await signalFailure(error);
      throw error;
    }
    const seam = scenario?.endsWith("-wake") && ["first-write", "metadata", "model-append", "effort-append"].includes(scenario.slice(0, -5)) ? scenario.slice(0, -5) : undefined;
    if (seam) {
      if (_event.reason !== "revive" || proofSession.sessionManager.getSessionId() !== ctx.sessionManager.getSessionId()) throw new Error("seam requires the exact revived child");
      if (seam === "effort-append") proofSession.setThinkingLevel(Effort.Low);
      const runner = proofSession.extensionRunner;
      const chains = lookup("retry.fallbackChains");
      if (!runner || !chains) throw new Error("seam observation unavailable");
      const history = () => structuredClone(proofSession.sessionManager.getBranch().filter(entry => entry.type === "model_change" || entry.type === "thinking_level_change"));
      const snapshot = () => ({ roles: proofSession.settings.getModelRoles(), chains: chains.get(proofSession.settings), model: proofSession.model, effort: proofSession.configuredThinkingLevel(), history: history() });
      await record({ event: "seam_before", ...snapshot() });
      const restores: Array<() => void> = [];
      let fired = 0;
      const restore = () => { for (const undo of restores.splice(0).reverse()) undo(); };
      runner.onError(error => {
        if (!error.extensionPath.endsWith("/revival-provider.ts") || error.event !== "before_agent_start") return;
        restore();
        appendFileSync(process.env.ECI_PROBE_LOG!, `${JSON.stringify({ scenario, detail: { event: "seam_error", error, fired, ...snapshot() } })}\n`);
      });
      if (seam === "first-write") {
        const setting = lookup("modelRoles");
        if (!setting) throw new Error("model roles setting unavailable");
        const original = setting.override;
        const wrapped: typeof original = function(this: typeof setting, scope, value) {
          if (scope !== proofSession.settings) return Reflect.apply(original, this, [scope, value]);
          setting.override = original;
          fired++;
          throw new Error("injected first policy write failure");
        };
        setting.override = wrapped;
        restores.push(() => { if (setting.override === wrapped) setting.override = original; });
      } else {
        const originalApply = proofSession.applyRoleModel;
        const wrappedApply: typeof originalApply = async function(this: typeof proofSession, entry) {
          proofSession.applyRoleModel = originalApply;
          let finalArmed = false;
          try {
            if (seam === "metadata") {
              const registry = proofSession.modelRegistry;
              const original = registry.refreshSelectedModelMetadata;
              const wrapped: typeof original = async function(this: typeof registry, model) {
                if (model.provider !== "enabled" || model.id !== "probe") return Reflect.apply(original, this, [model]);
                registry.refreshSelectedModelMetadata = original;
                fired++;
                throw new Error("injected metadata refresh failure");
              };
              registry.refreshSelectedModelMetadata = wrapped;
              restores.push(() => { if (registry.refreshSelectedModelMetadata === wrapped) registry.refreshSelectedModelMetadata = original; });
            } else if (seam === "model-append") {
              const manager = proofSession.sessionManager;
              const original = manager.appendModelChange;
              const wrapped: typeof original = function(...args) {
                manager.appendModelChange = original;
                fired++;
                throw new Error("injected model history failure");
              };
              manager.appendModelChange = wrapped;
              restores.push(() => { if (manager.appendModelChange === wrapped) manager.appendModelChange = original; });
            }
            const result = await Reflect.apply(originalApply, this, [entry]);
            if (seam === "effort-append") {
              await record({ event: "seam_after_apply", ...snapshot() });
              const manager = proofSession.sessionManager;
              const original = manager.appendThinkingLevelChange;
              const wrapped: typeof original = function(...args) {
                manager.appendThinkingLevelChange = original;
                fired++;
                throw new Error("injected thinking history failure");
              };
              manager.appendThinkingLevelChange = wrapped;
              restores.push(() => { if (manager.appendThinkingLevelChange === wrapped) manager.appendThinkingLevelChange = original; });
              finalArmed = true;
            }
            return result;
          } finally {
            if (!finalArmed) restore();
          }
        };
        proofSession.applyRoleModel = wrappedApply;
        restores.push(() => { if (proofSession.applyRoleModel === wrappedApply) proofSession.applyRoleModel = originalApply; });
      }
      pi.on("session_shutdown", () => { restore(); });
      pi.on("agent_start", () => { restore(); });
    }
    if (scenario === "hook-error-wake" || scenario === "install-error-wake") {
      const runner = proofSession.extensionRunner;
      const chains = lookup("retry.fallbackChains");
      if (!runner || !chains) throw new Error("proof error observation unavailable");
      const snapshot = () => ({ roles: proofSession.settings.getModelRoles(), chains: chains.get(proofSession.settings), model: proofSession.model, effort: proofSession.configuredThinkingLevel() });
      await record({ event: "failure_before", ...snapshot() });
      runner.onError(error => {
        appendFileSync(process.env.ECI_PROBE_LOG!, `${JSON.stringify({ scenario, detail: { event: "child_extension_error", error, ...snapshot() } })}\n`);
      });
    }
    if (["literal-carry-wake", "auto-carry-wake"].includes(scenario ?? "")) {
      proofSession.setThinkingLevel(scenario === "auto-carry-wake" ? "auto" : Effort.Low);
      await record({ event: "pre_recovery_effort", effort: proofSession.configuredThinkingLevel() });
    }
    if (process.env.ECI_PROBE_SCENARIO === "enabled-current-wake") {
      const session = AgentRegistry.global().get(ctx.agent.id)?.session;
      if (!session || !isLocalAgentSession(session)) throw new Error("proof child session unavailable");
      await record({ event: "current_before", model: session.model, effort: session.configuredThinkingLevel(), roles: session.settings.getModelRoles(), entries: ctx.sessionManager.getBranch().filter(entry => entry.type === "model_change" || entry.type === "thinking_level_change") });
    }
    if (process.env.ECI_PROBE_SCENARIO === "hook-error-wake") {
      const session = AgentRegistry.global().get(ctx.agent.id)?.session;
      if (!session || !isLocalAgentSession(session)) throw new Error("proof child session unavailable");
      const apply = session.applyRoleModel.bind(session);
      session.applyRoleModel = async entry => {
        await apply(entry);
        await record({ event: "application_injection", model: session.model, effort: session.configuredThinkingLevel() });
        throw new Error("injected post-application failure");
      };
    }
    if (process.env.ECI_PROBE_SCENARIO === "child-empty-wake") {
      const session = AgentRegistry.global().get(ctx.agent.id)?.session;
      if (!session || !isLocalAgentSession(session)) throw new Error("proof child session unavailable");
      installRetryFallbackRole(session.settings, `subagent:${ctx.agent.id}`, { primary: "primary/probe:low", chain: [] });
    }
    await record({ event: "probe_model_history", agent: ctx.agent, entries: ctx.sessionManager.getBranch().filter(entry => entry.type === "session_init") });
    if (process.env.ECI_PROBE_SCENARIO === "install-error-wake") {
      const setting = lookup("retry.fallbackChains");
      if (!setting) throw new Error("proof fallback setting unavailable");
      const original = setting.override.bind(setting);
      setting.override = (scope, value) => {
        const session = AgentRegistry.global().get(ctx.agent.id)?.session;
        if (!session || !isLocalAgentSession(session) || scope !== session.settings) return original(scope, value);
        setting.override = original;
        appendFileSync(process.env.ECI_PROBE_LOG!, `${JSON.stringify({ scenario: process.env.ECI_PROBE_SCENARIO, detail: { event: "first_policy_write", primary: session.settings.getModelRole(`subagent:${ctx.agent.id}`), sameScope: true } })}\n`);
        throw new Error("injected second settings write failure");
      };
    }
  });
  pi.on("agent_start", async (_event, ctx) => {
    if (ctx.agent.id !== "Probe" || process.env.ECI_PROBE_SCENARIO !== "enabled-current-wake") return;
    const session = AgentRegistry.global().get(ctx.agent.id)?.session;
    if (!session || !isLocalAgentSession(session)) throw new Error("proof child session unavailable");
    await record({ event: "current_after", model: session.model, effort: session.configuredThinkingLevel(), roles: session.settings.getModelRoles(), entries: ctx.sessionManager.getBranch().filter(entry => entry.type === "model_change" || entry.type === "thinking_level_change") });
  });
  pi.on("before_provider_request", async (_event, ctx) => {
    if (ctx.agent.id !== "Probe") return;
    const session = AgentRegistry.global().get(ctx.agent.id)?.session;
    if (!session || !isLocalAgentSession(session)) throw new Error("proof child session unavailable");
    await record({ event: "provider_request", model: session.model, effort: session.configuredThinkingLevel() });
    if (process.env.ECI_PROBE_SCENARIO === "alias-wake" && session.model?.provider === "openrouter") {
      const role = `subagent:${ctx.agent.id}`;
      await record({ event: "alias_role", primary: session.settings.getModelRole(role), chain: getRetryFallbackChains(session.settings)[role] });
    }
  });
  pi.on("after_provider_response", async (event, ctx) => {
    const session = AgentRegistry.global().get(ctx.agent.id)?.session;
    const effort = session && isLocalAgentSession(session) ? session.configuredThinkingLevel() : undefined;
    await record({ event: "after_provider_response", agent: ctx.agent, provider: ctx.model?.provider, status: event.status, effort });
  });
  pi.on("agent_end", async (event, ctx) => {
    if (ctx.agent.id !== "Probe" || !process.env.ECI_PROBE_SCENARIO?.endsWith("-wake")) return;
    const token = `${process.env.ECI_PROBE_SCENARIO}:answer`;
    const tail = event.messages.slice(-2);
    if (tail.length !== 2 || tail[0].role !== "assistant" || tail[1].role !== "toolResult" ||
        !JSON.stringify(tail[0]).includes('"name":"yield"') || !JSON.stringify(tail[0]).includes(token) ||
        !JSON.stringify(tail[1]).includes("Result submitted.")) return;
    await record({ event: "child_success", agent: ctx.agent, sessionId: ctx.sessionManager.getSessionId(), token });
    await fetch(`${process.env.ECI_PROBE_NOTIFY}/child-end`, { method: "POST" });
  });
}
