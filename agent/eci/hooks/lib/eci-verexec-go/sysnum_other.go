//go:build linux && !(arm64 || riscv64 || loong64 || amd64 || 386 || arm)

package main

// No execveat number is wired for this architecture; exec falls back to /proc/self/fd/N.
const sysExecveat = -1
