import { describe, test } from "node:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import pythonEvalGate, { PYTHON_EVAL_BLOCK_REASON } from "../extensions/python-eval-gate.ts";

type Handler = (event: unknown, ctx: unknown) => unknown;

// Registers the gate on a stub API and returns a tool_call dispatcher.
function setup() {
  const handlers: Handler[] = [];
  const pi = {
    on(name: string, handler: Handler) {
      if (name === "tool_call") handlers.push(handler);
    },
  };
  pythonEvalGate(pi as unknown as ExtensionAPI);
  assert.equal(handlers.length, 1, "the gate registers exactly one tool_call handler");
  return (toolName: string, input: object): unknown =>
    handlers[0]({ type: "tool_call", toolCallId: "call-1", toolName, input }, {});
}

describe("python eval gate", () => {
  for (const language of ["py", "python"]) {
    test(`blocks eval ${language} cells with the script route`, () => {
      const result = setup()("eval", { language, code: "print(1)" });
      assert.deepEqual(result, { block: true, reason: PYTHON_EVAL_BLOCK_REASON });
      assert.match(PYTHON_EVAL_BLOCK_REASON, /\[ECI_PYTHON_SCRIPT_REQUIRED\]/);
      assert.match(PYTHON_EVAL_BLOCK_REASON, /#!\/usr\/bin\/python3/);
    });
  }

  test("leaves eval js cells and cells without a language alone", () => {
    const fire = setup();
    assert.equal(fire("eval", { language: "js", code: "1" }), undefined);
    assert.equal(fire("eval", { code: "1" }), undefined);
  });

  test("ignores other tools, even with a py language field", () => {
    assert.equal(setup()("bash", { command: "python3 -V", language: "py" }), undefined);
  });
});
