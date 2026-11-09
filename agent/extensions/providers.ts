import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { cfgDisabledProviders } from "@oh-my-pi/pi-coding-agent/config/model-settings";

interface ProviderSettings {
  getAgentDir(): string;
  getGlobalSettings(): { disabledProviders?: unknown };
  reloadFromDisk(options?: { includeParents?: boolean }): Promise<void>;
  flush(): Promise<void>;
}

interface DisabledProviders<S extends ProviderSettings> {
  get(scope: S): readonly string[];
  provenance(scope: S): string;
  setMember(scope: S, id: string, options: { member: boolean }): void;
}

interface ProviderContext {
  hasUI: boolean;
  agent: { kind: string };
  modelRegistry: {
    getAll(mode: "all"): readonly { provider: string }[];
    getDiscoverableProviders(): string[];
    getDiscoveryProviderId(id: string): string | undefined;
  };
  ui: {
    select(title: string, options: { label: string; description?: string }[]): Promise<string | undefined>;
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
  await settings.reloadFromDisk({ includeParents: true });
  return globalEntries(settings).includes(id) === disabled;
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
  ctx.ui.notify(`Global profile settings (${settings.getAgentDir()}/config.yml or config.yaml) save immediately. Path-scoped entries, project, --config overlays and runtime overrides may change the effective state. This controls providers, not accounts or credentials; future model/child selection may be affected. The current model is not switched and in-flight work is not stopped.`, "info");

  try {
    while (true) {
      await settings.reloadFromDisk({ includeParents: true });
      const entries = globalEntries(settings);
      const effective = new Set(setting.get(settings));
      const provenance = setting.provenance(settings);
      const ids = new Set(ctx.modelRegistry.getAll("all").map(model => model.provider));
      for (const id of ctx.modelRegistry.getDiscoverableProviders()) ids.add(id);
      // The v18.18.2 registry omits disabled implicit providers even from discovery
      // lookup. Recover only these native model providers, not arbitrary config IDs.
      const implicit = new Set(["ollama", "llama.cpp", "lm-studio"]);
      if (process.platform === "darwin" && process.arch === "arm64") implicit.add("apple");
      const candidates: unknown[] = [...entries, ...effective];
      for (const entry of entries) {
        if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
        const scopedEntry = entry as Record<string, unknown>;
        for (const key of ["values", "items", "providers"]) {
          if (Array.isArray(scopedEntry[key])) candidates.push(...scopedEntry[key]);
        }
      }
      for (const id of candidates) {
        if (typeof id === "string" && (implicit.has(id) || ctx.modelRegistry.getDiscoveryProviderId(id) === id)) ids.add(id);
      }
      const choices = [...ids].sort().map(id => ({
        label: `[${entries.includes(id) ? " " : "x"}] ${id}`,
        description: `global ${entries.includes(id) ? "disabled" : "enabled"}; effective ${effective.has(id) ? "disabled" : "enabled"} (${provenance}); select to ${entries.includes(id) ? "enable" : "disable"} globally`,
      }));
      choices.push({ label: "Exit", description: "Close (or press Escape)" });
      const selected = await ctx.ui.select(
        `Providers: select to toggle globally; saves immediately; Esc exits`, choices,
      );
      if (!selected || selected === "Exit") return;
      const index = choices.findIndex(choice => choice.label === selected);
      if (index < 0 || index >= choices.length - 1) return;
      const id = selected.slice(4);
      const disabled = !entries.includes(id);
      if (await saveGlobalProvider(settings, setting, id, disabled)) {
        ctx.ui.notify(`${id}: global ${disabled ? "disabled" : "enabled"} saved. Effective state may differ; current model and in-flight work are unchanged.`, "info");
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
      await showProviders(ctx, pi.pi.settings, cfgDisabledProviders);
    },
  });
}
