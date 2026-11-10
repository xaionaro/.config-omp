package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

// patchHeader recognizes omp's anchored hashline edit header.
//
// Example: [file.go#ABCD] selects file.go.
var patchHeader = regexp.MustCompile(`^\[(.+)#[A-Za-z0-9]{4}\]\s*$`)

// evalWrite recognizes literal targets of supported eval filesystem operations.
//
// Example: Bun.write("file.go", data) selects file.go.
var evalWrite = regexp.MustCompile(`^(?:write|Bun\.write)\s*\(\s*["']([^"'\n]+)["']|^Path\s*\(\s*["']([^"'\n]+)["']\s*\)\s*\.\s*(?:write_text|write_bytes)\s*\(|^open\s*\(\s*["']([^"'\n]+)["']\s*,\s*["'][^"']*[wax+][^"']*["']`)

// writeTargets extracts concrete mutation paths from host tool inputs.
//
// Example: an edit rename contributes its destination path.
func writeTargets(req Request) ([]string, error) {
	switch req.Tool {
	case "write":
		if asString(req.Input["path"]) == "xd://resolve" {
			var paths []string
			for _, preview := range req.State.Pending { paths = append(paths, preview...) }
			return paths, nil
		}
		return []string{asString(req.Input["path"])}, nil
	case "edit", "apply_patch":
		if req.EditTargets != nil { return req.EditTargets, nil }
		paths := append([]string{asString(req.Input["path"])}, stringsFrom(req.Input["paths"])...)
		patch := asString(req.Input["input"])
		if patch == "" { patch = asString(req.Input["_input"]) }
		for _, line := range strings.Split(patch,"\n") {
			if match := patchHeader.FindStringSubmatch(line); len(match) == 2 { paths = append(paths, match[1]); continue }
			for _, prefix := range []string{"MV ","*** Add File: ","*** Update File: ","*** Delete File: ","*** Move to: ","*** Edit File: "} {
				if strings.HasPrefix(line,prefix) { paths = append(paths, strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line,prefix)," all"), `"'`)); break }
			}
		}
		for _, edit := range mapsFrom(req.Input["edits"]) { if rename := asString(edit["rename"]); rename != "" { paths = append(paths,rename) } }
		return paths,nil
	case "bash":
		cwd := req.CWD
		if path := asString(req.Input["cwd"]); path != "" { if filepath.IsAbs(path) { cwd = path } else { cwd = filepath.Join(cwd,path) } }
		return shellWriteTargets(asString(req.Input["command"]),cwd)
	case "eval":
		return evalTargets(asString(req.Input["code"])),nil
	}
	return nil,nil
}

// evalTargets selects literal filesystem writes outside strings and comments.
//
// Example: a quoted example of Bun.write is not an executed write.
func evalTargets(code string) []string {
	var paths []string
	var quote byte
	for i:=0; i<len(code); i++ {
		c:=code[i]
		if quote != 0 {
			if c=='\\' { i++; continue }; if c==quote { quote=0 }; continue
		}
		if c=='/' && i+1<len(code) && code[i+1]=='*' {
			end:=strings.Index(code[i+2:],"*/"); if end<0 { break }; i += end+3; continue
		}
		if c=='#' || (c=='/' && i+1<len(code) && code[i+1]=='/') {
			end:=strings.IndexByte(code[i:],'\n'); if end<0 { break }; i += end; continue
		}
		if c=='\'' || c=='"' || c=='`' { quote=c; continue }
		if i>0 && (isWord(code[i-1]) || strings.HasSuffix(strings.TrimRight(code[:i]," \t\r\n"),".")) { continue }
		match:=evalWrite.FindStringSubmatch(code[i:])
		if len(match)>0 { for _, path:=range match[1:] { if path!="" { paths=append(paths,path); break } } }
	}
	return paths
}

// isWord reports identifier bytes relevant to eval call boundaries.
//
// Example: xwrite is not the write function.
func isWord(c byte) bool { return c=='_' || c=='$' || c>='0' && c<='9' || c>='a' && c<='z' || c>='A' && c<='Z' }
