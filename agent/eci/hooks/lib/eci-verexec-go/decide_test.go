package main

import (
	"strings"
	"testing"
)

func TestDescriptorReasonTable(t *testing.T) {
	const euid = 1000
	reg := uint32(modeRegular)
	cases := []struct {
		name     string
		f        fileFacts
		readable bool
		want     string
	}{
		{"accept", fileFacts{reg | 0o755, 1, euid}, true, ""},
		{"directory", fileFacts{modeDir | 0o755, 1, euid}, true, reasonNotRegular},
		{"hard link", fileFacts{reg | 0o755, 2, euid}, true, reasonOutside},
		{"other owner", fileFacts{reg | 0o755, 1, 4242}, true, reasonNotOwned},
		{"root owner", fileFacts{reg | 0o755, 1, 0}, true, reasonNotOwned},
		{"unreadable mode", fileFacts{reg | 0o755, 1, euid}, false, reasonUnreadableMode},
		{"group writable", fileFacts{reg | 0o775, 1, euid}, true, reasonWritable},
		{"other writable", fileFacts{reg | 0o757, 1, euid}, true, reasonWritable},
		{"not executable", fileFacts{reg | 0o644, 1, euid}, true, reasonNotExecutable},
		{"user exec only", fileFacts{reg | 0o500, 1, euid}, true, ""},
		// First failure wins, in the loader's order.
		{"hard link and writable", fileFacts{reg | 0o777, 3, euid}, true, reasonOutside},
	}
	for _, c := range cases {
		if got := descriptorReason(c.f, euid, c.readable); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDirectoryTrusted(t *testing.T) {
	cases := []struct {
		mode, uid uint32
		want      bool
	}{
		{modeDir | 0o755, 1000, true},
		{modeDir | 0o700, 0, true},
		{modeDir | 0o775, 1000, false},
		{modeDir | 0o1777, 0, false},
		{modeDir | 0o755, 4242, false},
		{modeSymlink | 0o755, 1000, false},
	}
	for _, c := range cases {
		if got := directoryTrusted(c.mode, c.uid, 1000); got != c.want {
			t.Errorf("mode %o uid %d: got %v", c.mode, c.uid, got)
		}
	}
}

func TestELFMatchesHost(t *testing.T) {
	host := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xb7, 0}
	mut := func(i int, b byte) []byte { c := append([]byte(nil), host...); c[i] = b; return c }
	cases := []struct {
		name string
		hdr  []byte
		want bool
	}{
		{"same", host, true},
		{"shared object", mut(16, 3), true},
		{"osabi linux", mut(7, 3), true},
		{"osabi freebsd", mut(7, 9), false},
		{"class", mut(4, 1), false},
		{"byte order", mut(5, 2), false},
		{"machine", mut(18, 2), false},
		{"machine high", mut(19, 1), false},
		{"magic", mut(0, 0), false},
		{"fat magic", []byte{0xca, 0xfe, 0xba, 0xbe, 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xb7, 0}, false},
		{"script", []byte("#!/bin/sh\necho hi  ;"), false},
		{"short", host[:19], false},
	}
	for _, c := range cases {
		if got := elfMatchesHost(c.hdr, host); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

// The receipt table of test-eci-native-binary-loader.sh checks 48-52 and 69-76.
func TestReceiptTable(t *testing.T) {
	const name = "eci-hook-helper"
	digest := strings.Repeat("ab", 32)
	line := func(s string) []byte { return []byte(s) }
	cases := []struct {
		name, art string
		data      []byte
		want      string
	}{
		{"valid", name, line(digest + "  " + name + "\n"), ""},
		{"valid without newline", name, line(digest + "  " + name), ""},
		{"only first line counts", name, line(digest + "  " + name + "\ngarbage\n"), ""},
		{"wrong hash", name, line(strings.Repeat("cd", 32) + "  " + name + "\n"), reasonMismatch},
		{"wrong name", name, line(digest + "  eci-command-plan\n"), reasonMalformed},
		{"extra suffix", name, line(digest + "  " + name + " x\n"), reasonMalformed},
		{"uppercase hex", name, line(strings.ToUpper(digest) + "  " + name + "\n"), reasonMalformed},
		{"single space", name, line(digest + " " + name + "\n"), reasonMalformed},
		{"missing (empty)", name, nil, reasonMalformed},
		{"carriage return", name, line(digest + "  " + name + "\r\n"), reasonMalformed},
		{"NUL in line", name, line(digest + "  " + name[:3] + "\x00" + name[3:] + "\n"), reasonMalformed},
		{"name metachar", "eci.hook", line(digest + "  eci.hook\n"), reasonNameNotAllowed},
		{"empty name", "", line(digest + "  \n"), reasonNameNotAllowed},
	}
	for _, c := range cases {
		if got := receiptReason(c.art, c.data, digest); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAllowedTarget(t *testing.T) {
	for _, n := range []string{"eci-command-plan", "eci-safe-import", "eci-hook-helper"} {
		if !allowedTarget(n) {
			t.Errorf("%s denied", n)
		}
	}
	for _, n := range []string{"", "eci-verexec", "eci-worker-git", "../eci-hook-helper", "eci-hook-helper/"} {
		if allowedTarget(n) {
			t.Errorf("%q allowed", n)
		}
	}
}

func TestDenialText(t *testing.T) {
	want := `ECI native gate binary eci-hook-helper is unavailable or unverified (is not executable); provision it with "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" and retry` + "\n"
	if got := denialText("eci-hook-helper", reasonNotExecutable); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestParseArgs(t *testing.T) {
	inv, err := parseArgs([]string{"--check", "--deny-fd", "3", "eci-hook-helper"})
	if err != nil || !inv.check || inv.denyFD != 3 || inv.name != "eci-hook-helper" || len(inv.args) != 0 {
		t.Fatalf("got %+v %v", inv, err)
	}
	inv, err = parseArgs([]string{"--deny-fd", "4", "eci-command-plan", "--", "--hook", "--"})
	if err != nil || inv.check || inv.denyFD != 4 || strings.Join(inv.args, ",") != "--hook,--" {
		t.Fatalf("got %+v %v", inv, err)
	}
	for _, bad := range [][]string{{}, {"eci-hook-helper"}, {"--deny-fd"}, {"--deny-fd", "x", "n"}, {"--deny-fd", "3"}, {"--deny-fd", "3", "n", "x"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
