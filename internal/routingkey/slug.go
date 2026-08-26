// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package routingkey

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// slugReplacer handles the few letters that carry meaning in a slug but
// have no Unicode canonical decomposition, so the combining-mark strip
// in [HubSlug] would otherwise reduce them to a separator. "ß" must
// become "ss" (so "Außen" → "aussen", not "au-en").
var slugReplacer = strings.NewReplacer(
	"ß", "ss",
	"ẞ", "ss",
)

// newMarkStripper builds a transformer that decomposes accented letters
// (NFKD) and drops the resulting combining marks, turning "ü" → "u",
// "ö" → "o", "ä" → "a" — matching the Unicode transliteration the
// contract specifies, not a German "ü" → "ue" expansion.
//
// A fresh transformer is built per call: transform.Transformer carries
// internal state and is not safe for concurrent use, so a shared
// package-level instance would corrupt under parallel callers.
func newMarkStripper() transform.Transformer {
	return transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
}

// HubSlug slugifies a hub data-point name (system variable, program,
// connectivity, metric) before it becomes the parameter segment of the
// key. The rule is Unicode transliteration + lowercase + collapse every
// run of non-alphanumeric characters to a single "-" + trim.
//
// A naive replace-based cleaner diverges on any non-ASCII name and
// produces a different key, which loses the Home Assistant entity on
// cutover; see docs/external-clients/ha-drop-in-identity-and-scoping.md.
func HubSlug(name string) string {
	s := slugReplacer.Replace(name)
	if t, _, err := transform.String(newMarkStripper(), s); err == nil {
		s = t
	}
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	prevDash := true // start true so a leading separator emits nothing
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
