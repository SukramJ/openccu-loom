// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestBootstrapBuildsEveryCentral(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu-01", Host: "h1"},
		{Name: "ccu-02", Host: "h2"},
	}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer teardown()
	if got := reg.Names(); len(got) != 2 || got[0] != "ccu-01" || got[1] != "ccu-02" {
		t.Fatalf("names=%+v", got)
	}
}

// TestBootstrapCarriesEveryCentralsConfiguredInterfaces pins the boot half of
// the configured-interface set: a central configured in the file reports the
// wire id of each of its interfaces, as the incident producers record them, so
// an interface removed from the file before a daemon restart is gone from it.
func TestBootstrapCarriesEveryCentralsConfiguredInterfaces(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu-01", Host: "h1", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}, {Name: " CUxD "}}},
		{Name: "ccu-02", Host: "h2", Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}}},
	}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer teardown()
	want := map[string]bool{
		WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF):   true,
		WireInterfaceID("ccu-01", hmenum.InterfaceCUxD):     true,
		WireInterfaceID("ccu-02", hmenum.InterfaceBidCosRF): true,
	}
	if got := reg.ConfiguredInterfaceIDs(); !maps.Equal(got, want) {
		t.Fatalf("ConfiguredInterfaceIDs = %v, want %v", got, want)
	}
}

func TestBootstrapDetectsDuplicateName(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{
		{Name: "ccu", Host: "h1"},
		{Name: "ccu", Host: "h2"},
	}}
	_, _, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryStartStopAll(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "a", Host: "h"}}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := reg.StartAll(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	teardown()
}

func TestQueryFacadeDelegates(t *testing.T) {
	cfg := &config.Config{Centrals: []config.CentralConfig{{Name: "q", Host: "h"}}}
	reg, teardown, err := (&Bootstrap{}).Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer teardown()
	c, _ := reg.Get("q")
	qf := c.QueryFacade()
	if qf.Name() != "q" || qf.DeviceCount() != 0 {
		t.Fatalf("facade=%+v", qf)
	}
}
