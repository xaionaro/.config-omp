package main

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// OperationKind names the supported effects without accepting native Git verbs.
//
// Example: StageContent changes only the explicitly selected index entries.
type OperationKind int

// Fixed operations define the complete mutation surface.
//
// Example: Commit uses the prepared index and never selects worktree paths.
const (
	StageContent OperationKind = iota + 1
	StageRemovals
	StageHunks
	Unstage
	Restore
	Remove
	Move
	Commit
)

// SourceKind selects an immutable tree or the current index.
//
// Example: IndexSource restores a worktree from prepared index content.
type SourceKind int

// Source modes distinguish index content, current HEAD and a fixed tree object.
//
// Example: TreeSource uses only a fully specified object identifier.
const (
	IndexSource SourceKind = iota + 1
	HeadSource
	TreeSource
)

// DestinationKind identifies which repository state may change.
//
// Example: WorktreeDestination leaves the index untouched.
type DestinationKind int

// Destination modes make worktree writes explicit.
//
// Example: BothDestination updates the index and worktree together.
const (
	IndexDestination DestinationKind = iota + 1
	WorktreeDestination
	BothDestination
)

// Operation carries validated fixed modes and literal paths for one request.
//
// Example: Restore with HeadSource and WorktreeDestination discards named worktree changes.
type Operation struct {
	Repo        string
	Kind        OperationKind
	Paths       []string
	Source      SourceKind
	Destination DestinationKind
	TreeOID     string
	Message     string
	PatchFile   string
}

// ParseOperation accepts only the published operation grammar.
//
// Example: --repo /project stage-content -- file.txt stages that literal name.
func ParseOperation(arguments []string) (Operation, error) {
	operation := Operation{}
	if len(arguments) < 3 || arguments[0] != "--repo" || arguments[1] == "" {
		return operation, fmt.Errorf("usage: eci-worker-git --repo REPOSITORY OPERATION [fixed options] -- literal-paths")
	}
	operation.Repo = arguments[1]
	switch arguments[2] {
	case "stage-content":
		operation.Kind = StageContent
	case "stage-removals":
		operation.Kind = StageRemovals
	case "stage-hunks":
		operation.Kind = StageHunks
	case "unstage":
		operation.Kind = Unstage
	case "restore":
		operation.Kind = Restore
	case "remove":
		operation.Kind = Remove
	case "move":
		operation.Kind = Move
	case "commit":
		operation.Kind = Commit
	default:
		return operation, fmt.Errorf("unsupported operation %q", arguments[2])
	}
	seen := map[string]bool{}
	tail := arguments[3:]
	for len(tail) != 0 {
		option := tail[0]
		if option == "--" {
			operation.Paths = append([]string(nil), tail[1:]...)
			break
		}
		if len(tail) < 2 || seen[option] {
			return operation, fmt.Errorf("missing or duplicate fixed option %q", option)
		}
		seen[option] = true
		value := tail[1]
		switch {
		case option == "--message" && operation.Kind == Commit:
			operation.Message = value
		case option == "--patch-file" && operation.Kind == StageHunks:
			operation.PatchFile = value
		case option == "--source" && operation.Kind == Restore:
			switch value {
			case "index":
				operation.Source = IndexSource
			case "head":
				operation.Source = HeadSource
			default:
				decoded, err := hex.DecodeString(value)
				if err != nil || (len(decoded) != 20 && len(decoded) != 32) {
					return operation, fmt.Errorf("source must be index, head or a full object ID")
				}
				operation.Source, operation.TreeOID = TreeSource, value
			}
		case option == "--destination" && (operation.Kind == Restore || operation.Kind == Remove):
			switch value {
			case "index":
				operation.Destination = IndexDestination
			case "worktree":
				operation.Destination = WorktreeDestination
			case "both":
				operation.Destination = BothDestination
			default:
				return operation, fmt.Errorf("destination must be index, worktree or both")
			}
		default:
			return operation, fmt.Errorf("unsupported fixed option %q", option)
		}
		tail = tail[2:]
	}
	if operation.Kind == Commit {
		if operation.Message == "" || len(operation.Paths) != 0 {
			return operation, fmt.Errorf("commit requires --message and no paths; inspect the prepared index first")
		}
		return operation, nil
	}
	if len(operation.Paths) == 0 {
		return operation, fmt.Errorf("operation requires nonempty literal paths after --")
	}
	for _, path := range operation.Paths {
		if path == "" || strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
			return operation, fmt.Errorf("path must be a nonempty repository-relative name: %q", path)
		}
		for _, segment := range strings.Split(path, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return operation, fmt.Errorf("path must contain only canonical relative segments: %q", path)
			}
		}
	}
	switch {
	case operation.Kind == Move && len(operation.Paths) != 2:
		return operation, fmt.Errorf("move requires exactly source and destination")
	case operation.Kind == StageHunks && operation.PatchFile == "":
		return operation, fmt.Errorf("stage-hunks requires --patch-file")
	case operation.Kind == Restore && (operation.Source == 0 || operation.Destination == 0):
		return operation, fmt.Errorf("restore requires explicit --source and --destination")
	case operation.Kind == Restore && operation.Source == IndexSource && operation.Destination != WorktreeDestination:
		return operation, fmt.Errorf("index source supports only worktree destination")
	case operation.Kind == Remove && operation.Destination == 0:
		return operation, fmt.Errorf("remove requires explicit --destination")
	}
	return operation, nil
}
