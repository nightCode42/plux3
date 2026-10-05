// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"encoding/xml"
	"strings"
	"testing"
)

var cases = []Case{
	{File: "tests/a.scenario.yaml", Name: "passes", Line: 4},
	{File: "tests/a.scenario.yaml", Name: "fails", Line: 12},
	{File: "tests/b.scenario.yaml", Name: "breaks", Line: 3},
	{File: "tests/b.scenario.yaml", Name: "never runs", Line: 20},
}

// A trimmed run of `flutter test --reporter json`.
const report = `{"protocolVersion":"0.1.1","runnerVersion":"1.26.0","type":"start","time":0}
{"test":{"id":1,"name":"loading test/01_test.dart","suiteID":0,"groupIDs":[],"metadata":{"skip":false,"skipReason":null}},"type":"testStart","time":10}
{"testID":1,"result":"success","skipped":false,"hidden":true,"type":"testDone","time":900}
{"test":{"id":2,"name":"tests/a.scenario.yaml passes","suiteID":0,"groupIDs":[1]},"type":"testStart","time":1000}
{"testID":2,"result":"success","skipped":false,"hidden":false,"type":"testDone","time":1250}
{"test":{"id":3,"name":"tests/a.scenario.yaml fails","suiteID":0,"groupIDs":[1]},"type":"testStart","time":1300}
{"testID":3,"messageType":"print","message":"══╡ EXCEPTION CAUGHT BY FLUTTER TEST FRAMEWORK ╞═══\nThe following TestFailure was thrown running a test:\ntests/a.scenario.yaml:20:9: expected a navigation to \"places\", found \"profile\"\n\nWhen the exception was thrown, this was the stack:\n#0 x","type":"print","time":1400}
{"testID":3,"error":"Test failed. See exception logs above.","stackTrace":"","isFailure":false,"type":"error","time":1500}
{"testID":3,"result":"error","skipped":false,"hidden":false,"type":"testDone","time":1501}
{"test":{"id":4,"name":"tests/b.scenario.yaml breaks","suiteID":0,"groupIDs":[2]},"type":"testStart","time":1600}
{"testID":4,"messageType":"print","message":"══╡ EXCEPTION CAUGHT ╞═══\nThe following StateError was thrown running a test:\nBad state: no element\n\nWhen the exception was thrown, this was the stack:\n#0 y","type":"print","time":1700}
{"testID":4,"error":"Test failed. See exception logs above.","stackTrace":"","isFailure":false,"type":"error","time":1800}
{"testID":4,"result":"error","skipped":false,"hidden":false,"type":"testDone","time":1801}
{"testID":99,"error":"Failed to load \"test/02_test.dart\": compile error","stackTrace":"","isFailure":false,"type":"error","time":1900}
not json at all
{"success":false,"type":"done","time":2000}
`

// Verifies: TST-002.
func TestParseReportMatchesScenariosAndExtractsTheFailingExpectation(t *testing.T) {
	t.Parallel()
	got, err := ParseReport(strings.NewReader(report), cases)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		status Status
		msg    string
		ms     int64
	}
	for i, w := range []want{
		{Passed, "", 250},
		{Failed, `tests/a.scenario.yaml:20:9: expected a navigation to "places", found "profile"`, 201},
		{Errored, "Bad state: no element", 201},
		{NotRun, `the scenario did not run: Failed to load "test/02_test.dart": compile error`, 0},
	} {
		if got[i].Status != w.status || got[i].Message != w.msg || got[i].Millis != w.ms {
			t.Errorf("result %d = %+v, want %+v", i, got[i], w)
		}
	}
	sum := Summarize(got)
	if sum != (Summary{Total: 4, Passed: 1, Failed: 1, Errored: 2}) || sum.OK() {
		t.Errorf("summary = %+v", sum)
	}
}

// Verifies: TST-002.
func TestJUnitHasOneTestCaseAndSuitePerScenarioAndFile(t *testing.T) {
	t.Parallel()
	results, err := ParseReport(strings.NewReader(report), cases)
	if err != nil {
		t.Fatal(err)
	}
	data, err := JUnit(results)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name    string `xml:"name,attr"`
				File    string `xml:"file,attr"`
				Line    int    `xml:"line,attr"`
				Time    string `xml:"time,attr"`
				Failure *struct {
					Message string `xml:"message,attr"`
				} `xml:"failure"`
				Error *struct {
					Message string `xml:"message,attr"`
				} `xml:"error"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if doc.Tests != 4 || doc.Failures != 1 || doc.Errors != 2 || len(doc.Suites) != 2 {
		t.Fatalf("totals %+v", doc)
	}
	a := doc.Suites[0]
	if a.Name != "tests/a.scenario.yaml" || len(a.Cases) != 2 || a.Cases[0].Time != "0.250" || a.Cases[1].Line != 12 {
		t.Errorf("suite a = %+v", a)
	}
	if f := a.Cases[1].Failure; f == nil || !strings.HasPrefix(f.Message, "tests/a.scenario.yaml:20:9: expected") {
		t.Errorf("failure = %+v", f)
	}
	if e := doc.Suites[1].Cases[1].Error; e == nil || !strings.Contains(e.Message, "did not run") {
		t.Errorf("error = %+v", e)
	}
	again, _ := JUnit(results)
	if string(again) != string(data) {
		t.Error("the XML is not a function of the results")
	}
	if !strings.HasPrefix(string(data), "<?xml") {
		t.Error("no XML declaration")
	}
}

// Verifies: TST-002.
func TestParseReportOfAnEmptyRunMarksEverythingNotRun(t *testing.T) {
	t.Parallel()
	got, err := ParseReport(strings.NewReader(""), cases[:1])
	if err != nil || got[0].Status != NotRun || Summarize(got).OK() {
		t.Fatalf("%+v %v", got, err)
	}
}
