import { execFile } from "node:child_process";
import { accessSync, constants } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

// Runs the prebuilt Go sender from agent/cmd/matrix-notify. It reads Hermes's Matrix credentials in place and sends
// as its own device; no secret passes through omp. The Go sender owns the alias→Matrix ID table, input validation,
// and the runbook (its main.go package doc).
// The sender's module directory under the home directory; the binary, the runbook and the build command derive from it.
const SENDER_DIR_IN_HOME = ".omp/agent/cmd/matrix-notify";
const SENDER_BINARY_NAME = "matrix-notify";
const SENDER_BINARY = join(homedir(), SENDER_DIR_IN_HOME, SENDER_BINARY_NAME);
export const RUNBOOK = `~/${SENDER_DIR_IN_HOME}/main.go`;
const BUILD_HINT = `build it with \`cd ~/${SENDER_DIR_IN_HOME} && go build -tags goolm -o ${SENDER_BINARY_NAME} .\``;
const RUNBOOK_POINTER = `See ${RUNBOOK} for causes and recovery.`;
// The Go sender owns the run budget via -deadline; the kill comes later so it can still report its JSON result.
const SENDER_DEADLINE_S = 120;
const KILL_MARGIN_MS = 30_000;

export const ALIASES = ["void", "vickey"] as const;
export type Alias = (typeof ALIASES)[number];

// The Go sender's failure categories, in its wire order (agent/cmd/matrix-notify/category.go; bound by testdata/wire.json).
export const CATEGORIES = [
  "internal",
  "input",
  "credentials",
  "state",
  "identity",
  "auth",
  "network",
  "room",
  "crypto",
  "send",
] as const;
export type Category = (typeof CATEGORIES)[number];

export type UnknownReason =
  | "no answer to send"
  | "unexpected sender output"
  | "cancelled"
  | "sender stdin failed";

export type MatrixSendResult =
  | {
      status: "sent";
      room_id: string;
      event_id: string;
      recipient_membership: "join" | "invite";
      cleanup_error?: Category;
    }
  | { status: "invite_pending" | "key_pending"; room_id: string; cleanup_error?: Category }
  | { status: "error"; error: Category }
  // The message may or may not have reached Matrix: the run ended without proof either way.
  | { status: "unknown"; reason: UnknownReason; cleanup_error?: Category }
  | { status: "not_built" };

export type MatrixSender = (to: Alias, message: string, signal?: AbortSignal) => Promise<MatrixSendResult>;

const isNonEmptyString = (v: unknown): v is string => typeof v === "string" && v !== "";
const isCategory = (v: unknown): v is Category => (CATEGORIES as readonly unknown[]).includes(v);
const UNEXPECTED: MatrixSendResult = { status: "unknown", reason: "unexpected sender output" };

// parseSenderOutput accepts exactly the result shapes the sender prints for a send. Anything else means the sender
// ended abnormally, possibly after the send request, so it is an unknown outcome rather than "not sent".
// pi.zod exists only inside omp, so this closed check keeps the module testable under plain node.
export function parseSenderOutput(stdout: string): MatrixSendResult {
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(stdout.trim().split("\n").pop() ?? "");
  } catch {
    return UNEXPECTED;
  }
  if (raw === null || typeof raw !== "object") return UNEXPECTED;
  if (raw.cleanup_error !== undefined && !isCategory(raw.cleanup_error)) return UNEXPECTED;
  const cleanup = raw.cleanup_error === undefined ? {} : { cleanup_error: raw.cleanup_error };
  switch (raw.status) {
    case "sent":
      if (
        isNonEmptyString(raw.room_id) &&
        isNonEmptyString(raw.event_id) &&
        (raw.recipient_membership === "join" || raw.recipient_membership === "invite")
      ) {
        return {
          status: "sent",
          room_id: raw.room_id,
          event_id: raw.event_id,
          recipient_membership: raw.recipient_membership,
          ...cleanup,
        };
      }
      break;
    case "invite_pending":
    case "key_pending":
      if (isNonEmptyString(raw.room_id)) return { status: raw.status, room_id: raw.room_id, ...cleanup };
      break;
    case "unknown":
      return { status: "unknown", reason: "no answer to send", ...cleanup };
    case "error":
      if (isCategory(raw.error)) return { status: "error", error: raw.error };
      break;
  }
  return UNEXPECTED;
}

// isExecutable reports whether binary exists and may be executed by this process.
function isExecutable(binary: string): boolean {
  try {
    accessSync(binary, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

// matrixSenderBinary runs the binary directly (no shell), passes the message on stdin and trusts only its JSON stdout.
export function matrixSenderBinary(binary: string): MatrixSender {
  return (to, message, signal) => {
    // TODO: an executable check cannot tell a stale binary from a current one; have the sender report its embedded
    // build info (debug.ReadBuildInfo: vcs.revision, vcs.modified) and refuse with BUILD_HINT when
    // agent/cmd/matrix-notify differs from that revision.
    if (!isExecutable(binary)) return Promise.resolve({ status: "not_built" });
    const { promise, resolve } = Promise.withResolvers<MatrixSendResult>();
    const args = ["-to", to, "-deadline", `${SENDER_DEADLINE_S}s`];
    const child = execFile(binary, args, { timeout: SENDER_DEADLINE_S * 1000 + KILL_MARGIN_MS, signal }, (error, stdout) => {
      const result = parseSenderOutput(stdout);
      if (result.status === "unknown" && error?.name === "AbortError") {
        resolve({ status: "unknown", reason: "cancelled" });
        return;
      }
      resolve(result);
    });
    // A sender that exits before reading stdin closes the pipe (EPIPE); its JSON result is reported above. Any other
    // stdin failure may have delivered a partial message, so stop the sender and report an unknown outcome.
    child.stdin?.on("error", (err: NodeJS.ErrnoException) => {
      if (err.code === "EPIPE") return;
      child.kill();
      resolve({ status: "unknown", reason: "sender stdin failed" });
    });
    child.stdin?.end(message);
    return promise;
  };
}

function cleanupWarning(r: { cleanup_error?: Category }): string {
  return r.cleanup_error === undefined ? "" : ` Local state cleanup failed (category: ${r.cleanup_error}). ${RUNBOOK_POINTER}`;
}

function describe(to: Alias, r: MatrixSendResult): { text: string; isError: boolean } {
  switch (r.status) {
    case "sent": {
      const invite = r.recipient_membership === "invite" ? " The recipient has not joined the room yet." : "";
      return {
        text: `Matrix accepted the encrypted message to ${to}: event ${r.event_id} in ${r.room_id}. This is not a delivery or read receipt.${invite}${cleanupWarning(r)}`,
        isError: false,
      };
    }
    case "invite_pending":
      return {
        text: `NOT sent to ${to}: the recipient must join the DM before they can decrypt it.${cleanupWarning(r)}`,
        isError: true,
      };
    case "key_pending":
      return {
        text: `NOT sent to ${to}: no device of the recipient could receive the encryption key yet.${cleanupWarning(r)}`,
        isError: true,
      };
    case "error":
      return { text: `NOT sent to ${to}: error category: ${r.error}. ${RUNBOOK_POINTER}`, isError: true };
    case "not_built":
      return { text: `NOT sent to ${to}: the sender is not built; ${BUILD_HINT}. ${RUNBOOK_POINTER}`, isError: true };
    case "unknown":
      return {
        text: `The message to ${to} may or may not have been sent (${r.reason}); check the DM before retrying.${cleanupWarning(r)}`,
        isError: true,
      };
  }
}

export function matrixNotify(pi: ExtensionAPI, send: MatrixSender): void {
  const z = pi.zod;
  pi.registerTool({
    name: "matrix_send",
    label: "Matrix send",
    description:
      "Send an end-to-end encrypted Matrix DM from Hermes's Matrix account to 'void' (the user) or 'vickey'. " +
      "Use it to notify them outside this session, e.g. a result, decision or blocker. " +
      "The message is sent as literal text. Success means Matrix accepted the event, not that it was read.",
    parameters: z.object({
      to: z.enum(ALIASES),
      message: z.string().min(1),
    }),
    async execute(_toolCallId, params, signal) {
      const result = await send(params.to, params.message, signal);
      const { text, isError } = describe(params.to, result);
      return { content: [{ type: "text", text }], details: result, isError };
    },
  });
}

export default function matrixNotifyExtension(pi: ExtensionAPI): void {
  matrixNotify(pi, matrixSenderBinary(SENDER_BINARY));
}
