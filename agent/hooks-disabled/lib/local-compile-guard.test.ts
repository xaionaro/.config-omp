// Runs with: BUN_BE_BUN=1 omp test agent/hooks-disabled/lib/local-compile-guard.test.ts
import { expect, test } from "bun:test";
import { localCompileDenial } from "./local-compile-guard";

test("denies C++ and builds not established as C, also behind wrappers and shells", () => {
	for (const command of [
		"make -j8",
		"ninja -C out",
		"cmake --build build",
		"g++ -O2 a.cc",
		"/usr/bin/clang++ a.cc",
		"gcc -c a.cc",
		"clang -c a.C",
		"gcc -c a.c b.cpp",
		"clang -x c++ -c a.c",
		"clang -xc++ -c a.c",
		"g++ -c a.c",
		"cc -c @inputs.rsp",
		"clang -o a.c unknown",
		"cc -c unknown",
		"clang -xc a -x none b.cc",
		"gcc -fsyntax-only -",
		"cc -### -x c++ -c -",
		"clang -ObjC -c a.c",
		"clang --driver-mode=g++ -c a.c",
		"clang -c a.c @inputs.rsp",
		"gcc -c a.c; clang++ -c b.cc",
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
		"DISTCC_HOSTS=192.168.0.131/16 CC='distcc clang' nice -n 19 make -j16",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS='localhost/4 192.168.0.131/16' CC='distcc clang' nice -n 19 make",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CC='distcc clang' make -j16",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 nice -n 19 make -j16",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=127.0.0.1 CCACHE_PREFIX=distcc nice ninja",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CCACHE_PREFIX=distcc nice -n 19 g++ -c a.cc",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CXX='distcc g++' nice -n 19 ccache g++ -c a.cc",
	]) {
		const reason = localCompileDenial(command);
		expect(reason, command).toBeDefined();
		expect(reason).toContain("ECI_LOCAL_COMPILE_DENIED");
		expect(reason).toContain("ssh <gorbe|.131>");
	}
});

test("allows native C inputs without assuming a driver's default language", () => {
	for (const command of [
		"gcc-12 -c a.c -o a.o",
		"aarch64-linux-android21-clang -c a.c",
		"cc -I include -include config.h -c a.c",
		"clang -x c -c input -o out.o",
		"gcc -x c -fsyntax-only -",
		"/usr/bin/cc -### -x c -c -",
		"/usr/bin/aarch64-linux-gnu-gcc-15 -O2 -c asm_arm64.S -o asm.o",
		"/usr/bin/aarch64-linux-gnu-gcc-15 -o app main.o -L lib -lfoo",
		"clang -xc input -x none -c b.c",
		"nice -n 19 env CC=clang clang -c a.c",
		"bash -c 'cd src; cc -c a.c'",
	]) {
		expect(localCompileDenial(command), command).toBeUndefined();
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
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CC='distcc clang' CXX='distcc clang++' nice -n 19 make -j16",
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 CCACHE_PREFIX=distcc nice -n 19 ninja -C build",
		"export DISTCC_FALLBACK=0 DISTCC_HOSTS='192.168.0.131/16,lzo'; nice -n 19 env CCACHE_PREFIX=distcc cmake --build build -j16",
		"nice -n 19 env DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 distcc clang -c a.c",
		"nice -n 19 env DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 distcc clang++ -c a.cc",
		// The WingOut APK lane: the gate sees only the script; its remote-only-android-cxx
		// launcher compiles through distcc to .131 with DISTCC_FALLBACK=0.
		"DISTCC_FALLBACK=0 DISTCC_HOSTS=192.168.0.131/16 nice -n 19 ./scripts/build_android_debug.sh",
	]) {
		expect(localCompileDenial(command), command).toBeUndefined();
	}
});
