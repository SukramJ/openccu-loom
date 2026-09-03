// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// hmAdpFakeBackend is a distinguishable [backends.Operations] stand-in. Only
// identity matters here — the resolver must hand back the backend registered
// for the interface id it was asked about.
type hmAdpFakeBackend struct {
	backends.Operations
	name string
}

// TestHmAdpCUxDHotplugResolverUsesTheRegisteredBackend pins that the device
// ingest seam the CUxD wiring installs resolves per interface id. The seam is
// per unit, so it is reached with every interface's wire id — an announcement
// or an accepted deferred device on HmIP-RF included. Answering the CUxD
// backend for all of them hydrated an HmIP device's paramsets over CUxD's
// BIN-RPC connection, and silently: the ingestor's nil-backend skip is never
// reached when the resolver always returns something.
func TestHmAdpCUxDHotplugResolverUsesTheRegisteredBackend(t *testing.T) {
	t.Parallel()

	cuxd := &hmAdpFakeBackend{name: "cuxd"}
	hmip := &hmAdpFakeBackend{name: "hmip"}

	reg := newBackendRegistry()
	reg.put("ccu-01-CUxD", cuxd)
	reg.put("ccu-01-HmIP-RF", hmip)

	resolve := cuxdHotplugBackendResolver(reg, "ccu-01-CUxD", cuxd)

	cases := []struct {
		interfaceID string
		want        backends.Operations
	}{
		{"ccu-01-HmIP-RF", hmip},
		{"ccu-01-CUxD", cuxd},
		{"ccu-01-BidCos-RF", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := resolve(tc.interfaceID)
		if got != tc.want {
			gotName, wantName := "nil", "nil"
			if fb, ok := got.(*hmAdpFakeBackend); ok {
				gotName = fb.name
			}
			if fb, ok := tc.want.(*hmAdpFakeBackend); ok {
				wantName = fb.name
			}
			t.Fatalf("resolve(%q) = %s, want %s", tc.interfaceID, gotName, wantName)
		}
	}
}

// TestHmAdpCUxDHotplugResolverFallsBackForItsOwnID covers the CUxD-only setup:
// with no shared registry the resolver still answers for CUxD's own wire id,
// and for nothing else.
func TestHmAdpCUxDHotplugResolverFallsBackForItsOwnID(t *testing.T) {
	t.Parallel()

	cuxd := &hmAdpFakeBackend{name: "cuxd"}
	resolve := cuxdHotplugBackendResolver(nil, "ccu-01-CUxD", cuxd)

	if got := resolve("ccu-01-CUxD"); got != backends.Operations(cuxd) {
		t.Fatalf("resolve(own id) = %v, want the CUxD backend", got)
	}
	if got := resolve("ccu-01-HmIP-RF"); got != nil {
		t.Fatalf("resolve(foreign id) = %v, want nil so the ingest is skipped", got)
	}
}

// hmAdpCUxDEndpoint starts a BIN-RPC server that answers as a CUxD addon
// would, and records every method call it receives. `listDevices` returns an
// empty inventory so the boot ingest of the CUxD interface succeeds without
// materialising anything — the wiring then reaches the point where it
// installs the hot-plug ingestor. Every other call is recorded and answered
// with an empty struct, so any traffic caused by a later announcement shows
// up here rather than failing the ingest.
type hmAdpCUxDEndpoint struct {
	mu    sync.Mutex
	calls []string
}

// sawAddress reports whether any recorded call carried addr as its first
// string parameter. A call for a foreign interface's device address is the
// observable defect: it means CUxD's own connection was used to hydrate a
// device that lives on another interface.
func (e *hmAdpCUxDEndpoint) sawAddress(addr string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.calls {
		if strings.Contains(c, addr) {
			return true
		}
	}
	return false
}

func hmAdpStartCUxDEndpoint(t *testing.T) (endpoint *hmAdpCUxDEndpoint, port int) {
	t.Helper()
	ep := &hmAdpCUxDEndpoint{}
	srv, err := binrpc.NewServer(binrpc.ServerConfig{Addr: "127.0.0.1:0", Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("binrpc.NewServer: %v", err)
	}
	record := func(method string) func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		return func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
			arg := ""
			if len(params) > 0 {
				arg, _ = xmlrpc.AsString(params[0])
			}
			ep.mu.Lock()
			ep.calls = append(ep.calls, method+"("+arg+")")
			ep.mu.Unlock()
			return xmlrpc.StructValue{}, nil
		}
	}
	srv.Mux().Handle("listDevices", func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		return xmlrpc.ArrayValue(nil), nil
	})
	for _, m := range []string{
		"getDeviceDescription", "getParamsetDescription", "getParamset",
		"getValue", "init", "ping", "getVersion",
	} {
		srv.Mux().Handle(m, record(m))
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-served
	})
	return ep, srv.Addr().(*net.TCPAddr).Port
}

// TestHmAdpCUxDWiringInstallsAPerInterfaceHotplugResolver guards the caller,
// not the helper: [cuxdHotplugBackendResolver] is correct on its own, but the
// defect was the closure [wireCUxDInterface] passed at the install site. With
// the old `func(string) backends.Operations { return cuxdBackend }` the
// per-unit device-ingest seam answered CUxD's backend for every interface id,
// so an HmIP-RF announcement was hydrated over CUxD's BIN-RPC socket. Calling
// the helper directly cannot see that; this drives the seam the wiring
// actually installed, through [central.Unit.IngestDevices] — the same entry
// the callback handlers use.
func TestHmAdpCUxDWiringInstallsAPerInterfaceHotplugResolver(t *testing.T) {
	t.Parallel()

	const (
		foreignAddr = "0011DBE9A5F6C1"
		cuxdAddr    = "CUX2801001"
	)

	c, err := central.New(central.Config{Name: "ccu-cuxd-hotplug"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ep, port := hmAdpStartCUxDEndpoint(t)
	logger := slog.New(slog.DiscardHandler)

	cc := config.CentralConfig{
		Name:  "ccu-cuxd-hotplug",
		Host:  "127.0.0.1",
		Ports: map[string]int{string(hmenum.InterfaceCUxD): port},
	}
	// A BIN-RPC callback server must be present: the hot-plug ingestor is
	// installed inside the branch that registers the callback handler, so
	// wiring without one installs no seam at all and every assertion below
	// would hold vacuously.
	cbServer, err := rpcserver.NewBINRPCServer(rpcserver.BINRPCConfig{Addr: "127.0.0.1:0", Logger: logger})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = cbServer.Close() })

	closer, ingested, err := wireCUxDInterface(
		context.Background(), cc, c,
		NewDevicePipeline(c),
		client.NewValueWriter(),
		nil, // runner: the ReGa surface is not on the resolver path
		config.ReliabilityConfig{},
		nil, // masterValues: the CUxD poller tolerates a nil store
		newBackendRegistry(),
		cbServer, cbServer.Addr().String(),
		nil, // adoptBINRPCHandlers: pinned separately in the deferred-creation test
		logger,
	)
	if err != nil {
		t.Fatalf("wireCUxDInterface: %v", err)
	}
	if closer != nil {
		t.Cleanup(closer)
	}
	if !ingested {
		t.Fatal("CUxD boot ingest failed — the wiring never reached the hot-plug install")
	}

	// Positive control: the seam IS installed and does reach CUxD's backend
	// for CUxD's own wire id. Without this the negative assertion below holds
	// just as well when no ingestor was installed at all.
	ownDescs := []hmproto.DeviceDescription{
		{Address: cuxdAddr, Type: "CUX2801001"},
		{Address: cuxdAddr + ":1", Parent: cuxdAddr, Type: "SWITCH"},
	}
	if err := c.IngestDevices(context.Background(), WireInterfaceID(cc.Name, hmenum.InterfaceCUxD), ownDescs); err != nil {
		t.Fatalf("IngestDevices(CUxD): %v", err)
	}
	if !ep.sawAddress(cuxdAddr) {
		t.Fatalf("the CUxD endpoint saw no call for its own device %s — no hot-plug ingestor is installed, "+
			"so this test cannot observe which backend the resolver hands back", cuxdAddr)
	}

	descs := []hmproto.DeviceDescription{
		{Address: foreignAddr, Type: "HmIP-PS"},
		{Address: foreignAddr + ":1", Parent: foreignAddr, Type: "SWITCH_VIRTUAL_RECEIVER"},
	}
	if err := c.IngestDevices(context.Background(), WireInterfaceID(cc.Name, hmenum.InterfaceHmIPRF), descs); err != nil {
		t.Fatalf("IngestDevices: %v", err)
	}

	if ep.sawAddress(foreignAddr) {
		t.Fatalf("the CUxD BIN-RPC endpoint was called for %s, a device on HmIP-RF — "+
			"the hot-plug resolver the wiring installed answers CUxD's backend for every interface id", foreignAddr)
	}
	if _, ok := c.ModelRegistry.Get(foreignAddr); ok {
		t.Fatalf("%s was materialised from a CUxD-resolved backend — "+
			"the announcement should have been skipped for want of a backend on its own interface", foreignAddr)
	}
}
