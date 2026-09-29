// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// startLiteCentral brings one openccu-lite central up against fake through
// the real composition entry point and returns its unit.
func startLiteCentral(t *testing.T, fake *litefake.Fake, token string) *central.Unit {
	t.Helper()
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name:        "box",
		Host:        host,
		JSONRPCPort: port,
		SystemType:  hmenum.SystemTypeOpenCCULite,
		APIToken:    token,
		Interfaces:  []config.InterfaceSpec{{Name: "BidCos-RF"}, {Name: "HmIP-RF"}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{Writer: client.NewValueWriter()}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	return unit
}

func startFake(t *testing.T, opts litefake.Options) *litefake.Fake {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := litefake.Start(ctx, opts)
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestLiteCentralLoadsDevicesThroughProxy pins the lite bring-up end to
// end through WireCentrals: the central holds nothing while the box is not
// ready, then loads the fleet of both configured interfaces through the
// box's XML-RPC proxy — with the token as the credential — and never sends
// init, which the box refuses.
func TestLiteCentralLoadsDevicesThroughProxy(t *testing.T) {
	fake := startFake(t, litefake.Options{StartNotReady: true})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)

	time.Sleep(500 * time.Millisecond)
	if n := len(unit.ModelRegistry.List()); n != 0 {
		t.Fatalf("%d devices while the box was not ready; the lite bring-up is not gated", n)
	}

	fake.SetReady(true)
	deadline := time.Now().Add(20 * time.Second)
	for !unit.IsSouthboundReady() {
		if time.Now().After(deadline) {
			t.Fatalf("the lite central never came up; readiness = %+v", unit.Readiness())
		}
		time.Sleep(50 * time.Millisecond)
	}
	var ifaces []hmenum.Interface
	for _, d := range unit.ModelRegistry.List() {
		if !slices.Contains(ifaces, d.Interface) {
			ifaces = append(ifaces, d.Interface)
		}
	}
	for _, want := range []hmenum.Interface{hmenum.InterfaceBidCosRF, hmenum.InterfaceHmIPRF} {
		if !slices.Contains(ifaces, want) {
			t.Errorf("no device of %s was loaded (loaded interfaces: %v)", want, ifaces)
		}
	}

	sawListDevices := false
	for _, c := range fake.Calls() {
		if !strings.HasPrefix(c.Path, "/api/rpc/v1/xmlrpc/") {
			continue
		}
		if c.Subject == "" {
			t.Errorf("proxy call %s carried no accepted credential", c.Path)
		}
		if slices.Contains(c.RPCMethods, "init") {
			t.Fatalf("init reached the box on %s; the lite path must never announce a callback", c.Path)
		}
		if slices.Contains(c.RPCMethods, "listDevices") {
			sawListDevices = true
		}
	}
	if !sawListDevices {
		t.Error("no listDevices went through the proxy")
	}
	if got := unit.SystemInformation(); got.Serial == "" || got.Model != "openccu-lite" {
		t.Errorf("system information = %+v, want the box's serial and model openccu-lite", got)
	}
}

// TestLiteFeaturesFollowTokenScopes pins that a lite central's features
// follow its token: without power the reboot is absent with the scope
// named, and once the token is widened on the box and the scopes are read
// again the feature turns available with a change event.
func TestLiteFeaturesFollowTokenScopes(t *testing.T) {
	const token = "olt_cccccccccccccccccccccccccccccccc"
	fake := startFake(t, litefake.Options{Tokens: map[string][]string{token: {"rpc:configure", "meta:read", "system:read"}}})
	unit := startLiteCentral(t, fake, token)

	deadline := time.Now().Add(20 * time.Second)
	for !unit.Features().Known() {
		if time.Now().After(deadline) {
			t.Fatal("the lite central never published its features")
		}
		time.Sleep(50 * time.Millisecond)
	}
	f := unit.Features()
	if f.SystemType() != hmenum.SystemTypeOpenCCULite {
		t.Errorf("system type = %q", f.SystemType())
	}
	if s := f.State(hmenum.FeatureSystemReboot); s.Available || s.Reason != hmenum.FeatureReasonMissingScope || s.Scope != "power" {
		t.Errorf("system.reboot = %+v, want missing_scope power", s)
	}
	if s := f.State(hmenum.FeatureHubSysvars); s.Available || s.Reason != hmenum.FeatureReasonNotSupported {
		t.Errorf("hub.sysvars = %+v, want not_supported_by_system", s)
	}
	if !f.Available(hmenum.FeatureDeviceConfigure) || !f.Available(hmenum.FeatureDeviceControl) {
		t.Error("rpc:configure does not grant device.configure and device.control")
	}

	changed := make(chan struct{}, 4)
	unsub := events.Subscribe(unit.EventBus, func(hmevent.CentralFeaturesChangedEvent) { changed <- struct{}{} })
	t.Cleanup(unsub)
	fake.SetTokens(map[string][]string{token: {"rpc:configure", "meta:read", "system:read", "power"}})
	// Run the job the bring-up registered, exactly as the scheduler would.
	var ran bool
	for _, j := range unit.Scheduler.Jobs() {
		if j.Name == "lite.scopes.box" {
			if err := j.Run(context.Background()); err != nil {
				t.Fatalf("scope refresh: %v", err)
			}
			ran = true
		}
	}
	if !ran {
		t.Fatal("the bring-up registered no lite.scopes.box job")
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("widening the token published no features change")
	}
	if !unit.Features().Available(hmenum.FeatureSystemReboot) {
		t.Error("system.reboot still absent after the token gained power")
	}
}

// waitLiteReady waits for the lite central's first bring-up.
func waitLiteReady(t *testing.T, unit *central.Unit) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !unit.IsSouthboundReady() {
		if time.Now().After(deadline) {
			t.Fatalf("the lite central never came up; readiness = %+v", unit.Readiness())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLiteSerialMatchesSSDPCanonicalForm pins that a lite central's serial
// is the box's UPnP serial reduced exactly as SSDP discovery reduces it, so
// a discovered box and the configured central are recognised as the same.
func TestLiteSerialMatchesSSDPCanonicalForm(t *testing.T) {
	const upnpSerial = "3014F711A0001234567890AB"
	fake := startFake(t, litefake.Options{Serial: upnpSerial})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	if got, want := unit.SystemInformation().Serial, routingkey.CanonicalSerial(upnpSerial); got != want || got == "" {
		t.Errorf("serial = %q, want the SSDP canonical form %q", got, want)
	}
}

// TestLiteValueSeederReadsTheStateStore pins that a lite bring-up seeds
// values from the box's state store. The per-channel paramset fill for the
// parameters outside the store's set is pinned in the adapter's unit tests,
// where it can be told apart from the other boot-time reads.
func TestLiteValueSeederReadsTheStateStore(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	for _, c := range fake.Calls() {
		if c.Path == "/api/rpc/v1/state" {
			return
		}
	}
	t.Error("the seed did not read the box's state store")
}
