// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command sizegate checks what the runtime adds to a host app's download
// (RT-061, NFR-009): the same blank app built without and with
// plux_flutter (test/size), release mode.
//
//	sizegate -target android-arm64-apk -blank app-blank.apk -plux app-plux.apk
//	sizegate -target android-x86_64-apk -blank blank.apk -plux plux.apk -report
//	sizegate -target android-arm64-v8a-aab -blank blank.aab -plux plux.aab -report
//	sizegate -target ios-arm64-ipa -blank Blank.app -plux Plux.app
//
// An APK is measured as it is. An App Bundle is measured as what Play
// delivers to a device of one ABI, compressed: the base module's files
// with only that ABI's native libraries, each compressed at the highest
// level into one ZIP archive. A directory — an iOS .app — is measured as
// the ZIP archive an IPA is, with the app under Payload/, compressed at
// the highest level. The run fails (exit 1) when the runtime adds more
// than the target's budget (RT-061: 6.5 MiB to an APK, 3 MiB to an App
// Bundle download or an IPA), or more than 10% over the target's
// committed overhead in -baseline, unless -report only reports; -update
// writes the measured overhead there instead. Exit 2 is a usage or I/O
// error.
package main

import (
	"archive/zip"
	"compress/flate"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	exitOK     = 0
	exitFailed = 1
	exitError  = 2
)

// budget is the most the runtime may add to a target's build (RT-061,
// ADR-0036): 6.5 MiB to an APK, which stores the Dart code uncompressed,
// and 3 MiB to what a device downloads, an App Bundle's split or an IPA.
func budget(target string) int64 {
	if strings.HasSuffix(target, "-apk") {
		return 13 << 19
	}
	return 3 << 20
}

// growth is how much the overhead may grow over the committed baseline
// before the gate fails (QA-007: regressions beyond 10%).
const growth = 0.10

// targets are the builds the gate knows.
func targets() []string {
	return []string{
		"android-arm64-apk", "android-armeabi-v7a-apk", "android-x86_64-apk",
		"android-arm64-v8a-aab", "android-armeabi-v7a-aab", "android-x86_64-aab",
		"ios-arm64-ipa",
	}
}

// abi is the Android ABI an App Bundle target delivers, or "" for a
// target that is not an App Bundle.
func abi(target string) string {
	s, ok := strings.CutSuffix(target, "-aab")
	if !ok {
		return ""
	}
	return strings.TrimPrefix(s, "android-")
}

// main delegates to run so that the command logic is testable.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("sizegate", flag.ContinueOnError)
	fl.SetOutput(stderr)
	target := fl.String("target", "", "the build: "+fmt.Sprint(targets()))
	blank := fl.String("blank", "", "the blank app's build")
	plux := fl.String("plux", "", "the same app's build with plux_flutter")
	baselinePath := fl.String("baseline", "test/size/baseline.json", "the committed overheads, in bytes")
	update := fl.Bool("update", false, "write the measured overhead to -baseline")
	reportOnly := fl.Bool("report", false, "report without failing on the budget or the baseline")
	if err := fl.Parse(args); err != nil || !slices.Contains(targets(), *target) || *blank == "" || *plux == "" || fl.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: sizegate -target <target> -blank <build> -plux <build> [-baseline file] [-update]")
		return exitError
	}
	forABI := abi(*target)
	blankSize, err := measure(*blank, forABI)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	pluxSize, err := measure(*plux, forABI)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	baseline, err := readBaseline(*baselinePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	overhead := pluxSize - blankSize
	if *update {
		baseline[*target] = overhead
		if err := writeBaseline(*baselinePath, baseline); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitError
		}
	}
	code := report(stdout, stderr, *target, blankSize, pluxSize, baseline[*target])
	if err := writeContributions(stdout, *blank, *plux, forABI); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitError
	}
	if *reportOnly {
		return exitOK
	}
	return code
}

// contributionRows is how many of the largest differences are listed.
const contributionRows = 15

// entries lists the files of a build with the bytes each takes in it: an
// archive's entries (an APK) at their stored size, the files an App
// Bundle delivers for abi at their size compressed, a directory's files
// (an iOS app) at their size.
func entries(path, abi string) (map[string]int64, error) {
	out := map[string]int64{}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("sizegate: %w", err)
	}
	if info.IsDir() {
		err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			fi, err := d.Info()
			if err != nil {
				return fmt.Errorf("sizegate: %w", err)
			}
			rel, _ := filepath.Rel(path, p)
			out[filepath.ToSlash(rel)] = fi.Size()
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("sizegate: %w", err)
		}
		return out, nil
	}
	if abi != "" {
		err := delivered(path, abi, func(name string, r io.Reader) error {
			var n counter
			w, err := flate.NewWriter(&n, flate.BestCompression)
			if err != nil {
				return fmt.Errorf("sizegate: %w", err)
			}
			_, err = io.Copy(w, r)
			if err := errors.Join(err, w.Close()); err != nil {
				return fmt.Errorf("sizegate: %s: %w", name, err)
			}
			out[name] = int64(n)
			return nil
		})
		return out, err
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("sizegate: %s: %w", path, err)
	}
	defer func() { _ = z.Close() }()
	for _, f := range z.File {
		out[f.Name] = int64(f.CompressedSize64) //nolint:gosec // An archive entry's size fits int64.
	}
	return out, nil
}

// delivered calls fn, in the bundle's order, with each file of the App
// Bundle at path that Play delivers to a device of abi: the base
// module's, without the native libraries of other ABIs. Language and
// screen density splits are not applied, so every resource counts.
func delivered(path, abi string, fn func(name string, r io.Reader) error) error {
	z, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("sizegate: %s: %w", path, err)
	}
	defer func() { _ = z.Close() }()
	for _, f := range z.File {
		lib, isLib := strings.CutPrefix(f.Name, "base/lib/")
		if !strings.HasPrefix(f.Name, "base/") || strings.HasSuffix(f.Name, "/") || isLib && !strings.HasPrefix(lib, abi+"/") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return fmt.Errorf("sizegate: %s: %w", f.Name, err)
		}
		err = fn(f.Name, r)
		if err := errors.Join(err, r.Close()); err != nil {
			return err
		}
	}
	return nil
}

// writeContributions lists the files whose size differs most between
// the two builds: where the runtime's bytes go.
func writeContributions(w io.Writer, blank, plux, abi string) error {
	a, err := entries(blank, abi)
	if err != nil {
		return err
	}
	b, err := entries(plux, abi)
	if err != nil {
		return err
	}
	type row struct {
		name string
		diff int64
	}
	var rows []row
	for name, n := range b {
		if d := n - a[name]; d != 0 {
			rows = append(rows, row{name, d})
		}
	}
	for name, n := range a {
		if _, ok := b[name]; !ok {
			rows = append(rows, row{name, -n})
		}
	}
	slices.SortFunc(rows, func(x, y row) int {
		if c := cmpAbs(y.diff, x.diff); c != 0 {
			return c
		}
		return strings.Compare(x.name, y.name)
	})
	_, _ = fmt.Fprintf(w, "\n| File | Added bytes |\n|---|---:|\n")
	for _, r := range rows[:min(len(rows), contributionRows)] {
		_, _ = fmt.Fprintf(w, "| `%s` | %d |\n", r.name, r.diff)
	}
	if len(rows) > contributionRows {
		var rest int64
		for _, r := range rows[contributionRows:] {
			rest += r.diff
		}
		_, _ = fmt.Fprintf(w, "| %d other files | %d |\n", len(rows)-contributionRows, rest)
	}
	return nil
}

// cmpAbs compares |x| with |y|.
func cmpAbs(x, y int64) int {
	if x < 0 {
		x = -x
	}
	if y < 0 {
		y = -y
	}
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// report writes the Markdown summary and decides.
func report(stdout, stderr io.Writer, target string, blank, plux, committed int64) int {
	overhead := plux - blank
	_, _ = fmt.Fprintf(stdout, "| Build (%s) | Bytes | MiB |\n|---|---:|---:|\n", target)
	for _, row := range []struct {
		name string
		n    int64
	}{{"blank app", blank}, {"with plux_flutter", plux}, {"added by plux_flutter", overhead}, {"committed overhead", committed}} {
		_, _ = fmt.Fprintf(stdout, "| %s | %d | %.2f |\n", row.name, row.n, float64(row.n)/(1<<20))
	}
	code := exitOK
	if limit := budget(target); overhead > limit {
		_, _ = fmt.Fprintf(stderr, "sizegate: plux_flutter adds %d bytes to the %s, over the %.1f MiB of RT-061\n", overhead, target, float64(limit)/(1<<20))
		code = exitFailed
	}
	switch {
	case committed <= 0:
		_, _ = fmt.Fprintf(stderr, "sizegate: no committed overhead for %s; run with -update and commit the baseline\n", target)
		code = exitFailed
	case float64(overhead) > float64(committed)*(1+growth):
		_, _ = fmt.Fprintf(stderr, "sizegate: plux_flutter adds %d bytes to the %s, more than 10%% over the committed %d (QA-007)\n", overhead, target, committed)
		code = exitFailed
	}
	return code
}

// measure is the size of a file, of what an App Bundle delivers to a
// device of abi (when abi is set), or of a directory archived as an IPA.
func measure(path, abi string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("sizegate: %w", err)
	}
	var n counter
	switch {
	case abi != "":
		err = deliveredArchive(&n, path, abi)
	case info.IsDir():
		err = archive(&n, path)
	default:
		return info.Size(), nil
	}
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

// deliveredArchive writes what the App Bundle at path delivers to a
// device of abi as one ZIP archive, compressed at the highest level.
func deliveredArchive(w io.Writer, path, abi string) error {
	z := newArchive(w)
	err := delivered(path, abi, func(name string, r io.Reader) error {
		return addReader(z, name, r)
	})
	if err := errors.Join(err, z.Close()); err != nil {
		return fmt.Errorf("sizegate: %w", err)
	}
	return nil
}

// newArchive is a ZIP writer that compresses at the highest level.
func newArchive(w io.Writer) *zip.Writer {
	z := zip.NewWriter(w)
	z.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	return z
}

// counter counts the bytes written to it.
type counter int64

func (c *counter) Write(p []byte) (int, error) {
	*c += counter(len(p))
	return len(p), nil
}

// archive writes dir as a ZIP archive with dir under Payload/, its files
// in lexical order, compressed at the highest level.
func archive(w io.Writer, dir string) error {
	z := newArchive(w)
	prefix := "Payload/" + filepath.Base(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		// iOS app bundles hold regular files only; anything else (a
		// symbolic link) is left out rather than followed.
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("sizegate: %w", err)
		}
		return add(z, prefix+"/"+filepath.ToSlash(rel), path)
	})
	if err := errors.Join(err, z.Close()); err != nil {
		return fmt.Errorf("sizegate: %w", err)
	}
	return nil
}

// add writes the file at path into z as name.
func add(z *zip.Writer, name, path string) error {
	src, err := os.Open(path) //nolint:gosec // A file of the app the caller named.
	if err != nil {
		return fmt.Errorf("sizegate: %w", err)
	}
	err = addReader(z, name, src)
	if err := errors.Join(err, src.Close()); err != nil {
		return fmt.Errorf("sizegate: %s: %w", path, err)
	}
	return nil
}

// addReader writes r into z as name.
func addReader(z *zip.Writer, name string, r io.Reader) error {
	f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return fmt.Errorf("sizegate: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("sizegate: %s: %w", name, err)
	}
	return nil
}

func readBaseline(path string) (map[string]int64, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The baseline the caller named.
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sizegate: %w", err)
	}
	b := map[string]int64{}
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("sizegate: %s: %w", path, err)
	}
	return b, nil
}

// writeBaseline writes the baseline with its keys sorted.
func writeBaseline(path string, b map[string]int64) error {
	var out []byte
	out = append(out, "{\n"...)
	for i, k := range slices.Sorted(maps.Keys(b)) {
		sep := ","
		if i == len(b)-1 {
			sep = ""
		}
		out = fmt.Appendf(out, "  %q: %d%s\n", k, b[k], sep)
	}
	out = append(out, "}\n"...)
	if err := os.WriteFile(path, out, 0o644); err != nil { //nolint:gosec // A committed file, readable by all.
		return fmt.Errorf("sizegate: %w", err)
	}
	return nil
}
