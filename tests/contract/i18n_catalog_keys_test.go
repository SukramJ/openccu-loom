// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

	// Read the actual embedded catalogue files rather than probing a
	// hand-maintained key list: a real key that lands in one locale's
	// JSON and never gets translated in the other must be visible here,
	// whether or not anyone remembered to add it to a separate table.
	catalogDir := i18nCatalogDir(t)
	keySets := make(map[string]map[string]struct{}, len(locales))
	for _, loc := range locales {
		keySets[loc] = readCatalogKeys(t, filepath.Join(catalogDir, loc+".json"))
	}

	refLocale := locales[0]
	refKeys := keySets[refLocale]

	for _, loc := range locales[1:] {
		got := keySets[loc]
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

// i18nCatalogDir resolves the embedded catalogue directory
// (internal/i18n/catalogs, the same directory [i18n.NewCatalogs] embeds
// via go:embed) relative to this test file's own location.
func i18nCatalogDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "internal", "i18n", "catalogs")
}

// readCatalogKeys parses one production catalogue JSON file and returns
// its full key set.
func readCatalogKeys(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var msgs map[string]string
	if err := json.Unmarshal(buf, &msgs); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	out := make(map[string]struct{}, len(msgs))
	for k := range msgs {
		out[k] = struct{}{}
	}
	return out
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
