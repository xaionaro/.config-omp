// Detects local C/C++ compile commands, which this host forbids: compiles run remotely.

/** The denial names the remote routes that replace a local compile. */
export const LOCAL_COMPILE_HATCH = "build remotely: ssh <gorbe|.131> …, scripts/gorbe/go-test.sh, or distcc to .131 with no local fallback: `DISTCC_FALLBACK=0 DISTCC_HOSTS=<remote hosts only> CCACHE_PREFIX=distcc` (or CC/CXX=\"distcc …\") under `nice`, e.g. `DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CC=\"distcc clang\" nice -n 19 make -j16`";

const ASSIGNMENT = /^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/s;

/** Reports whether a DISTCC_HOSTS value names only remote hosts. */
function remoteOnlyHosts(value: string | undefined): boolean {
	if (value === undefined) return false;
	const hosts = value.split(/\s+/).filter((h) => h !== "" && !h.startsWith("-") && !h.startsWith("--"));
	if (hosts.length === 0) return false;
	return hosts.every((h) => {
		const host = h.replace(/^@/, "").replace(/^[^@]*@/, "").replace(/[/:,].*$/, "").replace(/^\[|\]$/g, "");
		return host !== "" && host !== "localhost" && !host.startsWith("127.") && host !== "::1" && host !== "0.0.0.0";
	});
}

/** Reports whether env routes every compile through distcc to remote hosts only, with no local fallback. */
function remoteDistcc(env: Record<string, string>, niced: boolean, program: string): boolean {
	if (!niced || env.DISTCC_FALLBACK !== "0" || !remoteOnlyHosts(env.DISTCC_HOSTS)) return false;
	if (program === "distcc") return true;
	if (env.CCACHE_PREFIX === "distcc") return true;
	const compilers = [env.CC, env.CXX].filter((v) => v !== undefined);
	return compilers.length > 0 && compilers.every((v) => /^(?:\S*\/)?distcc(?:\s|$)/.test(v.trim()));
}

/** Commands that run their remaining arguments as the real command. */
const WRAPPERS = ["env", "nice", "ionice", "timeout", "nohup", "time", "command", "exec", "sudo", "stdbuf", "chrt", "taskset", "setsid", "xargs", "prlimit", "systemd-run", "load-gate.sh"];

/** Wrapper options that take a separate value word. */
const WRAPPER_VALUE_OPTIONS: Record<string, string[]> = {
	env: ["-u", "--unset", "-C", "--chdir", "-S", "--split-string"],
	nice: ["-n", "--adjustment"],
	ionice: ["-c", "-n", "-p", "-P", "-u"],
	timeout: ["-s", "--signal", "-k", "--kill-after"],
	sudo: ["-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U"],
	stdbuf: ["-i", "-o", "-e"],
	xargs: ["-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s"],
	"systemd-run": ["-p", "--property", "-u", "--unit", "-E", "--setenv", "--slice", "-M", "--machine", "-H", "--host"],
};

/** Wrappers whose first non-option word is a value, not the command. */
const WRAPPER_LEADING_VALUES: Record<string, number> = { timeout: 1, chrt: 1, taskset: 1 };

const SHELLS = ["bash", "sh", "dash", "zsh", "ksh"];

/** Compiler and build-driver names, matched against a command's basename. */
const COMPILERS = /^(?:[\w.]+-)*(?:gcc|g\+\+|cc|c\+\+|clang|clang\+\+|cc1|cc1plus)(?:-\d+(?:\.\d+)*)?$/;
const BUILD_DRIVERS = ["make", "gmake", "ninja", "distcc", "ccache", "icecc", "samu"];

/** Removes here-document bodies, which are data rather than commands. */
function withoutHereDocuments(command: string): string {
	const lines = command.split("\n");
	const kept: string[] = [];
	const pending: string[] = [];
	for (const line of lines) {
		if (pending.length > 0) {
			if (line.replace(/^\t+/, "") === pending[0]) pending.shift();
			continue;
		}
		kept.push(line);
		for (const match of line.matchAll(/<<-?\s*(['"]?)([A-Za-z_][\w.-]*)\1/g)) pending.push(match[2]);
	}
	return kept.join("\n");
}

/** Splits a shell command into words per simple command; quotes are honored, expansions kept literal. */
function simpleCommands(command: string): string[][] {
	const commands: string[][] = [];
	let words: string[] = [];
	let word = "";
	let inWord = false;
	const endWord = () => {
		if (inWord) words.push(word);
		word = "";
		inWord = false;
	};
	const endCommand = () => {
		endWord();
		if (words.length > 0) commands.push(words);
		words = [];
	};
	for (let i = 0; i < command.length; i++) {
		const c = command[i];
		if (c === "'") {
			const end = command.indexOf("'", i + 1);
			word += command.slice(i + 1, end < 0 ? command.length : end);
			inWord = true;
			i = end < 0 ? command.length : end;
		} else if (c === '"') {
			i++;
			while (i < command.length && command[i] !== '"') {
				if (command[i] === "\\" && i + 1 < command.length) i++;
				word += command[i];
				i++;
			}
			inWord = true;
		} else if (c === "\\" && i + 1 < command.length) {
			if (command[i + 1] !== "\n") {
				word += command[i + 1];
				inWord = true;
			}
			i++;
		} else if (c === ";" || c === "&" || c === "|" || c === "\n" || c === "(" || c === ")" || c === "`") {
			endCommand();
		} else if (c === "$" && command[i + 1] === "(") {
			endCommand();
			i++;
		} else if (c === " " || c === "\t") {
			endWord();
		} else if (c === "#" && !inWord) {
			while (i < command.length && command[i] !== "\n") i++;
			endCommand();
		} else {
			word += c;
			inWord = true;
		}
	}
	endCommand();
	return commands;
}

const SHELL_KEYWORDS = ["if", "then", "else", "elif", "while", "until", "do", "!", "{", "}"];

/** Returns the denied program of one simple command, or undefined. env holds the variables
 * exported before it; niced is whether an enclosing command already runs under nice.
 */
function deniedProgram(words: string[], depth: number, exported: Record<string, string>, outerNiced: boolean): string | undefined {
	const env = { ...exported };
	let niced = outerNiced;
	let i = 0;
	while (i < words.length) {
		const w = words[i];
		const assignment = ASSIGNMENT.exec(w);
		if (assignment) {
			env[assignment[1]] = assignment[2];
			i++;
			continue;
		}
		if (SHELL_KEYWORDS.includes(w) || /^\d*[<>]/.test(w)) {
			i += /^\d*[<>]+&?$/.test(w) ? 2 : 1;
			continue;
		}
		const name = w.slice(w.lastIndexOf("/") + 1);
		if (WRAPPERS.includes(name)) {
			if (name === "nice") niced = true;
			i++;
			let leading = WRAPPER_LEADING_VALUES[name] ?? 0;
			while (i < words.length) {
				const a = words[i];
				if (a === "--") {
					i++;
					break;
				}
				if (a.startsWith("-") && a !== "-") {
					i += WRAPPER_VALUE_OPTIONS[name]?.includes(a) ? 2 : 1;
					continue;
				}
				const wrapperAssignment = name === "env" ? ASSIGNMENT.exec(a) : null;
				if (wrapperAssignment) {
					env[wrapperAssignment[1]] = wrapperAssignment[2];
					i++;
					continue;
				}
				if (leading > 0) {
					leading--;
					i++;
					continue;
				}
				break;
			}
			continue;
		}
		if (SHELLS.includes(name)) {
			const flag = words.findIndex((a, k) => k > i && /^-[a-z]*c[a-z]*$/.test(a));
			if (flag > 0 && flag + 1 < words.length && depth < 4) return localCompileProgram(words[flag + 1], depth + 1, env, niced);
			return undefined;
		}
		let program: string | undefined;
		if (COMPILERS.test(name) || BUILD_DRIVERS.includes(name)) program = name;
		else if (name === "cmake" && words.slice(i + 1).some((a) => a === "--build" || a === "--install")) program = "cmake --build";
		if (program === undefined || remoteDistcc(env, niced, name)) return undefined;
		return program;
	}
	return undefined;
}

function localCompileProgram(command: string, depth: number, inherited: Record<string, string>, niced: boolean): string | undefined {
	const exported = { ...inherited };
	for (const words of simpleCommands(command)) {
		if (words[0] === "export") {
			for (const word of words.slice(1)) {
				const assignment = ASSIGNMENT.exec(word);
				if (assignment) exported[assignment[1]] = assignment[2];
			}
			continue;
		}
		const program = deniedProgram(words, depth, exported, niced);
		if (program !== undefined) return program;
	}
	return undefined;
}

/** Returns the denial reason for a Bash command that compiles C/C++ locally, or undefined. */
export function localCompileDenial(command: string): string | undefined {
	const program = localCompileProgram(withoutHereDocuments(command), 0, {}, false);
	if (program === undefined) return undefined;
	return `ECI_LOCAL_COMPILE_DENIED: \`${program}\` compiles C/C++ on this host, and local C/C++ compiles are forbidden. ${LOCAL_COMPILE_HATCH}`;
}
