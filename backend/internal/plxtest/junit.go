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
	byName := map[string]int{}
	for i, c := range cases {
		byName[c.TestName()] = i
	}
	results := make([]CaseResult, len(cases))
	for i, c := range cases {
		results[i] = CaseResult{Case: c, Status: NotRun}
	}
	tests := map[int]int{} // test ID to case index
	started := map[int]int64{}
	var stray []string
	prints := map[int][]string{}
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
		switch e.Type {
		case "testStart":
			if i, ok := byName[e.Test.Name]; ok {
				tests[e.Test.ID] = i
				started[e.Test.ID] = e.Time
			}
		case "print":
			if _, ok := tests[e.TestID]; ok {
				prints[e.TestID] = append(prints[e.TestID], e.Message)
			}
		case "error":
			i, ok := tests[e.TestID]
			if !ok {
				stray = append(stray, firstLine(e.Error))
				continue
			}
			if thrown, failure, found := thrownInTest(prints[e.TestID]); found {
				e.Error, e.IsFailure = thrown, failure
			}
			if results[i].Status == Failed || results[i].Status == Errored {
				continue
			}
			results[i].Status = Errored
			if e.IsFailure {
				results[i].Status = Failed
			}
			results[i].Message, results[i].Detail = firstLine(e.Error), strings.TrimSpace(e.Error)
		case "testDone":
			i, ok := tests[e.TestID]
			if !ok {
				continue
			}
			results[i].Millis = max(e.Time-started[e.TestID], 0)
			switch {
			case e.Skipped:
				results[i].Status = Skipped
			case results[i].Status == NotRun && e.Result == "success":
				results[i].Status = Passed
			case results[i].Status == NotRun:
				results[i].Status = Errored
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the test report: %w", err)
	}
	for i := range results {
		if results[i].Status == NotRun {
			results[i].Message = "the scenario did not run"
			if len(stray) > 0 {
				results[i].Message += ": " + stray[0]
			}
			results[i].Detail = strings.Join(stray, "\n")
		}
	}
	return results, nil
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

// Summarize counts the results by status; a scenario that did not run
// counts as errored.
func Summarize(results []CaseResult) Summary {
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
