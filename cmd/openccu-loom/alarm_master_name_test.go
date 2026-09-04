// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// TestMasterPanelNameResolvesInEveryLocale pins the row the aggregate
// alarm panel's name comes from.
//
// [i18n.Catalogs.T] answers a missing row with the key itself. The MQTT
// publisher has always tested for that echo and fallen back to its English
// literal; the wiring path did not, so a dropped row would have named the
// same panel "discovery.alarm_system" on REST and WS while MQTT called it
// "Alarm system". Both sides treat the echo as absent now.
//
// What this test drives is the one direction that can actually produce
// the echo: the row missing from the DEFAULT locale. [i18n.Catalogs.T]
// falls back to the default before giving up (i18n.go: the locale lookup,
// then the DefaultLocale lookup, then the key), so dropping the German
// row renders the English string and no surface disagrees — measured, by
// removing it and watching this test stay green. Only losing the English
// row reaches the echo.
//
// The non-default locales are still asserted, because a per-locale miss
// is a translation defect even when it is not a naming defect: it makes a
// German operator read an English panel name.
func TestMasterPanelNameResolvesInEveryLocale(t *testing.T) {
	t.Parallel()

	catalogs, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("load catalogues: %v", err)
	}

	for _, locale := range catalogs.Locales() {
		t.Run(locale, func(t *testing.T) {
			t.Parallel()
			name := masterPanelName(catalogs, locale)
			if name == "" || name == "discovery.alarm_system" {
				t.Fatalf("locale %q resolves discovery.alarm_system to %q; REST and WS would fall "+
					"back to the core default while MQTT renders its own literal", locale, name)
			}
			// The fallback makes a per-locale miss invisible above, so the
			// locale's own catalogue file is read as well.
			if !catalogFileHasKey(t, locale, "discovery.alarm_system") {
				t.Errorf("locale %q has no discovery.alarm_system row of its own; "+
					"the panel renders the default locale's name to that operator", locale)
			}
		})
	}
}

// catalogFileHasKey reports whether the locale's own catalogue file
// carries key, bypassing [i18n.Catalogs.T]'s default-locale fallback.
func catalogFileHasKey(t *testing.T, locale, key string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "i18n", "catalogs", locale+".json"))
	if err != nil {
		t.Fatalf("read %s catalogue: %v", locale, err)
	}
	var msgs map[string]string
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("parse %s catalogue: %v", locale, err)
	}
	_, ok := msgs[key]
	return ok
}
