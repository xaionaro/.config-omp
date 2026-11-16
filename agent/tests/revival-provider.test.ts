import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { recoverModelLessRevival } from "../extensions/revival-provider.ts";

function fixture(options: { current?: boolean; pin?: boolean; fallback?: boolean; offline?: boolean; empty?: boolean; missingMetadata?: boolean } = {}) {
  const calls: unknown[] = [];
  const settings = { getModelRole: (role: string) => role === "task" ? "disabled/primary" : undefined, getModelRoles: () => ({ task: "disabled/primary" }) };
  const models = {
    find: () => undefined,
    hasProvider: () => true,
    getAvailable: () => [],
    hasConfiguredAuth: (_model: { provider: string }) => true,
  };
  const session = {
    settings, modelRegistry: models,
    sessionManager: { getSessionId: () => "same" },
    configuredThinkingLevel: (): string | undefined => "auto",
    setThinkingLevel: (level: unknown) => { calls.push(["effort", level]); },
    applyRoleModel: async (value: { model: { provider: string; id: string } }) => { calls.push(["apply", value]); },
  };
  const init = { perCallModel: options.pin, modelRole: "task", resolvedModel: "disabled/primary", ...(options.missingMetadata ? {} : { retryFallback: { primary: "disabled/primary", chain: [] } }) };
  const api = {
    session: () => session,
    local: () => true,
    setting: (name: string) => name === "disabledProviders" ? ["disabled"] : name === "models.offline" ? !!options.offline : options.fallback !== false,
    chains: () => ({ task: options.empty ? [] : ["enabled/one:medium", "backup/two:high"] }),
    chainKey: () => "task",
    candidates: () => options.empty ? [] : [
      { raw: "enabled/one:medium", provider: "enabled", id: "one", thinkingLevel: "medium" },
      { raw: "backup/two:high", provider: "backup", id: "two", thinkingLevel: "high" },
    ],
    resolve: (selector: string) => ({ model: { provider: selector.split("/")[0], id: selector.split("/")[1].split(":")[0] }, thinkingLevel: "medium", explicitThinkingLevel: true }),
    install: (_settings: unknown, role: string, value: unknown) => { calls.push(["install", role, value]); },
  };
  return { calls, api, init, session, settings, context: { agent: { id: "child" }, model: options.current ? { provider: "enabled", id: "one" } : undefined, sessionManager: { getSessionId: () => "same" } } };
}

describe("model-less revival provider recovery", () => {
  test("rebases the same child's role and preserves the effective remaining chain", async () => {
    const f = fixture();
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls, [
      ["install", "subagent:child", { primary: "enabled/one:medium", chain: ["backup/two:high"] }],
      ["apply", { role: "subagent:child", model: { provider: "enabled", id: "one" }, thinkingLevel: "medium", explicitThinkingLevel: true }],
      ["effort", "medium"],
    ]);
  });
  test("skips an unauthenticated candidate and installs the eligible backup", async () => {
    const f = fixture();
    f.session.modelRegistry.hasConfiguredAuth = (model: { provider: string }) => model.provider === "backup";
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls, [
      ["install", "subagent:child", { primary: "backup/two:high", chain: [] }],
      ["apply", { role: "subagent:child", model: { provider: "backup", id: "two" }, thinkingLevel: "high", explicitThinkingLevel: true }],
      ["effort", "high"],
    ]);
  });
  test("leaves policy and session unchanged when every candidate lacks configured auth", async () => {
    const f = fixture();
    f.session.modelRegistry.hasConfiguredAuth = () => false;
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls, []);
  });
  for (const [name, options] of Object.entries({ current: { current: true }, pin: { pin: true }, disabledFallback: { fallback: false }, offline: { offline: true }, empty: { empty: true } })) {
    test(`${name} does not change model or role`, async () => {
      const f = fixture(options);
      await recoverModelLessRevival(f.context, f.init, f.api);
      assert.deepEqual(f.calls, []);
    });
  }
  test("missing saved chain consults the current role policy", async () => {
    const f = fixture({ missingMetadata: true });
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls[0], ["install", "subagent:child", { primary: "enabled/one:medium", chain: ["backup/two:high"] }]);
    assert.deepEqual(f.calls[1], ["apply", { role: "subagent:child", model: { provider: "enabled", id: "one" }, thinkingLevel: "medium", explicitThinkingLevel: true }]);
  });
  test("unavailable effective settings fail before any mutation", async () => {
    const f = fixture();
    f.api.setting = () => undefined as never;
    await assert.rejects(recoverModelLessRevival(f.context, f.init, f.api), /recovery unavailable/);
    assert.deepEqual(f.calls, []);
  });
  for (const boundary of ["install", "apply-before", "apply-after", "effort-before", "effort-after"]) {
    test(`${boundary} surfaces the original failure and actual residue`, async () => {
      const f = fixture();
      const original = new Error(`failure at ${boundary}`);
      let policy = "old";
      let active: { provider: string; id: string } | undefined;
      let effort = "auto";
      Object.defineProperty(f.session, "model", { get: () => active });
      f.session.configuredThinkingLevel = () => effort;
      f.api.install = () => {
        policy = boundary === "install" ? "partial" : "installed";
        if (boundary === "install") throw original;
      };
      f.session.applyRoleModel = async value => {
        if (boundary === "apply-before") throw original;
        active = value.model;
        if (boundary === "apply-after") throw original;
      };
      f.session.setThinkingLevel = level => {
        if (boundary === "effort-before") throw original;
        effort = String(level);
        if (boundary === "effort-after") throw original;
      };
      let failure: unknown;
      try {
        await recoverModelLessRevival(f.context, f.init, f.api);
      } catch (error) {
        failure = error;
      }
      assert(failure instanceof Error);
      assert.equal(failure.cause, original);
      assert.match(failure.message, new RegExp(`failure at ${boundary}`));
      assert.match(failure.message, /child.*enabled\/one:medium/);
      assert.match(failure.message, boundary === "install" ? /install.*may have changed/ : boundary.startsWith("apply") ? /application/ : /effort/);
      assert.match(failure.message, new RegExp(`observed effort.*${effort}`));
      assert.match(failure.message, new RegExp(`requested effort medium, observed model ${active ? "enabled/one" : "none"}`));
      assert.equal(policy, boundary === "install" ? "partial" : "installed");
      assert.equal(active?.id, ["apply-after", "effort-before", "effort-after"].includes(boundary) ? "one" : undefined);
    });
  }
  test("a suffixless candidate carries configured auto effort", async () => {
    const f = fixture();
    f.api.candidates = () => [{ raw: "enabled/one", provider: "enabled", id: "one", thinkingLevel: undefined }] as never;
    f.api.resolve = () => ({ model: { provider: "enabled", id: "one" }, thinkingLevel: undefined, explicitThinkingLevel: false }) as never;
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls.at(-1), ["effort", "auto"]);
  });
  test("a suffixless candidate carries unset effort without inventing a level", async () => {
    const f = fixture();
    f.session.configuredThinkingLevel = () => undefined;
    f.api.candidates = () => [{ raw: "enabled/one", provider: "enabled", id: "one", thinkingLevel: undefined }] as never;
    await recoverModelLessRevival(f.context, f.init, f.api);
    assert.deepEqual(f.calls.at(-1), ["effort", undefined]);
  });
});
