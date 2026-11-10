import { afterAll, beforeAll, test } from "bun:test";
import assert from "node:assert/strict";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { mkdtempSync, mkdirSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import gate from "./coding-skill-gate.ts";

const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
let agentDir: string;
beforeAll(() => {
 agentDir = mkdtempSync(join(tmpdir(), "coding-skill-gate-"));
 process.env.PI_CODING_AGENT_DIR = agentDir;
 for (const name of ["coding-common", "coding-go", "coding-python"]) {
  const directory = join(agentDir, "skills", name);
  mkdirSync(directory, {recursive: true});
  writeFileSync(join(directory, "SKILL.md"), `# ${name}\nTest skill.\n`);
 }
});
afterAll(() => {
 if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
 else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
 rmSync(agentDir, {recursive: true, force: true});
});

type Event = { toolName: string; input: Record<string, unknown>; isError?: boolean; details?: unknown; content?: { type: string; text: string }[] };
type Context = { cwd: string; agent: { id: string; kind: string }; sessionManager: { getSessionId(): string; getBranch(): unknown[] } };
type Verdict = { block: boolean; reason: string } | undefined;
function details(name: string) {
 return {displayContent: {text: readFileSync(join(agentDir, "skills", name, "SKILL.md"), "utf8").replace(/\r\n/g, "\n").replace(/\n$/, ""), startLine: 1}};
}
function setup() {
 const handlers: Record<string, (event: Event, ctx: Context) => Verdict> = {};
 // This fixture supplies only event registration.
 gate({ on(name: string, handler: (event: Event, ctx: Context) => Verdict) { handlers[name] = handler; } } as unknown as ExtensionAPI);
 let branch: unknown[] = [];
 const ctx = (session = "one", id = "Main") => ({ cwd: "/tmp", agent: { id, kind: "main" }, sessionManager: { getSessionId: () => session, getBranch: () => branch } });
 handlers.session_start({toolName: "", input: {}}, ctx());
 const setBranch = (value: unknown[]) => { branch = value; };
 const tree = (c = ctx()) => handlers.session_tree({toolName: "", input: {}}, c);
 const call = (toolName: string, input: Record<string, unknown>, c = ctx()) => handlers.tool_call({ toolName, input }, c);
 const read = (path: string, c = ctx(), isError = false) => handlers.tool_result({ toolName: "read", input: { path }, isError, details: details(path.includes("coding-go") ? "coding-go" : "coding-common"), content: [{type: "text", text: "skill"}] }, c);
 const result = (toolName: string, input: Record<string, unknown>, details: unknown, c = ctx(), isError = false) => handlers.tool_result({ toolName, input, details, isError }, c);
 return { call, read, result, ctx, setBranch, tree, handlers };
}

test("requires both successful skill reads per agent and session", () => {
 const { call, read, ctx } = setup();
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common:1-2");
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common", ctx(), true);
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 read("skill://coding-common");
 assert.match(call("write", {path: "a.go"})!.reason, /coding-go/);
 read("skill://coding-go");
 assert.equal(call("write", {path: "a.go"}), undefined);
 assert.match(call("write", {path: "b.go"}, ctx("two"))!.reason, /coding-common/);
 assert.match(call("write", {path: "b.go"}, ctx("one", "advisor"))!.reason, /coding-common/);
 assert.match(call("write", {path: "b.py"})!.reason, /coding-python/);
 assert.equal(call("write", {path: "a.txt"}), undefined);
});

test("restores paired successful reads from the current branch and resets after navigation", () => {
 const { call, setBranch, tree } = setup();
 setBranch([
  { type: "message", message: { role: "assistant", content: [
   { type: "toolCall", id: "c", name: "read", arguments: {path: "skill://coding-common"} },
   { type: "toolCall", id: "g", name: "read", arguments: {path: "skill://coding-go"} },
  ] } },
  { type: "message", message: { role: "toolResult", toolCallId: "c", toolName: "read", isError: false, details: details("coding-common") } },
  { type: "message", message: { role: "toolResult", toolCallId: "g", toolName: "read", isError: false, details: details("coding-go") } },
 ]);
 assert.equal(call("write", {path: "a.go"}), undefined);
 setBranch([]);
 tree();
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 setBranch([
  { type: "message", message: { role: "assistant", content: [{type: "toolCall", id: "c", name: "read", arguments: {path: "skill://coding-common"}}] } },
  { type: "message", message: { role: "toolResult", toolCallId: "c", toolName: "read", isError: true } },
 ]);
 tree();
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
});

test("recognizes patch, shell, eval and pending structural edits", () => {
 const { call, result } = setup();
 for (const [tool, input] of [
  ["edit", {input: "*** Begin Patch\n[a.go#1234]\nCUT 1.=1\n*** End Patch"}],
  ["bash", {command: "printf hi > a.go"}],
  ["eval", {code: "write('a.go', 'x')"}],
 ] as const) assert.match(call(tool, input)!.reason, /coding-common/);
 assert.match(call("edit", {paths: ["first.txt", "second.go"]})!.reason, /coding-common/);
 assert.match(call("apply_patch", {input: "*** Begin Patch\n*** Add File: file.py\n+pass\n*** End Patch"})!.reason, /coding-common/);
 assert.equal(call("ast_edit", {paths: ["a.go"], ops: []}), undefined);
 result("ast_edit", {paths: ["src/"], ops: []}, {files: ["src/a.go"]});
 assert.match(call("write", {path: "xd://resolve"})!.reason, /coding-common/);
 assert.equal(call("bash", {command: "cat a.go"}), undefined);
 assert.equal(call("bash", {command: "printf '%s' 'example > a.go'"}), undefined);
 assert.equal(call("eval", {code: "print(\"write('a.go', 'x')\")"}), undefined);
 assert.match(call("bash", {command: "tee a.go"})!.reason, /coding-common/);
 assert.match(call("bash", {command: "cp a.txt b.py"})!.reason, /coding-common/);
 assert.match(call("edit", {path: "a.txt", edits: [{rename: "b.py"}]})!.reason, /coding-common/);
 assert.match(call("edit", {input: "*** Begin Patch\n[a.txt#1234]\nMV b.go\n*** End Patch"})!.reason, /coding-common/);
 assert.equal(call("eval", {code: "print('a.go')"}), undefined);
});

test("requires completed, untruncated whole reads in an earlier batch", () => {
 const { call, result } = setup();
 result("read", {path: "skill://coding-common"}, {truncation: {truncated: true}});
 result("read", {path: "skill://coding-go"}, {summary: {text: "partial"}});
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 assert.match(call("write", {path: "a.go"})!.reason, /earlier/i);
 result("read", {path: "skill://coding-common/SKILL.md"}, details("coding-common"));
 result("read", {path: "skill://coding-go/SKILL.md"}, details("coding-go"));
 assert.equal(call("write", {path: "a.go"}), undefined);
});

test("keeps all successful structural previews and ignores harmless syntax", () => {
 const { call, result } = setup();
 result("ast_edit", {}, {files: ["first.go"]});
 result("ast_edit", {}, {files: ["second.txt"]});
 assert.match(call("write", {path: "xd://resolve"})!.reason, /coding-common/);
 for (const command of [
  "cat > notes.md <<EOF\nexample > file.go\nEOF",
  "echo ok # example > file.go",
  "sed --expression 's/x/y/' file.go",
 ]) assert.equal(call("bash", {command}), undefined, command);
 assert.equal(call("eval", {code: "sys.stdout.write('file.py')"}), undefined);
});

test("does not hydrate partial results or leak another agent's branch evidence", () => {
 const { call, setBranch, tree, ctx } = setup();
 setBranch([
  {type: "message", message: {role: "assistant", content: [{type: "toolCall", id: "c", name: "read", arguments: {path: "skill://coding-common"}}]}},
  {type: "message", message: {role: "toolResult", toolCallId: "c", toolName: "read", isError: false, details: {truncation: {truncated: true}}}},
 ]);
 tree();
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 assert.match(call("write", {path: "a.go"}, ctx("one", "advisor"))!.reason, /coding-common/);
});

test("does not count a successful result with no proof of complete output", () => {
 const { call, result } = setup();
 result("read", {path: "skill://coding-common"}, {});
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
});

test("handles wrapped writes and gofmt writes without blocking read-only gofmt", () => {
 const {call} = setup();
 assert.match(call("write", {path: "[src/a.go#ABCD]"})!.reason, /coding-go/);
 const directory = join(agentDir, "go-project");
 mkdirSync(directory);
 writeFileSync(join(directory, "main.go"), "package main\n");
 assert.match(call("bash", {command: `gofmt -w '${directory}'`})!.reason, /coding-go/);
 assert.equal(call("bash", {command: "gofmt -d a.go"}), undefined);
});

test("recognizes direct literal eval filesystem writes without blocking reads", () => {
 const {call} = setup();
 for (const code of ["Bun.write('a.go', 'x')", "Path('a.py').write_text('x')", "open('a.py', 'w')"]) {
  assert.match(call("eval", {code})!.reason, /coding-/);
 }
 for (const code of ["open('a.py', 'r')", "Path('a.py').read_text()", "print(\"Bun.write('a.go', 'x')\")"]) {
  assert.equal(call("eval", {code}), undefined);
 }
});

test("uses canonical local reads and isolates an advisor from populated branch history", () => {
 const {call, read, ctx, setBranch, handlers} = setup();
 const root = join(agentDir, "skills");
 read(join(root, "coding-common", "SKILL.md"));
 read(join(root, "coding-go", "SKILL.md"));
 assert.equal(call("write", {path: "a.go"}), undefined);
 setBranch([
  {type: "message", message: {role: "assistant", content: [
   {type: "toolCall", id: "c", name: "read", arguments: {path: "skill://coding-common"}},
   {type: "toolCall", id: "g", name: "read", arguments: {path: "skill://coding-go"}},
  ]}},
  {type: "message", message: {role: "toolResult", toolCallId: "c", toolName: "read", isError: false, details: details("coding-common")}},
  {type: "message", message: {role: "toolResult", toolCallId: "g", toolName: "read", isError: false, details: details("coding-go")}},
 ]);
 assert.match(call("write", {path: "a.go"}, ctx("one", "advisor"))!.reason, /coding-common/);
 setBranch([]);
 for (const event of ["session_switch", "session_branch", "session_tree"]) {
  handlers[event]({toolName: "", input: {}}, ctx());
  assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
  read("skill://coding-common");
  read("skill://coding-go");
 }
});

test("does not mistake spaced member calls for the eval prelude", () => {
 const {call} = setup();
 assert.equal(call("eval", {code: "sys.stdout . write('a.py')"}), undefined);
});

test("recognizes sloppy patch edit headers", () => {
 const {call} = setup();
 assert.match(call("edit", {input: "*** Begin Patch\n*** Edit File: src/a.go all\n@@\n-x\n+y\n*** End Patch"})!.reason, /coding-go/);
});

test("does not require a missing language skill", () => {
 const file = join(agentDir, "skills", "coding-python", "SKILL.md");
 renameSync(file, `${file}.disabled`);
 try {
  assert.equal(setup().call("write", {path: "a.py"}), undefined);
 } finally {
  renameSync(`${file}.disabled`, file);
 }
});

test("does not block gofmt writing standard output", () => {
 const {call} = setup();
 assert.equal(call("bash", {command: "gofmt -w"}), undefined);
});

test("checks file-backed URLs and archive entry names", () => {
 const {call} = setup();
 for (const path of ["local://source.go", "vault://source.py", "source.zip:dir/source.go"]) {
  assert.match(call("write", {path})!.reason, /coding-/);
 }
 assert.equal(call("write", {path: "local://notes.md"}), undefined);
});

test("rejects truncation reported in host metadata", () => {
 const {call, result} = setup();
 result("read", {path: "skill://coding-common"}, {...details("coding-common"), meta: {truncation: {truncated: true}}});
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
});

test("does not treat the gofmt rewrite rule as an output file", () => {
 const {call} = setup();
 assert.equal(call("bash", {command: "gofmt -w -r 'a -> b'"}), undefined);
});

test("checks file-backed URL targets with peeled raw selectors", () => {
 const {call} = setup();
 assert.match(call("write", {path: "local://a.go:raw"})!.reason, /coding-go/);
});

test("ignores writes mentioned in eval block comments", () => {
 const {call} = setup();
 assert.equal(call("eval", {code: "/* write('a.go', 'x') */ print('ok')"}), undefined);
});

test("pending reads in a batch never satisfy its write preflight", () => {
 const {call, read} = setup();
 assert.equal(call("read", {path: "skill://coding-common"}), undefined);
 assert.equal(call("read", {path: "skill://coding-go"}), undefined);
 assert.match(call("write", {path: "batch.go"})!.reason, /earlier tool batch/);
 read("skill://coding-common");
 read("skill://coding-go");
 assert.equal(call("write", {path: "batch.go"}), undefined);
});

test("does not gate gofmt on directories without Go files", () => {
 const {call} = setup();
 const directory = join(agentDir, "empty-project");
 mkdirSync(directory);
 assert.equal(call("bash", {command: `gofmt -w '${directory}'`}), undefined);
});

test("gofmt finds Go files in hidden directories and Go symlinks", () => {
 const {call} = setup();
 const directory = join(agentDir, "nested-project");
 const hidden = join(directory, ".generated");
 mkdirSync(hidden, {recursive: true});
 writeFileSync(join(hidden, "main.go"), "package main\n");
 assert.match(call("bash", {command: `gofmt -w '${directory}'`})!.reason, /coding-go/);
 const links = join(agentDir, "link-project");
 mkdirSync(links);
 symlinkSync(join(hidden, "main.go"), join(links, "linked.go"));
 assert.match(call("bash", {command: `gofmt -w '${links}'`})!.reason, /coding-go/);
});

test("accepts whole raw skill reads but not raw line selections", () => {
 const {call, result} = setup();
 const common = details("coding-common");
 result("read", {path: "skill://coding-common:1-2:raw"}, common);
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 result("read", {path: "skill://coding-common:raw"}, {...common, raw: true, displayContent: {...common.displayContent, text: `${common.displayContent.text}\n`}});
 const go = details("coding-go");
 result("read", {path: "skill://coding-go:raw"}, {...go, raw: true, displayContent: {...go.displayContent, text: `${go.displayContent.text}\n`}});
 assert.equal(call("write", {path: "a.go"}), undefined);
});

test("accepts a complete raw canonical file and rejects a missing final newline", () => {
 const {call, result} = setup();
 const path = join(agentDir, "skills", "coding-common", "SKILL.md") + ":raw";
 const common = details("coding-common");
 result("read", {path}, {...common, raw: true});
 assert.match(call("write", {path: "a.go"})!.reason, /coding-common/);
 result("read", {path}, {...common, raw: true, displayContent: {...common.displayContent, text: `${common.displayContent.text}\n`}});
 assert.match(call("write", {path: "a.go"})!.reason, /coding-go/);
});
