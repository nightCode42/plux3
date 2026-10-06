// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package plxtest

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Status is how one scenario ended.
type Status string

// The statuses of a scenario.
const (
	Passed  Status = "passed"
	Failed  Status = "failed"  // an expectation did not hold
	Errored Status = "errored" // the test broke before it could say
	Skipped Status = "skipped"
	NotRun  Status = "notrun" // Flutter reported nothing for it
)

// CaseResult is the outcome of one scenario.
type CaseResult struct {
	Case   Case
	Status Status
	// Message is the first line of the failure, which names the file, line
	// and column of the failing step or expectation; Detail is all of it.
	Message, Detail string
	// Millis is how long the test took.
	Millis int64
}

// event is a line of `flutter test --reporter json`, the protocol of
// package:test: only the fields this package reads.
type event struct {
	Type      string `json:"type"`
	Time      int64  `json:"time"`
	TestID    int    `json:"testID"`
	Result    string `json:"result"`
	Skipped   bool   `json:"skipped"`
	Hidden    bool   `json:"hidden"`
	Error     string `json:"error"`
	Message   string `json:"message"`
	IsFailure bool   `json:"isFailure"`
	Test      struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"test"`
}

// ParseReport reads the JSON reporter's events and matches them with the
// generated cases, in the order of cases. Anything Flutter reports outside
// a scenario, such as a test file that does not compile, is attached to
// the scenarios that never reported.
func ParseReport(r io.Reader, cases []Case) ([]CaseResult, error) {
	rp := &reportParser{
		byName:  map[string]int{},
		results: make([]CaseResult, len(cases)),
		tests:   map[int]int{},
		started: map[int]int64{},
		prints:  map[int][]string{},
	}
	for i, c := range cases {
		rp.byName[c.TestName()] = i
		rp.results[i] = CaseResult{Case: c, Status: NotRun}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		rp.apply(e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the test report: %w", err)
	}
	rp.markNotRun()
	return rp.results, nil
}

// reportParser is the state of one reading of the JSON reporter's events.
type reportParser struct {
	byName  map[string]int // test name to case index
	results []CaseResult
	tests   map[int]int // test ID to case index
	started map[int]int64
	stray   []string
	prints  map[int][]string
}

// apply folds one event into the results.
func (rp *reportParser) apply(e event) {
	switch e.Type {
	case "testStart":
		if i, ok := rp.byName[e.Test.Name]; ok {
			rp.tests[e.Test.ID] = i
			rp.started[e.Test.ID] = e.Time
		}
	case "print":
		if _, ok := rp.tests[e.TestID]; ok {
			rp.prints[e.TestID] = append(rp.prints[e.TestID], e.Message)
		}
	case "error":
		rp.applyError(e)
	case "testDone":
		rp.applyDone(e)
	}
}

// applyError records an error of a test; one outside any scenario is stray.
func (rp *reportParser) applyError(e event) {
	i, ok := rp.tests[e.TestID]
	if !ok {
		rp.stray = append(rp.stray, firstLine(e.Error))
		return
	}
	if thrown, failure, found := thrownInTest(rp.prints[e.TestID]); found {
		e.Error, e.IsFailure = thrown, failure
	}
	res := &rp.results[i]
	if res.Status == Failed || res.Status == Errored {
		return
	}
	res.Status = Errored
	if e.IsFailure {
		res.Status = Failed
	}
	res.Message, res.Detail = firstLine(e.Error), strings.TrimSpace(e.Error)
}

// applyDone records the end of a test.
func (rp *reportParser) applyDone(e event) {
	i, ok := rp.tests[e.TestID]
	if !ok {
		return
	}
	res := &rp.results[i]
	res.Millis = max(e.Time-rp.started[e.TestID], 0)
	switch {
	case e.Skipped:
		res.Status = Skipped
	case res.Status == NotRun && e.Result == "success":
		res.Status = Passed
	case res.Status == NotRun:
		res.Status = Errored
	}
}

// markNotRun attaches the stray errors to the scenarios that never reported.
func (rp *reportParser) markNotRun() {
	for i := range rp.results {
		if rp.results[i].Status == NotRun {
			rp.results[i].Message = "the scenario did not run"
			if len(rp.stray) > 0 {
				rp.results[i].Message += ": " + rp.stray[0]
			}
			rp.results[i].Detail = strings.Join(rp.stray, "\n")
		}
	}
}

// thrownInTest finds what the test framework printed for the exception
// that ended a widget test: its reporter only says "Test failed. See
// exception logs above.", the exception itself is in a printed block. A
// TestFailure, which is how an expectation of a scenario fails, is a
// failure; anything else is an error.
func thrownInTest(prints []string) (text string, failure, ok bool) {
	const (
		lead  = "was thrown running a test:\n"
		trail = "\n\nWhen the exception was thrown"
	)
	for _, p := range prints {
		i := strings.Index(p, lead)
		if i < 0 {
			continue
		}
		header := p[:i]
		text = p[i+len(lead):]
		if j := strings.Index(text, trail); j >= 0 {
			text = text[:j]
		}
		return strings.TrimSpace(text), strings.HasSuffix(strings.TrimSpace(header), "TestFailure"), true
	}
	return "", false, false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// Summary counts results.
type Summary struct {
	Total, Passed, Failed, Errored, Skipped int
}

// Summarise counts the results by status; a scenario that did not run
// counts as errored.
func Summarise(results []CaseResult) Summary {
	s := Summary{Total: len(results)}
	for _, r := range results {
		switch r.Status {
		case Passed:
			s.Passed++
		case Failed:
			s.Failed++
		case Skipped:
			s.Skipped++
		case Errored, NotRun:
			s.Errored++
		}
	}
	return s
}

// OK reports whether every scenario passed.
func (s Summary) OK() bool { return s.Failed == 0 && s.Errored == 0 }

// The JUnit XML of the Ant schema every CI system reads.
type (
	junitSuites struct {
		XMLName xml.Name     `xml:"testsuites"`
		Name    string       `xml:"name,attr"`
		Tests   int          `xml:"tests,attr"`
		Failure int          `xml:"failures,attr"`
		Errors  int          `xml:"errors,attr"`
		Skipped int          `xml:"skipped,attr"`
		Time    string       `xml:"time,attr"`
		Suite   []junitSuite `xml:"testsuite"`
	}
	junitSuite struct {
		Name    string      `xml:"name,attr"`
		Tests   int         `xml:"tests,attr"`
		Failure int         `xml:"failures,attr"`
		Errors  int         `xml:"errors,attr"`
		Skipped int         `xml:"skipped,attr"`
		Time    string      `xml:"time,attr"`
		Cases   []junitCase `xml:"testcase"`
	}
	junitCase struct {
		Class   string        `xml:"classname,attr"`
		Name    string        `xml:"name,attr"`
		File    string        `xml:"file,attr"`
		Line    int           `xml:"line,attr"`
		Time    string        `xml:"time,attr"`
		Failure *junitProblem `xml:"failure,omitempty"`
		Error   *junitProblem `xml:"error,omitempty"`
		Skipped *struct{}     `xml:"skipped,omitempty"`
	}
	junitProblem struct {
		Message string `xml:"message,attr"`
		Type    string `xml:"type,attr"`
		Text    string `xml:",chardata"`
	}
)

// JUnit writes the results as JUnit XML: one test suite per scenario file,
// one test case per scenario, a failure with the file, line and column of
// the expectation that did not hold. The output depends on the results
// alone.
func JUnit(results []CaseResult) ([]byte, error) {
	doc := junitSuites{Name: "plux test"}
	var cur *junitSuite
	var total int64
	for _, r := range results {
		if cur == nil || cur.Name != r.Case.File {
			doc.Suite = append(doc.Suite, junitSuite{Name: r.Case.File})
			cur = &doc.Suite[len(doc.Suite)-1]
		}
		c := junitCase{Class: r.Case.File, Name: r.Case.Name, File: r.Case.File, Line: r.Case.Line, Time: seconds(r.Millis)}
		switch r.Status {
		case Failed:
			c.Failure = &junitProblem{Message: r.Message, Type: "ExpectationFailed", Text: r.Detail}
			cur.Failure++
		case Errored, NotRun:
			c.Error = &junitProblem{Message: r.Message, Type: "ScenarioError", Text: r.Detail}
			cur.Errors++
		case Skipped:
			c.Skipped = &struct{}{}
			cur.Skipped++
		case Passed:
		}
		cur.Tests++
		cur.Cases = append(cur.Cases, c)
		total += r.Millis
	}
	for i := range doc.Suite {
		s := &doc.Suite[i]
		var ms int64
		for _, r := range results {
			if r.Case.File == s.Name {
				ms += r.Millis
			}
		}
		s.Time = seconds(ms)
		doc.Tests += s.Tests
		doc.Failure += s.Failure
		doc.Errors += s.Errors
		doc.Skipped += s.Skipped
	}
	doc.Time = seconds(total)
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, errors.Join(errors.New("encode JUnit XML"), err)
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}

func seconds(ms int64) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }
