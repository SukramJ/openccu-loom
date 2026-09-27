// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestWireCentralsGatesOnProfileReadiness pins that the boot bring-up built by
// the real composition entry point waits on the central's south-profile
// readiness before it touches the system.
//
// The effect asserted is the one the gate exists for: while the CCU's boot
// marker says "not ready", no JSON-RPC request reaches it (so no device can be
// created without its name); once the marker flips to OK, the hub bring-up
// starts. A gate that is wired to anything but the profile's probe — or not
// wired at all — sends the login during the not-ready window.
func TestWireCentralsGatesOnProfileReadiness(t *testing.T) {
	var ready atomic.Bool
	var jsonRPCHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ise/checkrega.cgi", func(w http.ResponseWriter, _ *http.Request) {
		if ready.Load() {
			_, _ = w.Write([]byte("OK"))
			return
		}
		// What a booting CCU's lighttpd serves before ReGaHss is up.
		_, _ = w.Write([]byte("not yet"))
	})
	mux.HandleFunc("/api/homematic.cgi", func(w http.ResponseWriter, _ *http.Request) {
		jsonRPCHits.Add(1)
		// Refuse the login: the gate re-probes and the test only needs to
		// see that the request arrived.
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name:        "gate-pin",
		Host:        "127.0.0.1",
		JSONRPCPort: port,
		Username:    "Admin",
		Interfaces:  []config.InterfaceSpec{{Name: "HmIP-RF", Port: port}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "gate-pin"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)

	// Not ready: the gate probes and waits. One second covers the first probe
	// and leaves the goroutine ample time to misbehave if the gate is open.
	time.Sleep(time.Second)
	if n := jsonRPCHits.Load(); n != 0 {
		t.Fatalf("JSON-RPC reached the CCU %d time(s) while its boot marker said not ready; "+
			"the bring-up is not gated on the south profile's readiness", n)
	}

	// Ready: the next probe (default cadence 3 s) opens the gate and the hub
	// bring-up logs in.
	ready.Store(true)
	deadline := time.Now().Add(10 * time.Second)
	for jsonRPCHits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the CCU reported ready but the hub bring-up never reached JSON-RPC")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
