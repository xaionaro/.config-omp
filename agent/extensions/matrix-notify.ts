import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

// Sends a Matrix DM as Hermes's bot account. Credentials, E2EE crypto store and device stay in Hermes
// (~/.hermes/.env + config.yaml + platforms/matrix/store); this extension only runs Hermes's own Python
// MatrixAdapter in a short-lived subprocess, so nothing secret passes through omp.
export const RECIPIENTS = {
  void: "@xaionaro:matrix.dx.center",
  vickey: "@danusya:matrix.dx.center",
} as const;
export type Recipient = keyof typeof RECIPIENTS;

const HELPER_TIMEOUT_MS = 180_000;

export interface HermesRuntime {
  python: string;
  root: string;
}

// The `hermes` launcher pins the interpreter and source tree; reuse them instead of duplicating paths.
export function parseHermesLauncher(text: string): HermesRuntime | undefined {
  const python = /exec (\S+python3?) -I -c/.exec(text)?.[1];
  const root = /sys\.path\.insert\(0, '"'"'([^']+)'"'"'\)/.exec(text)?.[1];
  return python && root ? { python, root } : undefined;
}

function resolveHermesRuntime(): HermesRuntime {
  const outer = readFileSync(join(homedir(), ".local/bin/hermes"), "utf8");
  const inner = /exec (\S+) "\$@"/.exec(outer)?.[1];
  const runtime = parseHermesLauncher(inner ? readFileSync(inner, "utf8") : outer);
  if (!runtime) throw new Error("cannot locate Hermes python/source tree from the hermes launcher");
  return runtime;
}

// argv: <root> <mxid> <dry_run 0|1>; message on stdin; one JSON line on stdout.
export const HELPER_PY = String.raw`
import asyncio, json, logging, os, sys
root, mxid, dry_run = sys.argv[1], sys.argv[2], sys.argv[3] == "1"
sys.path.insert(0, root)
sys._hermes_pin_default_home = True
import hermes_bootstrap  # noqa: F401
if not os.environ.get("HERMES_HOME"):
    from hermes_constants import get_default_hermes_root
    os.environ["HERMES_HOME"] = str(get_default_hermes_root())
logging.disable(logging.WARNING)
message = sys.stdin.read()
from hermes_cli.send_cmd import _load_hermes_env
_load_hermes_env()
from gateway.config import load_gateway_config
from tools.send_message_tool import _resolve_platform_config, prepare_send_message_platforms
prepare_send_message_platforms()
_, pconfig, _, err = _resolve_platform_config("matrix", load_gateway_config())
if err:
    print(json.dumps({"ok": False, "error": str(err)})); sys.exit(1)
from mautrix.types import EventType, RoomID, UserID
from plugins.platforms.matrix.adapter import MatrixAdapter

async def membership(client, room, user):
    try:
        ev = await client.get_state_event(RoomID(room), EventType.ROOM_MEMBER, user)
        return str(getattr(ev, "membership", "") or "").rsplit(".", 1)[-1].lower()
    except Exception:
        return ""

async def find_dm(adapter):
    client = adapter._client
    joined = [str(r) for r in await client.get_joined_rooms()]
    dm = await adapter._fetch_m_direct() or {}
    listed = [r for r in dm.get(mxid, []) if r in joined]
    # m.direct rooms first (target joined or still invited), then any joined 2-member room with the target.
    for room in listed:
        if await membership(client, room, mxid) in ("join", "invite"):
            return room
    for room in joined:
        if room in listed:
            continue
        members = await client.get_joined_members(RoomID(room))
        if len(members) == 2 and UserID(mxid) in members:
            return room
    return None

async def main():
    adapter = MatrixAdapter(pconfig)
    out = {"ok": False, "recipient_mxid": mxid, "dry_run": dry_run}
    try:
        if not await adapter.connect():
            out["error"] = "Matrix connect failed"; return out
        out["sender_mxid"] = str(adapter._client.mxid)
        room = await find_dm(adapter)
        out["room_id"], out["created"] = room, False
        if dry_run:
            out["ok"], out["would_create"] = True, room is None
            return out
        if room is None:
            state = [{"type": "m.room.encryption", "state_key": "",
                      "content": {"algorithm": "m.megolm.v1.aes-sha2"}}] if adapter._encryption else None
            room = str(await adapter._client.create_room(is_direct=True, invitees=[UserID(mxid)], initial_state=state))
            await adapter._record_dm_room(room, mxid)
            out["room_id"], out["created"] = room, True
        result = await adapter.send(room, message)
        out["ok"], out["event_id"] = bool(result.success), result.message_id
        if not result.success:
            out["error"] = str(result.error)
        return out
    except Exception as exc:
        out["error"] = f"{type(exc).__name__}: {exc}"
        return out
    finally:
        try:
            await adapter.disconnect()
        except Exception:
            pass

res = asyncio.run(main())
print(json.dumps(res))
sys.exit(0 if res.get("ok") else 1)
`;

export interface HelperResult {
  ok: boolean;
  error?: string;
  [key: string]: unknown;
}

export type HelperRunner = (mxid: string, message: string, dryRun: boolean, signal?: AbortSignal) => Promise<HelperResult>;

export const runHermesHelper: HelperRunner = (mxid, message, dryRun, signal) => {
  const { python, root } = resolveHermesRuntime();
  const { promise, resolve } = Promise.withResolvers<HelperResult>();
  const child = execFile(
    python,
    ["-I", "-c", HELPER_PY, root, mxid, dryRun ? "1" : "0"],
    { timeout: HELPER_TIMEOUT_MS, signal, maxBuffer: 4 * 1024 * 1024 },
    (error, stdout) => {
      const line = stdout.trim().split("\n").pop() ?? "";
      try {
        resolve(JSON.parse(line) as HelperResult);
      } catch {
        // stderr is withheld: it can carry Hermes diagnostics we have not vetted for secrets.
        resolve({ ok: false, error: `Hermes helper failed${error ? `: ${error.message.split("\n")[0]}` : ""}` });
      }
    },
  );
  child.stdin?.end(message);
  return promise;
};

export function matrixNotify(pi: ExtensionAPI, run: HelperRunner): void {
  const z = pi.zod;
  pi.registerTool({
    name: "matrix_notify",
    label: "Matrix notify",
    description:
      "Send a Matrix direct message to a human via Hermes's Matrix bot account (end-to-end encrypted DM; created on first use). " +
      "recipient: 'void' (the user, @xaionaro:matrix.dx.center) or 'vickey' (@danusya:matrix.dx.center). " +
      "Use for notifying people outside this session (e.g. a Supervisor reporting a result or a blocker). " +
      "dry_run=true resolves the DM room without sending.",
    parameters: z.object({
      recipient: z.enum(["void", "vickey"]),
      message: z.string().min(1),
      dry_run: z.boolean().optional(),
    }),
    async execute(_toolCallId, params, signal) {
      const mxid = RECIPIENTS[params.recipient as Recipient];
      const result = await run(mxid, params.message, params.dry_run === true, signal);
      const summary = result.ok
        ? params.dry_run
          ? `Resolved ${params.recipient} (${mxid}) -> ${result.room_id ?? "no DM room yet; a send would create one"}`
          : `Sent to ${params.recipient} (${mxid}) in ${result.room_id}${result.created ? " (new DM room)" : ""}`
        : `Matrix send to ${params.recipient} failed: ${result.error ?? "unknown error"}`;
      return { content: [{ type: "text", text: summary }], details: result, isError: !result.ok };
    },
  });
}

export default function matrixNotifyExtension(pi: ExtensionAPI): void {
  matrixNotify(pi, runHermesHelper);
}
