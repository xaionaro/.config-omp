package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// migrationSource retains the bytes that must remain unchanged through activation.
//
// Example: an absent forecast has no source entry.
type migrationSource struct {
	Name string
	Data []byte
}

// readMigrationSources reads the three optional snapshots without following unsafe leaves.
//
// Example: a log-only session returns no sources.
func readMigrationSources(dir string) ([]migrationSource, error) {
	var sources []migrationSource
	for _, name := range []string{"project-understanding.yaml", "forecasts.yaml", "latest-status-report.yaml"} {
		path := filepath.Join(dir, name)
		if err := refuseUnsafeLeaf(path); err != nil {
			return nil, fmt.Errorf("unsafe snapshot %s: %w", name, err)
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read snapshot %s: %w", name, err)
		}
		sources = append(sources, migrationSource{Name: name, Data: data})
	}
	return sources, nil
}

// syncLedgerPath flushes either a regular candidate or its containing directory.
//
// Example: directory synchronization makes the installed filename durable.
func syncLedgerPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open for sync: %w", err)
	}
	return errors.Join(file.Sync(), file.Close())
}

