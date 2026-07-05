// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeConfigAdminService is a minimal test double for
// handlers.ConfigAdminService. Only Effective is exercised by
// restartPendingProvider; all other methods return zero values.
type fakeConfigAdminService struct {
	result *configstore.EffectiveResult
	err    error
}

func (f *fakeConfigAdminService) Effective(_ context.Context) (*configstore.EffectiveResult, error) {
	return f.result, f.err
}

func (f *fakeConfigAdminService) GetSection(_ context.Context, _ configstore.Section) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, nil
}

func (f *fakeConfigAdminService) PutSection(_ context.Context, _ configstore.Section, _ []byte, _ string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, nil
}

func (f *fakeConfigAdminService) DeleteSection(_ context.Context, _ configstore.Section) error {
	return nil
}

// Verify the test double satisfies the interface at compile time.
var _ handlers.ConfigAdminService = (*fakeConfigAdminService)(nil)

// bootConfig returns a Config whose restart-required fields are set to
// known values so mutations in eff are detectable.
func bootConfig() *config.Config {
	c := config.Default()
	c.DataDir = "/var/lib/loom"
	c.North.REST.Listen = ":8119"
	c.North.Matter.Enabled = false
	c.Centrals = []config.CentralConfig{{Name: "ccu1", Host: "192.0.2.1"}}
	return c
}

// TestRestartPendingProvider_NoPending verifies that when the effective
// config matches the boot config on all restart-required fields,
// Pending returns false with an empty fields slice.
func TestRestartPendingProvider_NoPending(t *testing.T) {
	t.Parallel()
	boot := bootConfig()
	eff := *boot // identical copy
	svc := &fakeConfigAdminService{result: &configstore.EffectiveResult{Config: &eff}}

	p := newRestartPendingProvider(boot, svc)
	pending, fields, err := p.Pending(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending {
		t.Errorf("expected pending=false, got true; fields=%v", fields)
	}
	if len(fields) != 0 {
		t.Errorf("expected empty fields, got %v", fields)
	}
}

// TestRestartPendingProvider_PendingOnMatterEnabled verifies that toggling
// North.Matter.Enabled in the effective config surfaces
// "north.matter.enabled" and sets pending=true.
func TestRestartPendingProvider_PendingOnMatterEnabled(t *testing.T) {
	t.Parallel()
	boot := bootConfig()
	eff := *boot
	eff.North.Matter.Enabled = true // differs from boot (false)
	eff.North.Matter.MDNSAdvertise = "noop"
	svc := &fakeConfigAdminService{result: &configstore.EffectiveResult{Config: &eff}}

	p := newRestartPendingProvider(boot, svc)
	pending, fields, err := p.Pending(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pending {
		t.Error("expected pending=true")
	}
	if len(fields) == 0 {
		t.Fatal("expected at least one field, got none")
	}
	found := false
	for _, f := range fields {
		if f == "north.matter.enabled" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected \"north.matter.enabled\" in fields %v", fields)
	}
}

// TestRestartPendingProvider_EffectiveError verifies that an error from
// Effective is propagated to the caller.
func TestRestartPendingProvider_EffectiveError(t *testing.T) {
	t.Parallel()
	boot := bootConfig()
	svc := &fakeConfigAdminService{err: errors.New("store unavailable")}

	p := newRestartPendingProvider(boot, svc)
	_, _, err := p.Pending(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestRestartPendingProvider_NilBoot verifies that a nil boot config
// (passed via newRestartPendingProvider) causes Pending to return
// false without panicking.
func TestRestartPendingProvider_NilBoot(t *testing.T) {
	t.Parallel()
	eff := bootConfig()
	svc := &fakeConfigAdminService{result: &configstore.EffectiveResult{Config: eff}}

	p := newRestartPendingProvider(nil, svc)
	pending, fields, err := p.Pending(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending {
		t.Errorf("expected pending=false with nil boot, got true; fields=%v", fields)
	}
}
