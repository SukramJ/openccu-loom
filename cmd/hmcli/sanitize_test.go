// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"strings"
	"testing"
)

func TestSanitizeForTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain passes through", "Living Room Lamp", "Living Room Lamp"},
		{"unicode passes through", "Küche Groß", "Küche Groß"},
		{"empty", "", ""},
		{"strips CSI colour codes", "\x1b[31mRED\x1b[0m", "RED"},
		{"strips cursor move CSI", "a\x1b[2Jb", "ab"},
		{"strips CSI with params", "x\x1b[1;32;40my", "xy"},
		{"strips bare ESC keeps next", "a\x1bb", "ab"},
		{"neutralises two-byte escape, keeps letters", "a\x1bcb", "acb"},
		{"strips OSC BEL terminated", "a\x1b]0;title\x07b", "ab"},
		{"strips OSC ST terminated", "a\x1b]0;title\x1b\\b", "ab"},
		{"strips C0 controls incl tab and newline", "a\tb\nc\rd", "abcd"},
		{"strips DEL", "a\x7fb", "ab"},
		{"strips C1 controls", "a\u0085b\u009fc", "abc"},
		{"lone trailing ESC", "abc\x1b", "abc"},
		{"multiple sequences", "\x1b[31m\x1b[1mBOLD RED\x1b[0m done", "BOLD RED done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeForTerminal(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeForTerminal(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The result must never contain an ESC or any other control rune.
			if strings.ContainsFunc(got, isControlRune) {
				t.Errorf("sanitizeForTerminal(%q) = %q still contains a control rune", tc.in, got)
			}
		})
	}
}

func TestSanitizeValue(t *testing.T) {
	t.Parallel()
	if got := sanitizeValue(42); got != "42" {
		t.Errorf("sanitizeValue(42) = %q, want 42", got)
	}
	if got := sanitizeValue(true); got != "true" {
		t.Errorf("sanitizeValue(true) = %q, want true", got)
	}
	if got := sanitizeValue("\x1b[31mevil\x1b[0m"); got != "evil" {
		t.Errorf("sanitizeValue(ansi string) = %q, want evil", got)
	}
	if got := sanitizeValue(3.14); got != "3.14" {
		t.Errorf("sanitizeValue(3.14) = %q, want 3.14", got)
	}
}
