import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

// Wraps the prebuilt Go helper from agent/tools/matrix-notify (see its main.go for the one-time build). The helper
// reads Hermes's Matrix credentials in place and sends as its own device; no secret passes through omp.
const BINARY = join(homedir(), ".omp/agent/matrix-notify/bin/matrix-notify");
const BUILD_HINT =
  "cd ~/.omp/agent/tools/matrix-notify && go build -tags goolm -o ~/.omp/agent/matrix-notify/bin/matrix-notify .";
const RUN_TIMEOUT_MS = 150_000;

export interface HelperResult {
  status: "sent" | "resolved" | "absent" | "invite_pending" | "key_pending" | "error";
  room_id?: string;
  event_id?: string;
  target_membership?: string;
  error?: string;
  [key: string]: unknown;
}

export type HelperRunner = (to: string, message: string, signal?: AbortSignal) => Promise<HelperResult>;

// helperAt runs the binary directly (no shell), passes the message on stdin and trusts only its JSON stdout.
export function helperAt(binary: string): HelperRunner {
  return (to, message, signal) => {
    if (!existsSync(binary)) {
      return Promise.resolve({ status: "error", error: `helper not built; run once: ${BUILD_HINT}` });
    }
    const { promise, resolve } = Promise.withResolvers<HelperResult>();
    const child = execFile(binary, ["-to", to], { timeout: RUN_TIMEOUT_MS, signal }, (error, stdout) => {
      try {
        resolve(JSON.parse(stdout.trim().split("\n").pop() ?? "") as HelperResult);
      } catch {
        resolve({ status: "error", error: error?.name === "AbortError" ? "cancelled" : "helper crashed or timed out" });
      }
    });
    // A helper that exits before reading stdin closes the pipe; its JSON result (or crash) is reported above.
    child.stdin?.on("error", () => {});
    child.stdin?.end(message);
    return promise;
  };
}

function describe(to: string, r: HelperResult): { text: string; isError: boolean } {
  if (r.status === "sent" && r.event_id) {
    const invite = r.target_membership === "invite" ? " The recipient has not joined the room yet." : "";
    return {
      text: `Matrix accepted the encrypted message to ${to}: event ${r.event_id} in ${r.room_id}. This is not a delivery or read receipt.${invite}`,
      isError: false,
    };
  }
  const why: Record<string, string> = {
    invite_pending: "the recipient must join the DM before they can decrypt it",
    key_pending: "the recipient has no Matrix device to encrypt for yet",
    error: `error category: ${r.error ?? "unknown"}`,
  };
  return { text: `NOT sent to ${to}: ${why[r.status] ?? "the helper reported no event ID"}.`, isError: true };
}

export function matrixNotify(pi: ExtensionAPI, helper: HelperRunner): void {
  const z = pi.zod;
  pi.registerTool({
    name: "matrix_send",
    label: "Matrix send",
    description:
      "Send an end-to-end encrypted Matrix DM from Hermes's Matrix account to 'void' (the user, @xaionaro:matrix.dx.center) " +
      "or 'vickey' (@danusya:matrix.dx.center). Use it to notify them outside this session, e.g. a result, decision or blocker. " +
      "The message is sent as literal text. Success means Matrix accepted the event, not that it was read.",
    parameters: z.object({
      to: z.enum(["void", "vickey"]),
      message: z.string().min(1),
    }),
    async execute(_toolCallId, params, signal) {
      if (params.message.trim() === "") {
        return { content: [{ type: "text", text: "NOT sent: message is empty or whitespace." }], details: {}, isError: true };
      }
      const result = await helper(params.to, params.message, signal);
      const { text, isError } = describe(params.to, result);
      return { content: [{ type: "text", text }], details: result, isError };
    },
  });
}

export default function matrixNotifyExtension(pi: ExtensionAPI): void {
  matrixNotify(pi, helperAt(BINARY));
}
