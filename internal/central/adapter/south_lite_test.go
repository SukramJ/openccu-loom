// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

func startTestFake(t *testing.T, opts litefake.Options) *litefake.Fake {
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

// liteCentralFor is a lite central config pointing at the fake.
func liteCentralFor(t *testing.T, f *litefake.Fake, token string) *config.CentralConfig {
	t.Helper()
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	return &config.CentralConfig{
		Name: "box", Host: host, JSONRPCPort: port, SystemType: hmenum.SystemTypeOpenCCULite,
		APIToken: token, Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}, {Name: "BidCos-RF"}},
	}
}

// TestLiteReadinessReasons pins what the lite readiness probe reports in
// each state an operator meets: a starting box, a rejected token, a token
// without the read tier, no configured interface running, and ready.
func TestLiteReadinessReasons(t *testing.T) {
	t.Parallel()
	const (
		metaOnly = "olt_dddddddddddddddddddddddddddddddd"
		unknown  = "olt_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	f := startTestFake(t, litefake.Options{
		StartNotReady: true,
		Tokens:        map[string][]string{litefake.DefaultToken: {"*"}, metaOnly: {"meta:read"}},
	})
	probe := func(token string) (bool, string) {
		p, err := newLiteProfile(liteCentralFor(t, f, token), nil)
		if err != nil {
			t.Fatalf("newLiteProfile: %v", err)
		}
		return p.Readiness().Probe(context.Background())
	}
	expect := func(name, token string, wantReady bool, wantReason string) {
		t.Helper()
		ready, reason := probe(token)
		if ready != wantReady || !strings.Contains(reason, wantReason) {
			t.Errorf("%s: ready=%v reason=%q, want ready=%v reason containing %q", name, ready, reason, wantReady, wantReason)
		}
	}

	expect("starting", litefake.DefaultToken, false, "occulited starting (503)")
	f.SetReady(true)
	expect("unknown token", unknown, false, "token rejected (401)")
	expect("no read tier", metaOnly, false, "token lacks rpc:read (403)")
	if err := f.SetInterfaceDown("HmIP-RF", true); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	if err := f.SetInterfaceDown("BidCos-RF", true); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	expect("interfaces down", litefake.DefaultToken, false, "no configured interface is running")
	if err := f.SetInterfaceDown("BidCos-RF", false); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	expect("ready", litefake.DefaultToken, true, "")
}

// TestLiteHubPlaneNotOfflineFromCheckrega pins that a lite central's hub
// plane liveness comes from the box's health read. The box answers the
// CCU's readiness CGI with its web UI (200, HTML), which the CCU probe
// classifies as "ReGa not serving" — routed through that probe, every lite
// hub plane would be published offline.
func TestLiteHubPlaneNotOfflineFromCheckrega(t *testing.T) {
	t.Parallel()
	f := startTestFake(t, litefake.Options{})
	cc := liteCentralFor(t, f, litefake.DefaultToken)

	target := newRegaLivenessTarget(cc)
	if target == nil {
		t.Fatal("a lite central has no hub liveness target")
	}
	if got := target.probe(context.Background()); got != regaProbeServing {
		t.Errorf("lite liveness = %v, want serving", got)
	}

	// Negative control: the CCU probe against the same box calls it down.
	ccu := *cc
	ccu.SystemType = hmenum.SystemTypeCCU
	ccuProbe := newCCUProfile(&ccu).Liveness()
	if got := ccuProbe.Probe(context.Background()); got != regaProbeNotServing {
		t.Errorf("CCU probe against the box = %v, want not serving (the box serves HTML for checkrega.cgi)", got)
	}
}

func TestLiteFeatureTableCoversEveryKey(t *testing.T) {
	t.Parallel()
	for _, k := range hmenum.AllFeatures() {
		if _, ok := liteFeatureTable[k]; !ok {
			t.Errorf("liteFeatureTable has no row for %s", k)
		}
	}
	if len(liteFeatureTable) != len(hmenum.AllFeatures()) {
		t.Errorf("liteFeatureTable has %d rows for %d features", len(liteFeatureTable), len(hmenum.AllFeatures()))
	}
	all := liteFeatures(map[string]bool{
		"*": true, "rpc:admin": true, "meta:write": true, "system:write": true, "power": true, "backup": true,
		"rpc:read": true, "rpc:operate": true, "rpc:configure": true, "meta:read": true, "system:read": true,
	})
	for _, k := range []hmenum.Feature{hmenum.FeatureHubSysvars, hmenum.FeatureHubPrograms, hmenum.FeatureDeviceCommunicationTest} {
		if all.Available(k) {
			t.Errorf("%s available on openccu-lite with every scope; it needs ReGa", k)
		}
	}
	if !all.Available(hmenum.FeatureTaxonomyTree) {
		t.Error("taxonomy.tree must be available on openccu-lite with meta:write")
	}
}

// paramsetOps records the VALUES reads the seeder makes.
type paramsetOps struct {
	fakeOperations
	reads  []string
	values map[string]map[string]any
}

func (p *paramsetOps) GetParamset(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]any, error) {
	p.reads = append(p.reads, address)
	return p.values[address], nil
}

// TestLiteValueSeederFillsOnlyWhatTheStateStoreLacks pins the seed's
// second step: a channel is read from its VALUES paramset only when it has
// a readable, non-edge-trigger parameter the state store did not deliver,
// and a value the store did deliver is never overwritten by the read.
func TestLiteValueSeederFillsOnlyWhatTheStateStoreLacks(t *testing.T) {
	t.Parallel()
	_, unit := registryWithUnit(t, "box")
	wireID := WireInterfaceID("box", hmenum.InterfaceHmIPRF)
	dev := device.New(device.Config{InterfaceID: wireID, Interface: hmenum.InterfaceHmIPRF, Address: "DEV1", Model: "HmIP-X"})
	dev.AddChannel("DEV1:1", 1, "X", hmenum.ParamsetKeyValues)
	dev.AddChannel("DEV1:2", 2, "X", hmenum.ParamsetKeyValues)
	dev.AddChannel("DEV1:3", 3, "X", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(dev)
	rw := hmenum.OperationsRead | hmenum.OperationsEvent
	reg := hmtypes.ParseWireInterfaceID(wireID)
	// :1 is covered by the store; :2 has a readable parameter the store
	// lacks; :3 has only an edge trigger and a write-only parameter.
	unit.ParamsetReg.Add(reg, "DEV1:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"STATE": {Type: hmenum.ParameterTypeBool, Operations: rw}}, "X")
	unit.ParamsetReg.Add(reg, "DEV1:2", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"STATE":  {Type: hmenum.ParameterTypeBool, Operations: rw},
		"ENERGY": {Type: hmenum.ParameterTypeFloat, Operations: rw},
	}, "X")
	unit.ParamsetReg.Add(reg, "DEV1:3", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"PRESS_SHORT": {Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
		"ON_TIME":     {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsWrite},
	}, "X")

	ops := &paramsetOps{values: map[string]map[string]any{"DEV1:2": {"STATE": false, "ENERGY": 12.5}}}
	set := &liteBackendSet{}
	set.put(hmenum.InterfaceHmIPRF, ops)
	s := &liteValueSeeder{unit: unit, centralName: "box", backends: set, logger: slog.New(slog.DiscardHandler)}
	out := map[string]map[string]any{
		"DEV1:1": {"STATE": true},
		"DEV1:2": {"STATE": true},
	}
	s.fillFromParamsets(context.Background(), hmenum.InterfaceHmIPRF, out)

	if !slices.Equal(ops.reads, []string{"DEV1:2"}) {
		t.Fatalf("VALUES reads = %v, want only DEV1:2", ops.reads)
	}
	if out["DEV1:2"]["STATE"] != true {
		t.Errorf("the paramset read overwrote the state store's STATE: %v", out["DEV1:2"])
	}
	if out["DEV1:2"]["ENERGY"] != 12.5 {
		t.Errorf("ENERGY = %v, want 12.5 from the paramset read", out["DEV1:2"]["ENERGY"])
	}
}

// TestLiteAnnouncerBlocksUntilInterfaceUp pins the announcement that
// replaces init on a lite interface: it waits while the stream is not live
// or the interface is down, returns once both hold, and gives up with an
// error when the caller's context ends first.
func TestLiteAnnouncerBlocksUntilInterfaceUp(t *testing.T) {
	t.Parallel()
	s := &liteStream{up: map[hmenum.Interface]bool{}, changed: make(chan struct{})}
	in := &liteEventIngress{stream: s}
	a := &liteAnnouncer{ingress: in, iface: hmenum.InterfaceHmIPRF}

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Init(short, "", ""); !errors.Is(err, errLiteInterfaceNotUp) {
		t.Fatalf("Init on a stream that is not live = %v, want errLiteInterfaceNotUp", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Init(context.Background(), "", "") }()
	s.setLive(true)
	select {
	case err := <-done:
		t.Fatalf("Init returned (%v) while the interface was still down", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.setUp(hmenum.InterfaceHmIPRF, true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Init = %v once live and up", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Init did not return once the interface came up")
	}
	if err := a.Deinit(context.Background(), ""); err != nil {
		t.Errorf("Deinit = %v, want nil (the box owns the subscription)", err)
	}
}

// reconcileOps lists a fixed inventory.
type reconcileOps struct {
	fakeOperations
	listed []hmproto.DeviceDescription
}

func (r *reconcileOps) ListDevices(context.Context) ([]hmproto.DeviceDescription, error) {
	return r.listed, nil
}

// TestLiteReconcileDeletesVanishedDevices pins the part of the lite
// reconciliation a CCU re-init does not need: a device the central knows
// but the interface no longer lists — its deleteDevices may have fallen
// into a gap of the stream — is deleted.
func TestLiteReconcileDeletesVanishedDevices(t *testing.T) {
	t.Parallel()
	_, unit := registryWithUnit(t, "box")
	wireID := WireInterfaceID("box", hmenum.InterfaceHmIPRF)
	wire := hmtypes.ParseWireInterfaceID(wireID)
	for _, addr := range []string{"KEEP1", "GONE1"} {
		unit.DeviceRegistry.Put(registry.DeviceEntry{Interface: wire, Address: addr, Model: "HmIP-X"})
	}
	ops := &reconcileOps{listed: []hmproto.DeviceDescription{{Address: "KEEP1", Type: "HmIP-X"}}}
	w := clientpkg.NewValueWriter()
	w.Register("box", wire, ops)
	h := NewCallbackHandlers(unit, nil)
	t.Cleanup(h.Stop)
	s := &liteStream{
		cc: config.CentralConfig{Name: "box"}, unit: unit, writer: w, handlers: h,
		logger:  slog.New(slog.DiscardHandler),
		initIDs: map[hmenum.Interface]string{hmenum.InterfaceHmIPRF: InitInterfaceID(unit.InstanceName(), "box", hmenum.InterfaceHmIPRF)},
		wireIDs: map[hmenum.Interface]string{hmenum.InterfaceHmIPRF: wireID},
		up:      map[hmenum.Interface]bool{}, changed: make(chan struct{}),
	}
	s.reconcile(context.Background(), hmenum.InterfaceHmIPRF)
	s.wg.Wait()
	if unit.DeviceRegistry.Has(wire, "GONE1") {
		t.Error("a device the interface no longer lists survived the reconciliation")
	}
	if !unit.DeviceRegistry.Has(wire, "KEEP1") {
		t.Error("a listed device was deleted")
	}
}
