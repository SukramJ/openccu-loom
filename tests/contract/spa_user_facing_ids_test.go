// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSPAUserFacingTextsCarryNoTrackingIDs extends the doc-purity rule to
// the strings an operator actually reads: the EN and DE catalogue VALUES of
// assets/ui/src/lib/i18n.ts must not name a wave, phase, audit item or
// other internal tracking code. Comments keep theirs — they are how the
// code finds its history — so only string values are checked, not the
// surrounding source. A tracking code that leaks into a label, toast or
// help text reads as gibberish to the operator and outlives the audit that
// minted it.
func TestSPAUserFacingTextsCarryNoTrackingIDs(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "ui", "src", "lib", "i18n.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read i18n.ts: %v", err)
	}

	// Extract broadly: every `"key": "value"` / `'value'` property pair in
	// the file, whatever block it is in. Narrowing to known-good shapes
	// would let a malformed entry hide from the check.
	pairRe := regexp.MustCompile(`"((?:[^"\\]|\\.)+)"\s*:\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')`)

	banned := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"Wave-N numbering", regexp.MustCompile(`\bWave[- ]\d+\b`)},
		{"wave<N> tag", regexp.MustCompile(`\bwave\d+\b`)},
		{"W<N>-<X> wave tag", regexp.MustCompile(`\bW\d+-[A-Z]\d*\b`)},
		{"Welle N numbering", regexp.MustCompile(`\bWelle\s+\d+\b`)},
		{"audit item A<N>-L<N>", regexp.MustCompile(`\bA\d+-L\d+\b`)},
		{"M-code M<4digits>", regexp.MustCompile(`\bM\d{4}\b`)},
		{"QW-<N> item", regexp.MustCompile(`\bQW-\d+\b`)},
		{"V<N>-N<N> item", regexp.MustCompile(`\bV\d+-N\d+\b`)},
		{"Phase-N tag", regexp.MustCompile(`\bPhase-\d+\b`)},
		{"parity audit reference", regexp.MustCompile(`\bparity_(?:audit|request)\.md\b`)},
	}

	pairs := pairRe.FindAllStringSubmatch(string(raw), -1)
	// A catalogue rewrite that this extractor no longer parses would turn
	// the check silently green; the SPA has thousands of entries, so a
	// low count means the extractor broke, not the catalogue.
	if len(pairs) < 1000 {
		t.Fatalf("i18n.ts extractor found only %d key/value pairs — the catalogue shape changed, fix the extractor", len(pairs))
	}

	var hits []string
	for _, m := range pairs {
		key, value := m[1], m[2]
		if value == "" {
			value = m[3]
		}
		for _, b := range banned {
			if loc := b.re.FindString(value); loc != "" {
				hits = append(hits, fmt.Sprintf("%s: %q contains %q (%s)", key, value, loc, b.name))
			}
		}
	}

	if len(hits) > 0 {
		t.Fatalf("%d i18n catalogue values carry internal tracking codes "+
			"(user-facing strings must not name waves, phases or audit items):\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}
