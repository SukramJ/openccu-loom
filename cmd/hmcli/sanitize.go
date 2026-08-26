// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"fmt"
	"strings"
)

// sanitizeForTerminal strips control characters and ANSI escape sequences from
// a server-derived string before it is printed to a human-readable (TTY) output
// path. A malicious or buggy daemon could otherwise smuggle ANSI CSI sequences
// (colour codes, cursor moves, screen clears) or raw C0/C1 control bytes into a
// device name, sysvar value, or event payload and rewrite the operator's
// terminal. The JSON output paths already escape these bytes, so this is only
// applied to the human-readable formatting.
//
// The transformation:
//   - drops complete ANSI escape sequences (CSI "ESC [ … final", OSC
//     "ESC ] … BEL/ST", and generic two-byte "ESC x" escapes),
//   - drops C0 control runes (U+0000–U+001F, including TAB/CR/LF so they cannot
//     forge extra tabwriter columns or new lines), DEL (U+007F), and C1 control
//     runes (U+0080–U+009F),
//   - passes every other rune through unchanged.
func sanitizeForTerminal(s string) string {
	if s == "" {
		return s
	}
	// Fast path: nothing to strip.
	if !strings.ContainsFunc(s, isUnsafeTerminalRune) {
		return s
	}

	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b { // ESC — start of an escape sequence; skip the whole thing.
			i = skipEscapeSequence(runes, i)
			continue
		}
		if isControlRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isUnsafeTerminalRune reports whether r needs stripping (a control rune or the
// ESC that introduces an escape sequence). Used only for the fast-path check.
func isUnsafeTerminalRune(r rune) bool {
	return r == 0x1b || isControlRune(r)
}

// isControlRune reports whether r is a C0 control (incl. TAB/CR/LF and ESC),
// DEL, or a C1 control. ESC is a control rune but is handled specially by the
// caller so the trailing escape-sequence bytes are consumed too.
func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// skipEscapeSequence consumes an ANSI escape sequence beginning at runes[start]
// (which must be ESC) and returns the index of its last rune, so the caller's
// loop increment lands on the first rune after the sequence.
//
// Handles the three shapes that reach a terminal:
//   - CSI:  ESC '[' (params 0x30–0x3F)* (intermediates 0x20–0x2F)* final 0x40–0x7E
//   - OSC:  ESC ']' … terminated by BEL (0x07) or ST (ESC '\')
//   - anything else: only the ESC itself is consumed. Dropping the ESC already
//     neutralises the escape (e.g. "ESC c" reset becomes the harmless letter
//     "c"), so the following rune is kept rather than risk eating legitimate
//     content after a stray ESC.
func skipEscapeSequence(runes []rune, start int) int {
	if start+1 >= len(runes) {
		return start // lone trailing ESC
	}
	switch runes[start+1] {
	case '[': // CSI
		i := start + 2
		for i < len(runes) && runes[i] >= 0x30 && runes[i] <= 0x3f { // parameter bytes
			i++
		}
		for i < len(runes) && runes[i] >= 0x20 && runes[i] <= 0x2f { // intermediate bytes
			i++
		}
		if i < len(runes) && runes[i] >= 0x40 && runes[i] <= 0x7e { // final byte
			return i
		}
		return i - 1
	case ']': // OSC
		i := start + 2
		for i < len(runes) {
			if runes[i] == 0x07 { // BEL terminator
				return i
			}
			if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '\\' { // ST terminator
				return i + 1
			}
			i++
		}
		return len(runes) - 1
	default: // stray/other escape: consume only the ESC, keep the next rune
		return start
	}
}

// sanitizeValue renders an arbitrary decoded JSON value for a human-readable
// output cell and sanitizes the result. Numbers and bools pass through
// unchanged; server-controlled strings are stripped of control/ANSI bytes.
func sanitizeValue(v any) string {
	return sanitizeForTerminal(fmt.Sprintf("%v", v))
}
