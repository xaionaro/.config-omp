/**
 * Remove OMP-generated <files> inventories from textual summary context.
 *
 * Load explicitly: omp --extension ./no-files-context.ts
 * Or put this file in the active profile's extensions directory and restart.
 * Default profile: ~/.omp/agent/extensions/
 *
 * Compaction continues normally. This filters each request's context;
 * stored session history and file-operation metadata remain available.
 * Only compactionSummary and branchSummary messages are transformed.
 * Literal <files> text in ordinary user, assistant, and tool messages is kept.
 * Snapcompact's separate FILES section and opaque native compaction payloads
 * are outside this filter's scope.
 *
 * API reference:
 * https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md
 * https://github.com/can1357/oh-my-pi/blob/main/docs/compaction.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

export default function noFilesContext(pi: ExtensionAPI): void {
  pi.on("context", (event) => {
    let changed = false;

    const messages = event.messages.map((message) => {
      if (
        message.role !== "compactionSummary" &&
        message.role !== "branchSummary"
      ) {
        return message;
      }
      if (typeof message.summary !== "string") return message;

      const summary = message.summary.replace(/<files>[\s\S]*?<\/files>\s*/g, "");
      if (summary === message.summary) return message;

      changed = true;
      return { ...message, summary: summary.trimEnd() };
    });

    if (changed) return { messages };
  });
}
