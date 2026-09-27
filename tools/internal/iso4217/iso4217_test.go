// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package iso4217

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const listOne = `<?xml version="1.0" encoding="UTF-8"?>
<ISO_4217 Pblshd="2026-09-17"><CcyTbl>
<CcyNtry><CtryNm>ETHIOPIA</CtryNm><CcyNm>Ethiopian Birr</CcyNm><Ccy>ETB</Ccy><CcyNbr>230</CcyNbr><CcyMnrUnts>2</CcyMnrUnts></CcyNtry>
<CcyNtry><CtryNm>JAPAN</CtryNm><CcyNm>Yen</CcyNm><Ccy>JPY</Ccy><CcyNbr>392</CcyNbr><CcyMnrUnts>0</CcyMnrUnts></CcyNtry>
<CcyNtry><CtryNm>ZZ08_Gold</CtryNm><CcyNm>Gold</CcyNm><Ccy>XAU</Ccy><CcyNbr>959</CcyNbr><CcyMnrUnts>N.A.</CcyMnrUnts></CcyNtry>
<CcyNtry><CtryNm>ANTARCTICA</CtryNm><CcyNm>No universal currency</CcyNm></CcyNtry>
</CcyTbl></ISO_4217>`

func TestDiff(t *testing.T) {
	t.Parallel()
	list, err := ReadList(strings.NewReader(listOne))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list %v", list)
	}
	ours, err := ReadCurrencies(strings.NewReader(`{"currencies": [{"code": "JPY", "minorUnits": 2}, {"code": "USD", "minorUnits": 2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ETB: in ISO 4217 (2 minor units), missing from currencies.json",
		"JPY: ISO 4217 has 0 minor units, currencies.json 2",
		"USD: in currencies.json, not in ISO 4217 list one",
	}
	if got := Diff(list, ours); !slices.Equal(got, want) {
		t.Errorf("diff:\n%s", strings.Join(got, "\n"))
	}
	if got := Diff(list, list); len(got) != 0 {
		t.Errorf("a list differs from itself: %v", got)
	}
}

func TestReadErrors(t *testing.T) {
	t.Parallel()
	if _, err := ReadList(strings.NewReader("<")); err == nil {
		t.Error("broken XML was accepted")
	}
	if _, err := ReadList(strings.NewReader(`<ISO_4217><CcyTbl><CcyNtry><Ccy>AAA</Ccy><CcyMnrUnts>two</CcyMnrUnts></CcyNtry></CcyTbl></ISO_4217>`)); err == nil {
		t.Error("non-numeric minor units were accepted")
	}
	if _, err := ReadCurrencies(strings.NewReader("{")); err == nil {
		t.Error("broken JSON was accepted")
	}
}

// The committed file parses.
func TestReadCommittedCurrencies(t *testing.T) {
	t.Parallel()
	f, err := os.Open(filepath.Join("..", "..", "..", "schema", "pxl", "currencies.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ours, err := ReadCurrencies(f)
	if err != nil || ours["ETB"] != 2 || ours["JPY"] != 0 || ours["XAD"] != 2 {
		t.Fatalf("currencies: %v %v", len(ours), err)
	}
}
