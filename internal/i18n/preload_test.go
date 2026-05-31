// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package i18n_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
)

func TestPreloadLocale_KnownLocale(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// Preloading an already-loaded locale must be idempotent.
	cats.PreloadLocale("en")
	cats.PreloadLocale("en")
	// T must still work after preload.
	_ = cats.T("en", "some.key")
}

func TestPreloadLocale_UnknownLocale(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// Preloading a missing locale must not panic or return an error.
	cats.PreloadLocale("zz")
	// Lookup should fall back to default.
	got := cats.T("zz", "missing.key")
	if got != "missing.key" {
		t.Errorf("T(zz, missing.key) = %q, want key as-is", got)
	}
}

func TestSchedulePreloadLocale_DoesNotPanic(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// Must not panic; goroutine runs in background.
	cats.SchedulePreloadLocale("en")
}

func TestResetForTesting_ClearsState(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("NewCatalogs: %v", err)
	}
	// After reset, Locales should be empty.
	cats.ResetForTesting()
	if got := cats.Locales(); len(got) != 0 {
		t.Errorf("after ResetForTesting, Locales() = %v, want empty", got)
	}
	if cats.DefaultLocale != "en" {
		t.Errorf("after ResetForTesting, DefaultLocale = %q, want en", cats.DefaultLocale)
	}
}
