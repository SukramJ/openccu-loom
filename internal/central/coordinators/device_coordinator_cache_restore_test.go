// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_coordinator_cache_restore_test.go covers:
// DeviceCoordinator.CheckAndCreateDevicesFromCache,
// DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices,
// DeviceCoordinator.AddNewDevicesManually + StoreDelayedDeviceDescriptions,
// DeviceCoordinator.CheckParamsetConsistency,
// DeviceCoordinator.ScheduleParamsetConsistencyCheck.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Note: stubLister and newDCFull/collectCreated/collectRemoved are defined in
// device_pull_test.go and device_deep_test.go respectively — shared across the
// coordinators package test binary.

// stubParamsetChecker fakes GetParamset for the consistency check.
type stubParamsetChecker struct {
	results map[string]map[string]any // channelAddress → paramName → value
	err     error
}

func (s *stubParamsetChecker) GetParamset(_ context.Context, channelAddress string, _ hmenum.ParamsetKey) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.results[channelAddress]; ok {
		return v, nil
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// CheckAndCreateDevicesFromCache
// ---------------------------------------------------------------------------

func TestCheckAndCreateDevicesFromCacheCreatesNewDevices(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	created := collectCreated(bus)

	// Seed the description registry directly (simulates persisted cache).
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "AA",
		Type:     "HmIP-X",
		Firmware: "1.0",
	})
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "AA:0",
		Parent:  "AA",
		Type:    "MAINTENANCE",
	})

	if devs.Len() != 0 {
		t.Fatal("device registry must be empty before cache restore")
	}

	if err := dc.CheckAndCreateDevicesFromCache(context.Background()); err != nil {
		t.Fatal(err)
	}

	if devs.Len() != 1 {
		t.Fatalf("DeviceRegistry len=%d after cache restore, want 1", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "AA"); !ok {
		t.Fatal("AA must be in DeviceRegistry after cache restore")
	}
	if len(*created) != 1 || (*created)[0].Address != "AA" {
		t.Fatalf("expected 1 DeviceCreatedEvent for AA, got %+v", *created)
	}
	if (*created)[0].Source != hmenum.SourceOfDeviceCreationCache {
		t.Fatalf("source=%v, want CACHE", (*created)[0].Source)
	}
}

func TestCheckAndCreateDevicesFromCacheIsIdempotent(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.DeviceCreatedEvent) { count.Add(1) })

	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "BB",
		Type:    "HmIP-Y",
	})

	// First restore.
	if err := dc.CheckAndCreateDevicesFromCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 1 || count.Load() != 1 {
		t.Fatalf("after first call devs=%d events=%d, want both 1", devs.Len(), count.Load())
	}

	// Second restore must be a no-op.
	if err := dc.CheckAndCreateDevicesFromCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 1 || count.Load() != 1 {
		t.Fatalf("after second call devs=%d events=%d, want still both 1", devs.Len(), count.Load())
	}
}

func TestCheckAndCreateDevicesFromCacheEmptyRegistryIsNoop(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.DeviceCreatedEvent) { count.Add(1) })

	if err := dc.CheckAndCreateDevicesFromCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 0 || count.Load() != 0 {
		t.Fatalf("empty registry: devs=%d events=%d, want both 0", devs.Len(), count.Load())
	}
}

// TestCheckAndCreateDevicesFromCacheSubscriberCallingBackDoesNotDeadlock pins
// the invariant that the DeviceCreatedEvent fired for a cache-restored device
// is published after c.mu is released. events.Publish dispatches every
// handler synchronously on the calling goroutine, so a subscriber that calls
// back into the coordinator (as RenameNewDeviceFromOverride does for real
// callers reacting to device creation) must not block on the same mutex
// CheckAndCreateDevicesFromCache is still holding.
func TestCheckAndCreateDevicesFromCacheSubscriberCallingBackDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)

	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "AA",
		Type:    "HmIP-X",
	})

	events.Subscribe(bus, func(_ hmevent.DeviceCreatedEvent) {
		dc.RenameNewDeviceFromOverride(hmenum.InterfaceHmIPRF, "AA", func(string, string) {})
	})

	done := make(chan error, 1)
	go func() {
		done <- dc.CheckAndCreateDevicesFromCache(context.Background())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("CheckAndCreateDevicesFromCache deadlocked: a DeviceCreatedEvent " +
			"subscriber calling back into the coordinator must not block on c.mu")
	}

	if devs.Len() != 1 {
		t.Fatalf("devs=%d, want 1", devs.Len())
	}
}

// ---------------------------------------------------------------------------
// RefreshDeviceDescriptionsAndCreateMissingDevices
// ---------------------------------------------------------------------------

func TestRefreshDeviceDescriptionsAndCreateMissingDevices(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	created := collectCreated(bus)

	// Seed with one pre-existing device.
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "OLD", Type: "HmIP-A"})
	devs.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "OLD", Model: "HmIP-A"})

	fetcher := &stubLister{snapshot: []hmproto.DeviceDescription{
		{Address: "OLD", Type: "HmIP-A"},
		{Address: "NEW", Type: "HmIP-B"},
		{Address: "NEW:0", Parent: "NEW", Type: "MAINTENANCE"},
	}}

	if err := dc.RefreshDeviceDescriptionsAndCreateMissingDevices(
		context.Background(), fetcher, hmenum.InterfaceHmIPRF,
	); err != nil {
		t.Fatal(err)
	}

	if devs.Len() != 2 {
		t.Fatalf("devs=%d after refresh, want 2", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "NEW"); !ok {
		t.Fatal("NEW must be in DeviceRegistry after refresh")
	}
	// Only NEW fires a created event; OLD already existed.
	if len(*created) != 1 || (*created)[0].Address != "NEW" {
		t.Fatalf("created events=%+v, want single NEW", *created)
	}
	if (*created)[0].Source != hmenum.SourceOfDeviceCreationRefresh {
		t.Fatalf("source=%v, want REFRESH", (*created)[0].Source)
	}
}

func TestRefreshDeviceDescriptionsNilFetcherErrors(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	if err := dc.RefreshDeviceDescriptionsAndCreateMissingDevices(
		context.Background(), nil, hmenum.InterfaceHmIPRF,
	); err == nil {
		t.Fatal("nil fetcher must return error")
	}
}

func TestRefreshDeviceDescriptionsFetcherErrorPropagates(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	fetcher := &stubLister{err: errors.New("network error")}
	err := dc.RefreshDeviceDescriptionsAndCreateMissingDevices(
		context.Background(), fetcher, hmenum.InterfaceHmIPRF,
	)
	if err == nil {
		t.Fatal("fetcher error must propagate")
	}
}

// ---------------------------------------------------------------------------
// AddNewDevicesManually + StoreDelayedDeviceDescriptions
// ---------------------------------------------------------------------------

func TestAddNewDevicesManuallyAcceptsDelayedDescriptions(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	created := collectCreated(bus)

	// Simulate a newDevices callback storing delayed descriptions.
	dc.StoreDelayedDeviceDescriptions(hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
		{Address: "AA:0", Parent: "AA", Type: "MAINTENANCE"},
	})

	var accepted []string
	acceptFn := func(_ context.Context, _ hmenum.Interface, address string) error {
		accepted = append(accepted, address)
		return nil
	}

	if err := dc.AddNewDevicesManually(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		map[string]string{"AA": "My Device"},
		acceptFn,
	); err != nil {
		t.Fatal(err)
	}

	if devs.Len() != 1 {
		t.Fatalf("devs=%d, want 1", devs.Len())
	}
	if _, ok := devs.Get(hmenum.InterfaceHmIPRF, "AA"); !ok {
		t.Fatal("AA must be in DeviceRegistry")
	}
	if len(*created) != 1 || (*created)[0].Source != hmenum.SourceOfDeviceCreationManual {
		t.Fatalf("created=%+v, want 1 MANUAL event", *created)
	}
	if len(accepted) != 1 || accepted[0] != "AA" {
		t.Fatalf("acceptor called with %v, want [AA]", accepted)
	}
}

func TestAddNewDevicesManuallyUnknownAddressIsSkipped(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)

	if err := dc.AddNewDevicesManually(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		map[string]string{"GHOST": ""},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if devs.Len() != 0 {
		t.Fatal("unknown address must not create a device")
	}
}

func TestStoreDelayedAndManualCleansUpEmptyInterfaceEntry(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	dc.StoreDelayedDeviceDescriptions(hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "AA", Type: "HmIP-X"},
	})

	// Accept the only delayed device.
	if err := dc.AddNewDevicesManually(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		map[string]string{"AA": ""},
		nil,
	); err != nil {
		t.Fatal(err)
	}

	// The delayed entry must be cleaned up: try accepting again — no events.
	dc2, bus2, devs2, _, _ := newDCFull(t)
	var count atomic.Int32
	events.Subscribe(bus2, func(_ hmevent.DeviceCreatedEvent) { count.Add(1) })
	_ = dc2.AddNewDevicesManually(context.Background(), hmenum.InterfaceHmIPRF,
		map[string]string{"AA": ""}, nil)
	if devs2.Len() != 0 || count.Load() != 0 {
		t.Fatal("second AddNewDevicesManually on empty delayed should be a no-op")
	}

	// Verify the original dc's delayed map is empty for the interface.
	dc.mu.Lock()
	_, stillExists := dc.delayedDescs[string(hmenum.InterfaceHmIPRF)]
	dc.mu.Unlock()
	if stillExists {
		t.Fatal("delayed map for interface must be cleaned up after all devices accepted")
	}
}

// ---------------------------------------------------------------------------
// CheckParamsetConsistency
// ---------------------------------------------------------------------------

func TestCheckParamsetConsistencyNilCheckerErrors(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	_, err := dc.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, []string{"AA"}, nil)
	if err == nil {
		t.Fatal("nil checker must return error")
	}
}

func TestCheckParamsetConsistencyNoHmIPInterfaceSkips(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)
	checker := &stubParamsetChecker{}
	// BidCos-RF is not affected by the HmIPServer bug.
	result, err := dc.CheckParamsetConsistency(
		context.Background(),
		hmenum.InterfaceBidCosRF,
		[]string{"AA"},
		checker,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("non-HmIP interface must return no inconsistencies, got %+v", result)
	}
}

func TestCheckParamsetConsistencyDetectsStaleParams(t *testing.T) {
	t.Parallel()
	dc, _, _, descs, psets := newDCFull(t)

	// Register a device with one channel on HmIP-RF.
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "AA",
		Type:     "HmIP-X",
		Children: []string{"AA:0"},
	})
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "AA:0",
		Parent:  "AA",
		Type:    "MAINTENANCE",
	})

	// The cached description says PARAM_STALE should exist (Operations=2 = WRITE).
	psets.Put(hmenum.InterfaceHmIPRF, "AA:0", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"PARAM_PRESENT": hmproto.ParameterData{Operations: hmenum.OperationsWrite},
		"PARAM_STALE":   hmproto.ParameterData{Operations: hmenum.OperationsWrite},
	})

	// The live CCU only returns PARAM_PRESENT.
	checker := &stubParamsetChecker{
		results: map[string]map[string]any{
			"AA:0": {"PARAM_PRESENT": float64(1)},
		},
	}

	inconsistencies, err := dc.CheckParamsetConsistency(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		[]string{"AA"},
		checker,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inconsistencies) != 1 {
		t.Fatalf("expected 1 inconsistency, got %d", len(inconsistencies))
	}
	ic := inconsistencies[0]
	if ic.DeviceAddress != "AA" {
		t.Fatalf("DeviceAddress=%q, want AA", ic.DeviceAddress)
	}
	if len(ic.MissingParameters) != 1 {
		t.Fatalf("MissingParameters=%v, want [AA:0:PARAM_STALE]", ic.MissingParameters)
	}
}

func TestCheckParamsetConsistencyCleanDeviceReturnsEmpty(t *testing.T) {
	t.Parallel()
	dc, _, _, descs, psets := newDCFull(t)

	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "BB",
		Type:     "HmIP-Y",
		Children: []string{"BB:0"},
	})
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "BB:0",
		Parent:  "BB",
		Type:    "SWITCH",
	})

	psets.Put(hmenum.InterfaceHmIPRF, "BB:0", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"PARAM_A": hmproto.ParameterData{Operations: hmenum.OperationsWrite},
	})

	// Live CCU returns everything the description says.
	checker := &stubParamsetChecker{
		results: map[string]map[string]any{
			"BB:0": {"PARAM_A": float64(0)},
		},
	}

	inconsistencies, err := dc.CheckParamsetConsistency(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		[]string{"BB"},
		checker,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inconsistencies) != 0 {
		t.Fatalf("clean device: expected 0 inconsistencies, got %+v", inconsistencies)
	}
}

// ---------------------------------------------------------------------------
// ScheduleParamsetConsistencyCheck
// ---------------------------------------------------------------------------

func TestScheduleParamsetConsistencyCheckCallsCallback(t *testing.T) {
	t.Parallel()
	dc, _, _, descs, psets := newDCFull(t)

	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "CC",
		Type:     "HmIP-Z",
		Children: []string{"CC:0"},
	})
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "CC:0",
		Parent:  "CC",
		Type:    "DIMMER",
	})
	psets.Put(hmenum.InterfaceHmIPRF, "CC:0", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"STALE": hmproto.ParameterData{Operations: hmenum.OperationsWrite},
	})
	checker := &stubParamsetChecker{
		results: map[string]map[string]any{"CC:0": {}}, // empty → STALE is missing
	}

	var mu sync.Mutex
	var received []ParamsetInconsistency
	done := make(chan struct{})
	cb := func(ics []ParamsetInconsistency) {
		mu.Lock()
		received = append(received, ics...)
		mu.Unlock()
		close(done)
	}

	dc.ScheduleParamsetConsistencyCheck(
		context.Background(),
		hmenum.InterfaceHmIPRF,
		[]string{"CC"},
		checker,
		cb,
	)

	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("callback not called within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].DeviceAddress != "CC" {
		t.Fatalf("callback received=%+v, want single CC inconsistency", received)
	}
}

// TestAddNewDevicesManuallyPublishesOutsideTheCoordinatorLock pins that
// DeviceCreatedEvent handlers do not run under the DeviceCoordinator's
// mutex.
//
// The bus dispatches synchronously on the publishing goroutine, and this
// event's real handlers do substantial work: the event bridge publishes
// the device's whole MQTT snapshot, the security domain rebuilds its
// index. Holding a non-reentrant coordinator lock across foreign handler
// code means any handler that reaches back into the coordinator
// deadlocks the daemon outright — and until one does, a slow broker
// stalls every other caller of the coordinator.
//
// The handler here calls back into the coordinator, which is exactly
// what the old code could not survive.
func TestAddNewDevicesManuallyPublishesOutsideTheCoordinatorLock(t *testing.T) {
	t.Parallel()

	dc, bus, _, _, _ := newDCFull(t)

	var reentered atomic.Bool
	unsub := events.Subscribe(bus, func(hmevent.DeviceCreatedEvent) {
		// A read that takes the coordinator's own lock.
		dc.StoreDelayedDeviceDescriptions(hmenum.InterfaceHmIPRF, nil)
		reentered.Store(true)
	})
	defer unsub()

	dc.StoreDelayedDeviceDescriptions(hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		{Address: "REENT0001", Type: "HmIP-STH"},
	})

	done := make(chan error, 1)
	go func() {
		done <- dc.AddNewDevicesManually(context.Background(), hmenum.InterfaceHmIPRF,
			map[string]string{"REENT0001": "Sensor"}, nil)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AddNewDevicesManually: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AddNewDevicesManually deadlocked: a DeviceCreatedEvent handler ran under the coordinator lock")
	}
	if !reentered.Load() {
		t.Fatal("handler never ran; the test would pass vacuously")
	}
}
