// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_coordinator_link_firmware_test.go — tests for
// DeviceCoordinator.RefreshDeviceLinkPeers and
// DeviceCoordinator.RefreshFirmwareDataByState.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ─── stubs ───────────────────────────────────────────────────────────────────

// stubLinkPeerFetcher implements LinkPeerFetcher.
type stubLinkPeerFetcher struct {
	peers map[string][]string // channelAddress → peers
	err   error
}

func (s *stubLinkPeerFetcher) GetLinkPeers(_ context.Context, _ hmenum.Interface, channelAddress string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.peers[channelAddress], nil
}

// stubFirmwareStateReader implements FirmwareStateReader.
type stubFirmwareStateReader struct {
	states map[string]hmenum.DeviceFirmwareState // address → state
}

func (s *stubFirmwareStateReader) DeviceFirmwareStates(_ hmenum.Interface) map[string]hmenum.DeviceFirmwareState {
	return s.states
}

// stubListDevices implements DeviceDescriptionFetcher for RefreshFirmwareDataByState.
type stubDeviceFetcher struct {
	descs []hmproto.DeviceDescription
	err   error
	calls int
}

func (s *stubDeviceFetcher) ListDevices(_ context.Context, _ hmenum.Interface) ([]hmproto.DeviceDescription, error) {
	s.calls++
	return s.descs, s.err
}

// newDCForW10 creates a minimal DeviceCoordinator for wave-10 tests.
func newDCForW10(t *testing.T) (*DeviceCoordinator, *events.Bus, *registry.DeviceRegistry, *registry.DeviceDescriptionRegistry) {
	t.Helper()
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	psets := registry.NewParamsetRegistry()
	dc := NewDeviceCoordinator("c1", bus, devs, descs, psets, nil)
	return dc, bus, devs, descs
}

// seedDevice adds a top-level device and its channel to the registries.
func seedDevice(
	devs *registry.DeviceRegistry,
	descs *registry.DeviceDescriptionRegistry,
	iface hmenum.Interface,
	deviceAddr, channelAddr string,
) {
	devs.Put(registry.DeviceEntry{
		Interface: iface,
		Address:   deviceAddr,
		Model:     "HmIP-WTH",
	})
	descs.Put(iface, hmproto.DeviceDescription{
		Address: deviceAddr,
	})
	descs.Put(iface, hmproto.DeviceDescription{
		Address: channelAddr,
		Parent:  deviceAddr,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// RefreshDeviceLinkPeers
// ─────────────────────────────────────────────────────────────────────────────

// TestRefreshDeviceLinkPeersPublishesEvent verifies that a LinkPeerChangedEvent
// is published for each channel that the fetcher reports as having peers.
func TestRefreshDeviceLinkPeersPublishesEvent(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	seedDevice(devs, descs, iface, "ABC123", "ABC123:1")

	fetcher := &stubLinkPeerFetcher{
		peers: map[string][]string{
			"ABC123":   {}, // top-level device — no peers expected
			"ABC123:1": {"DEF456:1"},
		},
	}

	var got []hmevent.LinkPeerChangedEvent
	unsub := events.Subscribe(bus, func(e hmevent.LinkPeerChangedEvent) { got = append(got, e) })
	defer unsub()

	dc.RefreshDeviceLinkPeers(context.Background(), fetcher, iface, "ABC123")

	if len(got) != 1 {
		t.Fatalf("expected 1 LinkPeerChangedEvent, got %d", len(got))
	}
	if got[0].Address != "ABC123:1" {
		t.Errorf("event address: want ABC123:1, got %s", got[0].Address)
	}
	if len(got[0].Peers) != 1 || got[0].Peers[0] != "DEF456:1" {
		t.Errorf("event peers: want [DEF456:1], got %v", got[0].Peers)
	}
}

// TestRefreshDeviceLinkPeersNilFetcherIsNoOp verifies that passing a nil
// fetcher does not panic and emits no events.
func TestRefreshDeviceLinkPeersNilFetcherIsNoOp(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	seedDevice(devs, descs, iface, "ABC123", "ABC123:1")

	var called int
	unsub := events.Subscribe(bus, func(_ hmevent.LinkPeerChangedEvent) { called++ })
	defer unsub()

	dc.RefreshDeviceLinkPeers(context.Background(), nil, iface, "ABC123")

	if called != 0 {
		t.Errorf("expected 0 events with nil fetcher, got %d", called)
	}
}

// TestRefreshDeviceLinkPeersUnknownDeviceIsNoOp verifies that a device
// not in the registry causes no event emission.
func TestRefreshDeviceLinkPeersUnknownDeviceIsNoOp(t *testing.T) {
	t.Parallel()
	dc, bus, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	fetcher := &stubLinkPeerFetcher{peers: map[string][]string{"GHOST:1": {"X"}}}

	var called int
	unsub := events.Subscribe(bus, func(_ hmevent.LinkPeerChangedEvent) { called++ })
	defer unsub()

	dc.RefreshDeviceLinkPeers(context.Background(), fetcher, iface, "GHOST")

	if called != 0 {
		t.Errorf("expected 0 events for unknown device, got %d", called)
	}
}

// TestRefreshDeviceLinkPeersFetcherErrorSkipsChannel verifies that a CCU
// error on one channel does not abort the entire refresh.
func TestRefreshDeviceLinkPeersFetcherErrorSkipsChannel(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	devs.Put(registry.DeviceEntry{Interface: iface, Address: "D1"})
	descs.Put(iface, hmproto.DeviceDescription{Address: "D1"})
	descs.Put(iface, hmproto.DeviceDescription{Address: "D1:1", Parent: "D1"})
	descs.Put(iface, hmproto.DeviceDescription{Address: "D1:2", Parent: "D1"})

	fetcher := &stubLinkPeerFetcher{err: errors.New("CCU unavailable")}

	var called int
	unsub := events.Subscribe(bus, func(_ hmevent.LinkPeerChangedEvent) { called++ })
	defer unsub()

	// Should not panic even though every GetLinkPeers call fails.
	dc.RefreshDeviceLinkPeers(context.Background(), fetcher, iface, "D1")

	if called != 0 {
		t.Errorf("expected 0 events on fetch error, got %d", called)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RefreshFirmwareDataByState
// ─────────────────────────────────────────────────────────────────────────────

// TestRefreshFirmwareDataByStateCallsFetcherForMatchingDevice verifies that
// RefreshDeviceDescriptionsAndCreateMissingDevices is called when a device
// firmware state matches the filter.
func TestRefreshFirmwareDataByStateCallsFetcherForMatchingDevice(t *testing.T) {
	t.Parallel()
	dc, _, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF

	devFetcher := &stubDeviceFetcher{descs: nil}
	stateReader := &stubFirmwareStateReader{
		states: map[string]hmenum.DeviceFirmwareState{
			"D1": hmenum.DeviceFirmwareStateDeliverFirmwareImage,
		},
	}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		devFetcher,
		stateReader,
		iface,
		[]hmenum.DeviceFirmwareState{
			hmenum.DeviceFirmwareStateDeliverFirmwareImage,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devFetcher.calls != 1 {
		t.Errorf("expected 1 fetcher call, got %d", devFetcher.calls)
	}
}

// TestRefreshFirmwareDataByStateNoMatchDoesNotCallFetcher verifies that
// no fetcher call is made when no device matches the state filter.
func TestRefreshFirmwareDataByStateNoMatchDoesNotCallFetcher(t *testing.T) {
	t.Parallel()
	dc, _, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF

	devFetcher := &stubDeviceFetcher{}
	stateReader := &stubFirmwareStateReader{
		states: map[string]hmenum.DeviceFirmwareState{
			"D1": hmenum.DeviceFirmwareStateUpToDate,
		},
	}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		devFetcher,
		stateReader,
		iface,
		[]hmenum.DeviceFirmwareState{
			hmenum.DeviceFirmwareStateDeliverFirmwareImage,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devFetcher.calls != 0 {
		t.Errorf("expected 0 fetcher calls, got %d", devFetcher.calls)
	}
}

// TestRefreshFirmwareDataByStateNilFetcherReturnsError verifies that a nil
// fetcher returns an error.
func TestRefreshFirmwareDataByStateNilFetcherReturnsError(t *testing.T) {
	t.Parallel()
	dc, _, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	stateReader := &stubFirmwareStateReader{}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		nil,
		stateReader,
		iface,
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateDeliverFirmwareImage},
	)
	if err == nil {
		t.Error("expected error for nil fetcher, got nil")
	}
}

// TestRefreshFirmwareDataByStateNilReaderIsNoOp verifies that a nil
// FirmwareStateReader results in a silent no-op (no error, no calls).
func TestRefreshFirmwareDataByStateNilReaderIsNoOp(t *testing.T) {
	t.Parallel()
	dc, _, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	devFetcher := &stubDeviceFetcher{}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		devFetcher,
		nil,
		iface,
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateDeliverFirmwareImage},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devFetcher.calls != 0 {
		t.Errorf("expected 0 fetcher calls with nil reader, got %d", devFetcher.calls)
	}
}

// TestRefreshFirmwareDataByStateEmptyStatesIsNoOp verifies that an empty
// state slice is a no-op.
func TestRefreshFirmwareDataByStateEmptyStatesIsNoOp(t *testing.T) {
	t.Parallel()
	dc, _, _, _ := newDCForW10(t)

	const iface = hmenum.InterfaceHmIPRF
	devFetcher := &stubDeviceFetcher{}
	stateReader := &stubFirmwareStateReader{
		states: map[string]hmenum.DeviceFirmwareState{
			"D1": hmenum.DeviceFirmwareStateDeliverFirmwareImage,
		},
	}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		devFetcher,
		stateReader,
		iface,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devFetcher.calls != 0 {
		t.Errorf("expected 0 fetcher calls with empty states, got %d", devFetcher.calls)
	}
}
