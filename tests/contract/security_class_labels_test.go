// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSPASecurityClassLabelsMatchDaemonCatalogues pins the Config UI's
// security class names to the daemon's.
//
// A security class is named in three places, not two. The daemon
// catalogues (internal/i18n/catalogs/{de,en}.json, key
// security.entity.class.<class>) feed both north-bound paths — MQTT
// discovery resolves them at publish time, the client reads them over
// GET /i18n/entities — while the SPA carries its own catalogue with its
// own keys (security.class.<class> in assets/ui/src/lib/i18n.ts). A
// rename that stops at the daemon therefore leaves the third surface
// behind, silently and in both locales at once.
//
// That is not hypothetical: the class names were changed from nouns to
// a verb pattern precisely because a noun reads as a verdict — an
// entity called "Einbruch" claims a burglary where the class only
// reports that some enrolled sensor is active, which an open window on
// a disarmed system is enough to do. The daemon was renamed; the SPA
// kept saying "Einbruch" over the same data, which is the wording the
// rename existed to remove.
//
// Word-identical is the contract, per locale. Operators move between
// the Config UI and Home Assistant looking at one installation, and two
// names for one class read as two things.
func TestSPASecurityClassLabelsMatchDaemonCatalogues(t *testing.T) {
	spa := loadSPASecurityClassLabels(t)

	var problems []string
	for _, locale := range []string{"en", "de"} {
		daemon := loadDaemonCatalogue(t, locale)
		for _, class := range hmenum.SecurityClasses() {
			daemonKey := "security.entity.class." + string(class)
			spaKey := "security.class." + string(class)

			want, ok := daemon[daemonKey]
			if !ok {
				problems = append(problems, strings.ToUpper(locale)+" "+daemonKey+
					": missing in internal/i18n/catalogs/"+locale+".json")
				continue
			}
			got, ok := spa[locale][spaKey]
			if !ok {
				problems = append(problems, strings.ToUpper(locale)+" "+spaKey+
					": missing in assets/ui/src/lib/i18n.ts (daemon says "+want+")")
				continue
			}
			if got != want {
				problems = append(problems, strings.ToUpper(locale)+" "+spaKey+
					" = "+got+", want "+want+" (from "+daemonKey+")")
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d security class label(s) drifted between the daemon catalogues and the SPA.\n"+
			"Rename in internal/i18n/catalogs/{en,de}.json AND assets/ui/src/lib/i18n.ts together —\n"+
			"the SPA is a third surface with its own keys, not a consumer of the daemon catalogue:\n%s",
			len(problems), strings.Join(problems, "\n"))
	}
}

// loadDaemonCatalogue reads one locale catalogue exactly as authored.
// The file is read rather than resolved through i18n.Catalogs on
// purpose: the lookup path merges the default locale in as a fallback,
// which would let a missing DE entry pass wearing the EN value.
func loadDaemonCatalogue(t *testing.T, locale string) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "i18n", "catalogs", locale+".json")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative test fixture path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// loadSPASecurityClassLabels parses the EN and DE catalogue blocks of
// i18n.ts and returns the security.class.* entries of each, keyed by
// locale. The file is TypeScript, so it is split at the `const DE`
// marker and scanned — the same approach the config-field label guard
// uses.
func loadSPASecurityClassLabels(t *testing.T) map[string]map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "assets", "ui", "src", "lib", "i18n.ts")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative test fixture path
	if err != nil {
		t.Fatalf("read i18n.ts: %v", err)
	}
	src := string(raw)
	deStart := strings.Index(src, "const DE")
	if deStart < 0 {
		t.Fatal("i18n.ts: 'const DE' catalogue marker not found")
	}
	entryRe := regexp.MustCompile(`"(security\.class\.[^"]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	collect := func(block string) map[string]string {
		out := map[string]string{}
		for _, m := range entryRe.FindAllStringSubmatch(block, -1) {
			var value string
			if err := json.Unmarshal([]byte(`"`+m[2]+`"`), &value); err != nil {
				t.Fatalf("decode i18n.ts value for %s: %v", m[1], err)
			}
			out[m[1]] = value
		}
		return out
	}
	return map[string]map[string]string{
		"en": collect(src[:deStart]),
		"de": collect(src[deStart:]),
	}
}
