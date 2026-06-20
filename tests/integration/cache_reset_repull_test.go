// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration exercises the cache-reset "clear + re-pull" flow
// (ADR 0042) against the in-process godevccu simulator. Two paths are
// covered: direct ReinitCentral on the BringUpManager and the higher-level
// cachereset.Service.Clear wrapper.
package integration

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// fixedTopology is a minimal Topology that enumerates exactly one central
// and one interface — enough for the cachereset.Service to expand a
// ScopeCentral into the (central, interface) pair it clears.
type fixedTopology struct {
	central string
	iface   string
}

func (f fixedTopology) Centrals() []string           { return []string{f.central} }
func (f fixedTopology) Interfaces(_ string) []string { return []string{f.iface} }

// waitForCondition polls cond every 100 ms until it returns true or the
// timeout elapses, at which point it calls t.Fatalf with msg.
func waitForCondition(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Since(deadline) >= 0 {
			t.Fatalf("timed out after %s: %s", timeout, msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestCacheResetReinitRepullsModel exercises the cache-reset re-pull seam
// (ADR 0042) end-to-end against the in-process godevccu simulator.
func TestCacheResetReinitRepullsModel(t *testing.T) {
	const (
		centralName = "cachereset-ccu"
		ifaceName   = "HmIP-RF"
	)

	// ── spin up simulator ─────────────────────────────────────────────────────
	srv := startMockCCUOpenCCU(t)

	// ── parse ephemeral ports from simulator URLs ──────────────────────────────
	xmlEphemeralPort := mustParsePort(t, srv.URL())
	jsonEphemeralPort := mustParsePort(t, srv.JSONRPCURL())

	// ── build config ──────────────────────────────────────────────────────────
	cfg := &config.Config{
		Locale: "en",
		Centrals: []config.CentralConfig{{
			Name:        centralName,
			Host:        "127.0.0.1",
			Username:    "Admin",
			Password:    "",
			JSONRPCPort: jsonEphemeralPort,
			Interfaces:  []config.InterfaceSpec{{Name: ifaceName, Port: xmlEphemeralPort}},
		}},
	}

	// ── create and register a central unit ────────────────────────────────────
	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	// ── start registry + wire southbound ─────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := reg.StartAll(ctx); err != nil {
		t.Fatalf("reg.StartAll: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}

	writer := client.NewValueWriter()

	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{
		Writer:       writer,
		Translations: translations,
	}, logger)
	if err != nil {
		t.Fatalf("adapter.WireCentrals: %v", err)
	}
	t.Cleanup(func() { mgr.Teardown() })

	// ── wait for the async readiness-gated bring-up to populate the model ─────
	waitForCondition(
		t, 30*time.Second,
		"ModelRegistry.Len() > 0 after initial bring-up",
		func() bool { return unit.ModelRegistry.Len() > 0 },
	)

	baselineCount := unit.ModelRegistry.Len()

	// Capture the first device address so we can assert it reappears after re-init.
	var sampleAddr string
	for _, d := range unit.ModelRegistry.List() {
		if d != nil {
			sampleAddr = d.Address
			break
		}
	}
	if sampleAddr == "" {
		t.Fatal("no device address in the model registry after bring-up")
	}

	// ── subtest A: direct BringUpManager.ReinitCentral path ──────────────────
	t.Run("ReinitCentral", func(t *testing.T) {
		ok := mgr.ReinitCentral(ctx, centralName)
		if !ok {
			t.Fatal("ReinitCentral returned false; central not managed by BringUpManager")
		}

		// ReinitCentral clears the model synchronously and starts an async re-pull.
		// Poll until the re-pull restores the full device count.
		waitForCondition(
			t, 30*time.Second,
			"ModelRegistry.Len() == "+strconv.Itoa(baselineCount)+" after ReinitCentral",
			func() bool { return unit.ModelRegistry.Len() == baselineCount },
		)

		found := false
		for _, d := range unit.ModelRegistry.List() {
			if d != nil && d.Address == sampleAddr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("device %q not present in ModelRegistry after ReinitCentral (got %d devices)",
				sampleAddr, unit.ModelRegistry.Len())
		}
	})

	// Wait for the model to settle after subtest A before running B.
	waitForCondition(
		t, 30*time.Second,
		"ModelRegistry.Len() == "+strconv.Itoa(baselineCount)+" before ServiceClearPath",
		func() bool { return unit.ModelRegistry.Len() == baselineCount },
	)

	// ── subtest B: cachereset.Service.Clear path ──────────────────────────────
	t.Run("ServiceClearPath", func(t *testing.T) {
		svc := cachereset.New(cachereset.Deps{
			Reiniter: mgr,
			Topology: fixedTopology{central: centralName, iface: ifaceName},
			Logger:   logger,
		})

		rep, err := svc.Clear(ctx, cachereset.Scope{
			Kind:    cachereset.ScopeCentral,
			Central: centralName,
		})
		if err != nil {
			t.Fatalf("svc.Clear: %v", err)
		}

		if !slices.Contains(rep.CentralsReinit, centralName) {
			t.Errorf("Report.CentralsReinit = %v; want to contain %q",
				rep.CentralsReinit, centralName)
		}

		waitForCondition(
			t, 30*time.Second,
			"ModelRegistry.Len() == "+strconv.Itoa(baselineCount)+" after svc.Clear",
			func() bool { return unit.ModelRegistry.Len() == baselineCount },
		)
	})
}

// mustParsePort extracts the TCP port number from a URL of the form
// "http://host:port/..." and fails the test if parsing fails.
func mustParsePort(t *testing.T, rawURL string) int {
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
