// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package decimal

// RoundingMode selects how Round and Div discard digits (PXL-005).
type RoundingMode uint8

// Rounding modes. The names are those PXL expressions use.
const (
	// HalfEven rounds to the nearest neighbour, ties to the even one
	// (banker's rounding). It is PXL's default.
	HalfEven RoundingMode = iota + 1
	// HalfUp rounds to the nearest neighbour, ties away from zero.
	HalfUp
	// Down rounds toward zero (truncation).
	Down
	// Up rounds away from zero.
	Up
	// Ceiling rounds toward positive infinity.
	Ceiling
	// Floor rounds toward negative infinity.
	Floor
)

// modeNames are the PXL names of the modes, indexed by mode.
var modeNames = [...]string{HalfEven: "halfEven", HalfUp: "halfUp", Down: "down", Up: "up", Ceiling: "ceiling", Floor: "floor"}

// String returns the PXL name of the mode.
func (m RoundingMode) String() string {
	if m < HalfEven || m > Floor {
		return "invalid"
	}
	return modeNames[m]
}

// ParseRoundingMode returns the mode with the given PXL name.
func ParseRoundingMode(name string) (RoundingMode, bool) {
	for m := HalfEven; m <= Floor; m++ {
		if modeNames[m] == name {
			return m, true
		}
	}
	return 0, false
}
