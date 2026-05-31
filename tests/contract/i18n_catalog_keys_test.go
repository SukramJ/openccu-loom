// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

// TestI18nCatalogParity enforces that every locale carries the same key
// set — missing keys are treated as a release blocker.
func TestI18nCatalogParity(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	locales := cats.Locales()
	sort.Strings(locales)
	if len(locales) < 2 {
		t.Fatalf("need at least two locales, got %v", locales)
	}

	// Use the first locale as the reference key set.
	refLocale := locales[0]
	refKeys := keysForLocale(cats, refLocale)

	for _, loc := range locales[1:] {
		got := keysForLocale(cats, loc)
		if missing := diff(refKeys, got); len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("locale %q missing keys vs %q: %s",
				loc, refLocale, strings.Join(missing, ", "))
		}
		if extra := diff(got, refKeys); len(extra) > 0 {
			sort.Strings(extra)
			t.Errorf("locale %q has keys absent in %q: %s",
				loc, refLocale, strings.Join(extra, ", "))
		}
	}
}

// keysForLocale extracts the key set of one locale by probing every
// reference key. Catalogs does not expose its internal map; we use
// T() with a sentinel and treat key == value as "missing".
//
// The helper is only valid when the reference locale is guaranteed
// to carry every documented key.
func keysForLocale(cats *i18n.Catalogs, loc string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, k := range documentedKeys() {
		if cats.T(loc, k) != k {
			out[k] = struct{}{}
		}
	}
	return out
}

// documentedKeys is the union of every key we know about. Updating
// this list is the opt-in signal that a new translation is
// expected. Keeping it explicit is deliberate — it.
func documentedKeys() []string {
	return []string{
		"nav.dashboard", "nav.devices", "nav.programs", "nav.sysvars",
		"nav.health", "nav.config", "nav.incidents", "nav.users",
		"nav.tokens", "nav.settings", "nav.about", "nav.login", "nav.logout",
		"dashboard.title", "dashboard.devices", "dashboard.programs",
		"dashboard.sysvars", "dashboard.health", "dashboard.active_alarms",
		"devices.title", "devices.address", "devices.name", "devices.model",
		"devices.interface", "devices.available", "devices.yes", "devices.no",
		"channel.title", "channel.parameter", "channel.value",
		"channel.observed", "channel.modified",
		"programs.title", "programs.run",
		"sysvars.title",
		"health.title", "health.overall", "health.component", "health.status",
		"config.title", "config.locale", "config.centrals",
		"login.title", "login.username", "login.password",
		"login.submit", "login.invalid", "login.oidc",
		"setup.title", "setup.intro", "setup.username",
		"setup.password", "setup.confirm", "setup.submit",
	}
}

func diff(a, b map[string]struct{}) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}
