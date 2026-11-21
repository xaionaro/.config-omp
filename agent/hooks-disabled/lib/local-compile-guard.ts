// Detects local C/C++ compile commands, which this host forbids: builds run remotely.

/** The denial names the remote route that replaces a local compile. */
export const LOCAL_COMPILE_HATCH = "build remotely: ssh <gorbe|.131> … (or scripts/gorbe/go-test.sh, which compiles on the gorbe server)";

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

/** Returns the denied program of one simple command, or undefined. */
function deniedProgram(words: string[], depth: number): string | undefined {
	let i = 0;
	while (i < words.length) {
		const w = words[i];
		if (SHELL_KEYWORDS.includes(w) || /^[A-Za-z_][A-Za-z0-9_]*=/.test(w) || /^\d*[<>]/.test(w)) {
			i += /^\d*[<>]+&?$/.test(w) ? 2 : 1;
			continue;
		}
		const name = w.slice(w.lastIndexOf("/") + 1);
		if (WRAPPERS.includes(name)) {
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
				if (name === "env" && /^[A-Za-z_][A-Za-z0-9_]*=/.test(a)) {
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
			if (flag > 0 && flag + 1 < words.length && depth < 4) return localCompileProgram(words[flag + 1], depth + 1);
			return undefined;
		}
		if (COMPILERS.test(name) || BUILD_DRIVERS.includes(name)) return name;
		if (name === "cmake" && words.slice(i + 1).some((a) => a === "--build" || a === "--install")) return "cmake --build";
		return undefined;
	}
	return undefined;
}

function localCompileProgram(command: string, depth: number): string | undefined {
	for (const words of simpleCommands(command)) {
		const program = deniedProgram(words, depth);
		if (program !== undefined) return program;
	}
	return undefined;
}

/** Returns the denial reason for a Bash command that compiles C/C++ locally, or undefined. */
export function localCompileDenial(command: string): string | undefined {
	const program = localCompileProgram(withoutHereDocuments(command), 0);
	if (program === undefined) return undefined;
	return `ECI_LOCAL_COMPILE_DENIED: \`${program}\` compiles C/C++ on this host, and local C/C++ compiles are forbidden. ${LOCAL_COMPILE_HATCH}`;
}
