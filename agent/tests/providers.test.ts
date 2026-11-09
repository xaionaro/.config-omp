import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { saveGlobalProvider } from "../extensions/providers.ts";

function fixture(initial: unknown[], options: { failFlush?: boolean; concurrent?: unknown[] } = {}) {
  let global = [...initial];
  let reloads = 0;
  let flushes = 0;
  const settings = {
    getAgentDir: () => "/profile/agent",
    getGlobalSettings: () => ({ disabledProviders: global }),
    reloadFromDisk: async () => {
      reloads++;
      if (reloads === 2 && options.concurrent) global = [...options.concurrent];
    },
    flush: async () => {
      flushes++;
      if (options.failFlush) throw new Error("disk full");
    },
  };
  const setting = {
    get: () => global.filter((value): value is string => typeof value === "string"),
    provenance: () => "global",
    setMember: (_scope: typeof settings, id: string, { member }: { member: boolean }) => {
      global = member ? [...global, id] : global.filter(value => value !== id);
    },
  };
  return { settings, setting, global: () => global, reloads: () => reloads, flushes: () => flushes };
}

describe("global provider persistence", () => {
  test("changes only the chosen unscoped entry and preserves path-scoped and unrelated entries", async () => {
    const scoped = { paths: ["/work"], providers: ["alpha"] };
    const f = fixture(["beta", scoped]);
    assert.equal(await saveGlobalProvider(f.settings, f.setting, "alpha", true), true);
    assert.equal(await saveGlobalProvider(f.settings, f.setting, "beta", false), true);
    assert.deepEqual(f.global(), [scoped, "alpha"]);
    assert.equal(f.reloads(), 4);
    assert.equal(f.flushes(), 2);
  });

  test("propagates a failed flush rather than reporting a save", async () => {
    const f = fixture([], { failFlush: true });
    await assert.rejects(saveGlobalProvider(f.settings, f.setting, "alpha", true), /disk full/);
    assert.equal(f.reloads(), 1);
  });

  test("does not report success when the reloaded global value differs", async () => {
    const f = fixture([], { concurrent: ["beta"] });
    assert.equal(await saveGlobalProvider(f.settings, f.setting, "alpha", true), false);
    assert.deepEqual(f.global(), ["beta"]);
  });
});
