// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// schemaNode is one JSON Schema restricted to the Plux profile (ADR-0025).
// Validation keywords that do not affect generated types are checked for
// being allowed and otherwise ignored.
type schemaNode struct {
	file string // schema file name, for errors and $ref resolution
	path string // JSON Pointer of the node inside its file

	boolean    *bool // a `true` or `false` schema
	ref        string
	types      []string
	constValue *string
	enum       []string
	properties []property
	required   []string
	items      *schemaNode
	closed     bool        // additionalProperties: false
	additional *schemaNode // additionalProperties: <schema>, i.e. a map
	patterns   []property
	oneOf      []*schemaNode
	anyOf      []*schemaNode
	defs       []property
	title      string
	desc       string
	raw        bool   // x-plux-raw: interpreted by the compiler, not typed
	typeName   string // x-plux-type: name of an inline object type
}

// property is a named sub-schema, in declaration order.
type property struct {
	name   string
	schema *schemaNode
}

// profileKeywords are the keywords a Plux schema may use.
var profileKeywords = []string{
	"$schema", "$id", "$ref", "$defs", "$comment", "title", "description",
	"type", "const", "enum", "properties", "required", "additionalProperties", "patternProperties",
	"propertyNames", "items", "oneOf", "anyOf", "not",
	"pattern", "minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems", "uniqueItems",
	"minProperties", "maxProperties", "format", "x-plux-raw", "x-plux-type",
}

// parseSchemaFile reads and parses one schema file.
func parseSchemaFile(dir, name string) (*schemaNode, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("codegen: %w", err)
	}
	return parseNode(data, name, "")
}

// parseNode parses a schema value.
func parseNode(data []byte, file, path string) (*schemaNode, error) {
	n := &schemaNode{file: file, path: path}
	trimmed := bytes.TrimSpace(data)
	if string(trimmed) == "true" || string(trimmed) == "false" {
		b := string(trimmed) == "true"
		n.boolean = &b
		return n, nil
	}
	keys, fields, err := orderedObject(data)
	if err != nil {
		return nil, n.errorf("%v", err)
	}
	for _, k := range keys {
		if !slices.Contains(profileKeywords, k) {
			return nil, n.errorf("keyword %q is outside the Plux schema profile", k)
		}
		if err := n.setKeyword(k, fields[k]); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// setKeyword stores one keyword of n.
func (n *schemaNode) setKeyword(k string, v json.RawMessage) error {
	var err error
	switch k {
	case "$ref":
		err = json.Unmarshal(v, &n.ref)
	case "type":
		n.types, err = stringOrList(v)
	case "const":
		var c any
		if err = json.Unmarshal(v, &c); err == nil {
			if s, ok := c.(string); ok {
				n.constValue = &s
			}
		}
	case "enum":
		err = json.Unmarshal(v, &n.enum)
	case "required":
		err = json.Unmarshal(v, &n.required)
	case "title":
		err = json.Unmarshal(v, &n.title)
	case "description":
		err = json.Unmarshal(v, &n.desc)
	case "x-plux-raw":
		err = json.Unmarshal(v, &n.raw)
	case "x-plux-type":
		err = json.Unmarshal(v, &n.typeName)
	case "properties", "patternProperties", "$defs":
		err = n.setNamedChildren(k, v)
	case "items", "additionalProperties", "oneOf", "anyOf":
		err = n.setChildren(k, v)
	}
	if err != nil {
		return n.errorf("%s: %v", k, err)
	}
	return nil
}

// setNamedChildren parses properties, patternProperties or $defs in order.
func (n *schemaNode) setNamedChildren(k string, v json.RawMessage) error {
	keys, fields, err := orderedObject(v)
	if err != nil {
		return err
	}
	for _, name := range keys {
		child, err := parseNode(fields[name], n.file, n.path+"/"+k+"/"+name)
		if err != nil {
			return err
		}
		p := property{name: name, schema: child}
		switch k {
		case "properties":
			n.properties = append(n.properties, p)
		case "patternProperties":
			n.patterns = append(n.patterns, p)
		default:
			n.defs = append(n.defs, p)
		}
	}
	return nil
}

// setChildren parses items, additionalProperties, oneOf or anyOf.
func (n *schemaNode) setChildren(k string, v json.RawMessage) error {
	switch k {
	case "items":
		child, err := parseNode(v, n.file, n.path+"/items")
		n.items = child
		return err
	case "additionalProperties":
		child, err := parseNode(v, n.file, n.path+"/additionalProperties")
		if err != nil {
			return err
		}
		if child.boolean != nil && !*child.boolean {
			n.closed = true
		} else {
			n.additional = child
		}
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(v, &list); err != nil {
		return fmt.Errorf("%s: %w", k, err)
	}
	for i, item := range list {
		child, err := parseNode(item, n.file, fmt.Sprintf("%s/%s/%d", n.path, k, i))
		if err != nil {
			return err
		}
		if k == "oneOf" {
			n.oneOf = append(n.oneOf, child)
		} else {
			n.anyOf = append(n.anyOf, child)
		}
	}
	return nil
}

// errorf returns an error located at n.
func (n *schemaNode) errorf(format string, args ...any) error {
	return fmt.Errorf("codegen: %s#%s: %s", n.file, n.path, fmt.Sprintf(format, args...))
}

// orderedObject decodes a JSON object, returning its keys in order.
func orderedObject(data []byte) ([]string, map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, nil, fmt.Errorf("object: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, nil, fmt.Errorf("object: %w", err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("object: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, nil, errors.New("object key is not a string")
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, nil, fmt.Errorf("object: %w", err)
		}
	}
	return keys, fields, nil
}

// stringOrList decodes a keyword that is a string or a list of strings.
func stringOrList(v json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(v, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(v, &many); err != nil {
		return nil, fmt.Errorf("string or list of strings: %w", err)
	}
	return many, nil
}

// def returns the definition named name.
func (n *schemaNode) def(name string) *schemaNode {
	for _, d := range n.defs {
		if d.name == name {
			return d.schema
		}
	}
	return nil
}

// isValidationOnly reports whether a oneOf branch only constrains which
// properties are present, so it does not affect the generated type.
func (n *schemaNode) isValidationOnly() bool {
	return n.ref == "" && len(n.types) == 0 && len(n.properties) == 0 && n.items == nil && len(n.required) > 0
}

// resolvedDoc returns the description of n, or of the definition it refers to.
func (n *schemaNode) resolvedDoc(target *schemaNode) string {
	if n.desc != "" || target == nil {
		return n.desc
	}
	return target.desc
}

// trimDollar returns a JSON property name without its leading "$".
func trimDollar(name string) string { return strings.TrimPrefix(name, "$") }
