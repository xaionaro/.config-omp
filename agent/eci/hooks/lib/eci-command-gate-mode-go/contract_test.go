package main

import (
	"errors"
	"reflect"
	"testing"
)

// TestCommandGateContractConstants keeps the wire-visible limits and names stable.
//
// Example: a future implementation must continue to use the same telemetry schema.
func TestCommandGateContractConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "config bytes", got: MaxConfigBytes, want: 32},
		{name: "denial bytes", got: MaxDenialBytes, want: 65_536},
		{name: "event bytes", got: MaxEventBytes, want: 4_096},
		{name: "log bytes", got: MaxLogBytes, want: 1_048_576},
		{name: "log files", got: MaxLogFiles, want: 4},
		{name: "schema", got: EventSchema, want: "eci-command-gate-event/v1"},
		{name: "config directory", got: ConfigDirectoryName, want: "eci"},
		{name: "config file", got: ConfigFileName, want: "command-gate-mode"},
		{name: "telemetry directory", got: TelemetryDirectoryName, want: "command-gate"},
		{name: "telemetry parent directory", got: TelemetryParentDirectoryName, want: "eci"},
		{name: "telemetry file", got: TelemetryFileName, want: "would-deny.jsonl"},
		{name: "rotation lock", got: RotationLockName, want: ".rotation.lock"},
		{
			name: "telemetry warning",
			got:  TelemetryUnavailableMessage,
			want: "eci-command-gate-mode: telemetry unavailable\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !reflect.DeepEqual(test.got, test.want) {
				t.Fatalf("contract constant = %#v, want %#v", test.got, test.want)
			}
		})
	}
}

// TestParseArgumentsAcceptsEveryPublishedCommandShape protects the CLI grammar.
//
// Example: `finalize omp worker active parser` must remain a valid command.
func TestParseArgumentsAcceptsEveryPublishedCommandShape(t *testing.T) {
	t.Parallel()

	getCommand, err := ParseArguments([]string{"get"})
	if err != nil {
		t.Fatalf("parse get: %v", err)
	}
	if want := (Command{Kind: CommandGet}); !reflect.DeepEqual(getCommand, want) {
		t.Fatalf("get command = %#v, want %#v", getCommand, want)
	}

	for _, mode := range []Mode{ModePermissive, ModeEnforcing} {
		mode := mode
		t.Run("set/"+string(mode), func(t *testing.T) {
			command, err := ParseArguments([]string{"set", string(mode)})
			if err != nil {
				t.Fatalf("parse set: %v", err)
			}
			want := Command{Kind: CommandSet, Mode: mode}
			if !reflect.DeepEqual(command, want) {
				t.Fatalf("set command = %#v, want %#v", command, want)
			}
		})
	}

	for _, provider := range []Provider{ProviderOmp} {
		provider := provider
		for _, role := range []Role{RoleCoordinator, RoleWorker} {
			role := role
			for _, marker := range []Marker{MarkerActive, MarkerInactive} {
				marker := marker
				for _, source := range []Source{SourceParser, SourceLegacy} {
					source := source
					t.Run(
						string(provider)+"/"+string(role)+"/"+string(marker)+"/"+string(source),
						func(t *testing.T) {
							args := []string{
								"finalize",
								string(provider),
								string(role),
								string(marker),
								string(source),
							}
							command, err := ParseArguments(args)
							if err != nil {
								t.Fatalf("parse finalize: %v", err)
							}
							want := Command{
								Kind:     CommandFinalize,
								Provider: provider,
								Role:     role,
								Marker:   marker,
								Source:   source,
							}
							if !reflect.DeepEqual(command, want) {
								t.Fatalf("finalize command = %#v, want %#v", command, want)
							}
						},
					)
				}
			}
		}
	}
}

// TestParseArgumentsRejectsUnsupportedCommandShapes keeps malformed CLI calls at status 2.
//
// Example: an unknown provider must not be interpreted as a valid finalizer request.
func TestParseArgumentsRejectsUnsupportedCommandShapes(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		nil,
		{},
		{"unknown"},
		{"get", "extra"},
		{"set"},
		{"set", "invalid"},
		{"set", "permissive", "extra"},
		{"finalize"},
		{"finalize", "other", "coordinator", "active", "parser"},
		{"finalize", "omp", "other", "active", "parser"},
		{"finalize", "omp", "coordinator", "other", "parser"},
		{"finalize", "omp", "coordinator", "active", "other"},
		{"finalize", "omp", "coordinator", "active", "parser", "extra"},
	}
	for _, args := range tests {
		args := args
		t.Run("reject", func(t *testing.T) {
			_, err := ParseArguments(args)
			if err == nil {
				t.Fatalf("ParseArguments(%#v) unexpectedly succeeded", args)
			}
			var usageError *UsageError
			if !errors.As(err, &usageError) {
				t.Fatalf("ParseArguments(%#v) error = %T, want *UsageError", args, err)
			}
		})
	}
}
