// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Tests for DeviceReloaderAdapter — both ReloadDeviceConfig and
// ReloadChannelConfig.
//
// ReloadDeviceConfig: uses a getDescOps fake that records GetDeviceDescription
// calls and returns per-address descriptions. Assertions verify that
// GetDeviceDescription is called for the device and each child channel, that
// ListDevices is never called, and that a per-channel error is skipped rather
// than aborting the reload.
//
// ReloadChannelConfig: uses a listDevicesOps fake so that the
// backendDescFetcher path (which still calls ListDevices) can be exercised.
// For ReloadChannelConfig, fakeOperations.GetParamsetDescription returns
// (nil, nil), so the coordinator succeeds before the device-level refresh runs.

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// reloaderWireID is the canonical `<central>-<iface>` id the registries are
// keyed by. It is deliberately distinct from the bare interface name so a
// bare-vs-wire key mismatch in the code under test cannot hide behind a
// fixture that collapses the two spaces.
const reloaderWireID = "ccu-01-HmIP-RF"

// ─── fakes shared by multiple test groups ────────────────────────────────────

// listDevicesOps is a fake backends.Operations that records ListDevices
// calls. Used by the ReloadChannelConfig tests which still go through
// backendDescFetcher.
type listDevicesOps struct {
	fakeOperations

	listCalls   int
	returnDescs []hmproto.DeviceDescription
	returnErr   error
}

func (f *listDevicesOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	f.listCalls++
	return f.returnDescs, f.returnErr
}

// getDescOps is a fake backends.Operations that records GetDeviceDescription
// calls and returns configured responses per address. Used by the
// ReloadDeviceConfig tests which go through singleDeviceDescFetcher.
type getDescOps struct {
	fakeOperations
	// descByAddr maps address → raw description to return.
	descByAddr map[string]map[string]any
	// errByAddr maps address → error to return.
	errByAddr map[string]error
	// calledAddrs records all addresses GetDeviceDescription was called with.
	calledAddrs []string
	// listCalls counts ListDevices invocations (must stay 0 for ReloadDeviceConfig).
	listCalls int
}

func (f *getDescOps) GetDeviceDescription(_ context.Context, addr string) (map[string]any, error) {
	f.calledAddrs = append(f.calledAddrs, addr)
	if err, ok := f.errByAddr[addr]; ok {
		return nil, err
	}
	if m, ok := f.descByAddr[addr]; ok {
		return m, nil
	}
	return nil, nil
}

func (f *getDescOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	f.listCalls++
	return nil, nil
}

// ─── helper functions ─────────────────────────────────────────────────────────

// rawDeviceMap builds a minimal CCU wire-format map for a top-level device.
func rawDeviceMap(address, typ string, children []string) map[string]any {
	childSlice := make([]any, len(children))
	for i, c := range children {
		childSlice[i] = c
	}
	return map[string]any{
		"ADDRESS":   address,
		"TYPE":      typ,
		"CHILDREN":  childSlice,
		"PARAMSETS": []any{"MASTER", "VALUES"},
	}
}

// rawChannelMap builds a minimal CCU wire-format map for a channel.
func rawChannelMap(address, parent string) map[string]any {
	return map[string]any{
		"ADDRESS":   address,
		"TYPE":      "SWITCH_VIRTUAL_RECEIVER",
		"PARENT":    parent,
		"PARAMSETS": []any{"VALUES"},
	}
}

// buildReloaderFixture creates a central with one device registered and a
// listDevicesOps fake wired via a ValueWriter. Used by ReloadChannelConfig
// tests and by error-path tests that never reach the backend.
func buildReloaderFixture(
	t *testing.T,
	deviceAddr string,
	descs []hmproto.DeviceDescription,
	backendErr error,
) (*DeviceReloaderAdapter, *listDevicesOps) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: reloaderWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddr,
		Model:       "HmIP-STH",
		Name:        "Sensor",
	})
	c.ModelRegistry.Put(dev)

	fake := &listDevicesOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDescs:    descs,
		returnErr:      backendErr,
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", reloaderWireID, fake)

	return NewDeviceReloaderAdapter(reg, w), fake
}

// buildSingleDeviceFixture creates a central with one device registered and a
// getDescOps fake wired via a ValueWriter. Used by ReloadDeviceConfig tests.
func buildSingleDeviceFixture(
	t *testing.T,
	deviceAddr string,
	fake *getDescOps,
) (*DeviceReloaderAdapter, *central.Unit) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: reloaderWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddr,
		Model:       "HmIP-STH",
		Name:        "Sensor",
	})
	c.ModelRegistry.Put(dev)
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", reloaderWireID, fake)
	return NewDeviceReloaderAdapter(reg, w), c
}

// ─── ReloadDeviceConfig ───────────────────────────────────────────────────────

func TestReloadDeviceConfigFetchesSingleDeviceDescription(t *testing.T) {
	t.Parallel()

	children := []string{"0001ABCD:0", "0001ABCD:1"}
	fake := &getDescOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		descByAddr: map[string]map[string]any{
			"0001ABCD":   rawDeviceMap("0001ABCD", "HmIP-STH", children),
			"0001ABCD:0": rawChannelMap("0001ABCD:0", "0001ABCD"),
			"0001ABCD:1": rawChannelMap("0001ABCD:1", "0001ABCD"),
		},
	}
	a, unit := buildSingleDeviceFixture(t, "0001ABCD", fake)

	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("ReloadDeviceConfig: %v", err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListDevices must not be called, got %d calls", fake.listCalls)
	}
	// device + 2 channels = 3 GetDeviceDescription calls
	if len(fake.calledAddrs) != 3 {
		t.Errorf("expected 3 GetDeviceDescription calls (device + 2 channels), got %d: %v",
			len(fake.calledAddrs), fake.calledAddrs)
	}
	if fake.calledAddrs[0] != "0001ABCD" {
		t.Errorf("first GetDeviceDescription call = %q, want 0001ABCD", fake.calledAddrs[0])
	}
	// The refreshed descriptions must land under the canonical wire id. A
	// second, bare key space is invisible to every other reader and is what
	// makes the periodic firmware sweep ask the value writer for a backend
	// that cannot exist.
	if _, ok := unit.DescRegistry.Get(hmenum.Interface(reloaderWireID), "0001ABCD"); !ok {
		t.Error("reloaded device description not stored under the wire interface id")
	}
	for _, got := range unit.DescRegistry.GetInterfaceIDs() {
		if string(got) != reloaderWireID {
			t.Errorf("description registry gained the key %q; every key must be the wire id %q",
				got, reloaderWireID)
		}
	}
}

func TestReloadDeviceConfigPartialChannelErrorSkipped(t *testing.T) {
	t.Parallel()

	children := []string{"0001ABCD:0", "0001ABCD:1"}
	fake := &getDescOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		descByAddr: map[string]map[string]any{
			"0001ABCD":   rawDeviceMap("0001ABCD", "HmIP-STH", children),
			"0001ABCD:0": rawChannelMap("0001ABCD:0", "0001ABCD"),
			// 0001ABCD:1 intentionally absent
		},
		errByAddr: map[string]error{
			"0001ABCD:1": errors.New("channel temporarily unreachable"),
		},
	}
	a, _ := buildSingleDeviceFixture(t, "0001ABCD", fake)

	// A per-channel error must not abort the reload.
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("ReloadDeviceConfig should succeed despite one channel error, got: %v", err)
	}
}

func TestReloadDeviceConfigUnknownDeviceReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	err := adapter.ReloadDeviceConfig(context.Background(), "DEADBEEF")
	if err == nil {
		t.Fatal("expected error for unknown device address")
	}
}

func TestReloadDeviceConfigNilRegistryReturnsError(t *testing.T) {
	t.Parallel()

	a := NewDeviceReloaderAdapter(nil, clientpkg.NewValueWriter())
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when registry is nil")
	}
}

func TestReloadDeviceConfigNilWriterReturnsError(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	a := NewDeviceReloaderAdapter(reg, nil)
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when writer is nil")
	}
}

func TestReloadDeviceConfigNoBackendReturnsError(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: reloaderWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	c.ModelRegistry.Put(dev)

	// Writer has no backend registered for this central/interface.
	w := clientpkg.NewValueWriter()
	a := NewDeviceReloaderAdapter(reg, w)

	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when backend not registered")
	}
}

// ─── ReloadChannelConfig ──────────────────────────────────────────────────────

func TestReloadChannelConfigCallsBackend(t *testing.T) {
	t.Parallel()

	// The backend returns one description so RefreshDeviceDescriptionsAndCreateMissingDevices
	// has something to work with after the channel paramset re-pull.
	descs := []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Children: []string{"0001ABCD:0", "0001ABCD:1"}},
	}
	adapter, fake := buildReloaderFixture(t, "0001ABCD", descs, nil)

	if err := adapter.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err != nil {
		t.Fatalf("ReloadChannelConfig: %v", err)
	}
	// The device-level refresh fires after the channel paramset re-pull.
	if fake.listCalls != 1 {
		t.Errorf("expected 1 ListDevices call after channel reload, got %d", fake.listCalls)
	}
}

func TestReloadChannelConfigUnknownChannelReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	err := adapter.ReloadChannelConfig(context.Background(), "DEADBEEF:1")
	if err == nil {
		t.Fatal("expected error for unknown channel address")
	}
}

func TestReloadChannelConfigEmptyAddressReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	if err := adapter.ReloadChannelConfig(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty channel address")
	}
}

func TestReloadChannelConfigNilRegistryReturnsError(t *testing.T) {
	t.Parallel()

	a := NewDeviceReloaderAdapter(nil, clientpkg.NewValueWriter())
	if err := a.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err == nil {
		t.Fatal("expected error when registry is nil")
	}
}

func TestReloadChannelConfigNoBackendReturnsError(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: reloaderWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	c.ModelRegistry.Put(dev)

	// Writer has no backend registered for this central/interface.
	w := clientpkg.NewValueWriter()
	a := NewDeviceReloaderAdapter(reg, w)

	if err := a.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err == nil {
		t.Fatal("expected error when backend not registered")
	}
}

// ─── backendLinkPeerFetcher ───────────────────────────────────────────────────

// linkPeerRecordingOps embeds fakeOperations and records GetLinkPeers calls
// with configurable return values.
type linkPeerRecordingOps struct {
	fakeOperations
	calls       int
	lastChannel string
	returnPeers []string
	returnErr   error
}

func (f *linkPeerRecordingOps) GetLinkPeers(_ context.Context, channelAddr string) ([]string, error) {
	f.calls++
	f.lastChannel = channelAddr
	return f.returnPeers, f.returnErr
}

func TestBackendLinkPeerFetcher_ForwardsChannelAddress(t *testing.T) {
	t.Parallel()

	want := []string{"0009ZZZZ:1", "0009ZZZZ:2"}
	fake := &linkPeerRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnPeers:    want,
	}
	fetcher := &backendLinkPeerFetcher{ops: fake}

	got, err := fetcher.GetLinkPeers(context.Background(), hmenum.InterfaceHmIPRF, "0001ABCD:1")
	if err != nil {
		t.Fatalf("GetLinkPeers: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("expected 1 GetLinkPeers call, got %d", fake.calls)
	}
	if fake.lastChannel != "0001ABCD:1" {
		t.Errorf("channelAddress forwarded as %q, want 0001ABCD:1", fake.lastChannel)
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("got peers %v, want %v", got, want)
	}
}

func TestBackendLinkPeerFetcher_IgnoresIfaceArg(t *testing.T) {
	t.Parallel()

	// Calling with a different interface must still forward only the channel
	// address to the backend (iface is dropped because the backend is
	// already interface-scoped).
	fake := &linkPeerRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnPeers:    []string{"PEER:1"},
	}
	fetcher := &backendLinkPeerFetcher{ops: fake}

	_, err := fetcher.GetLinkPeers(context.Background(), hmenum.InterfaceHmIPRF, "CHAN:3")
	if err != nil {
		t.Fatalf("GetLinkPeers: %v", err)
	}
	if fake.lastChannel != "CHAN:3" {
		t.Errorf("channelAddress = %q, want CHAN:3", fake.lastChannel)
	}
}

// reloadWithLinkPeerOps is a combined fake that records both
// GetDeviceDescription and GetLinkPeers calls, used to verify that
// ReloadDeviceConfig triggers the link-peer refresh path.
type reloadWithLinkPeerOps struct {
	fakeOperations
	getDescCalls  int
	linkPeerCalls int
	descByAddr    map[string]map[string]any
}

func (f *reloadWithLinkPeerOps) GetDeviceDescription(_ context.Context, addr string) (map[string]any, error) {
	f.getDescCalls++
	if m, ok := f.descByAddr[addr]; ok {
		return m, nil
	}
	return nil, nil
}

func (f *reloadWithLinkPeerOps) GetLinkPeers(_ context.Context, _ string) ([]string, error) {
	f.linkPeerCalls++
	return nil, nil
}

func TestReloadDeviceConfigInvokesLinkPeerRefresh(t *testing.T) {
	t.Parallel()

	children := []string{"0001ABCD:0", "0001ABCD:1"}
	fake := &reloadWithLinkPeerOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		descByAddr: map[string]map[string]any{
			"0001ABCD":   rawDeviceMap("0001ABCD", "HmIP-STH", children),
			"0001ABCD:0": rawChannelMap("0001ABCD:0", "0001ABCD"),
			"0001ABCD:1": rawChannelMap("0001ABCD:1", "0001ABCD"),
		},
	}

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: reloaderWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Sensor",
	})
	c.ModelRegistry.Put(dev)

	// Seed the DeviceRegistry so RefreshDeviceLinkPeers can find the device.
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmenum.Interface(reloaderWireID),
		Address:   "0001ABCD",
		Model:     "HmIP-STH",
	})
	// Seed a channel description so the coordinator walks at least one channel.
	c.DescRegistry.Put(hmenum.Interface(reloaderWireID), hmproto.DeviceDescription{
		Address: "0001ABCD:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "0001ABCD",
	})

	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", reloaderWireID, fake)

	a := NewDeviceReloaderAdapter(reg, w)
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("ReloadDeviceConfig: %v", err)
	}
	if fake.getDescCalls == 0 {
		t.Errorf("expected GetDeviceDescription to be called, got 0")
	}
	// The DeviceCoordinator's RefreshDeviceLinkPeers walks every channel of
	// the device; each channel triggers one GetLinkPeers call on the backend.
	if fake.linkPeerCalls == 0 {
		t.Errorf("expected GetLinkPeers to be called at least once during reload, got 0")
	}
}
