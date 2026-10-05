// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package plxtest runs the declarative test scenarios of a Plux project
// (TST-001, TST-002, ADR-0052). It reads the scenario files, compiles the
// project with the real compiler into a release signed with a key made
// for the run, generates a Flutter test project that starts the real
// runtime on that release, and turns the results of `flutter test` into
// JUnit XML. Everything up to running Flutter is a pure function of its
// inputs: the same project, scenarios and key entropy give the same
// generated files.
package plxtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

// Finding is a diagnostic placed in a scenario file by line and column,
// which is what the author of a YAML file navigates by.
type Finding struct {
	plxerr.Diagnostic
	// Line and Column are 1-based; 0 when the finding concerns the whole
	// file.
	Line, Column int
}

// String formats the finding as "file:line:col: severity PLX-NNNN message".
func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(f.File)
	if f.Line > 0 {
		b.WriteString(":" + strconv.Itoa(f.Line) + ":" + strconv.Itoa(f.Column))
	}
	fmt.Fprintf(&b, ": %s %s %s", f.Severity, f.Code, f.Message)
	return b.String()
}

// HasErrors reports whether any finding is an error.
func HasErrors(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == plxerr.SeverityError {
			return true
		}
	}
	return false
}

// File is a parsed scenario file.
type File struct {
	// Path is the file's path in the project, with forward slashes.
	Path string
	// Doc is the validated document.
	Doc schema.ScenarioDocument
	// tree locates JSON pointers in the file.
	tree *ast.File
}

// Locate returns the line and column of the value at a JSON pointer, or
// the closest enclosing value; both are 0 when the file has no content.
func (f *File) Locate(pointer string) (line, col int) { return locate(f.tree, pointer) }

// ParseFile decodes a scenario file, YAML or JSON, validates it against
// the scenario schema and returns its scenarios. Every problem is a
// finding placed at its line and column; the file is nil when any is an
// error.
func ParseFile(v *schema.Validator, lim limits.Set, path string, data []byte) (*File, []Finding) {
	fail := func(code plxerr.Code, tk *token.Token, format string, args ...any) []Finding {
		f := Finding{Diagnostic: plxerr.NewDiagnostic(code, plxerr.Location{File: path}, format, args...)}
		if tk != nil && tk.Position != nil {
			f.Line, f.Column = tk.Position.Line, tk.Position.Column
		}
		return []Finding{f}
	}
	if max := lim.Get(limits.DocumentFileSize); int64(len(data)) > max {
		return nil, fail(plxerr.ScenarioSyntaxInvalid, nil, "the file is %d bytes, over the limit of %d", len(data), max)
	}
	tree, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, fail(plxerr.ScenarioSyntaxInvalid, errToken(err), "%s", errMessage(err))
	}
	var generic any
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return nil, fail(plxerr.ScenarioSyntaxInvalid, errToken(err), "%s", errMessage(err))
	}
	text, err := json.Marshal(generic)
	if err != nil {
		return nil, fail(plxerr.ScenarioSyntaxInvalid, nil, "the file holds a value JSON cannot represent: %v", err)
	}
	jsonTree, err := jcs.Parse(text, int(lim.Get(limits.DocumentJSONDepth)))
	if err != nil {
		return nil, fail(plxerr.ScenarioSyntaxInvalid, nil, "%v", err)
	}
	f := &File{Path: path, tree: tree}
	var out []Finding
	for _, d := range v.Validate(schema.KindScenarios, jsonTree, path) {
		fd := Finding{Diagnostic: d}
		// The schema reports every problem as a scenario file problem.
		if d.Code != plxerr.InternalCompilerError {
			def, _ := plxerr.Lookup(plxerr.ScenarioFileInvalid)
			fd.Code, fd.Reason, fd.Cause, fd.Fix, fd.DocURL = def.Code, def.Reason, def.Cause, def.Fix, def.Code.DocURL()
		}
		fd.Line, fd.Column = f.Locate(d.Path)
		out = append(out, fd)
	}
	if len(out) > 0 {
		return nil, out
	}
	if err := json.Unmarshal(text, &f.Doc); err != nil {
		return nil, fail(plxerr.ScenarioFileInvalid, nil, "%v", err)
	}
	seen := map[string]bool{}
	for i, s := range f.Doc.Scenarios {
		if seen[s.Name] {
			fd := Finding{Diagnostic: plxerr.NewDiagnostic(plxerr.ScenarioNameDuplicate,
				plxerr.Location{File: path, Path: plxerr.Pointer("scenarios", strconv.Itoa(i), "name")}, "the name %q is used twice", s.Name)}
			fd.Line, fd.Column = f.Locate(fd.Path)
			out = append(out, fd)
		}
		seen[s.Name] = true
	}
	if len(out) > 0 {
		return nil, out
	}
	return f, nil
}

// positioned is what the YAML library's errors offer.
type positioned interface {
	GetToken() *token.Token
	GetMessage() string
}

func errToken(err error) *token.Token {
	var p positioned
	if errors.As(err, &p) {
		return p.GetToken()
	}
	return nil
}

func errMessage(err error) string {
	var p positioned
	if errors.As(err, &p) {
		return p.GetMessage()
	}
	return err.Error()
}

// locate finds the position of the value at a JSON pointer: of its key
// when it is a mapping entry, of the item when it is a sequence entry.
func locate(file *ast.File, pointer string) (line, col int) {
	if file == nil || len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return 0, 0
	}
	node := file.Docs[0].Body
	pos := nodePos(node)
	for _, seg := range splitPointer(pointer) {
		next, at := child(node, seg)
		if next == nil {
			break
		}
		node = next
		if at != nil {
			pos = at
		} else {
			pos = nodePos(node)
		}
	}
	if pos == nil {
		return 0, 0
	}
	return pos.Line, pos.Column
}

// child returns the value of a mapping key or a sequence index and the
// position to report for it.
func child(node ast.Node, seg string) (ast.Node, *token.Position) {
	switch n := node.(type) {
	case *ast.AnchorNode:
		return child(n.Value, seg)
	case *ast.TagNode:
		return child(n.Value, seg)
	case *ast.MappingNode:
		for _, mv := range n.Values {
			if mv.Key.GetToken().Value == seg {
				return mv.Value, mv.Key.GetToken().Position
			}
		}
	case *ast.MappingValueNode:
		if n.Key.GetToken().Value == seg {
			return n.Value, n.Key.GetToken().Position
		}
	case *ast.SequenceNode:
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(n.Values) {
			return n.Values[i], nodePos(n.Values[i])
		}
	}
	return nil, nil
}

// nodePos is where a node starts; a mapping starts at its first key.
func nodePos(node ast.Node) *token.Position {
	switch n := node.(type) {
	case *ast.MappingNode:
		if len(n.Values) > 0 {
			return n.Values[0].Key.GetToken().Position
		}
	case *ast.MappingValueNode:
		return n.Key.GetToken().Position
	case *ast.AnchorNode:
		return nodePos(n.Value)
	case *ast.TagNode:
		return nodePos(n.Value)
	}
	if tk := node.GetToken(); tk != nil {
		return tk.Position
	}
	return nil
}

// splitPointer splits an RFC 6901 pointer into its unescaped segments.
func splitPointer(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}
