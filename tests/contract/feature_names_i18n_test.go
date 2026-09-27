// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFeatureKeysHaveNamesInBothLocales pins the SPA's wording of the
// per-central features: every key the daemon can report absent needs
// feature.name.<key> in the EN and DE catalogues of
// assets/ui/src/lib/i18n.ts. Without it an operator reads the raw key where
// a hidden action names what is missing.
func TestFeatureKeysHaveNamesInBothLocales(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "ui", "src", "lib", "i18n.ts"))
	if err != nil {
		t.Fatalf("read i18n.ts: %v", err)
	}
	src := string(raw)
	deStart := strings.Index(src, "const DE")
	if deStart < 0 {
		t.Fatal("i18n.ts: 'const DE' catalogue marker not found")
	}
	keyRe := regexp.MustCompile(`"feature\.name\.([^"]+)"\s*:`)
	collect := func(block string) map[string]bool {
		out := map[string]bool{}
		for _, m := range keyRe.FindAllStringSubmatch(block, -1) {
			out[m[1]] = true
		}
		return out
	}
	en, de := collect(src[:deStart]), collect(src[deStart:])
	var missing []string
	for _, f := range hmenum.AllFeatures() {
		if !en[string(f)] {
			missing = append(missing, "EN feature.name."+string(f))
		}
		if !de[string(f)] {
			missing = append(missing, "DE feature.name."+string(f))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d feature names missing in assets/ui/src/lib/i18n.ts:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}
