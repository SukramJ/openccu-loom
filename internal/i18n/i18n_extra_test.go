// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package i18n

import (
	"sync"
	"testing"
)

// TestNewCatalogsMultipleLocales verifies all expected locales are present
// and accessible.
func TestNewCatalogsMultipleLocales(t *testing.T) {
	c, err := NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	locales := c.Locales()
	found := make(map[string]bool)
	for _, l := range locales {
		found[l] = true
	}
	for _, want := range []string{"en", "de"} {
		if !found[want] {
			t.Errorf("locale %q not loaded", want)
		}
	}
}

// TestPreloadLocale_DefaultLocale exercises the empty-string path, which
// should normalise to DefaultLocale ("en").
func TestPreloadLocale_DefaultLocale(t *testing.T) {
	c, err := NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// Passing empty string should not panic and should not corrupt the store.
	c.PreloadLocale("")
	if got := c.T("en", "nav.devices"); got == "nav.devices" {
		t.Errorf("T(en, nav.devices) fell through to key after PreloadLocale(\"\"); got %q", got)
	}
}

// TestPreloadLocale_ConcurrentSafe fires many goroutines preloading the same
// locale to exercise the write-lock-after-read-lock branch without data races.
func TestPreloadLocale_ConcurrentSafe(t *testing.T) {
	c, err := NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// Reset so the locale is not already loaded, forcing the write path.
	c.ResetForTesting()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			c.PreloadLocale("en")
		})
	}
	wg.Wait()
	// After concurrent preloads the locale must be accessible.
	if got := c.T("en", "nav.devices"); got == "nav.devices" {
		t.Errorf("locale key not translated after concurrent preload: %q", got)
	}
}

// TestTFallsBackToKeyWhenLocaleAbsent exercises the branch where locale is
// the DefaultLocale and the key is missing (final fallthrough to key).
func TestTFallsBackToKeyWhenDefaultMisses(t *testing.T) {
	c, err := NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// "en" is the default locale; a missing key in en should return the key.
	got := c.T("en", "completely.absent.key")
	if got != "completely.absent.key" {
		t.Errorf("T(en, absent) = %q, want key as-is", got)
	}
}
