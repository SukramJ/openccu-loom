// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_coordinator_paramset_consistency_test.go pins the
// CheckParamsetConsistency invariants. Clusters: cross-channel coherency,
// zero-length / missing parameter handling, multi-device cache consistency.

package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// fakeChecker is a fake ParamsetConsistencyChecker used by the tests.
type fakeChecker struct {
	// perChannel maps channelAddress → paramset returned by GetParamset.
	// If the channel is absent the call returns the configured error.
	perChannel map[string]map[string]any
	err        error
	calls      []string
}

func (f *fakeChecker) GetParamset(_ context.Context, ch string, _ hmenum.ParamsetKey) (map[string]any, error) {
	f.calls = append(f.calls, ch)
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.perChannel[ch]; ok {
		return v, nil
	}
	return map[string]any{}, nil
}

// buildCoordinator builds a minimal DeviceCoordinator populated with the
// given descriptions and MASTER paramsets so CheckParamsetConsistency can
// run without a real CCU.
func buildCoordinator(
	iface hmenum.Interface,
	descs []hmproto.DeviceDescription,
	paramsets map[string]hmproto.Paramset, // channelAddr → Paramset
) *DeviceCoordinator {
	bus := events.NewBus()
	devReg := registry.NewDeviceRegistry()
	descReg := registry.NewDeviceDescriptionRegistry()
	psReg := registry.NewParamsetRegistry()

	for i := range descs {
		descReg.Put(wireKey(iface), descs[i])
	}
	for ch, ps := range paramsets {
		psReg.Put(wireKey(iface), ch, hmenum.ParamsetKeyMaster, ps)
	}

	return NewDeviceCoordinator(testCentralName, bus, devReg, descReg, psReg, nil, nil)
}

// ─── Test 1: nil checker returns error ───────────────────────────────────────

func TestCheckParamsetConsistencyNilCheckerReturnsError(t *testing.T) {
	t.Parallel()
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, nil, nil)
	_, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil checker")
	}
}

// ─── Test 2: non-HmIP interfaces are skipped (no inconsistencies) ────────────

func TestCheckParamsetConsistencySkipsNonHmIPInterfaces(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "BID0001", Parent: "", Type: "HM-LC-Sw1-Pl"},
		{Address: "BID0001:1", Parent: "BID0001", Type: "HM-LC-Sw1-Pl"},
	}
	ps := hmproto.Paramset{
		"POLLING": {Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	}
	coord := buildCoordinator(hmenum.InterfaceBidCosRF, descs, map[string]hmproto.Paramset{
		"BID0001:1": ps,
	})
	checker := &fakeChecker{perChannel: map[string]map[string]any{}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceBidCosRF, wireKey(hmenum.InterfaceBidCosRF), []string{"BID0001"}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no inconsistencies for BidCos-RF, got %d", len(results))
	}
	if len(checker.calls) != 0 {
		t.Fatalf("checker must not be called for non-HmIP interfaces, got %d calls", len(checker.calls))
	}
}

// ─── Test 3: all parameters present → no inconsistency ───────────────────────

func TestCheckParamsetConsistencyAllParametersPresent(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0001", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0001:1", Parent: "HMIP0001", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"CHANNEL_OPERATION_MODE": {Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0001:1": ps,
	})
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0001:1": {"CHANNEL_OPERATION_MODE": 0},
	}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0001"}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no inconsistencies when all params present, got %d", len(results))
	}
}

// ─── Test 4: missing parameter → inconsistency recorded ──────────────────────

func TestCheckParamsetConsistencyMissingParameterDetected(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0002", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0002:1", Parent: "HMIP0002", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"PARAM_A": {Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		"PARAM_B": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0002:1": ps,
	})
	// Live CCU only knows PARAM_A, missing PARAM_B
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0002:1": {"PARAM_A": 1},
	}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0002"}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 inconsistency, got %d", len(results))
	}
	if results[0].DeviceAddress != "HMIP0002" {
		t.Errorf("DeviceAddress=%q, want HMIP0002", results[0].DeviceAddress)
	}
	if results[0].InterfaceID != string(wireKey(hmenum.InterfaceHmIPRF)) {
		t.Errorf("InterfaceID=%q, want %s", results[0].InterfaceID, wireKey(hmenum.InterfaceHmIPRF))
	}
	found := false
	for _, mp := range results[0].MissingParameters {
		if mp == "HMIP0002:1:PARAM_B" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HMIP0002:1:PARAM_B in MissingParameters, got %v", results[0].MissingParameters)
	}
}

// ─── Test 5: parameters with Operations==0 are not expected ──────────────────

func TestCheckParamsetConsistencyOperationsZeroParamsIgnored(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0003", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0003:1", Parent: "HMIP0003", Type: "HmIP-SW"},
	}
	// HIDDEN_PARAM has Operations=0 and must not be expected from the CCU
	ps := hmproto.Paramset{
		"VISIBLE":      {Operations: hmenum.OperationsRead},
		"HIDDEN_PARAM": {Operations: 0},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0003:1": ps,
	})
	// Live CCU only has VISIBLE
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0003:1": {"VISIBLE": 1},
	}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0003"}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("HIDDEN_PARAM (operations=0) must not cause inconsistency, got %v", results)
	}
}

// ─── Test 6: transport error on fetch → channel is skipped ───────────────────

func TestCheckParamsetConsistencyFetchErrorSkipsChannel(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0004", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0004:1", Parent: "HMIP0004", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"PARAM_X": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0004:1": ps,
	})
	checker := &fakeChecker{err: errors.New("tcp timeout")}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0004"}, checker)
	if err != nil {
		t.Fatalf("unexpected outer error: %v", err)
	}
	// Transport error is non-fatal; channel is skipped → no inconsistency
	if len(results) != 0 {
		t.Fatalf("fetch error must skip channel, got %d inconsistencies", len(results))
	}
}

// ─── Test 7: empty device list → no calls, no inconsistencies ────────────────

func TestCheckParamsetConsistencyEmptyDeviceListIsNoop(t *testing.T) {
	t.Parallel()
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, nil, nil)
	checker := &fakeChecker{perChannel: map[string]map[string]any{}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty device list must produce no results, got %d", len(results))
	}
	if len(checker.calls) != 0 {
		t.Fatalf("empty device list must not call checker, got %d calls", len(checker.calls))
	}
}

// ─── Test 8: multi-device cache consistency — each device independent ─────────

func TestCheckParamsetConsistencyMultiDeviceIndependence(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0010", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0010:1", Parent: "HMIP0010", Type: "HmIP-SW"},
		{Address: "HMIP0011", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0011:1", Parent: "HMIP0011", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"P": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0010:1": ps,
		"HMIP0011:1": ps,
	})
	// HMIP0010 live CCU has P; HMIP0011 live CCU missing P
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0010:1": {"P": 1},
		"HMIP0011:1": {},
	}}
	results, err := coord.CheckParamsetConsistency(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0010", "HMIP0011"}, checker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 inconsistency (HMIP0011), got %d", len(results))
	}
	if results[0].DeviceAddress != "HMIP0011" {
		t.Errorf("DeviceAddress=%q, want HMIP0011", results[0].DeviceAddress)
	}
}

// ─── Test 9: ScheduleParamsetConsistencyCheck callback receives results ───────

func TestScheduleParamsetConsistencyCheckCallbackReceivesResults(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0020", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0020:1", Parent: "HMIP0020", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"PP": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0020:1": ps,
	})
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0020:1": {}, // PP missing
	}}

	done := make(chan []ParamsetInconsistency, 1)
	coord.ScheduleParamsetConsistencyCheck(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0020"}, checker,
		func(results []ParamsetInconsistency) {
			done <- results
		})

	select {
	case results := <-done:
		if len(results) != 1 {
			t.Fatalf("callback must receive 1 inconsistency, got %d", len(results))
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("timeout waiting for ScheduleParamsetConsistencyCheck callback")
	}
}

// ─── Test 10: Stop waits for the in-flight background goroutine ──────────────

// blockingChecker blocks GetParamset until release is closed, letting the
// test observe that Stop does not return while the goroutine spawned by
// ScheduleParamsetConsistencyCheck is still running.
type blockingChecker struct {
	release chan struct{}
}

func (b *blockingChecker) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	<-b.release
	return map[string]any{}, nil
}

func TestScheduleParamsetConsistencyCheckStopWaitsForInFlightGoroutine(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0030", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0030:1", Parent: "HMIP0030", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"PP": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0030:1": ps,
	})
	checker := &blockingChecker{release: make(chan struct{})}

	coord.ScheduleParamsetConsistencyCheck(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0030"}, checker, nil)

	stopDone := make(chan struct{})
	go func() {
		coord.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatal("Stop returned before the in-flight consistency-check goroutine finished")
	case <-time.After(200 * time.Millisecond):
		// Expected: Stop is still blocked on the running goroutine.
	}

	close(checker.release)

	select {
	case <-stopDone:
	case <-time.After(eventWaitTimeout):
		t.Fatal("Stop did not return after the background goroutine finished")
	}
}

// ─── Test 11: a panic in the checker is recovered, not fatal ──────────────────

// panicChecker panics on every GetParamset call, simulating a misbehaving
// checker implementation.
type panicChecker struct{}

func (panicChecker) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	panic("boom")
}

func TestScheduleParamsetConsistencyCheckRecoversFromPanic(t *testing.T) {
	t.Parallel()
	descs := []hmproto.DeviceDescription{
		{Address: "HMIP0031", Parent: "", Type: "HmIP-SW"},
		{Address: "HMIP0031:1", Parent: "HMIP0031", Type: "HmIP-SW"},
	}
	ps := hmproto.Paramset{
		"PP": {Operations: hmenum.OperationsRead},
	}
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, descs, map[string]hmproto.Paramset{
		"HMIP0031:1": ps,
	})

	coord.ScheduleParamsetConsistencyCheck(context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF), []string{"HMIP0031"}, panicChecker{}, nil)

	// Stop must return promptly: the panic must be recovered inside the
	// goroutine (not propagate and crash the test binary) and the WaitGroup
	// must still be released via the deferred Done.
	done := make(chan struct{})
	go func() {
		coord.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(eventWaitTimeout):
		t.Fatal("Stop hung after the background goroutine panicked")
	}
}

// TestCheckParamsetConsistencyRunsWithoutDeviceDescriptions pins the check on
// the registry shape a first-ever boot produces: the paramset registry is
// filled by the hydration pass that fetched the descriptions, while the
// device-description registry stays empty until the CCU announces its devices
// over the callback — which happens after init(), long after the check is
// scheduled.
//
// Enumerating the device's channels from the description registry made the
// whole check a no-op in exactly that window: no channels, no comparison, a
// clean bill of health for a device it never looked at. The channels the check
// is about are the ones that HAVE a cached MASTER description, so the paramset
// registry is the registry that knows them.
func TestCheckParamsetConsistencyRunsWithoutDeviceDescriptions(t *testing.T) {
	t.Parallel()
	ps := hmproto.Paramset{
		"PARAM_A": {Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		"PARAM_B": {Operations: hmenum.OperationsRead},
	}
	// No descriptions — the cold-boot shape.
	coord := buildCoordinator(hmenum.InterfaceHmIPRF, nil, map[string]hmproto.Paramset{
		"HMIP0009:1": ps,
	})
	checker := &fakeChecker{perChannel: map[string]map[string]any{
		"HMIP0009:1": {"PARAM_A": 1},
	}}
	results, err := coord.CheckParamsetConsistency(
		context.Background(), hmenum.InterfaceHmIPRF, wireKey(hmenum.InterfaceHmIPRF),
		[]string{"HMIP0009"}, checker,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 inconsistency without device descriptions, got %d — "+
			"the check found no channel to compare", len(results))
	}
	if got := results[0].MissingParameters; len(got) != 1 || got[0] != "HMIP0009:1:PARAM_B" {
		t.Errorf("MissingParameters=%v, want [HMIP0009:1:PARAM_B]", got)
	}
}
