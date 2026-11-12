import { describe, expect, test } from "bun:test";
import workerStopNoticeHook, { workerStopNotice } from "./worker-stop-notice.ts";

const at = new Date("2026-11-11T23:32:06.692Z");

describe("workerStopNotice", () => {
	test("aborted run names worker, owner, and routes", () => {
		const text = workerStopNotice(
			{ kind: "sub", id: "SkillReadOwner.SR1RepairCritic3", parentId: "SkillReadOwner" },
			[{ role: "user" }, { role: "assistant", stopReason: "aborted", errorMessage: "Request was aborted" }],
			at,
		);
		expect(text).toContain("Worker SkillReadOwner.SR1RepairCritic3 stopped at 2026-11-11T23:32:06.692Z with stopReason=aborted (Request was aborted)");
		expect(text).toContain("write agent://SkillReadOwner");
	});
	test("normal stop gives no notice", () => {
		expect(workerStopNotice({ kind: "sub", id: "w" }, [{ role: "assistant", stopReason: "stop" }], at)).toBeUndefined();
	});
});

describe("hook wiring", () => {
	test("subagent abort reaches the main session with a wake", () => {
		const sent: unknown[] = [];
		const make = () => {
			const handlers = new Map<string, (event: unknown, ctx: unknown) => void>();
			const api = { on: (name: string, fn: never) => handlers.set(name, fn), sendMessage: (...args: unknown[]) => sent.push(args) };
			workerStopNoticeHook(api as never);
			return handlers;
		};
		make().get("session_start")!({}, { agent: { kind: "main", id: "Main" } });
		make().get("agent_end")!(
			{ messages: [{ role: "assistant", stopReason: "aborted" }] },
			{ agent: { kind: "sub", id: "Owner.W", parentId: "Owner" } },
		);
		expect(sent).toHaveLength(1);
		expect((sent[0] as [unknown, { triggerTurn: boolean }])[1].triggerTurn).toBe(true);
	});
});
