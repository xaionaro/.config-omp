// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/local-compile-guard.test.ts
import { expect, test } from "bun:test";
import { localCompileDenial } from "./local-compile-guard";

test("denies local C/C++ compiles, also behind wrappers and shells", () => {
	for (const command of [
		"make -j8",
		"ninja -C out",
		"cmake --build build",
		"g++ -O2 a.cc",
		"/usr/bin/clang++ a.cc",
		"aarch64-linux-android21-clang -c a.c",
		"gcc-12 a.c",
		"distcc gcc a.c",
		"ccache clang a.c",
		"cd src && make",
		"nice -n 19 make",
		"env CC=clang make all",
		"timeout 600 ninja",
		"~/bin/load-gate.sh make -j4",
		"bash -c 'cd x; make'",
		"sudo -u builder nice ninja",
		"FOO=1 make",
		"echo start; ( make )",
		"prlimit --as=1073741824 -- make -j4",
		"systemd-run --scope -p MemoryMax=1G ninja",
	]) {
		const reason = localCompileDenial(command);
		expect(reason, command).toBeDefined();
		expect(reason).toContain("ECI_LOCAL_COMPILE_DENIED");
		expect(reason).toContain("ssh <gorbe|.131>");
	}
});

test("allows remote builds, Go, Bazel, gorbe and text that only mentions compilers", () => {
	for (const command of [
		"ssh gorbe 'cd src && make -j8'",
		"ssh 192.168.0.131 ninja -C out",
		"go test ./...",
		"go build ./cmd/x",
		"bazel build //...",
		"GORBE_ROOT=. ./scripts/gorbe/go-test.sh --cgo ./pkg/ffstream",
		"grep -n make Makefile",
		"git commit -F msg.txt",
		"echo 'make -j8'",
		"cmake -S . -B build",
		"cat > m.txt <<'EOF'\nmake all\ng++ a.cc\nEOF\ngit status",
		"rg 'gcc|clang' .",
	]) {
		expect(localCompileDenial(command), command).toBeUndefined();
	}
});
