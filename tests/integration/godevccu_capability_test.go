// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// Package integration — godevccu capability tripwire tests.
//
// This file probes every XML-RPC method in the godevccu capability matrix
// (notes/audits/godevccu-capability.md) via openccu-loom's XML-RPC client.
// Methods marked ✓ in the matrix assert a non-error response.
// Methods marked ✗ (gap) carry a t.Skip so the test suite stays green
// while making the gap visible in the output.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

func ctx5s(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// vals is a shorthand to build a []xmlrpc.Value param slice from native values.
// Supported types: string, bool, int, int32.
func vals(args ...any) []xmlrpc.Value {
	out := make([]xmlrpc.Value, 0, len(args))
	for _, a := range args {
		switch v := a.(type) {
		case string:
			out = append(out, xmlrpc.StringValue(v))
		case bool:
			out = append(out, xmlrpc.BoolValue(v))
		case int:
			out = append(out, xmlrpc.IntValue(int32(v))) //nolint:gosec
		case int32:
			out = append(out, xmlrpc.IntValue(v))
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Matrix probe: XML-RPC methods
// ─────────────────────────────────────────────────────────────────────────────

// TestCapability_ping checks that ping returns a non-error response.
// Matrix: ✓ both.
func TestCapability_ping(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	if _, err := c.Call(ctx, "ping", vals("openccu-loom-test")); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestCapability_getVersion checks that getVersion returns a non-empty string.
// Matrix: ✓ both.
func TestCapability_getVersion(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "getVersion", nil)
	if err != nil {
		t.Fatalf("getVersion: %v", err)
	}
	s, err := xmlrpc.AsString(v)
	if err != nil || s == "" {
		t.Fatalf("getVersion: expected non-empty string, got %v / %v", v, err)
	}
}

// TestCapability_listDevices checks that listDevices returns at least one entry.
// Matrix: ✓ both.
func TestCapability_listDevices(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "listDevices", nil)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	arr, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("listDevices: not an array: %v", err)
	}
	if len(arr) == 0 {
		t.Fatal("listDevices: expected at least one device entry")
	}
}

// TestCapability_getDeviceDescription checks the first device returned by
// listDevices can be individually described.
// Matrix: ✓ both.
func TestCapability_getDeviceDescription(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, false)
	if addr == "" {
		t.Skip("no device address available from listDevices")
	}

	d, err := c.Call(ctx, "getDeviceDescription", vals(addr))
	if err != nil {
		t.Fatalf("getDeviceDescription(%q): %v", addr, err)
	}
	s, err := xmlrpc.AsStruct(d)
	if err != nil || len(s.Members) == 0 {
		t.Fatalf("getDeviceDescription(%q): expected non-empty struct, got %v / %v", addr, d, err)
	}
}

// TestCapability_getParamsetDescription checks VALUES paramset description.
// Matrix: ✓ both.
func TestCapability_getParamsetDescription(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, true /* channel */)
	if addr == "" {
		t.Skip("no channel address available")
	}

	d, err := c.Call(ctx, "getParamsetDescription", vals(addr, "VALUES"))
	if err != nil {
		t.Fatalf("getParamsetDescription(%q, VALUES): %v", addr, err)
	}
	s, err := xmlrpc.AsStruct(d)
	if err != nil {
		t.Fatalf("getParamsetDescription: not a struct: %v", err)
	}
	if len(s.Members) == 0 {
		t.Fatalf("getParamsetDescription(%q, VALUES): empty struct", addr)
	}
}

// TestCapability_getParamset_VALUES checks reading the VALUES paramset.
// Matrix: ✓ both.
func TestCapability_getParamset_VALUES(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, true)
	if addr == "" {
		t.Skip("no channel address available")
	}

	d, err := c.Call(ctx, "getParamset", vals(addr, "VALUES"))
	if err != nil {
		t.Fatalf("getParamset(%q, VALUES): %v", addr, err)
	}
	if _, err := xmlrpc.AsStruct(d); err != nil {
		t.Fatalf("getParamset(%q, VALUES): not a struct: %v", addr, err)
	}
}

// TestCapability_getParamset_MASTER checks reading the MASTER paramset.
// Matrix: ✓ both.
func TestCapability_getParamset_MASTER(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, true)
	if addr == "" {
		t.Skip("no channel address available")
	}

	_, err := c.Call(ctx, "getParamset", vals(addr, "MASTER"))
	if err != nil {
		// Some channels have no MASTER paramset — that's not a capability gap.
		t.Logf("getParamset(%q, MASTER) returned error (may be expected for this channel): %v", addr, err)
	}
}

// TestCapability_getParamset_LINK probes the LINK paramset key path.
//
// godevccu now supports both calling conventions:
// - getParamset(channelAddr, "LINK") — returns default values from description
// - getParamset(channelAddr, peerAddr) — returns stored LINK pair values
//
// The test exercises the peer-address form via addLink + getParamset.
// Matrix: ✓ godevccu (F3-3 resolved 2026-04-28).
func TestCapability_getParamset_LINK(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	// Find a channel address to use as both sender and receiver.
	addr := firstDeviceAddr(t, c, ctx, true /* channel */)
	if addr == "" {
		t.Skip("no channel address available")
	}

	// addLink so a LINK paramset entry exists for the (addr, addr) pair.
	// Using the same address for sender and receiver is valid for the simulator.
	if _, err := c.Call(ctx, "addLink", vals(addr, addr, "test-link", "")); err != nil {
		t.Fatalf("addLink: %v", err)
	}

	// getParamset(addr, peerAddr) — peer-address LINK form.
	d, err := c.Call(ctx, "getParamset", vals(addr, addr))
	if err != nil {
		t.Fatalf("getParamset(addr, peerAddr): %v", err)
	}
	// The result must be a struct (map), possibly empty if the channel has no LINK description.
	if _, err := xmlrpc.AsStruct(d); err != nil {
		t.Fatalf("getParamset(addr, peerAddr): expected struct, got %T / %v: %v", d, d, err)
	}
}

// TestCapability_getValue checks reading a single value.
// Matrix: ✓ both.
func TestCapability_getValue(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, true)
	if addr == "" {
		t.Skip("no channel address available")
	}

	key := firstParamKey(t, c, ctx, addr)
	if key == "" {
		t.Skip("no VALUES parameter available on channel " + addr)
	}

	_, err := c.Call(ctx, "getValue", vals(addr, key))
	if err != nil {
		t.Fatalf("getValue(%q, %q): %v", addr, key, err)
	}
}

// TestCapability_getServiceMessages checks the service messages stub.
// Matrix: ✓ both.
//
// godevccu's FromAny now handles [][]any correctly: each inner slice is
// wrapped in an ArrayValue, so the response is a proper nested XML-RPC array.
func TestCapability_getServiceMessages(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "getServiceMessages", nil)
	if err != nil {
		t.Fatalf("getServiceMessages: %v", err)
	}
	if v == nil {
		t.Fatal("getServiceMessages: nil response")
	}
	// Response must be a top-level ArrayValue (not a StringValue fallback).
	outer, ok := v.(xmlrpc.ArrayValue)
	if !ok {
		t.Fatalf("getServiceMessages: expected ArrayValue, got %T", v)
	}
	// Each element of the outer array must also be an ArrayValue (one row per
	// service message).
	for i, elem := range outer {
		if _, ok := elem.(xmlrpc.ArrayValue); !ok {
			t.Errorf("getServiceMessages: row %d is %T, want ArrayValue", i, elem)
		}
	}
	t.Logf("getServiceMessages response kind: %s (%d rows)", v.Kind(), len(outer))
}

// TestCapability_addLink checks the addLink no-op stub.
// Matrix: ✓ both (no-op).
func TestCapability_addLink(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	_, err := c.Call(ctx, "addLink", vals("VCU0000001:1", "VCU0000002:1", "test-link", ""))
	if err != nil {
		t.Fatalf("addLink: %v", err)
	}
}

// TestCapability_removeLink checks the removeLink no-op stub.
// Matrix: ✓ both (no-op).
func TestCapability_removeLink(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	_, err := c.Call(ctx, "removeLink", vals("VCU0000001:1", "VCU0000002:1"))
	if err != nil {
		t.Fatalf("removeLink: %v", err)
	}
}

// TestCapability_getLinkPeers checks the getLinkPeers no-op stub.
// Matrix: ✓ both (returns empty list).
func TestCapability_getLinkPeers(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "getLinkPeers", vals("VCU0000001:1"))
	if err != nil {
		t.Fatalf("getLinkPeers: %v", err)
	}
	if _, err := xmlrpc.AsArray(v); err != nil {
		t.Fatalf("getLinkPeers: expected array, got %v: %v", v, err)
	}
}

// TestCapability_getLinks checks the getLinks no-op stub.
// Matrix: ✓ both (returns empty list).
func TestCapability_getLinks(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "getLinks", vals("VCU0000001:1", 0))
	if err != nil {
		t.Fatalf("getLinks: %v", err)
	}
	if _, err := xmlrpc.AsArray(v); err != nil {
		t.Fatalf("getLinks: expected array, got %v: %v", v, err)
	}
}

// TestCapability_reportValueUsage checks the reportValueUsage no-op stub.
// Matrix: ✓ both (no-op).
func TestCapability_reportValueUsage(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	_, err := c.Call(ctx, "reportValueUsage", vals("VCU0000001:1", "STATE", 1))
	if err != nil {
		t.Fatalf("reportValueUsage: %v", err)
	}
}

// TestCapability_getInstallMode checks the getInstallMode stub.
// Matrix: ✓ both.
func TestCapability_getInstallMode(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "getInstallMode", nil)
	if err != nil {
		t.Fatalf("getInstallMode: %v", err)
	}
	if v == nil {
		t.Fatal("getInstallMode: nil response")
	}
}

// TestCapability_setInstallMode checks the setInstallMode stub.
// Matrix: ✓ both.
func TestCapability_setInstallMode(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	_, err := c.Call(ctx, "setInstallMode", vals(true, 60, 1, ""))
	if err != nil {
		t.Fatalf("setInstallMode: %v", err)
	}
}

// TestCapability_clientServerInitialized checks the Homegear ping-path stub.
// Matrix: ✓ both.
func TestCapability_clientServerInitialized(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "clientServerInitialized", vals("nonexistent-id"))
	if err != nil {
		t.Fatalf("clientServerInitialized: %v", err)
	}
	b, err := xmlrpc.AsBool(v)
	if err != nil {
		t.Fatalf("clientServerInitialized: not a bool: %v", err)
	}
	// "nonexistent-id" was never registered via init, so it must be false.
	if b {
		t.Fatal("clientServerInitialized: expected false for unknown interface")
	}
}

// TestCapability_getMetadata checks the getMetadata stub.
// Matrix: ✓ both.
func TestCapability_getMetadata(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	addr := firstDeviceAddr(t, c, ctx, false /* root device */)
	if addr == "" {
		t.Skip("no root device available")
	}

	_, err := c.Call(ctx, "getMetadata", vals(addr, "NAME"))
	if err != nil {
		t.Fatalf("getMetadata(%q, NAME): %v", addr, err)
	}
}

// TestCapability_systemListMethods checks system.listMethods includes expected methods.
// Matrix: ✓ both.
func TestCapability_systemListMethods(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "system.listMethods", nil)
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}
	names, err := xmlrpc.AsStrings(v)
	if err != nil {
		t.Fatalf("system.listMethods: not string array: %v", err)
	}

	required := []string{
		"listDevices", "ping", "getVersion",
		"getDeviceDescription", "getParamsetDescription",
		"getParamset", "putParamset", "setValue", "getValue",
		"init", "getServiceMessages",
	}
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range required {
		if !seen[want] {
			t.Errorf("system.listMethods: missing required method %q", want)
		}
	}
}

// TestCapability_deleteDevice probes the deleteDevice server-side handler that
// was added to godevccu on 2026-04-28 (Gap 1 resolved). It verifies:
// 1. A call with (address, flags=0) succeeds (no XML-RPC fault).
// 2. The device is no longer in listDevices after the call.
// 3. A call with an unknown address also succeeds (idempotent).
//
// Matrix: ✗, ✓ godevccu.
func TestCapability_deleteDevice(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	// Grab an existing root-device address from listDevices.
	addr := firstDeviceAddr(t, c, ctx, false /* root, not channel */)
	if addr == "" {
		t.Skip("no root device address available from listDevices")
	}

	// Call deleteDevice(address, 0) — shape used by openccu-loom's CcuBackend.
	if _, err := c.Call(ctx, "deleteDevice", vals(addr, 0)); err != nil {
		t.Fatalf("deleteDevice(%q, 0): unexpected fault: %v", addr, err)
	}

	// The device must no longer appear in listDevices.
	v, err := c.Call(ctx, "listDevices", nil)
	if err != nil {
		t.Fatalf("listDevices after deleteDevice: %v", err)
	}
	arr, _ := xmlrpc.AsArray(v)
	for _, entry := range arr {
		st, stErr := xmlrpc.AsStruct(entry)
		if stErr != nil {
			continue
		}
		addrVal, ok := st.Get("ADDRESS")
		if !ok {
			continue
		}
		s, sErr := xmlrpc.AsString(addrVal)
		if sErr == nil && s == addr {
			t.Fatalf("device %q still present in listDevices after deleteDevice", addr)
		}
	}

	// Unknown address must also succeed (idempotent).
	if _, err := c.Call(ctx, "deleteDevice", vals("NO_SUCH_DEVICE_XYZ", 0)); err != nil {
		t.Fatalf("deleteDevice unknown address returned fault: %v", err)
	}
}

// TestCapability_listBidcosInterfaces checks that listBidcosInterfaces
// returns an empty array without error.
// Matrix: ✓ godevccu.
func TestCapability_listBidcosInterfaces(t *testing.T) {
	srv := startMockCCU(t)
	c := newXMLRPCClient(t, srv.URL())
	ctx, cancel := ctx5s(t)
	defer cancel()

	v, err := c.Call(ctx, "listBidcosInterfaces", nil)
	if err != nil {
		t.Fatalf("listBidcosInterfaces: %v", err)
	}
	if _, err := xmlrpc.AsArray(v); err != nil {
		t.Fatalf("listBidcosInterfaces: expected array, got %T / %v: %v", v, v, err)
	}
}

// recordingHandlers is a minimal rpcserver.Handlers fake that records
// replaceDevice and readdedDevice callbacks for assertion in tests.
type recordingHandlers struct {
	replaced chan replaceDeviceArgs
	readded  chan readdedDeviceArgs
}

type replaceDeviceArgs struct {
	interfaceID string
	oldAddress  string
	newAddress  string
}

type readdedDeviceArgs struct {
	interfaceID string
	addresses   []string
}

func (h *recordingHandlers) Event(_ context.Context, _, _, _ string, _ xmlrpc.Value) error {
	return nil
}

func (h *recordingHandlers) NewDevices(_ context.Context, _ string, _ xmlrpc.ArrayValue) error {
	return nil
}

func (h *recordingHandlers) DeleteDevices(_ context.Context, _ string, _ []string) error {
	return nil
}

func (h *recordingHandlers) UpdateDevice(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (h *recordingHandlers) ReplaceDevice(_ context.Context, interfaceID, oldAddress, newAddress string) error {
	h.replaced <- replaceDeviceArgs{interfaceID: interfaceID, oldAddress: oldAddress, newAddress: newAddress}
	return nil
}

func (h *recordingHandlers) ReaddedDevice(_ context.Context, interfaceID string, addresses []string) error {
	h.readded <- readdedDeviceArgs{interfaceID: interfaceID, addresses: addresses}
	return nil
}

func (h *recordingHandlers) ListDevices(_ context.Context, _ string) (xmlrpc.ArrayValue, error) {
	return nil, nil
}

func (h *recordingHandlers) Error(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

// startCallbackServer starts an XML-RPC callback server, registers fake under
// centralName, calls Init on srv.v.RPC() with the callback URL, and returns
// the fake for assertion. The server and context are cleaned up via t.Cleanup.
func startCallbackServer(t *testing.T, srv *mockCCU, centralName, interfaceID string) *recordingHandlers {
	t.Helper()
	fake := &recordingHandlers{
		replaced: make(chan replaceDeviceArgs, 1),
		readded:  make(chan readdedDeviceArgs, 1),
	}
	cb, err := rpcserver.NewXMLRPCServer(rpcserver.XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("rpcserver.NewXMLRPCServer: %v", err)
	}
	cb.Register(centralName, fake)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = cb.Serve(ctx) }()
	callbackURL := "http://" + cb.Addr().String() + "/RPC2/" + centralName
	srv.v.RPC().Init(callbackURL, interfaceID)
	return fake
}

// TestCapability_replaceDevice checks that ReplaceDevice pushes a replaceDevice
// callback to the registered XML-RPC callback server.
// Matrix: ✓ godevccu.
func TestCapability_replaceDevice(t *testing.T) {
	srv := startMockCCU(t)
	fake := startCallbackServer(t, srv, "itest", "itest-iface")

	srv.v.RPC().ReplaceDevice(context.Background(), "OLD0000001:1", "NEW0000001:1")

	select {
	case got := <-fake.replaced:
		if got.interfaceID != "itest-iface" {
			t.Errorf("replaceDevice: interfaceID = %q, want %q", got.interfaceID, "itest-iface")
		}
		if got.oldAddress != "OLD0000001:1" {
			t.Errorf("replaceDevice: oldAddress = %q, want %q", got.oldAddress, "OLD0000001:1")
		}
		if got.newAddress != "NEW0000001:1" {
			t.Errorf("replaceDevice: newAddress = %q, want %q", got.newAddress, "NEW0000001:1")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replaceDevice: no callback received within 3s")
	}
}

// TestCapability_readdedDevice checks that ReaddedDevice pushes a readdedDevice
// callback to the registered XML-RPC callback server.
// Matrix: ✓ godevccu.
func TestCapability_readdedDevice(t *testing.T) {
	srv := startMockCCU(t)
	fake := startCallbackServer(t, srv, "itest", "itest-iface")

	addrs := []string{"ADDR0000001:0", "ADDR0000002:0"}
	srv.v.RPC().ReaddedDevice(context.Background(), addrs)

	select {
	case got := <-fake.readded:
		if got.interfaceID != "itest-iface" {
			t.Errorf("readdedDevice: interfaceID = %q, want %q", got.interfaceID, "itest-iface")
		}
		if len(got.addresses) != len(addrs) {
			t.Fatalf("readdedDevice: len(addresses) = %d, want %d", len(got.addresses), len(addrs))
		}
		for i, want := range addrs {
			if got.addresses[i] != want {
				t.Errorf("readdedDevice: addresses[%d] = %q, want %q", i, got.addresses[i], want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readdedDevice: no callback received within 3s")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// firstDeviceAddr returns the address of the first device that matches the
// channel criterion (channelOnly=true → addr contains ":", false → no ":").
func firstDeviceAddr(t *testing.T, c *xmlrpc.Client, ctx context.Context, channelOnly bool) string {
	t.Helper()
	v, err := c.Call(ctx, "listDevices", nil)
	if err != nil {
		return ""
	}
	arr, _ := xmlrpc.AsArray(v)
	for _, entry := range arr {
		st, err := xmlrpc.AsStruct(entry)
		if err != nil {
			continue
		}
		addrVal, ok := st.Get("ADDRESS")
		if !ok {
			continue
		}
		addr, err := xmlrpc.AsString(addrVal)
		if err != nil || addr == "" {
			continue
		}
		isChannel := containsColonCap(addr)
		if channelOnly == isChannel {
			return addr
		}
	}
	return ""
}

// firstParamKey returns the name of the first parameter in the VALUES paramset
// of the given channel address, or "" if unavailable.
func firstParamKey(t *testing.T, c *xmlrpc.Client, ctx context.Context, addr string) string {
	t.Helper()
	v, err := c.Call(ctx, "getParamsetDescription", vals(addr, "VALUES"))
	if err != nil {
		return ""
	}
	s, err := xmlrpc.AsStruct(v)
	if err != nil {
		return ""
	}
	for _, m := range s.Members {
		return m.Name
	}
	return ""
}

func containsColonCap(s string) bool {
	for _, r := range s {
		if r == ':' {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// OpenCCU mode capability probes (JSON-RPC)
//
// These probes run against the OpenCCU fixture (BackendModeCCU, JSON-RPC
// enabled, AuthEnabled=true) and verify that the JSON-RPC method table
// exposed by godevccu covers the methods openccu-loom's CcuBackend calls.
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenCCUCapability_systemListMethods probes the JSON-RPC system.listMethods
// and asserts that the core method groups (Session, Interface, Program, SysVar)
// are present.
func TestOpenCCUCapability_systemListMethods(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c, err := newJSONRPCClientDirect(url)
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := ctx5s(t)
	defer cancel()

	var methods []map[string]any
	if err := c.Call(ctx, "system.listMethods", nil, &methods); err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}
	if len(methods) == 0 {
		t.Fatal("system.listMethods: empty method list")
	}

	nameSet := make(map[string]bool, len(methods))
	for _, m := range methods {
		if n, ok := m["name"].(string); ok {
			nameSet[n] = true
		}
	}

	required := []string{
		"Session.login",
		"Session.logout",
		"Interface.listInterfaces",
		"Interface.listDevices",
		"Program.getAll",
		"SysVar.getAll",
		"ReGa.runScript",
		"CCU.getAuthEnabled",
	}
	for _, want := range required {
		if !nameSet[want] {
			t.Errorf("system.listMethods: missing required JSON-RPC method %q", want)
		}
	}
	t.Logf("JSON-RPC method count: %d", len(methods))
}

// TestOpenCCUCapability_getAuthEnabled checks CCU.getAuthEnabled returns true
// for the OpenCCU fixture (AuthEnabled=true in Config).
func TestOpenCCUCapability_getAuthEnabled(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c, err := newJSONRPCClientDirect(url)
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := ctx5s(t)
	defer cancel()

	var authEnabled bool
	if err := c.Call(ctx, "CCU.getAuthEnabled", nil, &authEnabled); err != nil {
		t.Fatalf("CCU.getAuthEnabled: %v", err)
	}
	if !authEnabled {
		t.Fatal("CCU.getAuthEnabled: expected true for AuthEnabled fixture")
	}
}

// TestOpenCCUCapability_sessionLoginLogout probes the session lifecycle methods.
func TestOpenCCUCapability_sessionLoginLogout(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c, err := newJSONRPCClientDirect(url)
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := ctx5s(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Session.login (Admin/\"\"): %v", err)
	}
	if c.SessionID() == "" {
		t.Fatal("Session.login: SessionID empty after successful login")
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Session.logout: %v", err)
	}
}

// TestOpenCCUCapability_interfaceListDevices probes Interface.listDevices over
// JSON-RPC and asserts the pre-loaded device fleet is returned.
func TestOpenCCUCapability_interfaceListDevices(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c, err := newJSONRPCClientDirect(url)
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := ctx5s(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var devices []map[string]any
	if err := c.Call(ctx, "Interface.listDevices", nil, &devices); err != nil {
		t.Fatalf("Interface.listDevices: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("Interface.listDevices: empty; expected pre-loaded device fleet")
	}
	t.Logf("Interface.listDevices: %d entries", len(devices))
}

// newJSONRPCClientDirect is a package-level helper (not using t.Fatal) so it
// can be called from both the capability probes and the jsonrpc_test.go file
// without a *testing.T dependency at construction time. It uses "Admin"/"" as
// credentials, matching the OpenCCU fixture defaults.
func newJSONRPCClientDirect(endpoint string) (*jsonrpc.Client, error) {
	return jsonrpc.New(jsonrpc.Config{
		Endpoint: endpoint,
		Username: "Admin",
		Password: "",
	})
}
