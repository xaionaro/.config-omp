import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { cfgDisabledProviders } from "@oh-my-pi/pi-coding-agent/config/model-settings";
import { findScopedSettings, Settings } from "@oh-my-pi/pi-coding-agent/config/settings";
import { PROVIDER_DESCRIPTORS } from "@oh-my-pi/pi-catalog/provider-models";
import { getAllProvidersInfo } from "@oh-my-pi/pi-coding-agent/capability";

interface ProviderSettings {
  getAgentDir(): string;
  getGlobalSettings(): { disabledProviders?: unknown };
  reloadFromDisk(options?: { includeParents?: boolean }): Promise<void>;
  flush(): Promise<void>;
  getCwd(): string;
}

interface DisabledProviders<S extends ProviderSettings> {
  get(scope: S): readonly string[];
  provenance(scope: S): string;
  setMember(scope: S, id: string, options: { member: boolean }): void;
}

interface ProviderContext {
  hasUI: boolean;
  agent: { kind: string };
  model: { provider: string } | undefined;
  modelRegistry: {
    authStorage: { keys: { source(id: string): { concrete: boolean } | undefined; keyless(id: string): boolean } };
    getAvailable(mode: "all"): readonly { provider: string }[];
    getAll(mode: "all"): readonly { provider: string }[];
    getDiscoverableProviders(): string[];
    getProviderDiscoveryState(id: string): { optional: boolean; status: string } | undefined;
    getDiscoveryProviderId(id: string): string | undefined;
    getRuntimeDiscoveryFailures(): readonly { provider: string }[];
  };
  ui: {
    select(title: string, options: { label: string; description?: string }[], options2?: { initialIndex?: number }): Promise<string | undefined>;
    notify(message: string, type?: "info" | "warning" | "error"): void;
  };
}

function globalEntries(settings: ProviderSettings): readonly unknown[] {
  const value = settings.getGlobalSettings().disabledProviders;
  return Array.isArray(value) ? value : [];
}

/** Change only the unscoped global membership, then confirm what the settings store saved. */
async function saveGlobalProvider<S extends ProviderSettings>(settings: S, setting: DisabledProviders<S>, id: string, disabled: boolean): Promise<boolean> {
  await settings.reloadFromDisk({ includeParents: true });
  setting.setMember(settings, id, { member: disabled });
  await settings.flush();
  const saved = await Settings.loadReadOnly({ cwd: settings.getCwd(), agentDir: settings.getAgentDir() });
  return globalEntries(saved).includes(id) === disabled;
}

async function showProviders<S extends ProviderSettings>(ctx: ProviderContext, settings: S, setting: DisabledProviders<S>): Promise<void> {
  if (!ctx.hasUI) {
    ctx.ui.notify("Provider controls require an interactive UI.", "error");
    return;
  }
  if (ctx.agent.kind !== "main") {
    ctx.ui.notify("Provider controls require the main session; child settings writes are not persistent.", "error");
    return;
  }
  ctx.ui.notify(`Global profile settings (${settings.getAgentDir()}/config.yml or config.yaml) save immediately. Path-scoped entries, project, --config overlays and runtime overrides can change effective state. IDs marked [source] also control discovery of configuration, rules, MCP or other resources. No account or credential control. Disabling the active provider does not switch its model or stop in-flight work; later requests may fail or fall back. Future child model selection may also change. Some extension providers without models, including successfully discovered empty runtime-only providers, may not be listed.`, "info");

  let selectedId: string | undefined;
  let showOtherProviders = false;
  try {
    while (true) {
      await settings.reloadFromDisk({ includeParents: true });
      const entries = globalEntries(settings);
      const effective = new Set(setting.get(settings));
      const provenance = setting.provenance(settings);
      const sourceIds = new Set(getAllProvidersInfo().map(provider => provider.id));
      const registry = ctx.modelRegistry;
      const modelIds = new Set(PROVIDER_DESCRIPTORS.map(descriptor => descriptor.providerId));
      const catalogIds = new Set(registry.getAll("all").map(model => model.provider));
      for (const id of catalogIds) modelIds.add(id);
      const availableIds = new Set(registry.getAvailable("all").map(model => model.provider));
      const discoverable = new Set(registry.getDiscoverableProviders());
      const ids = new Set<string>();
      const keys = registry.authStorage.keys;
      const automaticKeyless: Record<string, true> = { local: true, web: true, ollama: true, "llama.cpp": true, "lm-studio": true, apple: true };
      for (const id of Object.keys(automaticKeyless)) modelIds.add(id);
      for (const id of availableIds) modelIds.add(id);
      const configured = (id: string): boolean => keys.source(id)?.concrete === true || keys.keyless(id);
      // Automatic keyless backends are not evidence of a configured endpoint.
      const eligible = (id: string): boolean => {
        if (configured(id)) return true;
        if (keys.source(id) !== undefined) return false;
        const discovery = registry.getProviderDiscoveryState(id);
        if (automaticKeyless[id]) {
          return (discoverable.has(id) && discovery?.optional === false) ||
            (availableIds.has(id) && (discovery?.status === "ok" || discovery?.status === "cached"));
        }
        return !discovery?.optional || (discovery.status !== "idle" && discovery.status !== "unavailable");
      };
      for (const id of availableIds) {
        if (eligible(id)) ids.add(id);
      }
      for (const id of modelIds) {
        if (configured(id)) ids.add(id);
      }
      for (const id of discoverable) {
        modelIds.add(id);
        if (ids.has(id)) continue;
        if (!configured(id) && catalogIds.has(id) && registry.getProviderDiscoveryState(id)?.optional !== false) continue;
        if (eligible(id)) ids.add(id);
      }
      for (const failure of registry.getRuntimeDiscoveryFailures()) {
        modelIds.add(failure.provider);
        ids.add(failure.provider);
      }
      if (ctx.model) ids.add(ctx.model.provider);
      if (selectedId !== undefined) ids.add(selectedId);
      const candidates: unknown[] = [...entries, ...effective];
      for (const entry of entries) {
        if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
        const scopedEntry = entry as Record<string, unknown>;
        for (const key of ["values", "items", "providers"]) {
          if (Array.isArray(scopedEntry[key])) candidates.push(...scopedEntry[key]);
        }
      }
      for (const id of candidates) {
        if (typeof id !== "string") continue;
        if (!sourceIds.has(id) || modelIds.has(id) || registry.getDiscoveryProviderId(id) === id) ids.add(id);
      }
      if (showOtherProviders) {
        for (const id of modelIds) ids.add(id);
      }
      const sortedIds = [...ids].sort();
      const choices = sortedIds.map(id => ({
        label: `[${entries.includes(id) ? " " : "x"}] ${id}${sourceIds.has(id) ? " [source]" : ""}${ctx.model?.provider === id ? " [active]" : ""} — global ${entries.includes(id) ? "off" : "on"}; effective ${effective.has(id) ? "off" : "on"} (${provenance})`,
        description: `Select to ${entries.includes(id) ? "enable" : "disable"} globally${ctx.model?.provider === id ? "; active model's next requests may fail or fall back" : ""}`,
      }));
      const visibilityAction = showOtherProviders ? "Hide other providers" : "Show other providers";
      choices.push({ label: visibilityAction, description: showOtherProviders ? "Show the compact provider list" : "Include other known providers, even if not configured" });
      choices.push({ label: "Exit", description: "Close (or press Escape)" });
      const selectedIndex = selectedId === undefined ? 0 : sortedIds.indexOf(selectedId);
      const selected = await ctx.ui.select(
        `Providers — global; ${showOtherProviders ? "all known" : "configured, usable or disabled"}; select to toggle; Esc exits\n[source] also affects config, rules, MCP and other discovery.\nThe active model remains selected; later requests may fail or fall back.`, choices,
        { initialIndex: selectedIndex < 0 ? sortedIds.length : selectedIndex },
      );
      if (!selected || selected === "Exit") return;
      if (selected === visibilityAction) {
        showOtherProviders = !showOtherProviders;
        continue;
      }
      const index = choices.findIndex(choice => choice.label === selected);
      if (index < 0 || index >= sortedIds.length) return;
      const id = sortedIds[index];
      selectedId = id;
      const disabled = !entries.includes(id);
      if (await saveGlobalProvider(settings, setting, id, disabled)) {
        ctx.ui.notify(`${id}: global ${disabled ? "disabled" : "enabled"} saved. Effective state may differ.${disabled && ctx.model?.provider === id ? " The current model remains selected; later requests may fail or fall back." : ""}`, "info");
      } else {
        ctx.ui.notify(`${id}: global change was not saved (the setting changed concurrently). Reload and try again.`, "error");
      }
    }
  } catch (error) {
    ctx.ui.notify(`Provider settings could not be loaded or saved: ${error instanceof Error ? error.message : String(error)}. Check the config and retry; a failed save may remain pending.`, "error");
  }
}
export default function providers(pi: ExtensionAPI): void {
  pi.registerCommand("provider-controls", {
    description: "Enable or disable model providers globally (not accounts)",
    handler: async (_args, ctx) => {
      const settings = findScopedSettings(ctx.cwd);
      if (!settings || settings.overlayParent() || !settings.getStorage()) {
        ctx.ui.notify("Provider controls require a persistent, non-overlay session settings scope.", "error");
        return;
      }
      await showProviders(ctx, settings, cfgDisabledProviders);
    },
  });
}
