import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import type * as AgentRegistryModule from "@oh-my-pi/pi-coding-agent/registry/agent-registry";
import type * as SessionHandleModule from "@oh-my-pi/pi-coding-agent/session/agent-session-handle";
import type * as SettingsRegistryModule from "@oh-my-pi/pi-coding-agent/config/registry";
import type * as ModelResolverModule from "@oh-my-pi/pi-coding-agent/config/model-resolver";
import type * as RetryFallbackModule from "@oh-my-pi/pi-coding-agent/session/retry-fallback-chains";

// Deep stock APIs are internal. After every omp update, run all controls listed
// in agent/tests/revival-provider-stock-proof.ts against the installed binary.
// On failure, move this file out of the extension discovery directory, then
// reload or restart at quiescence; restore it only after qualification succeeds.
// Hook errors do not veto native revival; stock may retain partially changed state.

type Model = { provider: string; id: string };
type Candidate = { raw: string; provider: string; id: string; thinkingLevel?: string };
type Settings = { getModelRole(role: string): string | undefined; getModelRoles(): Record<string, string> };
type Registry = {
  find(provider: string, id: string): Model | undefined;
  hasProvider(provider: string): boolean;
  getAvailable(): Model[];
  hasConfiguredAuth(model: Model): boolean;
};
type Session = {
  settings: Settings;
  readonly model?: Model;
  modelRegistry: Registry;
  sessionManager: { getSessionId(): string };
  configuredThinkingLevel(): string | undefined;
  setThinkingLevel(level: string | undefined): void;
  applyRoleModel(entry: { role: string; model: Model; thinkingLevel?: string; explicitThinkingLevel: boolean }): Promise<void>;
};
type Context = { agent: { id: string }; model?: Model; sessionManager: { getSessionId(): string } };
type Init = { perCallModel?: boolean; modelRole?: string; resolvedModel?: string; retryFallback?: { primary: string; chain: string[] } };
type Resolution = { chains: Record<string, string[]>; getModelRole(role: string): string | undefined; modelLookup: Registry };
type RecoveryAPI = {
  session(id: string): Session | undefined;
  local(session: Session): boolean;
  setting(name: string, settings: Settings): unknown;
  chains(settings: Settings): Record<string, string[]>;
  chainKey(context: Resolution, primary: string, model: undefined, role: string): string | undefined;
  candidates(context: Resolution, key: string, primary: string, model: undefined, options: { allowMissingPrimary: boolean }): Candidate[];
  resolve(selector: string, registry: Registry, settings: Settings): { model?: Model; thinkingLevel?: string; explicitThinkingLevel: boolean };
  install(settings: Settings, role: string, value: { primary: string; chain: string[] }): void;
};

const unavailable = (reason: string): never => { throw new Error(`Model-less revival recovery unavailable: ${reason}`); };

/** Restore a model-less ordinary child using its effective stock fallback policy. */
export async function recoverModelLessRevival(ctx: Context, init: Init, api: RecoveryAPI): Promise<void> {
  if (ctx.model || init.perCallModel) return;
  const session = api.session(ctx.agent.id);
  if (!session || !api.local(session) || typeof session.sessionManager?.getSessionId !== "function" ||
      typeof ctx.sessionManager?.getSessionId !== "function" || session.sessionManager.getSessionId() !== ctx.sessionManager.getSessionId()) {
    unavailable("same local child session is not accessible");
  }
  const { settings, modelRegistry } = session;
  if (!settings || typeof settings.getModelRole !== "function" || typeof settings.getModelRoles !== "function" || !modelRegistry ||
      typeof modelRegistry.find !== "function" || typeof modelRegistry.hasProvider !== "function" ||
      typeof modelRegistry.getAvailable !== "function" || typeof modelRegistry.hasConfiguredAuth !== "function" ||
      typeof session.configuredThinkingLevel !== "function" || typeof session.setThinkingLevel !== "function" ||
      typeof session.applyRoleModel !== "function") unavailable("required session capabilities are missing");
  const disabled = api.setting("disabledProviders", settings);
  const fallback = api.setting("retry.modelFallback", settings);
  const offline = api.setting("models.offline", settings);
  if (!Array.isArray(disabled) || !disabled.every(id => typeof id === "string") ||
      typeof fallback !== "boolean" || typeof offline !== "boolean") unavailable("effective provider or retry settings are missing");
  if (!fallback || offline) return;
  const role = `subagent:${ctx.agent.id}`;
  const primary = init.retryFallback?.primary ?? init.resolvedModel ?? (init.modelRole && settings.getModelRole(init.modelRole));
  if (!primary) unavailable("persisted primary model is missing");
  const context: Resolution = { chains: api.chains(settings), getModelRole: name => settings.getModelRole(name), modelLookup: modelRegistry };
  const key = api.chainKey(context, primary, undefined, init.retryFallback ? role : init.modelRole ?? role);
  if (!key) return;
  const candidates = api.candidates(context, key, primary, undefined, { allowMissingPrimary: true });
  const disabledIds = new Set(disabled);
  for (let index = 0; index < candidates.length; index++) {
    const candidate = candidates[index];
    if (disabledIds.has(candidate.provider)) continue;
    const resolved = api.resolve(candidate.raw, modelRegistry, settings);
    const model = resolved.model ?? modelRegistry.find(candidate.provider, candidate.id);
    if (!model || disabledIds.has(model.provider) || !modelRegistry.hasConfiguredAuth(model)) continue;
    const level = candidate.thinkingLevel ?? session.configuredThinkingLevel();
    let operation = "policy installation (incomplete; settings may have changed)";
    try {
      // Expand the remaining selectors against the original primary, not this target.
      api.install(settings, role, { primary: candidate.raw, chain: candidates.slice(index + 1).map(entry => entry.raw) });
      operation = "model application";
      await session.applyRoleModel({ role, model, thinkingLevel: candidate.thinkingLevel, explicitThinkingLevel: candidate.thinkingLevel !== undefined });
      operation = "effort application";
      session.setThinkingLevel(level);
    } catch (cause) {
      let observedModel = "unavailable";
      let observedEffort = "unavailable";
      try {
        observedModel = session.model ? `${session.model.provider}/${session.model.id}` : "none";
      } catch { /* Preserve the original failure when observation fails. */ }
      try {
        observedEffort = String(session.configuredThinkingLevel());
      } catch { /* Preserve the original failure when observation fails. */ }
      const message = cause instanceof Error ? cause.message : String(cause);
      throw new Error(`Model-less revival recovery unavailable for child ${ctx.agent.id}, candidate ${candidate.raw}: ${operation} failed; requested effort ${String(level)}, observed model ${observedModel}, observed effort ${observedEffort}: ${message}`, { cause });
    }
    return;
  }
}

export default function revivalProvider(pi: ExtensionAPI): void {
  pi.on("before_agent_start", async (event, ctx) => {
    if (event.reason !== "revive" || ctx.agent.kind !== "sub" || ctx.model) return;
    if (typeof ctx.sessionManager?.getBranch !== "function") unavailable("session history API is missing");
    const init = ctx.sessionManager.getBranch().findLast(entry => entry.type === "session_init");
    if (!init) unavailable("persisted session contract is missing");
    if (init.perCallModel) return;
    let registry: typeof AgentRegistryModule;
    let handles: typeof SessionHandleModule;
    let settings: typeof SettingsRegistryModule;
    let resolver: typeof ModelResolverModule;
    let fallback: typeof RetryFallbackModule;
    try {
      [registry, handles, settings, resolver, fallback] = await Promise.all([
        import("@oh-my-pi/pi-coding-agent/registry/agent-registry"),
        import("@oh-my-pi/pi-coding-agent/session/agent-session-handle"),
        import("@oh-my-pi/pi-coding-agent/config/registry"),
        import("@oh-my-pi/pi-coding-agent/config/model-resolver"),
        import("@oh-my-pi/pi-coding-agent/session/retry-fallback-chains"),
      ]);
    } catch (error) {
      unavailable(`stock APIs could not be loaded: ${String(error)}`);
    }
    if (typeof registry.AgentRegistry?.global !== "function" || typeof handles.isLocalAgentSession !== "function" ||
        typeof settings.lookup !== "function" || typeof resolver.resolveModelOverride !== "function" ||
        typeof fallback.getRetryFallbackChains !== "function" || typeof fallback.resolveRetryFallbackChainKey !== "function" ||
        typeof fallback.findRetryFallbackCandidates !== "function" || typeof fallback.installRetryFallbackRole !== "function") {
      unavailable("required stock APIs are missing");
    }
    for (const name of ["modelRoles", "retry.fallbackChains"]) {
      const handle = settings.lookup(name);
      if (!handle || typeof handle.get !== "function" || typeof handle.override !== "function") {
        unavailable(`setting ${name} cannot install a session role`);
      }
    }
    const api: RecoveryAPI = {
      session: id => registry.AgentRegistry.global().get(id)?.session as unknown as Session | undefined,
      local: session => handles.isLocalAgentSession(session as never),
      setting: (name, scope) => {
        const handle = settings.lookup(name);
        if (!handle || typeof handle.get !== "function") unavailable(`setting ${name} is missing`);
        return handle.get(scope as never);
      },
      chains: scope => fallback.getRetryFallbackChains(scope as never),
      chainKey: (context, primary, model, role) => fallback.resolveRetryFallbackChainKey(context as never, primary, model, role),
      candidates: (context, key, primary, model, options) => fallback.findRetryFallbackCandidates(context as never, key, primary, model, options),
      resolve: (selector, modelRegistry, scope) => resolver.resolveModelOverride([selector], modelRegistry as never, scope as never),
      install: (scope, role, value) => fallback.installRetryFallbackRole(scope as never, role, value),
    };
    await recoverModelLessRevival(ctx as unknown as Context, init, api);
  });
}
