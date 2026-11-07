package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportCopiesOpenedSourceToAbsentLeaf(t *testing.T) {
	t.Parallel()

	root, session := makeSession(t)
	source := writeFile(t, filepath.Join(t.TempDir(), "source"), "opened source bytes\n")

	if err := Import(Request{
		ProofRoot:  root,
		SessionDir: session,
		Leaf:       LeafWaitReport,
		Source:     source,
	}); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertFileContents(t, filepath.Join(session, waitReportName), "opened source bytes\n")
}

func TestImportRewritesValidatedExistingLeaf(t *testing.T) {
	t.Parallel()

	root, session := makeSession(t)
	source := writeFile(t, filepath.Join(t.TempDir(), "source"), "replacement bytes\n")
	destination := writeFile(t, filepath.Join(session, singletonManifestName), "old bytes\n")

	if err := Import(Request{
		ProofRoot:  root,
		SessionDir: session,
		Leaf:       LeafSingletonManifest,
		Source:     source,
	}); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertFileContents(t, destination, "replacement bytes\n")
}

func TestImportUsesSourceDescriptorAfterPathSwap(t *testing.T) {
	t.Parallel()

	root, session := makeSession(t)
	sourceDir := t.TempDir()
	source := writeFile(t, filepath.Join(sourceDir, "source"), "opened inode bytes\n")
	moved := filepath.Join(sourceDir, "moved")

	err := importRequest(Request{
		ProofRoot:  root,
		SessionDir: session,
		Leaf:       LeafWaitReport,
		Source:     source,
	}, importHooks{
		afterSourceOpen: func() {
			if err := os.Rename(source, moved); err != nil {
				t.Fatalf("Rename(source) error = %v", err)
			}
			writeFile(t, source, "replacement pathname bytes\n")
		},
	})
	if err != nil {
		t.Fatalf("importRequest() error = %v", err)
	}
	assertFileContents(t, filepath.Join(session, waitReportName), "opened inode bytes\n")
	assertFileContents(t, source, "replacement pathname bytes\n")
}

func TestImportAllowsParentAndFinalSourceAliases(t *testing.T) {
	t.Parallel()

	sourceDirectory := t.TempDir()
	source := writeFile(t, filepath.Join(sourceDirectory, "source"), "aliased source bytes\n")
	parentAlias := filepath.Join(t.TempDir(), "source-parent-alias")
	if err := os.Symlink(sourceDirectory, parentAlias); err != nil {
		t.Fatalf("Symlink(parent) error = %v", err)
	}
	finalAlias := filepath.Join(t.TempDir(), "source-final-alias")
	if err := os.Symlink(source, finalAlias); err != nil {
		t.Fatalf("Symlink(final) error = %v", err)
	}

	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "parent", source: filepath.Join(parentAlias, "source")},
		{name: "final", source: finalAlias},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root, session := makeSession(t)
			if err := Import(Request{
				ProofRoot:  root,
				SessionDir: session,
				Leaf:       LeafWaitReport,
				Source:     test.source,
			}); err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			assertFileContents(t, filepath.Join(session, waitReportName), "aliased source bytes\n")
		})
	}
}

func TestImportTreatsAliasedExistingLeafAsBindingCheckedNoOp(t *testing.T) {
	t.Parallel()

	t.Run("preserves bytes", func(t *testing.T) {
		root, session := makeSession(t)
		destination := writeFile(t, filepath.Join(session, waitReportName), "preserved destination bytes\n")
		sourceAlias := filepath.Join(t.TempDir(), "destination-source-alias")
		if err := os.Symlink(destination, sourceAlias); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}

		if err := Import(Request{
			ProofRoot:  root,
			SessionDir: session,
			Leaf:       LeafWaitReport,
			Source:     sourceAlias,
		}); err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		assertFileContents(t, destination, "preserved destination bytes\n")
	})

	t.Run("rejects raced binding", func(t *testing.T) {
		root, session := makeSession(t)
		destination := writeFile(t, filepath.Join(session, waitReportName), "preserved destination bytes\n")
		sourceAlias := filepath.Join(t.TempDir(), "destination-source-alias")
		if err := os.Symlink(destination, sourceAlias); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}
		moved := filepath.Join(session, "moved-destination")
		foreign := writeFile(t, filepath.Join(t.TempDir(), "foreign"), "foreign destination bytes\n")

		err := importRequest(Request{
			ProofRoot:  root,
			SessionDir: session,
			Leaf:       LeafWaitReport,
			Source:     sourceAlias,
		}, importHooks{
			afterExistingOpen: func() {
				if err := os.Rename(destination, moved); err != nil {
					t.Fatalf("Rename(destination) error = %v", err)
				}
				if err := os.Symlink(foreign, destination); err != nil {
					t.Fatalf("Symlink(destination) error = %v", err)
				}
			},
		})
		if err == nil {
			t.Fatal("importRequest() unexpectedly accepted a raced same-inode destination")
		}
		assertFileContents(t, moved, "preserved destination bytes\n")
		assertFileContents(t, foreign, "foreign destination bytes\n")
	})
}

func TestImportRejectsUnsafeDestinationLeavesWithoutChangingThem(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string) string
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, destination string) string {
				t.Helper()
				foreign := writeFile(t, filepath.Join(t.TempDir(), "foreign"), "foreign bytes\n")
				if err := os.Symlink(foreign, destination); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
				return foreign
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, destination string) string {
				t.Helper()
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatalf("Mkdir() error = %v", err)
				}
				return destination
			},
		},
		{
			name: "hardlink",
			setup: func(t *testing.T, destination string) string {
				t.Helper()
				foreign := writeFile(t, filepath.Join(t.TempDir(), "foreign"), "foreign bytes\n")
				if err := os.Link(foreign, destination); err != nil {
					t.Fatalf("Link() error = %v", err)
				}
				return foreign
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root, session := makeSession(t)
			source := writeFile(t, filepath.Join(t.TempDir(), "source"), "new bytes\n")
			destination := filepath.Join(session, waitReportName)
			foreign := test.setup(t, destination)

			err := Import(Request{
				ProofRoot:  root,
				SessionDir: session,
				Leaf:       LeafWaitReport,
				Source:     source,
			})
			if err == nil {
				t.Fatal("Import() unexpectedly accepted unsafe destination")
			}
			if test.name == "symlink" || test.name == "hardlink" {
				assertFileContents(t, foreign, "foreign bytes\n")
			}
			if test.name == "symlink" {
				info, statErr := os.Lstat(destination)
				if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("destination was changed: info=%v err=%v", info, statErr)
				}
			}
		})
	}
}

func TestImportRejectsDestinationPublicationRaces(t *testing.T) {
	t.Parallel()

	t.Run("absent leaf", func(t *testing.T) {
		root, session := makeSession(t)
		source := writeFile(t, filepath.Join(t.TempDir(), "source"), "source bytes\n")
		destination := filepath.Join(session, waitReportName)

		err := importRequest(Request{
			ProofRoot:  root,
			SessionDir: session,
			Leaf:       LeafWaitReport,
			Source:     source,
		}, importHooks{
			beforeAbsentPublish: func() {
				writeFile(t, destination, "raced destination bytes\n")
			},
		})
		if err == nil {
			t.Fatal("importRequest() unexpectedly published over a raced destination")
		}
		assertFileContents(t, destination, "raced destination bytes\n")
	})

	t.Run("existing leaf", func(t *testing.T) {
		root, session := makeSession(t)
		source := writeFile(t, filepath.Join(t.TempDir(), "source"), "source bytes\n")
		destination := writeFile(t, filepath.Join(session, waitReportName), "old destination bytes\n")
		moved := filepath.Join(session, "moved-destination")
		foreign := writeFile(t, filepath.Join(t.TempDir(), "foreign"), "foreign bytes\n")

		err := importRequest(Request{
			ProofRoot:  root,
			SessionDir: session,
			Leaf:       LeafWaitReport,
			Source:     source,
		}, importHooks{
			afterExistingOpen: func() {
				if err := os.Rename(destination, moved); err != nil {
					t.Fatalf("Rename(destination) error = %v", err)
				}
				if err := os.Symlink(foreign, destination); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
			},
		})
		if err == nil {
			t.Fatal("importRequest() unexpectedly accepted a raced existing destination")
		}
		assertFileContents(t, foreign, "foreign bytes\n")
		assertFileContents(t, moved, "source bytes\n")
	})
}

func TestImportRejectsNonRegularSourceAndOutOfSessionDestination(t *testing.T) {
	t.Parallel()

	root, session := makeSession(t)
	nonRegularSource := filepath.Join(t.TempDir(), "nonregular-source")
	if err := os.Mkdir(nonRegularSource, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := Import(Request{
		ProofRoot:  root,
		SessionDir: session,
		Leaf:       LeafWaitReport,
		Source:     nonRegularSource,
	}); err == nil {
		t.Fatal("Import() unexpectedly accepted a nonregular source")
	}
	if _, err := os.Lstat(filepath.Join(session, waitReportName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(destination) error = %v, want not exist", err)
	}

	foreign := writeFile(t, filepath.Join(t.TempDir(), "foreign"), "foreign source bytes\n")

	if err := Import(Request{
		ProofRoot:  root,
		SessionDir: filepath.Join(root, "other-session"),
		Leaf:       LeafWaitReport,
		Source:     foreign,
	}); err == nil {
		t.Fatal("Import() unexpectedly accepted an out-of-session destination")
	}
}

func makeSession(t *testing.T) (string, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "proof")
	session := filepath.Join(root, "t00-safe-import")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(proof root) error = %v", err)
	}
	return canonicalRoot, filepath.Join(canonicalRoot, "t00-safe-import")
}

func writeFile(t *testing.T, path string, contents string) string {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func assertFileContents(t *testing.T, path string, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("ReadFile(%q) = %q, want %q", path, got, want)
	}
}

func TestLeafDestinationRejectsInvalidRepositoryID(t *testing.T) {
	t.Parallel()

	_, err := leafDestination(LeafAggregateManifest, "../wrong")
	if !errors.Is(err, errInvalidRequest) {
		t.Fatalf("leafDestination() error = %v, want errInvalidRequest", err)
	}
}
