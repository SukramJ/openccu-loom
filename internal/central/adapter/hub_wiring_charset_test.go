// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "testing"

// TestDecodeRegaFieldLatin1 reproduces the real bug: a program's
// condition/activity summary UriEncodes a Latin-1 CCU object name (%FC = "ü"),
// which after url-unescape is a raw Latin-1 byte — invalid UTF-8 that renders
// as U+FFFD ("Sp�le"). decodeRegaField must recover "Spüle".
func TestDecodeRegaFieldLatin1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Wassersensor%20Sp%FCle", "Wassersensor Spüle"}, // Latin-1 ü
		{"L%FCftung%20Aus", "Lüftung Aus"},               // Latin-1 ü
		{"St%F6rung%20HAP", "Störung HAP"},               // Latin-1 ö
		{"K%C3%BCche", "Küche"},                          // already UTF-8 ü — untouched
		{"Plain%20Name", "Plain Name"},                   // ASCII
		{"", ""},
	}
	for _, c := range cases {
		if got := decodeRegaField(c.in); got != c.want {
			t.Errorf("decodeRegaField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
