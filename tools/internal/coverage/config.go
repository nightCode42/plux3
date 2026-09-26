// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package coverage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Rule is the coverage floor for one toolchain.
type Rule struct {
	// Floor is the minimum total coverage, in percent.
	Floor float64 `json:"floor"`
	// Packages maps repository-relative directory prefixes to stricter floors.
	// A prefix that matches no measured file is reported as not present yet.
	Packages map[string]float64 `json:"packages"`
}

// Config holds one rule per toolchain: "go", "dart" and "studio".
type Config map[string]Rule

// LoadConfig reads a coverage configuration file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("coverage.LoadConfig: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("coverage.LoadConfig %s: %w", path, err)
	}
	for kind, rule := range cfg {
		if err := rule.validate(); err != nil {
			return nil, fmt.Errorf("coverage.LoadConfig %s: %s: %w", path, kind, err)
		}
	}
	return cfg, nil
}

// validate checks that every floor is a percentage.
func (r Rule) validate() error {
	if r.Floor <= 0 || r.Floor > 100 {
		return fmt.Errorf("floor %v is not in (0, 100]", r.Floor)
	}
	for prefix, floor := range r.Packages {
		if floor <= 0 || floor > 100 {
			return fmt.Errorf("floor %v for %s is not in (0, 100]", floor, prefix)
		}
	}
	return nil
}
