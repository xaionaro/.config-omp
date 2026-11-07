package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestNullStatusSyncBootstrapReplays checks null metadata through real refresh.
//
// Example: A report with null sync state can publish its first progress event.
func TestNullStatusSyncBootstrapReplays(t *testing.T) {
	var mapping map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &mapping))
	mapping["status_sync"] = nil
	flow, err := json.Marshal(mapping)
	require.NoError(t, err)
	for _, tc := range []struct{ name, input string }{
		{"flow null", string(flow)},
		{"flow empty", strings.Replace(string(flow), `"status_sync":null`, `"status_sync":`, 1)},
		{"flow empty anchor", strings.Replace(string(flow), `"status_sync":null`, `"status_sync": &owner `, 1)},
		{"flow empty tag", strings.Replace(string(flow), `"status_sync":null`, `"status_sync": !!null `, 1)},
		{"flow quoted empty", strings.Replace(string(flow), `"status_sync":null`, `"status_sync": !!null ""`, 1)},
		{"flow quoted escaped", strings.Replace(string(flow), `"status_sync":null`, `"status_sync": !!null "n\u0075ll"`, 1)},
		{"block null", linkedReport + "status_sync: null # owner note\n"},
		{"block tilde", linkedReport + "status_sync: ~ # owner note\n"},
		{"block empty", linkedReport + "status_sync: # owner note\n"},
		{"block CRLF", strings.ReplaceAll(linkedReport+"status_sync: null # owner note\n", "\n", "\r\n")},
		{"tagged null", linkedReport + "status_sync: !!null null # owner note\n"},
		{"long tagged null", linkedReport + "status_sync: !<tag:yaml.org,2002:null> null # owner note\n"},
		{"tagged empty", linkedReport + "status_sync: !!null # owner note\n"},
		{"anchored null", linkedReport + "status_sync: &owner null # owner note\n"},
		{"tagged anchored null", linkedReport + "status_sync: !!null &owner null # owner note\n"},
		{"multiline anchor", linkedReport + "status_sync: &owner\n  null # owner note\n"},
		{"multiline tag", linkedReport + "status_sync: !!null\n  null # owner note\n"},
		{"multiline tag anchor", linkedReport + "status_sync: !!null\n  &owner null # owner note\n"},
		{"multiline CRLF", strings.ReplaceAll(linkedReport+"status_sync: !!null\n  &owner null # owner note\n", "\n", "\r\n")},
		{"anchor at EOF", linkedReport + "status_sync: &owner"},
		{"property comments", linkedReport + "status_sync: !!null # tag note\n  &owner # anchor note\n  null # owner note\n"},
		{"quoted tag", linkedReport + "status_sync: !!null 'null' # owner note\n"},
		{"double quoted tag", linkedReport + "status_sync: !!null \"null\" # owner note\n"},
		{"empty single quote", linkedReport + "status_sync: !!null '' # owner note\n"},
		{"empty double quote", linkedReport + "status_sync: !!null \"\" # owner note\n"},
		{"escaped double quote", linkedReport + "status_sync: !!null \"n\\u0075ll\" # owner note\n"},
		{"escaped quote CRLF", strings.ReplaceAll(linkedReport+"status_sync: !!null \"n\\u0075ll\" # owner note\n", "\n", "\r\n")},
		{"anchored empty quote", linkedReport + "status_sync: !!null &owner '' # owner note\n"},
	} {
		t.Run(tc.name,
			// Replay one durable event and retain adjacent owner text.
			//
			// Example: A second refresh leaves the persisted report unchanged.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.input), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(`{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first real progress"}}]}}}`+"\n"), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, parsed.Progress, "first real progress", string(result))
				require.NotNil(t, parsed.StatusSync)
				require.Equal(t, 1, parsed.StatusSync.Cursor)
				if strings.Contains(tc.input, "# owner note") {
					require.Contains(t, string(result), "# owner note")
				}
				if tc.name == "property comments" {
					require.Contains(t, string(result), "# tag note")
					require.Contains(t, string(result), "# anchor note")
				}
				if strings.Contains(tc.input, "\r\n") {
					require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestNullSyncAliasDoesNotOverwriteOwnerData checks shared YAML anchors.
//
// Example: A null root-completions alias cannot silently become sync metadata.
func TestNullSyncAliasDoesNotOverwriteOwnerData(t *testing.T) {
	input := strings.Replace(linkedReport, "decisions: [Use YAML]", "status_sync: &owner null\nroot_completions: *owner\ndecisions: [Use YAML]", 1)
	_, err := parse([]byte(input))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(`{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new claim"}}]}}}`+"\n"), 0600))
	var out, errOut bytes.Buffer
	require.NotEqual(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "status_sync: &owner null\nroot_completions: *owner")
	require.NotContains(t, string(result), "new claim")
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.RootCompletions)
	require.Nil(t, parsed.StatusSync)
}
