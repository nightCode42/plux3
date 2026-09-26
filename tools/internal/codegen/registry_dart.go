// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/nightCode42/plux3/tools/internal/registry"
)

// registryDart renders packages/plux_flutter/lib/src/schema/registry.g.dart:
// the permanent IDs the runtime decodes bundles with (BND-011). The runtime's
// prop decoders are generated from the same descriptors in P3.
func registryDart(r *registry.Registry) []byte {
	var b bytes.Buffer
	b.WriteString(header(LangDart, RegistrySource))
	b.WriteString("/// Permanent IDs of the widget and action registries (BND-011, WGT-002).\nlibrary;\n\n")
	b.WriteString(dartRegistryClasses)
	b.WriteString("\n/// Every widget type, sorted by name.\nconst List<WidgetDescriptor> widgetDescriptors = [\n")
	for _, w := range r.Widgets {
		fmt.Fprintf(&b, "  WidgetDescriptor(\n    %s,\n    %d,\n    layer: %d,\n    revision: %d,\n", quoteDart(w.Type), w.ID, w.Layer, w.Revision)
		writeDartIDs(&b, "props", w.Props, func(p registry.Prop) (string, uint32) { return p.Name, p.ID })
		writeDartIDs(&b, "events", w.Events, func(e registry.Event) (string, uint32) { return e.Name, e.ID })
		writeDartIDs(&b, "slots", w.Slots, func(s registry.Slot) (string, uint32) { return s.Name, s.ID })
		b.WriteString("  ),\n")
	}
	b.WriteString("];\n\n/// Every value type, sorted by name.\nconst List<ValueTypeDescriptor> valueTypeDescriptors = [\n")
	for _, t := range r.Types {
		fmt.Fprintf(&b, "  ValueTypeDescriptor(\n    %s,\n    %d,\n    revision: %d,\n", quoteDart(t.Name), t.ID, t.Revision)
		writeDartIDs(&b, "fields", t.Fields, func(f registry.Field) (string, uint32) { return f.Name, f.ID })
		b.WriteString("  ),\n")
	}
	b.WriteString("];\n\n/// Every enum, sorted by name.\nconst List<EnumDescriptor> enumDescriptors = [\n")
	for _, e := range r.Enums {
		fmt.Fprintf(&b, "  EnumDescriptor(\n    %s,\n    %d,\n    revision: %d,\n", quoteDart(e.Name), e.ID, e.Revision)
		writeDartIDs(&b, "values", e.Values, func(v registry.EnumValue) (string, uint32) { return v.Name, v.ID })
		b.WriteString("  ),\n")
	}
	b.WriteString("];\n\n/// Every action, sorted by name.\nconst List<ActionDescriptor> actionDescriptors = [\n")
	for _, a := range r.Actions {
		fmt.Fprintf(&b, "  ActionDescriptor(\n    %s,\n    %d,\n", quoteDart(a.Name), a.ID)
		writeDartIDs(&b, "inputs", a.Inputs, func(in registry.Input) (string, uint32) { return in.Name, in.ID })
		b.WriteString("  ),\n")
	}
	b.WriteString("];\n")
	return b.Bytes()
}

// writeDartIDs renders a named argument mapping member names to IDs, in ID
// order; nothing when there are no members.
func writeDartIDs[T any](b *bytes.Buffer, name string, members []T, key func(T) (string, uint32)) {
	if len(members) == 0 {
		return
	}
	sorted := sortedByID(members, func(m T) uint32 { _, id := key(m); return id })
	entries := make([]string, len(sorted))
	for i, m := range sorted {
		n, id := key(m)
		entries[i] = fmt.Sprintf("%s: %d", quoteDart(n), id)
	}
	fmt.Fprintf(b, "    %s: {%s},\n", name, strings.Join(entries, ", "))
}

// dartRegistryClasses declares the descriptor types of registry.g.dart.
const dartRegistryClasses = `/// A widget type and the permanent IDs of its members.
final class WidgetDescriptor {
  /// Creates a descriptor.
  const WidgetDescriptor(
    this.type,
    this.id, {
    required this.layer,
    required this.revision,
    this.props = const {},
    this.events = const {},
    this.slots = const {},
  });

  /// The type name used in documents.
  final String type;

  /// The permanent ID encoded in bundles.
  final int id;

  /// 1 for core primitives, 2 for Plux components.
  final int layer;

  /// The current descriptor revision (WGT-004).
  final int revision;

  /// Prop IDs by name.
  final Map<String, int> props;

  /// Event IDs by name.
  final Map<String, int> events;

  /// Slot IDs by name.
  final Map<String, int> slots;
}

/// A value type and the permanent IDs of its fields.
final class ValueTypeDescriptor {
  /// Creates a descriptor.
  const ValueTypeDescriptor(this.name, this.id, {required this.revision, this.fields = const {}});

  /// The type name.
  final String name;

  /// The permanent ID encoded in bundles.
  final int id;

  /// The current revision.
  final int revision;

  /// Field IDs by name.
  final Map<String, int> fields;
}

/// An enum and the permanent IDs of its values.
final class EnumDescriptor {
  /// Creates a descriptor.
  const EnumDescriptor(this.name, this.id, {required this.revision, this.values = const {}});

  /// The enum name.
  final String name;

  /// The permanent ID encoded in bundles.
  final int id;

  /// The current revision.
  final int revision;

  /// Value IDs by name.
  final Map<String, int> values;
}

/// An action and the permanent IDs of its inputs.
final class ActionDescriptor {
  /// Creates a descriptor.
  const ActionDescriptor(this.name, this.id, {this.inputs = const {}});

  /// The action name used in action graphs.
  final String name;

  /// The permanent ID encoded in bundles.
  final int id;

  /// Input IDs by name.
  final Map<String, int> inputs;
}
`
