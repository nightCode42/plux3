// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	list := write("list.xml", `<ISO_4217><CcyTbl><CcyNtry><Ccy>ETB</Ccy><CcyMnrUnts>2</CcyMnrUnts></CcyNtry></CcyTbl></ISO_4217>`)
	same := write("same.json", `{"currencies": [{"code": "ETB", "minorUnits": 2}]}`)
	other := write("other.json", `{"currencies": []}`)
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{list, same}, 0},
		{[]string{list, other}, 1},
		{[]string{list}, 2},
		{[]string{filepath.Join(dir, "none.xml"), same}, 2},
		{[]string{list, filepath.Join(dir, "none.json")}, 2},
	} {
		var out, errOut bytes.Buffer
		if got := run(tc.args, &out, &errOut); got != tc.code {
			t.Errorf("%v: exit %d, want %d (%s%s)", tc.args, got, tc.code, out.String(), errOut.String())
		}
	}
}
