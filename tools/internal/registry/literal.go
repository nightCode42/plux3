// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Literal forms of the primitive types (docs/reference/document-model.md §3).
var (
	decimalLiteral  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	currencyLiteral = regexp.MustCompile(`^[A-Z]{3}$`)
	colorLiteral    = regexp.MustCompile(`^#([0-9A-Fa-f]{6}|[0-9A-Fa-f]{8})$`)
	routeLiteral    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// maxSafeInteger is the largest integer an I-JSON number holds exactly.
const maxSafeInteger = 1<<53 - 1

// decodeLiteral parses a JSON literal, keeping numbers as json.Number.
func decodeLiteral(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return v, nil
}

// checkLiteral checks that v is a literal of type t; a value type's
// literal may be the name of one of its constants.
func (ix *index) checkLiteral(v any, t Type) error {
	if v == nil {
		if t.Nullable {
			return nil
		}
		return fmt.Errorf("null is not a %s", t)
	}
	switch t.Kind {
	case KindPrimitive:
		return checkPrimitive(v, t.Name)
	case KindList:
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("want a list for %s", t)
		}
		for i, e := range list {
			if err := ix.checkLiteral(e, *t.Elem); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return nil
	case KindMap:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("want an object for %s", t)
		}
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			if err := ix.checkLiteral(obj[k], *t.Elem); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		return nil
	case KindNamed:
		if e, ok := ix.enums[t.Name]; ok {
			return checkEnumLiteral(v, e)
		}
		return ix.checkObject(v, ix.types[t.Name], true)
	default:
		return fmt.Errorf("a %s value has no literal", t)
	}
}

// checkEnumLiteral accepts the name of a value of e.
func checkEnumLiteral(v any, e *Enum) error {
	s, ok := v.(string)
	if ok && slices.ContainsFunc(e.Values, func(x EnumValue) bool { return x.Name == s }) {
		return nil
	}
	return fmt.Errorf("%v is not a value of %s", v, e.Name)
}

// checkObject accepts an object literal of vt, or a constant name.
func (ix *index) checkObject(v any, vt *ValueType, constants bool) error {
	if s, ok := v.(string); ok && constants {
		if slices.ContainsFunc(vt.Constants, func(c Constant) bool { return c.Name == s }) {
			return nil
		}
		return fmt.Errorf("%q is not a constant of %s", s, vt.Name)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("want an object literal of %s", vt.Name)
	}
	fields := map[string]Field{}
	for _, f := range vt.Fields {
		fields[f.Name] = f
	}
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		f, ok := fields[k]
		if !ok {
			return fmt.Errorf("%s has no field %q", vt.Name, k)
		}
		ft, err := ParseType(f.Type)
		if err != nil {
			return err
		}
		if err := ix.checkLiteral(obj[k], ft); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	for _, f := range vt.Fields {
		if _, ok := obj[f.Name]; f.Required && !ok {
			return fmt.Errorf("%s requires field %q", vt.Name, f.Name)
		}
	}
	return nil
}

// checkPrimitive checks a literal of a primitive type.
func checkPrimitive(v any, name string) error {
	switch name {
	case "bool":
		if _, ok := v.(bool); ok {
			return nil
		}
	case "int":
		if n, ok := v.(json.Number); ok && !strings.ContainsAny(n.String(), ".eE") {
			if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil && -maxSafeInteger <= i && i <= maxSafeInteger {
				return nil
			}
		}
	case "double":
		if _, ok := v.(json.Number); ok {
			return nil
		}
	case "duration":
		if n, ok := v.(json.Number); ok && !strings.ContainsAny(n.String(), ".eE-") {
			if _, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
				return nil
			}
		}
		return fmt.Errorf("%v is not a duration: want whole milliseconds", v)
	case "money":
		return checkMoney(v)
	default:
		return checkStringPrimitive(v, name)
	}
	return fmt.Errorf("%v is not a %s", v, name)
}

// checkStringPrimitive checks the primitives written as strings.
func checkStringPrimitive(v any, name string) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%v is not a %s: want a string", v, name)
	}
	var valid bool
	switch name {
	case "string":
		valid = true
	case "decimal":
		valid = decimalLiteral.MatchString(s)
	case "date":
		_, err := time.Parse(time.DateOnly, s)
		valid = err == nil
	case "dateTime":
		_, err := time.Parse(time.RFC3339Nano, s)
		valid = err == nil
	case "color":
		valid = colorLiteral.MatchString(s)
	case "route":
		valid = routeLiteral.MatchString(s)
	default: // asset: referenced with {"$asset": …}, never a literal
		return fmt.Errorf("a %s value has no literal", name)
	}
	if !valid {
		return fmt.Errorf("%q is not a %s", s, name)
	}
	return nil
}

// checkMoney accepts {"amount": "<decimal>", "currency": "<ISO 4217>"}.
func checkMoney(v any) error {
	obj, ok := v.(map[string]any)
	if !ok || len(obj) != 2 {
		return fmt.Errorf("want {\"amount\", \"currency\"} for money")
	}
	amount, _ := obj["amount"].(string)
	currency, _ := obj["currency"].(string)
	if !decimalLiteral.MatchString(amount) || !currencyLiteral.MatchString(currency) {
		return fmt.Errorf("money needs a decimal amount and an ISO 4217 currency")
	}
	return nil
}

// numeric reports whether t is a number type, which min and max constrain.
func numeric(t Type) bool {
	return t.Kind == KindPrimitive && (t.Name == "int" || t.Name == "double" || t.Name == "decimal" || t.Name == "duration")
}

// sized reports whether t has a length, which minLength and maxLength
// constrain: strings in code points, lists in items.
func sized(t Type) bool {
	return t.Kind == KindList || t.Kind == KindPrimitive && t.Name == "string"
}
