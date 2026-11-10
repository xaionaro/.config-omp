// Disabled until deliberately installed under hooks/. Successful reads are scoped
// to the current agent and reconstructed from its active transcript branch.
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const LANGUAGES: Record<string, string> = { go: "go", py: "python" };
const PATCH_HEADER = /^\[(.+)#[a-zA-Z0-9]{4}\]\s*$/gm;

function language(path: string): string | undefined {
 const extension = /\.([a-zA-Z0-9]+)$/.exec(path)?.[1]?.toLowerCase();
 if (!extension) return undefined;
 const name = LANGUAGES[extension] ?? extension;
 return existsSync(join(homedir(), ".omp", "agent", "skills", `coding-${name}`, "SKILL.md")) ? name : undefined;
}

// Recognize literal shell words and redirections without interpreting expansions.
function shellWrites(command: string): string[] {
 const tokens: { value: string; operator: boolean }[] = [];
 let word = "", quote = "", escaped = false;
 const flush = () => { if (word) tokens.push({ value: word, operator: false }); word = ""; };
 for (let i = 0; i < command.length; i++) {
  const c = command[i];
  if (escaped) { word += c; escaped = false; continue; }
  if (c === "\\" && quote !== "'") { escaped = true; continue; }
  if (quote) { if (c === quote) quote = ""; else word += c; continue; }
  if (c === "'" || c === '"') { quote = c; continue; }
  if (" \t\r\n".includes(c)) { flush(); continue; }
  if ("<>|;&".includes(c)) {
   flush();
   let operator = c;
   if (command[i + 1] === c) { operator += c; i++; }
   tokens.push({ value: operator, operator: true });
   continue;
  }
  word += c;
 }
 flush();
 const paths: string[] = [];
 let argv: string[] = [];
 const collect = () => {
  const name = argv[0]?.split("/").pop();
  if (name === "tee") paths.push(...argv.slice(1).filter(arg => !arg.startsWith("-")));
  if ((name === "cp" || name === "mv" || name === "install") && argv.length > 2) paths.push(argv[argv.length - 1]);
  if (name === "sed" && argv.some(arg => /^-.*i/.test(arg))) paths.push(argv[argv.length - 1]);
  argv = [];
 };
 for (let i = 0; i < tokens.length; i++) {
  const token = tokens[i];
  if (token.operator && (token.value === ">" || token.value === ">>")) {
   const target = tokens[++i];
   if (target && !target.operator && !/[$`*?{}]/.test(target.value)) paths.push(target.value);
  } else if (token.operator) collect();
  else argv.push(token.value);
 }
 collect();
 return paths;
}

// Ignore apparent calls occurring inside strings or comments.
function evalWrites(code: string): string[] {
 const paths: string[] = [];
 let quote = "", escaped = false, comment = false;
 for (let i = 0; i < code.length; i++) {
  const c = code[i];
  if (comment) { if (c === "\n") comment = false; continue; }
  if (escaped) { escaped = false; continue; }
  if (quote) {
   if (c === "\\") escaped = true;
   else if (c === quote) quote = "";
   continue;
  }
  if (c === "#" || (c === "/" && code[i + 1] === "/")) { comment = true; continue; }
  if (c === "'" || c === '"' || c === "`") { quote = c; continue; }
  if (i > 0 && /[\w$]/.test(code[i - 1])) continue;
  const match = /^(?:write|writeFile|writeFileSync)\s*\(\s*(['"])([^'"\n]+)\1/.exec(code.slice(i));
  if (match) paths.push(match[2]);
 }
 return paths;
}

function pathsFor(tool: string, input: Record<string, unknown>): string[] {
 if (tool === "write") return typeof input.path === "string" ? [input.path] : [];
 if (tool === "edit") {
  const paths = typeof input.path === "string" ? [input.path] : [];
  if (typeof input.input === "string") {
   paths.push(...Array.from(input.input.matchAll(PATCH_HEADER), m => m[1]));
   paths.push(...Array.from(input.input.matchAll(/^(?:MV |\*\*\* (?:Add File|Update File|Delete File|Move to): )(.+)$/gm), m => m[1].replace(/^(['"])(.*)\1$/, "$2")));
  }
  if (Array.isArray(input.edits)) for (const edit of input.edits) {
   if (edit && typeof edit === "object" && "rename" in edit && typeof edit.rename === "string") paths.push(edit.rename);
  }
  return paths;
 }
 if (tool === "bash" && typeof input.command === "string") return shellWrites(input.command);
 if (tool === "eval" && typeof input.code === "string") return evalWrites(input.code);
 return [];
}

export default function codingSkillGate(pi: ExtensionAPI): void {
 const reads = new Map<string, Set<string>>();
 const pending = new Map<string, string[]>();
 function key(ctx: { sessionManager: { getSessionId(): string }; agent?: { id: string } }): string {
  return JSON.stringify([ctx.sessionManager.getSessionId(), ctx.agent?.id ?? "main"]);
 }
 function seenFor(ctx: ExtensionContext): Set<string> {
  const id = key(ctx);
  const existing = reads.get(id);
  if (existing) return existing;
  const seen = new Set<string>();
  // The advisor shares its parent's runner; its reads are not in that branch.
  if (ctx.agent?.id !== "advisor") {
   const calls = new Map<string, string>();
   for (const entry of ctx.sessionManager.getBranch()) {
    if (entry.type !== "message") continue;
    const message = entry.message;
    if (message.role === "assistant") {
     for (const part of message.content) {
      if (part.type !== "toolCall" || part.name !== "read" || typeof part.arguments.path !== "string") continue;
      const match = /^skill:\/\/(coding-(?:common|[a-z0-9-]+))$/.exec(part.arguments.path);
      if (match) calls.set(part.id, match[1]);
     }
    } else if (message.role === "toolResult" && message.toolName === "read" && !message.isError) {
     const skill = calls.get(message.toolCallId);
     if (skill) seen.add(skill);
    }
   }
  }
  reads.set(id, seen);
  return seen;
 }
 const reset = (_event: unknown, ctx: ExtensionContext) => {
  reads.delete(key(ctx));
  pending.delete(key(ctx));
 };
 pi.on("session_tree", reset);
 pi.on("session_branch", reset);
 pi.on("session_switch", reset);
 pi.on("tool_result", (event, ctx) => {
  const id = key(ctx);
  if (event.toolName === "read" && !event.isError && typeof event.input.path === "string") {
   const match = /^skill:\/\/(coding-(?:common|[a-z0-9-]+))$/.exec(event.input.path);
   if (match) {
    seenFor(ctx).add(match[1]);
   }
  }
  if (event.toolName === "ast_edit" && !event.isError && event.details && typeof event.details === "object" && "files" in event.details && Array.isArray(event.details.files)) {
   pending.set(id, event.details.files.filter((path): path is string => typeof path === "string"));
  }
 });
 pi.on("tool_call", (event, ctx) => {
  const id = key(ctx);
  const input = event.input as Record<string, unknown>;
  const paths = event.toolName === "write" && input.path === "xd://resolve" ? pending.get(id) ?? [] : pathsFor(event.toolName, input);
  for (const path of paths) {
   const lang = language(path);
   if (!lang) continue;
   const seen = seenFor(ctx);
   const missing = ["coding-common", `coding-${lang}`].filter(skill => !seen?.has(skill));
   if (missing.length) return { block: true, reason: `Coding skill gate: writing ${path} requires reading ${missing.map(skill => `skill://${skill}`).join(" and ")} in this agent first; read those skills, then retry. If the hook is unavailable or incorrect, ask the user to temporarily disable this disabled-by-default hook.` };
  }
  return undefined;
 });
}
