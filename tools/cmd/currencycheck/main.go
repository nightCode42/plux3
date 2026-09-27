// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command currencycheck compares schema/pxl/currencies.json with an ISO
// 4217 list one XML file downloaded from SIX (make currencies-check).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/nightCode42/plux3/tools/internal/iso4217"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run returns 0 when the files agree, 1 when they differ, 2 on usage or
// I/O errors.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: currencycheck <list-one.xml> <currencies.json>")
		return 2
	}
	list, err := readWith(args[0], iso4217.ReadList)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	ours, err := readWith(args[1], iso4217.ReadCurrencies)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	diff := iso4217.Diff(list, ours)
	for _, d := range diff {
		_, _ = fmt.Fprintln(stdout, d)
	}
	if len(diff) > 0 {
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "✓ currencies.json matches ISO 4217 list one (%d currencies)\n", len(list))
	return 0
}

func readWith(path string, read func(io.Reader) (map[string]int, error)) (map[string]int, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a path given by the developer.
	if err != nil {
		return nil, fmt.Errorf("currencycheck: %w", err)
	}
	defer f.Close()
	return read(f)
}
