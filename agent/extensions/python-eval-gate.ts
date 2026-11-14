/**
 * Block eval tool Python cells.
 *
 * A Python cell calls the Python interpreter directly. This workstation runs
 * Python only as an executable script file (ECI_PYTHON_SCRIPT_REQUIRED; the
 * command planner enforces the same route for Bash). JavaScript cells are unaffected.
 *
 * Load: place in the active profile's extensions directory; delete this file and restart to disable.
 * Catalog: ~/.omp/agent/eci/hooks/gate-escape-hatches.md
 *
 * API reference:
 * https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

export const PYTHON_EVAL_BLOCK_REASON =
  "[ECI_PYTHON_SCRIPT_REQUIRED] eval Python cells call the Python interpreter directly and are blocked. Write the code to a script file whose first line is #!/usr/bin/python3 (not #!/usr/bin/env python3, which resolves python3 through PATH and can reach a refusing wrapper), chmod +x it, and run it by path with the bash tool, for example ./script.py.";

export default function pythonEvalGate(pi: ExtensionAPI): void {
  pi.on("tool_call", (event) => {
    if (event.toolName !== "eval") return undefined;
    const language = "language" in event.input ? event.input.language : undefined;
    // The eval tool also accepts "python" as an alias of "py".
    if (language === "py" || language === "python") return { block: true, reason: PYTHON_EVAL_BLOCK_REASON };
    return undefined;
  });
}
