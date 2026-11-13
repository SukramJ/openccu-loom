// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAutoCentralComesUpWhenItsTypeResolvesBeforeItIsRegistered drives the
// interleaving that left an `auto` central waiting for good: the probe
// identifies the system and asks for the placeholder to be replaced before
// the caller of buildAndStart has put the placeholder into the manager.
//
// Both callers register the handle after buildAndStart returns
// (`mgr.add(mgr.buildAndStart(cc, unit))`), and the probe of a system that
// answers at once needs one local round trip. Here the registration is held
// back until the type has been persisted — the step right before the
// replacement — plus a pause, so the replacement runs first.
func TestAutoCentralComesUpWhenItsTypeResolvesBeforeItIsRegistered(t *testing.T) {
	startCtx, cancelStart := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStart()
	fake, err := litefake.Start(startCtx, litefake.Options{})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name: "box", Host: host, JSONRPCPort: port, SystemType: hmenum.SystemTypeAuto, APIToken: litefake.DefaultToken,
		Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}, {Name: "HmIP-RF"}},
	}}}
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	var persisted atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	mgr := newBringUpManager()
	mgr.parentCancel = cancel
	mgr.parentCtx = ctx
	mgr.cfg = cfg
	mgr.deps = WireDeps{
		Writer: clientpkg.NewValueWriter(),
		PersistSystemType: func(context.Context, string, hmenum.SystemType) {
			persisted.Store(true)
		},
	}
	mgr.logger = slog.New(slog.DiscardHandler)
	t.Cleanup(mgr.Teardown)

	placeholder := mgr.buildAndStart(&cfg.Centrals[0], unit)

	deadline := time.Now().Add(10 * time.Second)
	for !persisted.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the system type was never resolved; the interleaving under test did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The replacement is the probe goroutine's next statement after the
	// persist callback; give it the time to run before the registration.
	time.Sleep(300 * time.Millisecond)
	mgr.add(placeholder)

	deadline = time.Now().Add(20 * time.Second)
	for !unit.IsSouthboundReady() {
		if time.Now().After(deadline) {
			t.Fatalf("the central never came up after its placeholder was registered late; readiness = %+v", unit.Readiness())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
