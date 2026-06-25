// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestConfigFieldsHaveLabelsAndHelp enforces the SPA contract: every config
// field the section editor can render (one per cfg-tagged leaf, the same list
// ClassifyFields feeds GET /api/v1/config/schema) MUST carry an explicit label
// AND an inline-help description in BOTH locales — i.e. config.field.<path> and
// config.help.<path> in the EN and DE catalogues of assets/ui/src/lib/i18n.ts.
//
// Without the label key the editor falls back to a machine-humanised, untranslated
// string; without the help key the hint row is dropped silently. Either is the
// "field without a description / translation" regression operators have reported.
//
// Paths that are genuinely not rendered as a single scalar field (slice/map roots
// handled by a dedicated editor) are listed in nonRenderedConfigPaths with a reason.
func TestConfigFieldsHaveLabelsAndHelp(t *testing.T) {
	enKeys, deKeys := loadConfigI18nKeys(t)

	var missing []string
	for _, d := range config.ClassifyFields(&config.Config{}) {
		if reason, skip := nonRenderedConfigPaths[d.Path]; skip {
			_ = reason
			continue
		}
		for _, kind := range []string{"field", "help"} {
			key := "config." + kind + "." + d.Path
			if !enKeys[key] {
				missing = append(missing, "EN "+key+"  ("+d.GoType+")")
			}
			if !deKeys[key] {
				missing = append(missing, "DE "+key+"  ("+d.GoType+")")
			}
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d config field i18n entries missing in assets/ui/src/lib/i18n.ts "+
			"(every config field needs config.field.<path> AND config.help.<path> in EN and DE):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

// nonRenderedConfigPaths lists cfg-tagged paths that the section editor does NOT
// surface as a standalone scalar field (so they need no per-field label/help).
// Keep this list tiny and justified; prefer adding a label/help over an exclusion.
var nonRenderedConfigPaths = map[string]string{}

// loadConfigI18nKeys parses the EN and DE catalogue blocks of i18n.ts and returns
// the set of config.field.* / config.help.* keys present in each.
func loadConfigI18nKeys(t *testing.T) (en, de map[string]bool) {
	t.Helper()
	path := filepath.Join("..", "..", "assets", "ui", "src", "lib", "i18n.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read i18n.ts: %v", err)
	}
	src := string(raw)
	deStart := strings.Index(src, "const DE")
	if deStart < 0 {
		t.Fatal("i18n.ts: 'const DE' catalogue marker not found")
	}
	keyRe := regexp.MustCompile(`"(config\.(?:field|help)\.[^"]+)"\s*:`)
	collect := func(block string) map[string]bool {
		out := make(map[string]bool)
		for _, m := range keyRe.FindAllStringSubmatch(block, -1) {
			out[m[1]] = true
		}
		return out
	}
	return collect(src[:deStart]), collect(src[deStart:])
}
