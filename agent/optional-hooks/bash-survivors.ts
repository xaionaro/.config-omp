// Optional advisory hook (Linux /proc only): when a Bash call finishes, tell the model which processes it left running.
// Foreground calls are checked at their tool result. Background jobs (`async: true` or auto-backgrounded) are checked
// when a `wait` result or an async-result message reports them settled; omp has no job-completion hook event.
// Named services are meant to keep running and are never checked. The Go helper owns the scan and the notice text;
// any failure means no notice. Not covered: jobs settled through eval's wait(), processes alive at session teardown,
// and processes launched with a cleared environment (env -i, sudo, systemd-run, containers).
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { ensureHookBinary, waitForProvision } from "../eci/native-hook-binary.ts";

const exec = promisify(execFile);
// Every tool result waits on this hook, so a slow helper build or scan costs at most this long before failing open.
const HELPER_TIMEOUT_MS = 2000;
let warned = false;

type BashDetails = { async?: { state?: unknown; jobId?: unknown }; service?: unknown };
type SettledJob = { id?: unknown; jobId?: unknown; status?: unknown };

// Notice naming processes still tagged with any of callIds, or undefined when none or on failure.
async function survivorNotice(callIds: string[], ctx: ExtensionContext): Promise<string | undefined> {
	if (callIds.length === 0) return undefined;
	try {
		const binary = await waitForProvision(ensureHookBinary(), HELPER_TIMEOUT_MS);
		const { stdout } = await exec(binary, ["call-survivors", ...callIds], { timeout: HELPER_TIMEOUT_MS });
		return stdout.trim() || undefined;
	} catch (error) {
		if (!warned) {
			warned = true;
			const message = `Bash survivor notice unavailable (${String(error)}). Rebuild with: bash "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" eci-hook-helper`;
			console.error(message);
			ctx.ui?.notify(message, "warning");
		}
		return undefined;
	}
}

export default function bashSurvivors(pi: ExtensionAPI): void {
	// This session's running background Bash jobs: job ID -> tool call ID.
	const runningJobs = new Map<string, string>();
	const forgetJobs = () => { runningJobs.clear(); };
	pi.on("session_start", forgetJobs);
	pi.on("session_switch", forgetJobs);
	pi.on("session_tree", forgetJobs);
	pi.on("session_branch", forgetJobs);

	// Claims the tool call IDs of remembered jobs that settled; `wait` keys jobs by `id`, async-result by `jobId`.
	function claimSettled(jobs: unknown, key: "id" | "jobId"): string[] {
		if (!Array.isArray(jobs)) return [];
		const callIds: string[] = [];
		for (const job of jobs as SettledJob[]) {
			const jobId = job[key];
			if (typeof jobId !== "string" || job.status === "running") continue;
			const callId = runningJobs.get(jobId);
			if (callId === undefined) continue;
			runningJobs.delete(jobId);
			callIds.push(callId);
		}
		return callIds;
	}

	pi.on("tool_result", async (event, ctx) => {
		let callIds: string[];
		switch (event.toolName) {
			case "wait":
				callIds = claimSettled((event.details as { jobs?: unknown } | undefined)?.jobs, "id");
				break;
			case "bash": {
				const details = event.details as BashDetails | undefined;
				if (details?.service !== undefined) return undefined;
				if (details?.async?.state === "running") {
					if (typeof details.async.jobId === "string") runningJobs.set(details.async.jobId, event.toolCallId);
					return undefined;
				}
				callIds = [event.toolCallId];
				break;
			}
			default:
				return undefined;
		}
		const additionalContext = await survivorNotice(callIds, ctx);
		return additionalContext ? { additionalContext } : undefined;
	});

	pi.on("message_end", async (event, ctx) => {
		const message = event.message as { role?: unknown; customType?: unknown; details?: { jobs?: unknown } };
		if (message.role !== "custom" || message.customType !== "async-result") return;
		const content = await survivorNotice(claimSettled(message.details?.jobs, "jobId"), ctx);
		if (content) pi.sendMessage({ customType: "bash-survivors", content, display: false }, { deliverAs: "aside" });
	});
}
