import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

// Runs the prebuilt Go sender from agent/tools/matrix-notify. It reads Hermes's Matrix credentials in place and sends
// as its own device; no secret passes through omp. The Go sender owns the alias→Matrix ID table and input validation.
const SENDER_BINARY = join(homedir(), ".omp/agent/matrix-notify/bin/matrix-notify");
const BUILD_HINT =
  "build it as documented in ~/.omp/agent/tools/matrix-notify/main.go, and rebuild after any change there";
// The Go sender owns the run budget via -deadline; the kill comes later so it can still report its JSON result.
const SENDER_DEADLINE_S = 120;
const KILL_MARGIN_MS = 30_000;

const ALIASES = ["void", "vickey"] as const;
export type Alias = (typeof ALIASES)[number];

export type MatrixSendResult =
  | { status: "sent"; room_id: string; event_id: string; target_membership: string }
  | { status: "invite_pending" | "key_pending"; room_id: string }
  | { status: "error"; error: string };

export type MatrixSender = (to: Alias, message: string, signal?: AbortSignal) => Promise<MatrixSendResult>;

const isString = (v: unknown): v is string => typeof v === "string" && v !== "";

// parseSenderOutput accepts exactly the result shapes a send can produce; anything else is an error result.
// pi.zod exists only inside omp, so this closed check keeps the module testable under plain node.
export function parseSenderOutput(stdout: string): MatrixSendResult {
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(stdout.trim().split("\n").pop() ?? "");
  } catch {
    return { status: "error", error: "unreadable sender output" };
  }
  switch (raw?.status) {
    case "sent":
      if (isString(raw.room_id) && isString(raw.event_id) && isString(raw.target_membership)) {
        return { status: "sent", room_id: raw.room_id, event_id: raw.event_id, target_membership: raw.target_membership };
      }
      break;
    case "invite_pending":
    case "key_pending":
      if (isString(raw.room_id)) return { status: raw.status, room_id: raw.room_id };
      break;
    case "error":
      if (isString(raw.error)) return { status: "error", error: raw.error };
      break;
  }
  return { status: "error", error: "unexpected sender output" };
}

// matrixSenderBinary runs the binary directly (no shell), passes the message on stdin and trusts only its JSON stdout.
export function matrixSenderBinary(binary: string): MatrixSender {
  return (to, message, signal) => {
    // TODO(C5): existsSync cannot tell a stale binary from a current one, and the binary lives in the secret state
    // dir; embed a source digest at build time (-ldflags -X), refuse a mismatch with BUILD_HINT, and move the
    // artifact beside the repo's other generated binaries.
    if (!existsSync(binary)) {
      return Promise.resolve({ status: "error", error: `sender not built; ${BUILD_HINT}` });
    }
    const { promise, resolve } = Promise.withResolvers<MatrixSendResult>();
    const args = ["-to", to, "-deadline", `${SENDER_DEADLINE_S}s`];
    const child = execFile(binary, args, { timeout: SENDER_DEADLINE_S * 1000 + KILL_MARGIN_MS, signal }, (error, stdout) => {
      const result = parseSenderOutput(stdout);
      if (result.status === "error" && error?.name === "AbortError") {
        resolve({ status: "error", error: "cancelled" });
        return;
      }
      resolve(result);
    });
    // A sender that exits before reading stdin closes the pipe; its JSON result (or crash) is reported above.
    child.stdin?.on("error", (err: NodeJS.ErrnoException) => {
      if (err.code !== "EPIPE") throw err;
    });
    child.stdin?.end(message);
    return promise;
  };
}

function describe(to: Alias, r: MatrixSendResult): { text: string; isError: boolean } {
  switch (r.status) {
    case "sent": {
      const invite = r.target_membership === "invite" ? " The recipient has not joined the room yet." : "";
      return {
        text: `Matrix accepted the encrypted message to ${to}: event ${r.event_id} in ${r.room_id}. This is not a delivery or read receipt.${invite}`,
        isError: false,
      };
    }
    case "invite_pending":
      return { text: `NOT sent to ${to}: the recipient must join the DM before they can decrypt it.`, isError: true };
    case "key_pending":
      return { text: `NOT sent to ${to}: no device of the recipient could receive the encryption key yet.`, isError: true };
    case "error":
      return { text: `NOT sent to ${to}: error category: ${r.error}.`, isError: true };
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
