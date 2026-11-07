package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestCurrentClientGrammar checks complete sections using synthetic fixtures;
// upstream shaped records reflect source formatting and provide no vendor evidence.
//
// Example: a matching first record followed by a malformed record is unknown.
func TestCurrentClientGrammar(
	t *testing.T,
) {
	upstream := "(Camera ID: 0, Cost: -100, PID: 9775, Score: +0, State: -1, Conflicting Client Devices: {})"
	withUser := "(Camera ID: 0, Cost: 100, PID: 9775, Score: 0, State: 1User Id: 0, Client Package Name: com.example.camera, Conflicting Client Devices: {1, 2, })"
	for _, fixture := range []struct {
		Name string
		Body string
		PID  string
		Code int
	}{
		{"empty", "[]", "9775", 1},
		{"empty whitespace", "[ \n ]", "9775", 1},
		{"upstream synthetic", "[" + upstream + "]", "9775", 0},
		{"upstream user package synthetic", "[" + withUser + "]", "9775", 0},
		{"user without package double comma", "[" + strings.Replace(withUser, "Client Package Name: com.example.camera", "", 1) + "]", "9775", 0},
		{"package without user", "[" + strings.Replace(withUser, "User Id: 0, ", ", ", 1) + "]", "9775", 0},
		{"multiple source records", "[" + upstream + "\n" + strings.Replace(upstream, "9775", "97750", 1) + "]", "97750", 0},
		{"other PID", "[" + upstream + "]", "97750", 1},
		{"flat compatibility", "[Camera ID: 0, Cost: 100, PID: 9775, Score: 0]", "9775", 0},
		{"flat minimal", "[Camera ID: rear, PID: 97750]", "9775", 1},
		{"labeled vendor", "[" + strings.Replace(upstream, ", Conflicting", ", Vendor Mode: shared, Conflicting", 1) + "]", "9775", 0},
		{"large textual PID held", "[Camera ID: 0, PID: 9007199254740993]", "9007199254740993", 0},
		{"large textual PID absent", "[Camera ID: 0, PID: 9007199254740992]", "9007199254740993", 1},
		{"noncanonical target", "[" + upstream + "]", "09775", 2},
		{"negative target", "[" + upstream + "]", "-9775", 2},
		{"partial PID", "[Camera ID: 0, PID: 9775oops]", "9775", 2},
		{"leading zero PID", "[Camera ID: 0, PID: 09775]", "9775", 2},
		{"negative PID", "[Camera ID: 0, PID: -9775]", "9775", 2},
		{"duplicate PID", "[Camera ID: 0, PID: 9775, PID: 97750]", "9775", 2},
		{"duplicate camera", "[Camera ID: 0, Camera ID: 1, PID: 9775]", "9775", 2},
		{"empty camera", "[Camera ID: , PID: 9775]", "9775", 2},
		{"missing camera", "[PID: 9775]", "9775", 2},
		{"missing conflicts", "[" + strings.Replace(upstream, ", Conflicting Client Devices: {}", "", 1) + "]", "9775", 2},
		{"missing cost", "[" + strings.Replace(upstream, "Cost: -100, ", "", 1) + "]", "9775", 2},
		{"missing state", "[" + strings.Replace(upstream, "State: -1, ", "", 1) + "]", "9775", 2},
		{"missing score", "[" + strings.Replace(upstream, "Score: +0, ", "", 1) + "]", "9775", 2},
		{"partial signed cost", "[" + strings.Replace(upstream, "-100", "-100x", 1) + "]", "9775", 2},
		{"partial signed score", "[" + strings.Replace(upstream, "+0", "+", 1) + "]", "9775", 2},
		{"bad user", "[" + strings.Replace(withUser, "User Id: 0", "User Id: 0x", 1) + "]", "9775", 2},
		{"empty package", "[" + strings.Replace(withUser, "com.example.camera", "", 1) + "]", "9775", 2},
		{"underfilled second", "[" + upstream + "(PID: 44)]", "9775", 2},
		{"malformed second", "[" + upstream + "(Camera ID: 1, PID: 44oops)]", "9775", 2},
		{"trailing material", "[" + upstream + " garbage]", "9775", 2},
		{"trailing comma", "[Camera ID: 0, PID: 9775,]", "9775", 2},
		{"unbalanced", "[" + strings.TrimSuffix(upstream, ")") + "]", "9775", 2},
		{"unbalanced conflicts", "[" + strings.Replace(upstream, "{}", "{", 1) + "]", "9775", 2},
		{"conflict empty middle", "[" + strings.Replace(upstream, "{}", "{1,,2}", 1) + "]", "9775", 2},
		{"conflict genuine empty whitespace", "[" + strings.Replace(upstream, "{}", "{ \t }", 1) + "]", "9775", 0},
		{"conflict single trailing comma", "[" + strings.Replace(upstream, "{}", "{1,}", 1) + "]", "9775", 0},
		{"conflict repeated trailing comma", "[" + strings.Replace(upstream, "{}", "{1,,}", 1) + "]", "9775", 2},
		{"conflict comma only matching", "[" + strings.Replace(upstream, "{}", "{,}", 1) + "]", "9775", 2},
		{"conflict comma only nonmatching", "[" + strings.Replace(upstream, "{}", "{,}", 1) + "]", "97750", 2},
		{"conflict comma only later after match", "[" + upstream + strings.Replace(strings.Replace(upstream, "9775", "44", 1), "{}", "{,}", 1) + "]", "9775", 2},
		{"conflict spaced comma only matching", "[" + strings.Replace(upstream, "{}", "{ , }", 1) + "]", "9775", 2},
		{"conflict spaced comma only nonmatching", "[" + strings.Replace(upstream, "{}", "{ , }", 1) + "]", "97750", 2},
		{"conflict spaced comma only later after match", "[" + upstream + strings.Replace(strings.Replace(upstream, "9775", "44", 1), "{}", "{ , }", 1) + "]", "9775", 2},
		{"double comma without user", "[" + strings.Replace(upstream, ", Conflicting", ",, Conflicting", 1) + "]", "9775", 2},
		{"double comma with package", "[" + strings.Replace(withUser, ", Conflicting", ",, Conflicting", 1) + "]", "9775", 2},
		{"unlabeled vendor", "[" + strings.Replace(upstream, ", Conflicting", ", shared, Conflicting", 1) + "]", "9775", 2},
		{"ambiguous vendor", "[" + strings.Replace(upstream, ", Conflicting", ", Vendor: a: b, Conflicting", 1) + "]", "9775", 2},
		{"flat optional malformed", "[Camera ID: 0, PID: 9775, State: 1x]", "9775", 2},
		{"flat second camera", "[Camera ID: 0, PID: 9775, Camera ID: 1, PID: 44]", "9775", 2},
		{"partial state", "[" + strings.Replace(upstream, "State: -1", "State: -1x", 1) + "]", "9775", 2},
		{"duplicate optional", "[" + strings.Replace(upstream, ", Conflicting", ", Cost: 1, Conflicting", 1) + "]", "9775", 2},
		{"trailing source record comma", "[" + upstream + ",]", "9775", 2},
		{"duplicate vendor", "[" + strings.Replace(upstream, ", Conflicting", ", Vendor: a, Vendor: b, Conflicting", 1) + "]", "9775", 2},
		{"empty vendor", "[" + strings.Replace(upstream, ", Conflicting", ", Vendor: , Conflicting", 1) + "]", "9775", 2},
		{"double comma package elsewhere", "[" + strings.Replace(upstream, "State: -1,", "Client Package Name: com.example.camera, State: -1User Id: 0,,", 1) + "]", "9775", 2},
	} {
		// The subtest runs the actual AWK program with one synthetic section.
		//
		// Example: malformed remainder must prevent a held result.
		t.Run(fixture.Name, func(
			t *testing.T,
		) {
			dump := "Active Camera Clients:\n" + fixture.Body + "\nAllowed user IDs: 0\nCONNECT (PID 9775)\nActive Client listener PIDs: 9775\n"
			command := exec.Command("awk", "-v", "pid="+fixture.PID, "-f", "current-client.awk")
			command.Stdin = strings.NewReader(dump)
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("run parser: %v", err)
				}
				code = exit.ExitCode()
			}
			if code != fixture.Code || len(output) != 0 {
				t.Fatalf("exit=%d want=%d output=%q", code, fixture.Code, output)
			}
		})
	}
}

// TestCurrentClientSection requires exactly one terminated ownership section.
//
// Example: history alone cannot establish active camera ownership.
func TestCurrentClientSection(
	t *testing.T,
) {
	for _, fixture := range []struct {
		Name string
		Dump string
		Code int
	}{
		{"empty ignores history", "Active Camera Clients:\n[]\nAllowed user IDs: 0\nCONNECT (PID 9775)\nActive Client listener PIDs: 9775\n", 1},
		{"missing", "Permission denied\n", 2},
		{"history only", "CONNECT (PID 9775)\nActive Client listener PIDs: 9775\n", 2},
		{"truncated", "Active Camera Clients:\n[]\n", 2},
		{"garbage", "Active Camera Clients:\ngarbage\nAllowed user IDs: 0\n", 2},
		{"duplicate", "Active Camera Clients:\n[]\nAllowed user IDs: 0\nActive Camera Clients:\n[]\nAllowed user IDs: 0\n", 2},
		{"nested header", "Active Camera Clients:\nActive Camera Clients:\n[]\nAllowed user IDs: 0\n", 2},
		{"after list", "Active Camera Clients:\n[Camera ID: 0, PID: 9775] garbage\nAllowed user IDs: 0\n", 2},
	} {
		// The subtest checks the complete captured section rather than historical text.
		//
		// Example: a missing terminator produces unknown.
		t.Run(fixture.Name, func(
			t *testing.T,
		) {
			command := exec.Command("awk", "-v", "pid=9775", "-f", "current-client.awk")
			command.Stdin = strings.NewReader(fixture.Dump)
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("run parser: %v", err)
				}
				code = exit.ExitCode()
			}
			if code != fixture.Code || len(output) != 0 {
				t.Fatalf("exit=%d want=%d output=%q", code, fixture.Code, output)
			}
		})
	}
}
