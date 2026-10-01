// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Registry is the complete, checked registry.
type Registry struct {
	// Widgets, value types, enums and actions, each sorted by name.
	Widgets []*Widget
	Types   []*ValueType
	Enums   []*Enum
	Actions []*Action
	// API is the Flutter snapshot the coverage was computed against.
	API *FlutterAPI
	// Coverage has one entry per Flutter class named by a widget or value
	// type, in the order of Widgets, then Types.
	Coverage []ClassCoverage
	// EnumCoverage has one entry per mirrored enum, sorted by name.
	EnumCoverage []EnumCoverage
	// Lock is the permanent-ID lock including this registry's entries.
	Lock Lock
}

// Widget is a widget descriptor (schema/json/registry/widget-descriptor.schema.json).
type Widget struct {
	Type           string          `json:"type"`
	ID             uint32          `json:"id"`
	Layer          int             `json:"layer"`
	Phase          string          `json:"phase"`
	Revision       int             `json:"revision"`
	Revisions      []Revision      `json:"revisions"`
	Flutter        *FlutterClass   `json:"flutter,omitempty"`
	Category       string          `json:"category"`
	Icon           string          `json:"icon"`
	Description    string          `json:"description"`
	Platforms      []string        `json:"platforms"`
	Cost           int             `json:"cost"`
	Accessibility  Accessibility   `json:"accessibility"`
	TypeParameters []string        `json:"typeParameters,omitempty"`
	Props          []Prop          `json:"props,omitempty"`
	Events         []Event         `json:"events,omitempty"`
	Children       *Children       `json:"children,omitempty"`
	Slots          []Slot          `json:"slots,omitempty"`
	Excluded       []Exclusion     `json:"excluded,omitempty"`
	Deprecated     *Deprecation    `json:"deprecated,omitempty"`
	File           string          `json:"-"`
	Schema         json.RawMessage `json:"$schema,omitempty"`
}

// Revision maps a descriptor revision to the first runtime implementing it.
type Revision struct {
	Revision int    `json:"revision"`
	Runtime  string `json:"runtime"`
}

// FlutterClass names a Flutter class and the constructors mirrored.
type FlutterClass struct {
	Library      string   `json:"library"`
	Class        string   `json:"class"`
	Constructors []string `json:"constructors"`
}

// Key is the class's key in the Flutter snapshot.
func (c FlutterClass) Key() string { return c.Library + "#" + c.Class }

// Accessibility holds the accessibility requirements of a widget.
type Accessibility struct {
	Role        string `json:"role"`
	Interactive bool   `json:"interactive"`
}

// Prop is a widget prop.
type Prop struct {
	Name        string          `json:"name"`
	ID          uint32          `json:"id"`
	Type        string          `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Constraints *Constraints    `json:"constraints,omitempty"`
	Revision    int             `json:"revision,omitempty"`
	Deprecated  *Deprecation    `json:"deprecated,omitempty"`
	Bindable    *bool           `json:"bindable,omitempty"`
	Flutter     FlutterNames    `json:"flutter,omitempty"`
	// Constructor is the named Flutter constructor a bool prop selects
	// when true (WGT-011).
	Constructor string `json:"constructor,omitempty"`
	Description string `json:"description,omitempty"`
}

// IsBindable reports whether the prop accepts bindings; the default is true.
func (p Prop) IsBindable() bool { return p.Bindable == nil || *p.Bindable }

// Event is a widget event.
type Event struct {
	Name        string       `json:"name"`
	ID          uint32       `json:"id"`
	Payload     string       `json:"payload,omitempty"`
	Revision    int          `json:"revision,omitempty"`
	Deprecated  *Deprecation `json:"deprecated,omitempty"`
	Flutter     FlutterNames `json:"flutter,omitempty"`
	Description string       `json:"description,omitempty"`
}

// Children declares that a widget takes a list of children.
type Children struct {
	Flutter FlutterNames `json:"flutter,omitempty"`
	Min     *int         `json:"min,omitempty"`
	Max     *int         `json:"max,omitempty"`
}

// Slot is a named slot of a widget.
type Slot struct {
	Name        string       `json:"name"`
	ID          uint32       `json:"id"`
	List        bool         `json:"list,omitempty"`
	Required    bool         `json:"required,omitempty"`
	Template    bool         `json:"template,omitempty"`
	Revision    int          `json:"revision,omitempty"`
	Deprecated  *Deprecation `json:"deprecated,omitempty"`
	Flutter     FlutterNames `json:"flutter,omitempty"`
	Description string       `json:"description,omitempty"`
}

// Exclusion records why a Flutter parameter or enum value is not supported
// (WGT-003).
type Exclusion struct {
	Flutter string `json:"flutter"`
	Reason  string `json:"reason"`
	Note    string `json:"note,omitempty"`
}

// Exclusion reasons of WGT-003, in canonical order.
const (
	ReasonCallback        = "callback→event"
	ReasonController      = "controller→state"
	ReasonBuilder         = "builder→template"
	ReasonNonSerialisable = "non-serialisable"
	ReasonDeprecated      = "deprecated"
	ReasonDeferred        = "deferred"
)

// reasons lists the exclusion reasons in canonical order.
var reasons = []string{ReasonCallback, ReasonController, ReasonBuilder, ReasonNonSerialisable, ReasonDeprecated, ReasonDeferred}

// Reasons returns the exclusion reasons of WGT-003 in canonical order.
func Reasons() []string { return slices.Clone(reasons) }

// Deprecation marks a member or entry as deprecated from a revision on.
type Deprecation struct {
	Revision int    `json:"revision"`
	Message  string `json:"message"`
}

// Constraints restrict literal values.
type Constraints struct {
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MinLength *int     `json:"minLength,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
}

// ValueType is a structured prop value (schema/json/registry/value-type.schema.json).
type ValueType struct {
	Name        string          `json:"name"`
	ID          uint32          `json:"id"`
	Revision    int             `json:"revision"`
	Revisions   []Revision      `json:"revisions"`
	Description string          `json:"description"`
	Flutter     []FlutterClass  `json:"flutter,omitempty"`
	Fields      []Field         `json:"fields"`
	Constants   []Constant      `json:"constants,omitempty"`
	Excluded    []Exclusion     `json:"excluded,omitempty"`
	File        string          `json:"-"`
	Schema      json.RawMessage `json:"$schema,omitempty"`
}

// Field is a field of a value type.
type Field struct {
	Name        string          `json:"name"`
	ID          uint32          `json:"id"`
	Type        string          `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Revision    int             `json:"revision,omitempty"`
	Deprecated  *Deprecation    `json:"deprecated,omitempty"`
	Flutter     FlutterNames    `json:"flutter,omitempty"`
	Description string          `json:"description,omitempty"`
}

// Constant is a named value of a value type, such as Alignment.center; a
// literal of the type may be written as the constant's name.
type Constant struct {
	Name        string          `json:"name"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description,omitempty"`
}

// Enum is an enumeration (schema/json/registry/enum.schema.json).
type Enum struct {
	Name        string          `json:"name"`
	ID          uint32          `json:"id"`
	Revision    int             `json:"revision"`
	Revisions   []Revision      `json:"revisions"`
	Description string          `json:"description"`
	Flutter     *FlutterEnum    `json:"flutter,omitempty"`
	Values      []EnumValue     `json:"values"`
	Excluded    []Exclusion     `json:"excluded,omitempty"`
	File        string          `json:"-"`
	Schema      json.RawMessage `json:"$schema,omitempty"`
}

// FlutterEnum names the Flutter enum an enum mirrors.
type FlutterEnum struct {
	Library string `json:"library"`
	Enum    string `json:"enum"`
}

// Key is the enum's key in the Flutter snapshot.
func (e FlutterEnum) Key() string { return e.Library + "#" + e.Enum }

// EnumValue is a value of an enum.
type EnumValue struct {
	Name        string       `json:"name"`
	ID          uint32       `json:"id"`
	Revision    int          `json:"revision,omitempty"`
	Deprecated  *Deprecation `json:"deprecated,omitempty"`
	Description string       `json:"description,omitempty"`
}

// Action is a built-in action (schema/json/registry/action.schema.json).
type Action struct {
	Name         string          `json:"name"`
	ID           uint32          `json:"id"`
	Phase        string          `json:"phase"`
	Category     string          `json:"category"`
	Description  string          `json:"description"`
	TypeParams   []TypeParam     `json:"typeParameters,omitempty"`
	Inputs       []Input         `json:"inputs,omitempty"`
	Output       string          `json:"output,omitempty"`
	Branches     []string        `json:"branches,omitempty"`
	BranchesFrom string          `json:"branchesFrom,omitempty"`
	Effects      []string        `json:"effects,omitempty"`
	Deprecated   *Deprecation    `json:"deprecated,omitempty"`
	File         string          `json:"-"`
	Schema       json.RawMessage `json:"$schema,omitempty"`
}

// TypeParam is a type parameter of an action, bound from the step's
// context.
type TypeParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Input is a typed input of an action.
type Input struct {
	Name        string          `json:"name"`
	ID          uint32          `json:"id"`
	Type        string          `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Ref         string          `json:"ref,omitempty"`
	Description string          `json:"description,omitempty"`
}

// FlutterNames is the Flutter parameter, or parameters, a member covers:
// a string or a non-empty list of strings in JSON.
type FlutterNames []string

// UnmarshalJSON accepts a string or a list of strings.
func (n *FlutterNames) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*n = FlutterNames{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("flutter: want a parameter name or a list of names: %w", err)
	}
	if len(many) == 0 {
		return fmt.Errorf("flutter: empty list of parameter names")
	}
	*n = many
	return nil
}
