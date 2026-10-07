// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"os"
	"reflect"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/bundle/fbs"
)

// hostEventRow is a host event of a meta section, flattened.
type hostEventRow struct {
	name      string
	direction fbs.HostEventDirection
	fields    [][2]string
}

// metaHostEvents lists the host events of the app bundle in section order.
func metaHostEvents(m *fbs.Meta) []hostEventRow {
	var out []hostEventRow
	var ev fbs.HostEvent
	var f fbs.HostEventField
	for i := range m.HostEventsLength() {
		m.HostEvents(&ev, i)
		row := hostEventRow{name: string(ev.Name()), direction: ev.Direction()}
		for j := range ev.FieldsLength() {
			ev.Fields(&f, j)
			row.fields = append(row.fields, [2]string{string(f.Name()), string(f.Type())})
		}
		out = append(out, row)
	}
	return out
}

// TestAppBundleCarriesHostEvents checks that the app bundle's meta lists
// the declared host events with their direction and field types, so the
// runtime can check what Plux.sendEvent receives; plugin bundles carry
// none.
// Verifies: HST-013.
func TestAppBundleCarriesHostEvents(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		dir  string
		want []hostEventRow
	}{
		{triggersDir, []hostEventRow{
			{"ping", fbs.HostEventDirectionToPlux, [][2]string{{"n", "int"}}},
			{"priced", fbs.HostEventDirectionBoth, [][2]string{{"amount", "decimal"}, {"on", "date"}, {"note", "string?"}}},
		}},
		{featuresDir, []hostEventRow{{"taskCompleted", fbs.HostEventDirectionToHost, [][2]string{
			{"title", "string"}, {"priority", "Priority"}, {"note", "string?"},
		}}}},
	} {
		res := Compile(os.DirFS(c.dir), DefaultOptions())
		clean(t, res)
		bundles := readAll(t, res)
		if got := metaHostEvents(metaOf(t, bundles[0])); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: host events %+v, want %+v", c.dir, got, c.want)
		}
		for _, b := range bundles[1:] {
			if got := metaHostEvents(metaOf(t, b)); len(got) != 0 {
				t.Errorf("%s: plugin bundle carries host events %+v", c.dir, got)
			}
		}
	}
}
