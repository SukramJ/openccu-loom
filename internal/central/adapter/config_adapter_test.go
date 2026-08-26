// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ============================================================
// ConfigAdapter tests
// ============================================================

// TestConfigAdapterNilSourceReturnsZero verifies that a nil config
// source produces a zero-value ConfigSnapshot without panic.
func TestConfigAdapterNilSourceReturnsZero(t *testing.T) {
	t.Parallel()
	a := NewConfigAdapter(nil, nil)
	snap := a.SanitizedConfig()
	if snap.Locale != "" {
		t.Errorf("Locale = %q, want empty", snap.Locale)
	}
	if len(snap.Centrals) != 0 {
		t.Errorf("Centrals = %v, want empty", snap.Centrals)
	}
}

// TestConfigAdapterSanitizedConfig verifies that the adapter correctly
// projects the config fields into the ConfigSnapshot.
func TestConfigAdapterSanitizedConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Locale: "de",
		Callback: config.CallbackConfig{
			Port:    8120,
			BinPort: 8129,
		},
		North: config.NorthConfig{
			REST: config.NorthREST{Enabled: new(true)},
			UI:   config.NorthUI{Enabled: new(false)},
			MQTT: config.NorthMQTT{
				Enabled:          true,
				RawEnabled:       true,
				DiscoveryEnabled: false,
			},
		},
		Centrals: []config.CentralConfig{
			{
				Name: "ccu-01",
				Host: "192.168.1.10",
				Interfaces: []config.InterfaceSpec{
					{Name: "HmIP-RF"},
					{Name: "BidCos-RF"},
				},
			},
		},
	}
	a := NewConfigAdapter(cfg, nil)
	snap := a.SanitizedConfig()

	if snap.Locale != "de" {
		t.Errorf("Locale = %q, want de", snap.Locale)
	}
	if snap.CallbackPorts.XMLRPC != 8120 {
		t.Errorf("XMLRPC port = %d, want 8120", snap.CallbackPorts.XMLRPC)
	}
	if snap.CallbackPorts.BINRPC != 8129 {
		t.Errorf("BINRPC port = %d, want 8129", snap.CallbackPorts.BINRPC)
	}
	if !snap.Features["rest"] {
		t.Error("rest feature must be true")
	}
	if snap.Features["ui"] {
		t.Error("ui feature must be false")
	}
	if !snap.Features["mqtt"] {
		t.Error("mqtt feature must be true")
	}
	if !snap.Features["raw_topics"] {
		t.Error("raw_topics feature must be true")
	}
	if snap.Features["ha_discovery"] {
		t.Error("ha_discovery feature must be false")
	}
	if len(snap.Centrals) != 1 {
		t.Fatalf("Centrals = %d, want 1", len(snap.Centrals))
	}
	cc := snap.Centrals[0]
	if cc.Name != "ccu-01" {
		t.Errorf("central Name = %q, want ccu-01", cc.Name)
	}
	if cc.Host != "192.168.1.10" {
		t.Errorf("central Host = %q, want 192.168.1.10", cc.Host)
	}
	if len(cc.Interfaces) != 2 || cc.Interfaces[0] != "HmIP-RF" {
		t.Errorf("Interfaces = %v", cc.Interfaces)
	}
}

// TestConfigAdapterSanitizedConfigReportsEffectivePortsWhenSet is the
// regression guard for the dynamic-port-mode defect: with
// `callback.port_range` set, applyDefaults rewrites the unset
// callback.port to 8120 — a port nothing listens on — while the XML-RPC
// listener actually binds the first free port in the range. Before
// SetEffectiveCallbackPorts, SanitizedConfig always reported the
// configured (wrong) value; this pins that a bound effective port wins.
func TestConfigAdapterSanitizedConfigReportsEffectivePortsWhenSet(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Callback: config.CallbackConfig{
			Port:    8120, // applyDefaults' rewrite of an unset port_range config
			BinPort: 0,    // the genuinely dynamic BIN-RPC mode
		},
	}
	a := NewConfigAdapter(cfg, nil)

	// Before the listeners bind, the snapshot still falls back to the
	// configured value.
	snap := a.SanitizedConfig()
	if snap.CallbackPorts.XMLRPC != 8120 || snap.CallbackPorts.BINRPC != 0 {
		t.Fatalf("pre-bind snapshot = %+v, want the configured values unchanged", snap.CallbackPorts)
	}

	a.SetEffectiveCallbackPorts(18310, 18320)
	snap = a.SanitizedConfig()
	if snap.CallbackPorts.XMLRPC != 18310 {
		t.Errorf("XMLRPC port = %d, want the effective bound port 18310, not the configured 8120", snap.CallbackPorts.XMLRPC)
	}
	if snap.CallbackPorts.BINRPC != 18320 {
		t.Errorf("BINRPC port = %d, want the effective bound port 18320, not the configured 0", snap.CallbackPorts.BINRPC)
	}
}

// TestConfigAdapterMultipleCentrals verifies multiple centrals are all
// included in the snapshot.
func TestConfigAdapterMultipleCentrals(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: "ccu-a", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}},
			{Name: "ccu-b", Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}}},
		},
	}
	a := NewConfigAdapter(cfg, nil)
	snap := a.SanitizedConfig()
	if len(snap.Centrals) != 2 {
		t.Fatalf("Centrals = %d, want 2", len(snap.Centrals))
	}
}

// TestConfigAdapterReportsTheLiveCentralSet pins that the endpoint reports
// the CCUs the daemon actually serves. A CCU adopted or removed through the
// SPA never reaches the boot config, so reading that slice made the endpoint
// documented as the *effective* configuration go stale until the next restart.
func TestConfigAdapterReportsTheLiveCentralSet(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: "ccu-boot", Host: "10.0.0.1", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}},
			{Name: "ccu-gone", Host: "10.0.0.2", Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}}},
		},
	}
	reg := central.NewRegistry()
	for _, name := range []string{"ccu-boot", "ccu-adopted"} {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("reg.Register(%s): %v", name, err)
		}
	}

	snap := NewConfigAdapter(cfg, reg).SanitizedConfig()

	got := make(map[string]hmapi.ConfigCentral, len(snap.Centrals))
	for _, c := range snap.Centrals {
		got[c.Name] = c
	}
	if len(got) != 2 {
		t.Fatalf("centrals = %v, want exactly the two registered ones", snap.Centrals)
	}
	if _, ok := got["ccu-adopted"]; !ok {
		t.Error("a central adopted at runtime is missing from the effective config")
	}
	if _, ok := got["ccu-gone"]; ok {
		t.Error("a central that is no longer registered is still reported")
	}
	// A registered central that has not wired an interface yet still reports
	// the wiring its boot row declares.
	if boot := got["ccu-boot"]; boot.Host != "10.0.0.1" || len(boot.Interfaces) != 1 {
		t.Errorf("boot central = %+v, want host 10.0.0.1 with one interface", boot)
	}
}

// ============================================================
// HealthAdapter tests
// ============================================================

// TestHealthAdapterFallbackWhenNoRegistry verifies that the health
// adapter returns from the fallback tracker when no registry is given.
func TestHealthAdapterFallbackWhenNoRegistry(t *testing.T) {
	t.Parallel()
	a := NewHealthAdapter(nil, nil)
	// With a new (empty) fallback tracker, status must be Unknown and
	// Score 0.
	if got := a.Overall(); got != health.StatusUnknown {
		t.Errorf("Overall = %v, want Unknown", got)
	}
	if got := a.Score(); got != 0 {
		t.Errorf("Score = %v, want 0", got)
	}
	if snap := a.Snapshot(); len(snap) != 0 {
		t.Errorf("Snapshot = %v, want empty", snap)
	}
}

// TestHealthAdapterEmptyRegistryFallsBackToFallback verifies that an
// empty registry (no centrals) uses the fallback tracker.
func TestHealthAdapterEmptyRegistryFallsBackToFallback(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewHealthAdapter(reg, nil)
	// Empty registry → fallback path.
	if got := a.Overall(); got != health.StatusUnknown {
		t.Errorf("Overall = %v, want Unknown", got)
	}
}

// TestHealthAdapterPicksFirstCentralTracker verifies that the
// aggregator surfaces samples from a registered central's tracker.
// The historical "pick first" behaviour was generalised to a real
// multi-tracker merge (daemon-global + every per-central tracker);
// the assertions stay valid because the single registered central's
// healthy sample still wins the aggregated verdict.
func TestHealthAdapterPicksFirstCentralTracker(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Record a healthy sample on the central's tracker.
	c.Health.Record("HmIP-RF", health.Sample{Healthy: true})

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}

	a := NewHealthAdapter(reg, nil)
	if got := a.Overall(); got != health.StatusHealthy {
		t.Errorf("Overall = %v, want Healthy", got)
	}
	if got := a.Score(); got <= 0 {
		t.Errorf("Score = %v, want > 0", got)
	}
	if snap := a.Snapshot(); len(snap) == 0 {
		t.Error("Snapshot must not be empty when a component is tracked")
	}
}

// TestHealthAdapterCustomFallback verifies that a custom fallback
// tracker is used when the registry is empty.
func TestHealthAdapterCustomFallback(t *testing.T) {
	t.Parallel()
	fallback := health.NewTracker()
	fallback.Record("probe", health.Sample{Healthy: true})

	a := NewHealthAdapter(central.NewRegistry(), fallback)
	if got := a.Overall(); got != health.StatusHealthy {
		t.Errorf("Overall = %v, want Healthy (from custom fallback)", got)
	}
}
