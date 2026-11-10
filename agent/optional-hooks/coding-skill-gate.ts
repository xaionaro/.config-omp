// Disabled until deliberately installed under hooks/. Successful reads are scoped
// to the current agent and reconstructed from its active transcript branch.
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { existsSync, lstatSync, opendirSync, readFileSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";

const LANGUAGES: Record<string, string> = { go: "go", py: "python" };
const PATCH_HEADER = /^\[(.+)#[a-zA-Z0-9]{4}\]\s*$/gm;

function languageForExistingSkills(path: string): string | undefined {
 path = path.replace(/^\[(.*)#[0-9a-fA-F]{4}\]$/, "$1");
 if (/^(?:local|vault):\/\//.test(path)) path = path.replace(/:(?:raw|conflicts)$/, "");
 const extension = /\.([a-zA-Z0-9]+)$/.exec(path)?.[1]?.toLowerCase();
 if (!extension) return undefined;
 const name = LANGUAGES[extension];
 if (!name) return undefined;
 return ["common", name].every(skill => existsSync(join(process.env.PI_CODING_AGENT_DIR ?? join(homedir(), ".omp", "agent"), "skills", `coding-${skill}`, "SKILL.md"))) ? name : undefined;
}

function verifiedReadSkill(input: Record<string, unknown>, result: { isError?: boolean; details?: unknown }, cwd: string): string | undefined {
 if (result.isError || typeof input.path !== "string") return undefined;
 const details = result.details as { raw?: boolean; summary?: unknown; truncation?: unknown; meta?: { truncation?: unknown; limits?: unknown }; displayContent?: { text?: string; startLine?: number; lineNumbers?: (number | null)[] } } | undefined;
 if (details?.summary || details?.truncation || details?.meta?.truncation || details?.meta?.limits || details?.displayContent?.startLine !== 1) return undefined;
 const display = details.displayContent;
 if (display.lineNumbers?.some((number, index) => number !== index + 1)) return undefined;
 const raw = input.path.endsWith(":raw");
 const readPath = raw ? input.path.slice(0, -4) : input.path;
 if (raw !== (details.raw === true)) return undefined;
 const uri = /^skill:\/\/(coding-(?:common|go|python))(?:\/SKILL\.md)?$/.exec(readPath);
 if (!uri && /:\d|:raw|[?;]/.test(readPath)) return undefined;
 for (const name of ["common", "go", "python"]) {
  const canonical = join(process.env.PI_CODING_AGENT_DIR ?? join(homedir(), ".omp", "agent"), "skills", `coding-${name}`, "SKILL.md");
  try {
   if (uri ? uri[1] !== `coding-${name}` : realpathSync(resolve(cwd, readPath)) !== realpathSync(canonical)) continue;
   const text = readFileSync(canonical, "utf8").replace(/\r\n/g, "\n");
   if (display.text?.replace(/\r\n/g, "\n") === (raw ? text : text.replace(/\n$/, ""))) return `coding-${name}`;
  } catch (error) {
   if (!(error instanceof Error && "code" in error && (error.code === "ENOENT" || error.code === "ENOTDIR"))) throw error;
  }
 }
 return undefined;
}

// Scan one directory while sharing the traversal's entry budget.
function scanGofmtDirectory(path: string, directories: string[], budget: { remaining: number }): string | undefined {
	const directory = opendirSync(path);
	try {
		while (budget.remaining > 0) {
			const entry = directory.readSync();
			if (!entry) return undefined;
			budget.remaining--;
			const child = join(path, entry.name);
			if (entry.isDirectory()) {
				directories.push(child);
				continue;
			}
			if (!entry.name.startsWith(".") && entry.name.endsWith(".go") && (entry.isFile() || entry.isSymbolicLink())) return child;
		}
	} finally {
		directory.closeSync();
	}
	return undefined;
}

// Bound directory inspection; uncertainty alone is not evidence of a write.
function findGofmtTarget(operands: string[], cwd: string): string | undefined {
	const directories: string[] = [];
	for (const operand of operands) {
		if (/[$`*?{}]/.test(operand)) continue;
		const path = resolve(cwd, operand);
		try {
			if (lstatSync(path).isDirectory()) directories.push(path);
			else if (path.endsWith(".go")) return path;
		} catch (error) {
			if (!(error instanceof Error && "code" in error && ["ENOENT", "ENOTDIR", "EACCES", "EPERM"].includes(String(error.code)))) throw error;
		}
	}
	const budget = { remaining: 4096 };
	while (directories.length && budget.remaining > 0) {
		const path = directories.pop()!;
		try {
			const target = scanGofmtDirectory(path, directories, budget);
			if (target) return target;
		} catch (error) {
			if (!(error instanceof Error && "code" in error && ["ENOENT", "ENOTDIR", "EACCES", "EPERM"].includes(String(error.code)))) throw error;
		}
	}
	return undefined;
}

// Recognize literal shell words and redirections without interpreting expansions.
function literalShellWriteTargets(command: string, cwd: string): string[] {
 // Heredoc bodies are data, not shell commands.
 let delimiter: string | undefined;
 command = command.split("\n").map(line => {
  if (delimiter) { if (line.trim() === delimiter) delimiter = undefined; return ""; }
  const match = /<<-?\s*(['"]?)([A-Za-z_][\w-]*)\1/.exec(line);
  if (match) delimiter = match[2];
  return line;
 }).join("\n");
 const tokens: { value: string; operator: boolean }[] = [];
 let word = "", quote = "", escaped = false;
 const flush = () => { if (word) tokens.push({ value: word, operator: false }); word = ""; };
 for (let i = 0; i < command.length; i++) {
  const c = command[i];
  if (escaped) { word += c; escaped = false; continue; }
  if (c === "\\" && quote !== "'") { escaped = true; continue; }
  if (quote) { if (c === quote) quote = ""; else word += c; continue; }
  if (c === "'" || c === '"') { quote = c; continue; }
  if (c === "#" && !word) { while (i < command.length && command[i] !== "\n") i++; flush(); tokens.push({ value: ";", operator: true }); continue; }
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
  if (name === "sed" && argv.some(arg => /^-[^-]*i/.test(arg) || /^--in-place(?:=|$)/.test(arg))) paths.push(argv[argv.length - 1]);
  if (name === "gofmt") {
   let writes = false, options = true;
   const operands: string[] = [];
   for (let i = 1; i < argv.length; i++) {
    const arg = argv[i];
    if (options && arg === "--") { options = false; continue; }
    if (options && (arg === "-r" || arg === "--r")) { i++; continue; }
    if (options && /^(?:-w|--w)(?:=true)?$/.test(arg)) { writes = true; continue; }
    if (options && arg.startsWith("-")) continue;
    operands.push(arg);
   }
   if (writes) {
    const target = findGofmtTarget(operands, cwd);
    if (target) paths.push(target);
   }
  }
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
function literalEvalWriteTargets(code: string): string[] {
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
  if (c === "/" && code[i + 1] === "*") {
   const end = code.indexOf("*/", i + 2);
   if (end < 0) break;
   i = end + 1;
   continue;
  }
  if (c === "#" || (c === "/" && code[i + 1] === "/")) { comment = true; continue; }
  if (c === "'" || c === '"' || c === "`") { quote = c; continue; }
  if (i > 0 && (/[\w$]/.test(code[i - 1]) || /\.\s*$/.test(code.slice(0, i)))) continue;
  const remaining = code.slice(i);
  const match = /^(?:write|Bun\.write)\s*\(\s*(['"])([^'"\n]+)\1/.exec(remaining)
   ?? /^Path\s*\(\s*(['"])([^'"\n]+)\1\s*\)\s*\.\s*(?:write_text|write_bytes)\s*\(/.exec(remaining)
   ?? /^open\s*\(\s*(['"])([^'"\n]+)\1\s*,\s*['"][^'"]*[wax+][^'"]*['"]/.exec(remaining);
  if (match) paths.push(match[2]);
 }
 return paths;
}

function writeTargetsFor(tool: string, input: Record<string, unknown>, cwd: string): string[] {
 if (tool === "write") return typeof input.path === "string" ? [input.path] : [];
 if (tool === "edit" || tool === "apply_patch") {
  const paths = typeof input.path === "string" ? [input.path] : [];
  if (Array.isArray(input.paths)) paths.push(...input.paths.filter((path): path is string => typeof path === "string"));
  const patch = typeof input.input === "string" ? input.input : typeof input._input === "string" ? input._input : "";
  if (patch) {
   paths.push(...Array.from(patch.matchAll(PATCH_HEADER), m => m[1]));
   paths.push(...Array.from(patch.matchAll(/^(?:MV |\*\*\* (?:Add File|Update File|Delete File|Move to): )(.+)$/gm), m => m[1].replace(/^(['"])(.*)\1$/, "$2")));
   paths.push(...Array.from(patch.matchAll(/^\*\*\* Edit File: (.+?)(?: all)?\r?$/gm), m => m[1]));
  }
  if (Array.isArray(input.edits)) for (const edit of input.edits) {
   if (edit && typeof edit === "object" && "rename" in edit && typeof edit.rename === "string") paths.push(edit.rename);
  }
  return paths;
 }
 if (tool === "bash" && typeof input.command === "string") return literalShellWriteTargets(input.command, typeof input.cwd === "string" ? resolve(cwd, input.cwd) : cwd);
 if (tool === "eval" && typeof input.code === "string") return literalEvalWriteTargets(input.code);
 return [];
}

export default function codingSkillGate(pi: ExtensionAPI): void {
 const reads = new Map<string, Set<string>>();
 const pending = new Map<string, Set<string>>();
 let ownAgent: string | undefined;
 function agentKey(ctx: ExtensionContext): string {
  return JSON.stringify([ctx.sessionManager.getSessionId(), ctx.agent?.id]);
 }
 function hydrateReads(ctx: ExtensionContext): Set<string> {
  const id = agentKey(ctx);
  const existing = reads.get(id);
  if (existing) return existing;
  const seen = new Set<string>();
  reads.set(id, seen);
  if (ownAgent === undefined || ctx.agent?.id !== ownAgent) return seen;
  const calls = new Map<string, Record<string, unknown>>();
  for (const entry of ctx.sessionManager.getBranch()) {
   if (entry.type !== "message") continue;
   const message = entry.message;
   if (message.role === "assistant") {
    for (const part of message.content) {
     if (part.type === "toolCall" && part.name === "read") calls.set(part.id, part.arguments);
    }
    continue;
   }
   if (message.role !== "toolResult" || message.toolName !== "read") continue;
   const input = calls.get(message.toolCallId);
   const skill = input && verifiedReadSkill(input, message, ctx.cwd);
   if (skill) seen.add(skill);
  }
  return seen;
 }
 pi.on("session_start", (_event, ctx) => { reads.clear(); pending.clear(); ownAgent = ctx.agent?.id; });
 const reset = () => { reads.clear(); pending.clear(); };
 pi.on("session_tree", reset);
 pi.on("session_branch", reset);
 pi.on("session_switch", reset);
 pi.on("tool_result", (event, ctx) => {
  const id = agentKey(ctx);
  if (event.toolName === "read") {
   const skill = verifiedReadSkill(event.input as Record<string, unknown>, event, ctx.cwd);
   if (skill) hydrateReads(ctx).add(skill);
  }
  if (event.toolName === "ast_edit" && !event.isError && event.details && typeof event.details === "object" && "files" in event.details && Array.isArray(event.details.files)) {
   const files = pending.get(id) ?? new Set<string>();
   for (const path of event.details.files) if (typeof path === "string") files.add(path);
   pending.set(id, files);
  }
 });
 pi.on("tool_call", (event, ctx) => {
  const input = event.input as Record<string, unknown>;
  const paths = event.toolName === "write" && input.path === "xd://resolve" ? [...pending.get(agentKey(ctx)) ?? []] : writeTargetsFor(event.toolName, input, ctx.cwd);
  for (const path of paths) {
   const lang = languageForExistingSkills(path);
   if (!lang) continue;
   const seen = hydrateReads(ctx);
   const missing = ["coding-common", `coding-${lang}`].filter(skill => !seen.has(skill));
   if (missing.length) {
    const retry = input.path === "xd://resolve" ? "First write xd://reject, then read the skills and rerun the preview." : "Then retry.";
    const base = join(process.env.PI_CODING_AGENT_DIR ?? join(homedir(), ".omp", "agent"), "skills");
    return { block: true, reason: `Coding skill gate: writing ${path} requires ${missing.map(skill => `skill://${skill} (or ${join(base, skill, "SKILL.md")})`).join(" and ")}. Read each named skill without a line range and let the reads finish in an earlier tool batch. ${retry}` };
   }
  }
  return undefined;
 });
}
