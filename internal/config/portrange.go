// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidPortRange is returned by [ParsePortRange] when the input
// string is syntactically or semantically invalid.
var ErrInvalidPortRange = errors.New("config: invalid port_range")

// ParsePortRange parses a "lo-hi" port-range string (e.g. "30000-30099").
//
// Rules:
//   - Empty input returns (0, 0, nil) — "no range configured".
//   - Both lo and hi must be in [1, 65535].
//   - lo must be ≤ hi.
//   - lo == hi is valid (degenerate single-port range).
//
// Returns [ErrInvalidPortRange] (wrapped) on bad syntax.
func ParsePortRange(s string) (lo, hi int, err error) {
	if s == "" {
		return 0, 0, nil
	}
	idx := strings.Index(s, "-")
	if idx <= 0 {
		return 0, 0, fmt.Errorf("%w: %q — expected \"lo-hi\" format", ErrInvalidPortRange, s)
	}
	loStr := s[:idx]
	hiStr := s[idx+1:]

	lo, err = parsePort(loStr)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q — lo: %w", ErrInvalidPortRange, s, err)
	}
	hi, err = parsePort(hiStr)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q — hi: %w", ErrInvalidPortRange, s, err)
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("%w: %q — lo (%d) > hi (%d)", ErrInvalidPortRange, s, lo, hi)
	}
	return lo, hi, nil
}

// parsePort converts a decimal string to a port number in [1, 65535].
func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("out of range [1, 65535]: %d", n)
	}
	return n, nil
}
