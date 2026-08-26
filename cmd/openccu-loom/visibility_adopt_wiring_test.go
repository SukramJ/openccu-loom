// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestVisibilityUnIgnoreReachesACentralAdoptedAfterBoot pins the un_ignore
// union against a fleet that changes at runtime.
//
// The union used to be computed once, over the centrals registered at that
// instant. A CCU adopted through the SPA afterwards was in no union, so every
// parameter the operator had explicitly un-ignored on it stayed suppressed on
// REST, MQTT and the SPA until the next restart — with nothing reporting it.
func TestVisibilityUnIgnoreReachesACentralAdoptedAfterBoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()

	// Boot: no centrals at all — the first-run onboarding case.
	remove := wireVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)
	t.Cleanup(remove)

	if visReg.Parameter().IsUnIgnored("HmIP-PSM", "", hmenum.ParamsetKeyValues, "AES_ACTIVE") {
		t.Fatal("AES_ACTIVE is un-ignored before any central was adopted")
	}

	// What the adopt path persists for the CCU the operator adds.
	if err := store.Replace(ctx, "ccu-late", []string{"AES_ACTIVE"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	unit, err := central.New(central.Config{Name: "ccu-late"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !visReg.Parameter().IsUnIgnored("HmIP-PSM", "", hmenum.ParamsetKeyValues, "AES_ACTIVE") {
		t.Fatal("the adopted CCU's un_ignore patterns never reached the shared visibility registry — " +
			"every parameter the operator un-ignored on it stays suppressed until the next restart")
	}

	// Removing the CCU withdraws its patterns again: they must not keep
	// widening what the remaining centrals expose.
	if !reg.Unregister("ccu-late") {
		t.Fatal("Unregister reported no such central")
	}
	if visReg.Parameter().IsUnIgnored("HmIP-PSM", "", hmenum.ParamsetKeyValues, "AES_ACTIVE") {
		t.Error("the removed CCU's un_ignore patterns are still in effect")
	}
}
