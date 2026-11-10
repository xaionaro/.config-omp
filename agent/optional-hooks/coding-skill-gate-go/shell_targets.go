package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shellWriteTargets identifies concrete file targets without executing the shell.
//
// Example: a literal output redirection selects its concrete target.
func shellWriteTargets(
	command string,
	cwd string,
) ([]string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil { return nil, err }
	var targets []string
	var scanErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		stmt, ok := node.(*syntax.Stmt)
		if !ok || scanErr != nil { return scanErr == nil }
		call, simple := stmt.Cmd.(*syntax.CallExpr)
		for _, redir := range stmt.Redirs {
			switch redir.Op {
			case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll:
				if target, known := shellWord(redir.Word); known && target != "" { targets = append(targets, target) }
			}
		}
		if !simple || len(call.Args) == 0 { return true }
		args := make([]string, 0, len(call.Args))
		for _, word := range call.Args {
			value, known := shellWord(word)
			if !known { return true }
			args = append(args, value)
		}
		switch filepath.Base(args[0]) {
		case "tee":
			for _, arg := range args[1:] { if !strings.HasPrefix(arg, "-") { targets = append(targets, arg) } }
		case "cp", "mv", "install":
			if len(args) > 2 {
				dest := args[len(args)-1]
				targets = append(targets, dest)
				path := dest
				if !filepath.IsAbs(path) { path = filepath.Join(cwd, path) }
				info, err := os.Stat(path)
				if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) { scanErr = err; return false }
				if err == nil && info.IsDir() {
					for _, source := range args[1:len(args)-1] {
						if strings.HasPrefix(source, "-") { continue }
						sourcePath := source
						if !filepath.IsAbs(sourcePath) { sourcePath = filepath.Join(cwd, sourcePath) }
						switch _, err := os.Stat(sourcePath); {
						case err == nil:
							targets = append(targets, filepath.Join(dest, filepath.Base(source)))
						case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission):
							scanErr = err
							return false
						}
					}
				}
			}
		case "sed":
			for _, arg := range args[1:] {
				if strings.HasPrefix(arg, "--in-place") || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "i")) {
					targets = append(targets, args[len(args)-1])
					break
				}
			}
		case "gofmt":
			writes := false
			var operands []string
			for i := 1; i < len(args); i++ {
				arg := args[i]
				if arg == "-r" || arg == "--r" { i++; continue }
				if arg == "-w" || arg == "--w" || arg == "-w=true" || arg == "--w=true" { writes = true; continue }
				if !strings.HasPrefix(arg, "-") { operands = append(operands, arg) }
			}
			if writes {
				for _, operand := range operands {
					path := operand
					if !filepath.IsAbs(path) { path = filepath.Join(cwd, path) }
					found, err := gofmtTarget(path)
					if err != nil { scanErr = err; break }
					if found != "" { targets = append(targets, found); break }
				}
			}
		}
		return true
	})
	return targets, scanErr
}

// shellWord resolves shell words containing only literal quoted or unquoted parts.
//
// Example: "source.go" identifies a literal file without executing expansions.
func shellWord(word *syntax.Word) (string, bool) {
	if word == nil { return "", false }
	var out strings.Builder
	if !appendLiteralParts(&out, word.Parts) { return "", false }
	return out.String(), true
}

// appendLiteralParts appends only text whose value does not require expansion.
//
// Example: adjacent single-quoted and plain fragments form one argument.
func appendLiteralParts(
	out *strings.Builder,
	items []syntax.WordPart,
) bool {
	for _, item := range items {
		switch part := item.(type) {
		case *syntax.Lit:
			out.WriteString(part.Value)
		case *syntax.SglQuoted:
			if part.Dollar { return false }
			out.WriteString(part.Value)
		case *syntax.DblQuoted:
			if part.Dollar || !appendLiteralParts(out, part.Parts) { return false }
		default:
			return false
		}
	}
	return true
}

// gofmtTarget returns one Go file selected by a file or directory operand.
//
// Example: gofmt -w . resolves the first visible Go source beneath the directory.
func gofmtTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) { return "", nil }
	if err != nil { return "", err }
	if !info.IsDir() {
		if strings.HasSuffix(path, ".go") { return path, nil }
		return "", nil
	}
	var found string
	err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrPermission) || errors.Is(walkErr, fs.ErrNotExist) { return nil }
		if walkErr != nil { return walkErr }
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".go") {
			found = candidate
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}
