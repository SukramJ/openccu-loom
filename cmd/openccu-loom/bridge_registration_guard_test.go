// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// bridge_registration_guard_test.go pins the ADR 0047 §7 invariant:
// every north-bound surface registers on the northBridges registry
// (completeness) and the registration order determines the reverse-stop
// teardown order (stability). The seam is deps.onNorthBridges, which is
// called once after all surfaces are registered and before StartAll.

import (
	"bytes"
	"context"
	"testing"
	"time"

	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// serviceNames extracts the Name() of every registered service in order.
func serviceNames(r *northbridge.Registry) []string {
	svcs := r.Services()
	out := make([]string, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s.Name())
	}
	return out
}

// TestNorthBridgeRegistrationCompleteAndOrdered boots the daemon with REST
// enabled and Matter disabled, then asserts that all expected north-bound
// surfaces are registered in the correct order. Order determines reverse-stop
// teardown: mqtt (first in, last out), then webhook-outbound, then rest
// (last in, first out).
func TestNorthBridgeRegistrationCompleteAndOrdered(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(false)
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	gotCh := make(chan []string, 1)

	deps := newReloadDeps()
	deps.onNorthBridges = func(r *northbridge.Registry) {
		names := serviceNames(r)
		select {
		case gotCh <- names:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- daemonServeWithDeps(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	}()

	want := []string{"mqtt", "webhook-outbound", "rest"}

	var got []string
	select {
	case got = <-gotCh:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for onNorthBridges callback")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("daemon returned error after cancel: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Error("daemon did not shut down in time")
	}

	if len(got) != len(want) {
		t.Fatalf("registration count: got %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("services[%d]: got %q, want %q (full slice: %v)", i, got[i], name, got)
		}
	}
}

// TestNorthBridgeRegistrationIncludesMatterWhenEnabled boots the daemon with
// Matter enabled and asserts that the matter service appears between
// webhook-outbound and rest in the registration order.
func TestNorthBridgeRegistrationIncludesMatterWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Discriminator = 0xF00
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	gotCh := make(chan []string, 1)

	deps := newReloadDeps()
	deps.onNorthBridges = func(r *northbridge.Registry) {
		names := serviceNames(r)
		select {
		case gotCh <- names:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- daemonServeWithDeps(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	}()

	// With REST disabled, the onNorthBridges hook is still called after all
	// registration is complete. Matter registers after webhook-outbound; REST
	// is absent because it is disabled.
	want := []string{"mqtt", "webhook-outbound", "matter"}

	var got []string
	select {
	case got = <-gotCh:
	case <-time.After(25 * time.Second):
		t.Fatal("timed out waiting for onNorthBridges callback")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("daemon returned error after cancel: %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Error("daemon did not shut down in time")
	}

	if len(got) != len(want) {
		t.Fatalf("registration count: got %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("services[%d]: got %q, want %q (full slice: %v)", i, got[i], name, got)
		}
	}
}
