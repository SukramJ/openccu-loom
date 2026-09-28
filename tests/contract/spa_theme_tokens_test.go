// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The SPA's theme contract (assets/ui/CLAUDE.md): every view renders in all
// four skin × mode combinations, driven by the --ha-* tokens of app.css.
// Three structural failures survive review because nothing renders wrong in
// the combination the author happened to look at:
//
//  1. html.dark defines a token :root never sets — light mode inherits
//     nothing and the element flashes unstyled before the pre-paint script.
//  2. A component consumes var(--ha-…) with a typo'd name and no fallback —
//     the property resolves to nothing in every combination, silently.
//  3. A raw hex colour in a component styles exactly one combination.
//
// This test pins all three against app.css and the component sources.
// Hex colours that are var() fallbacks are fine (they are the standalone
// default under the HA skin), and files whose colours are CONTENT — a
// colour picker, a physical colour-temperature gradient, chart series —
// are listed in contentColourFiles with the reason.

var themeBlockRes = map[string]*regexp.Regexp{
	":root":                regexp.MustCompile(`(?s)(?:^|\n):root\s*\{([^}]*)\}`),
	`html.dark`:            regexp.MustCompile(`(?s)\nhtml\.dark\s*\{([^}]*)\}`),
	`html[data-skin="ha"]`: regexp.MustCompile(`(?s)\nhtml\[data-skin="ha"\]\s*\{([^}]*)\}`),
	`html[data-skin].dark`: regexp.MustCompile(`(?s)\nhtml\[data-skin="ha"\]\.dark\s*\{([^}]*)\}`),
}

func themeTokensIn(block string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range regexp.MustCompile(`(--ha-[\w-]+)\s*:`).FindAllStringSubmatch(block, -1) {
		out[m[1]] = true
	}
	return out
}

func loadThemeBlocks(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "ui", "src", "app.css"))
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	blocks := make(map[string]map[string]bool)
	for name, re := range themeBlockRes {
		var all strings.Builder
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			all.WriteString(m[1])
			all.WriteString("\n")
		}
		blocks[name] = themeTokensIn(all.String())
	}
	if len(blocks[":root"]) < 10 {
		t.Fatalf("app.css extractor found only %d :root tokens — the block shape changed, fix the extractor", len(blocks[":root"]))
	}
	return blocks
}

// TestSPAThemeTokenSetsAreConsistent: rules 1 (dark ⊆ light) for both the
// loom and the HA skin.
func TestSPAThemeTokenSetsAreConsistent(t *testing.T) {
	blocks := loadThemeBlocks(t)
	rootSet, haSet := blocks[":root"], blocks[`html[data-skin="ha"]`]

	var bad []string
	for tok := range blocks["html.dark"] {
		if !rootSet[tok] {
			bad = append(bad, tok+" is set for html.dark but not on :root — light mode inherits nothing")
		}
	}
	for tok := range blocks[`html[data-skin].dark`] {
		if !rootSet[tok] && !haSet[tok] {
			bad = append(bad, tok+` is set for html[data-skin="ha"].dark but neither on :root nor on html[data-skin="ha"]`)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("theme token sets are inconsistent in assets/ui/src/app.css:\n%s", strings.Join(bad, "\n"))
	}
}

// spaSourceFiles walks assets/ui/src for the given extensions.
func spaSourceFiles(t *testing.T, exts ...string) []string {
	t.Helper()
	root := filepath.Join("..", "..", "assets", "ui", "src")
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) && !strings.HasSuffix(path, ".test.ts") {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(files) < 50 {
		t.Fatalf("found only %d SPA sources under %s — the tree moved, fix the walker", len(files), root)
	}
	return files
}

// TestSPAConsumedThemeTokensAreDefined: rule 2 — every var(--ha-…) used
// WITHOUT a fallback anywhere in the SPA sources must be defined on :root.
// A consumption WITH a fallback is deliberate (host-provided HA variables).
func TestSPAConsumedThemeTokensAreDefined(t *testing.T) {
	rootSet := loadThemeBlocks(t)[":root"]
	noFallbackRe := regexp.MustCompile(`var\((--ha-[\w-]+)\s*\)`)

	var bad []string
	for _, path := range spaSourceFiles(t, ".svelte", ".ts", ".css") {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range noFallbackRe.FindAllStringSubmatch(string(raw), -1) {
			if !rootSet[m[1]] {
				rel, _ := filepath.Rel(filepath.Join("..", ".."), path)
				bad = append(bad, fmt.Sprintf("%s: var(%s) has no fallback and %s is not defined on :root", rel, m[1], m[1]))
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		bad = dedupeStrings(bad)
		t.Fatalf("%d consumed theme tokens are undefined (typo, or add the token to app.css):\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// contentColourFiles are components whose colours ARE the content and must
// not follow the theme. Keep every entry justified.
var contentColourFiles = map[string]string{
	"lib/components/MultiSeriesChart.svelte":                      "categorical series palette, deliberately fixed hues that read on light and dark",
	"lib/components/schedule/ClimateScheduleVisualization.svelte": "HA climate temperature colour scale, ported 1:1 from schedule-core",
	"lib/control/controls/ControlColorPalette.svelte":             "colour-picker preset swatches — the colours are the values being picked",
	"lib/control/controls/ControlColorTempSlider.svelte":          "physical colour-temperature gradient — the colours are the scale itself",
}

// TestSPAComponentsCarryNoRawThemeColours: rule 3 — a hex colour in a
// .svelte file must be a var() fallback or live in a content-colour file.
func TestSPAComponentsCarryNoRawThemeColours(t *testing.T) {
	hexRe := regexp.MustCompile(`#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b`)
	varExprRe := regexp.MustCompile(`var\([^)]*\)`)

	var bad []string
	for _, path := range spaSourceFiles(t, ".svelte") {
		rel, _ := filepath.Rel(filepath.Join("..", "..", "assets", "ui", "src"), path)
		if reason, ok := contentColourFiles[filepath.ToSlash(rel)]; ok {
			_ = reason
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			// A hex inside var(--…, #hex) is the standalone fallback — fine.
			stripped := varExprRe.ReplaceAllString(line, "")
			if m := hexRe.FindString(stripped); m != "" {
				bad = append(bad, fmt.Sprintf("%s:%d: raw colour %s (use a --ha-* token or a dark: variant; content colours go on the contentColourFiles list)", filepath.ToSlash(rel), i+1, m))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d raw colours in SPA components:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

func dedupeStrings(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}
