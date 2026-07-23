// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// This file is the headline proof for live CCU adopt: a SECOND central is
// adopted at runtime — via [centralOrchestrator], the same orchestrator
// CreateCentral/DeleteCentral drive through [liveCentralAdmin] — against a
// live godevccu instance, alongside a first central that came up through
// the normal boot-time WireCentrals path. Its devices must appear in the
// registry/model without a restart, and removing it again must deregister
// its callback route, evict its devices, and leave no goroutine behind.
//
// It lives in package main (not tests/integration) because
// [centralOrchestrator] is unexported — Go cannot import a `main` package
// from an external test package, so exercising it directly requires a
// same-package test. The godevccu bring-up helpers below intentionally
// mirror tests/integration/godevccu.go's shape (that file is itself
// package-private to tests/integration and cannot be reused here).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// adoptTestMockDevices is a small fleet, enough to prove devices appear /
// disappear without paying the cost of the full ~399-device catalogue.
var adoptTestMockDevices = []string{"HmIP-SWSD", "HmIP-BSM"}

// startAdoptTestCCU spins up a godevccu instance in the OpenCCU/CCU
// personality (XML-RPC + JSON-RPC, both on OS-assigned ports) — the same
// setup [adapter.WireCentrals] expects. Mirrors
// tests/integration/godevccu.go's startMockCCUOpenCCU. The returned stop
// func lets the goroutine-leak assertion shut a simulator's own
// accept-loop goroutines down explicitly instead of waiting for
// t.Cleanup (which only fires after the whole test function returns) —
// [godevccu.VirtualCCU.Stop] is idempotent, so t.Cleanup calling it again
// is harmless.
func startAdoptTestCCU(t *testing.T, serial string) (xmlURL, jsonURL string, stop func()) {
	t.Helper()
	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Username:      "Admin",
		Password:      "",
		AuthEnabled:   true,
		Devices:       adoptTestMockDevices,
		Serial:        serial,
		SetupDefaults: true,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	xmlAddr, ok := v.XMLRPCAddr().(*net.TCPAddr)
	if !ok || xmlAddr == nil || xmlAddr.Port == 0 {
		t.Fatalf("godevccu: ephemeral XML-RPC port not resolved: %v", v.XMLRPCAddr())
	}
	jsonAddr, ok := v.JSONRPCAddr().(*net.TCPAddr)
	if !ok || jsonAddr == nil || jsonAddr.Port == 0 {
		t.Fatalf("godevccu: ephemeral JSON-RPC port not resolved: %v", v.JSONRPCAddr())
	}
	return fmt.Sprintf("http://%s/", xmlAddr.String()), fmt.Sprintf("http://%s/api/homematic.cgi", jsonAddr.String()), func() { _ = v.Stop() }
}

// mustParseAdoptTestPort extracts the TCP port from a URL of the form
// "http://host:port/...".
func mustParseAdoptTestPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", u.Host, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("strconv.Atoi(%q): %v", portStr, err)
	}
	return port
}

// waitForAdoptTestCondition polls cond every 100ms until it returns true or
// timeout elapses.
func waitForAdoptTestCondition(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %s", timeout, msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// adoptTestGoroutineLeakThreshold mirrors the small non-zero budget used
// elsewhere in this repo (e.g.
// internal/client/reliability/coalesce_eviction_test.go) for the same class
// of assertion: Go-runtime-internal goroutines (GC, finalizer) may still be
// winding down right after a drain call returns.
const adoptTestGoroutineLeakThreshold = 5

func eventuallyAdoptTestGoroutineDelta(baseline int, total time.Duration) int {
	deadline := time.Now().Add(total)
	delta := runtime.NumGoroutine() - baseline
	for time.Now().Before(deadline) && delta > adoptTestGoroutineLeakThreshold {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		delta = runtime.NumGoroutine() - baseline
	}
	return delta
}

// TestCentralOrchestratorAdoptsAndRemovesSecondCentralWithoutRestart is the
// PR3 headline proof: central A comes up the normal boot-time way (New +
// Register + StartAll + WireCentrals, exactly as central.Bootstrap +
// daemon.go do), then central B is adopted purely at RUNTIME through
// [centralOrchestrator.adoptCentral] — the same call the REST
// CreateCentral handler drives via [liveCentralAdmin] — while A stays live
// and unaffected. Removing B again must deregister its callback route,
// evict its devices from the model, and leave no goroutine behind, all
// without disturbing A.
func TestCentralOrchestratorAdoptsAndRemovesSecondCentralWithoutRestart(t *testing.T) {
	const (
		centralNameA = "adopt-live-a"
		centralNameB = "adopt-live-b"
		ifaceName    = "HmIP-RF"
	)

	logger := slog.New(slog.DiscardHandler)
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	writer := client.NewValueWriter()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ── central A comes up the normal boot-time way ──────────────────────────
	xmlURLA, jsonURLA, _ := startAdoptTestCCU(t, "GODEVCCUAAAA")
	cfg := &config.Config{
		Locale: "en",
		Centrals: []config.CentralConfig{{
			Name:        centralNameA,
			Host:        "127.0.0.1",
			Username:    "Admin",
			JSONRPCPort: mustParseAdoptTestPort(t, jsonURLA),
			Interfaces:  []config.InterfaceSpec{{Name: ifaceName, Port: mustParseAdoptTestPort(t, xmlURLA)}},
		}},
	}

	reg := central.NewRegistry()
	unitA, err := central.New(central.Config{Name: centralNameA, Logger: logger})
	if err != nil {
		t.Fatalf("central.New(A): %v", err)
	}
	if err := reg.Register(unitA); err != nil {
		t.Fatalf("reg.Register(A): %v", err)
	}
	if err := reg.StartAll(ctx); err != nil {
		t.Fatalf("reg.StartAll: %v", err)
	}

	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{
		Writer:       writer,
		Translations: translations,
	}, logger)
	if err != nil {
		t.Fatalf("adapter.WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)

	waitForAdoptTestCondition(t, 30*time.Second, "central A model populated",
		func() bool { return unitA.ModelRegistry.Len() > 0 })

	// The orchestrator wraps the same BringUpManager the daemon's REST
	// decorator does (see daemon.go's centralOrch construction). Southbound
	// wiring deps only need `reg` for this scenario — every other field
	// wireCentralNorthbound reads is nil-guarded (MQTT, health tracker, ...).
	orch := newCentralOrchestrator(reg, mgr, southboundWiringDeps{reg: reg, logger: logger}, cfg, logger, "",
		nil, nil, nil, nil)
	if orch == nil {
		t.Fatal("newCentralOrchestrator returned nil (bringUp manager was nil)")
	}

	baselineGoroutines := runtime.NumGoroutine()

	// ── central B is adopted at RUNTIME, no restart ──────────────────────────
	xmlURLB, jsonURLB, stopCCUB := startAdoptTestCCU(t, "GODEVCCUBBBB")
	ccB := config.CentralConfig{
		Name:        centralNameB,
		Host:        "127.0.0.1",
		Username:    "Admin",
		JSONRPCPort: mustParseAdoptTestPort(t, jsonURLB),
		Interfaces:  []config.InterfaceSpec{{Name: ifaceName, Port: mustParseAdoptTestPort(t, xmlURLB)}},
	}
	if err := orch.adoptCentral(ctx, ccB); err != nil {
		t.Fatalf("adoptCentral(B): %v", err)
	}

	unitB, ok := reg.Get(centralNameB)
	if !ok {
		t.Fatal("central B not present in the shared registry after adoptCentral")
	}
	waitForAdoptTestCondition(t, 30*time.Second, "central B model populated after live adopt",
		func() bool { return unitB.ModelRegistry.Len() > 0 })

	// Central A is untouched by the live adopt of B — multi-CCU safety check.
	if unitA.ModelRegistry.Len() == 0 {
		t.Error("central A lost its devices after adopting central B live")
	}
	if got, want := len(mgr.Centrals()), 2; got != want {
		t.Errorf("BringUpManager.Centrals() len = %d, want %d (%v)", got, want, mgr.Centrals())
	}

	// Adopting the same name twice must fail cleanly (no duplicate Unit / handle).
	if err := orch.adoptCentral(ctx, ccB); err == nil {
		t.Error("adoptCentral(B) a second time succeeded; want an already-registered error")
	}

	// ── central B is removed at RUNTIME, no restart ──────────────────────────
	bDeviceCount := unitB.ModelRegistry.Len()
	if err := orch.removeCentral(ctx, centralNameB); err != nil {
		t.Fatalf("removeCentral(B): %v", err)
	}

	if _, ok := reg.Get(centralNameB); ok {
		t.Error("central B still present in the shared registry after removeCentral")
	}
	for _, name := range mgr.Centrals() {
		if name == centralNameB {
			t.Error("BringUpManager still manages central B after removeCentral (callback route not deregistered)")
		}
	}
	if got := unitB.ModelRegistry.Len(); got != 0 {
		t.Errorf("central B ModelRegistry.Len() = %d after removeCentral, want 0 (had %d devices before removal)", got, bDeviceCount)
	}

	// Central A must be entirely unaffected by removing B.
	if _, ok := reg.Get(centralNameA); !ok {
		t.Error("central A was unregistered as a side effect of removing central B")
	}
	if unitA.ModelRegistry.Len() == 0 {
		t.Error("central A lost its devices after removing central B")
	}

	// Removing an already-removed / never-adopted central must be tolerated,
	// not panic or hang.
	if err := orch.removeCentral(ctx, centralNameB); err == nil {
		t.Error("removeCentral(B) a second time succeeded; want errCentralNotLive")
	}

	// removeCentral only tears down the daemon-side wiring; central B's own
	// godevccu simulator (accept-loop goroutines for its XML-RPC/JSON-RPC
	// listeners) keeps running until stopped here — leaving it up would show
	// as a false-positive "leak" that has nothing to do with removeCentral.
	stopCCUB()

	if delta := eventuallyAdoptTestGoroutineDelta(baselineGoroutines, 10*time.Second); delta > adoptTestGoroutineLeakThreshold {
		t.Errorf("goroutine delta after adopt+remove cycle = %d, want <= %d (leak suspected)", delta, adoptTestGoroutineLeakThreshold)
	}
}
