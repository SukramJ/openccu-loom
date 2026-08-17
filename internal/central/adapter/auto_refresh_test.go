// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- ProductGroup discrimination ----------------------------------

func TestIsHmIPInterfaceDiscriminates(t *testing.T) {
	t.Parallel()

	if !isHmIPInterface(hmenum.InterfaceHmIPRF) {
		t.Errorf("isHmIPInterface(%q) = false, want true", hmenum.InterfaceHmIPRF)
	}

	classicCases := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}
	for _, iface := range classicCases {
		if isHmIPInterface(iface) {
			t.Errorf("isHmIPInterface(%q) = true, want false (classic HM)", iface)
		}
	}
}

// ---------- newMasterPollerForInterface ----------------------------------

// fakeMasterGetter satisfies backends.MasterGetter.
type fakeMasterGetter struct {
	mu     sync.Mutex
	calls  int
	result map[string]any
	err    error
}

func (f *fakeMasterGetter) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]any, len(f.result))
	maps.Copy(out, f.result)
	return out, nil
}

func TestNewMasterPollerForInterfaceReturnsNilForHmIP(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	getter := &fakeMasterGetter{}
	p := newMasterPollerForInterface(hmenum.InterfaceHmIPRF, c, getter, nil, "", "", nil)
	if p != nil {
		t.Fatal("expected nil MasterPoller for HmIP interface")
	}
}

func TestNewMasterPollerForInterfaceReturnsPollerForClassicHM(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	getter := &fakeMasterGetter{}

	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	} {
		p := newMasterPollerForInterface(iface, c, getter, nil, "", "", nil)
		if p == nil {
			t.Errorf("expected non-nil MasterPoller for classic iface %q", iface)
			continue
		}
		p.Close()
	}
}

// TestMasterPollerOnRefreshPushesValues verifies that when the poller
// fires OnRefresh, the fresh MASTER values are pushed into the channel's
// MasterParameter data points via OnWireValue.
func TestMasterPollerOnRefreshPushesValues(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	dev := device.New(device.Config{
		InterfaceID:  "BidCos-RF",
		Interface:    hmenum.InterfaceBidCosRF,
		Address:      "0001BBBB",
		ProductGroup: hmenum.ProductGroupHM,
	})
	c.ModelRegistry.Put(dev)
	ch := dev.AddChannel("0001BBBB:1", 1, "TEST_TYPE", hmenum.ParamsetKeyMaster)

	// Add a fake MASTER data point that records OnWireValue calls.
	dp := newFloatDP(hmenum.Parameter("SHORT_ON_TIME"), "0001BBBB:1")
	// flip to MASTER key for test
	ch.PutMaster(dp)

	getter := &fakeMasterGetter{result: map[string]any{"SHORT_ON_TIME": float64(3.14)}}
	poller := newMasterPollerForInterface(hmenum.InterfaceBidCosRF, c, getter, nil, "", "", nil)
	defer poller.Close()

	// Fast test: inject a very short interval.
	poller.Interval = 10 * time.Millisecond
	poller.SchedulePoll("0001BBBB:1", hmenum.ParamsetKeyMaster)

	// Wait for the poll to complete (max 2 s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		getter.mu.Lock()
		calls := getter.calls
		getter.mu.Unlock()
		if calls >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	getter.mu.Lock()
	gotCalls := getter.calls
	getter.mu.Unlock()
	if gotCalls < 1 {
		t.Fatalf("expected GetParamset to be called at least once, got %d", gotCalls)
	}
}

// ---------- wireConfigPendingHook ----------------------------------------

// TestWireConfigPendingHookIgnoresClassicHM verifies that a CONFIG_PENDING
// True→False transition on a classic HM interface does not reach the settle
// handler. Classic interfaces do not emit CONFIG_PENDING reliably and are
// covered by the post-write MasterPoller instead; running the settle path for
// them would issue a redundant getParamset(MASTER) against a duty-cycle-bound
// radio. The event is driven through the real callback path so the
// discrimination under test is production's, not the test's.
func TestWireConfigPendingHookIgnoresClassicHM(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-01"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID(centralName, hmenum.InterfaceBidCosRF)

	dev := buildDeviceWithMasterChannels(1, "0001CCCC", wireID)
	dev.Channel("0001CCCC:0").Put(newConfigPendingDP(wireID, "0001CCCC:0"))
	c.ModelRegistry.Put(dev)

	resolved := make(chan string, 4)
	getterFor := func(interfaceID string) backends.MasterGetter {
		resolved <- interfaceID
		return &perChannelGetter{}
	}
	wireConfigPendingHook(context.Background(), c, openAdapterTestDB(t), centralName, getterFor, nil)

	handlers := NewCallbackHandlers(c, nil)
	t.Cleanup(handlers.Stop)
	initID := InitInterfaceID(c.InstanceName(), centralName, hmenum.InterfaceBidCosRF)
	for _, v := range []bool{true, false} {
		if err := handlers.Event(context.Background(), initID, "0001CCCC:0",
			string(hmenum.ParameterConfigPending), xmlrpc.BoolValue(v)); err != nil {
			t.Fatalf("Event(CONFIG_PENDING=%v): %v", v, err)
		}
	}

	select {
	case got := <-resolved:
		t.Fatalf("classic HM interface %q must not run the settle handler", got)
	case <-time.After(200 * time.Millisecond):
	}
}

// ---------- MasterPoller close on ingest failure -------------------------

// TestNewMasterPollerForInterfaceWiresOnError verifies that the OnError
// callback is set (non-nil) when a classic HM poller is constructed.
func TestNewMasterPollerForInterfaceWiresOnError(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	getter := &fakeMasterGetter{}
	p := newMasterPollerForInterface(hmenum.InterfaceBidCosRF, c, getter, nil, "", "", nil)
	if p == nil {
		t.Fatal("expected non-nil poller")
	}
	defer p.Close()
	if p.OnError == nil {
		t.Fatal("OnError must be set so poll errors are surfaced to debug log")
	}
	if p.OnRefresh == nil {
		t.Fatal("OnRefresh must be set so polled values flow into the model")
	}
}

// compileCheckMasterGetter ensures backends.MasterGetter is satisfied.
var _ backends.MasterGetter = (*fakeMasterGetter)(nil)

// TestWireConfigPendingHookSettlesOnWireInterfaceID drives a CONFIG_PENDING
// True→False transition through the real callback path — the id the CCU
// echoes back (`loom-<instance>-<central>-HmIP-RF`), canonicalised by
// [CallbackHandlers.Event] to the wire form `<central>-HmIP-RF` — and asserts
// the settle handler resolves the interface's MASTER getter.
//
// The handler used to classify the interface by casting the incoming id to
// [hmenum.Interface] and comparing it to the bare "HmIP-RF": on any named
// central that comparison is false for every event, so the week-profile
// reload, the operation-mode visibility re-apply and the targeted
// getParamset(MASTER) + cache write all silently never ran. HmIP has no
// MasterPoller fallback, so nothing else covered it.
func TestWireConfigPendingHookSettlesOnWireInterfaceID(t *testing.T) {
	t.Parallel()

	const centralName = "GoOtto"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID(centralName, hmenum.InterfaceHmIPRF)

	dev := buildDeviceWithMasterChannels(1, "0006ABCD", wireID)
	ch := dev.Channel("0006ABCD:0")
	ch.Put(newConfigPendingDP(wireID, "0006ABCD:0"))
	c.ModelRegistry.Put(dev)

	resolved := make(chan string, 4)
	getterFor := func(interfaceID string) backends.MasterGetter {
		resolved <- interfaceID
		return &perChannelGetter{}
	}
	wireConfigPendingHook(context.Background(), c, openAdapterTestDB(t), centralName, getterFor, nil)

	handlers := NewCallbackHandlers(c, nil)
	t.Cleanup(handlers.Stop)
	initID := InitInterfaceID(c.InstanceName(), centralName, hmenum.InterfaceHmIPRF)
	for _, v := range []bool{true, false} {
		if err := handlers.Event(context.Background(), initID, "0006ABCD:0",
			string(hmenum.ParameterConfigPending), xmlrpc.BoolValue(v)); err != nil {
			t.Fatalf("Event(CONFIG_PENDING=%v): %v", v, err)
		}
	}

	select {
	case got := <-resolved:
		if got != wireID {
			t.Fatalf("settle handler resolved the getter for %q, want the canonical wire id %q", got, wireID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CONFIG_PENDING True→False never reached the settle handler on a named central")
	}
}

// countingSimpleReloadLoader records every Load and signals it, so a test can
// observe whether the CONFIG_PENDING settle reloaded a non-climate schedule.
type countingSimpleReloadLoader struct {
	loaded chan struct{}
}

func (l *countingSimpleReloadLoader) Load(_ context.Context) (*schedule.Simple, error) {
	select {
	case l.loaded <- struct{}{}:
	default:
	}
	return schedule.NewSimple(), nil
}

// TestWireConfigPendingHookReloadsSimpleWeekProfile pins that a CONFIG_PENDING
// True→False settle reloads the *simple* (non-climate) week profile, not only
// the climate one. A switch / cover / light / lock schedule write settles the
// same way; without the simple reload the retained MQTT schedule_data would
// stay at its boot snapshot until the daemon restarts. The event is driven
// through the real callback path so the reload under test is production's.
func TestWireConfigPendingHookReloadsSimpleWeekProfile(t *testing.T) {
	t.Parallel()

	const centralName = "GoLock"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID(centralName, hmenum.InterfaceHmIPRF)

	dev := buildDeviceWithMasterChannels(2, "000SIMPLE", wireID)
	dev.Channel("000SIMPLE:0").Put(newConfigPendingDP(wireID, "000SIMPLE:0"))
	schedCh := dev.Channel("000SIMPLE:1")
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    centralName,
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	loader := &countingSimpleReloadLoader{loaded: make(chan struct{}, 8)}
	wp.AttachSimpleProfile(weekprofile.NewDefault(loader, nil))
	schedCh.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	// Prime Current() so the hook's has_schedule gate passes, then drain the
	// priming signal so only the settle-driven reload remains to observe.
	if _, err := wp.Simple().Load(context.Background()); err != nil {
		t.Fatalf("prime simple load: %v", err)
	}
	<-loader.loaded

	wireConfigPendingHook(context.Background(), c, nil, centralName, nil, nil)

	handlers := NewCallbackHandlers(c, nil)
	t.Cleanup(handlers.Stop)
	initID := InitInterfaceID(c.InstanceName(), centralName, hmenum.InterfaceHmIPRF)
	for _, v := range []bool{true, false} {
		if err := handlers.Event(context.Background(), initID, "000SIMPLE:0",
			string(hmenum.ParameterConfigPending), xmlrpc.BoolValue(v)); err != nil {
			t.Fatalf("Event(CONFIG_PENDING=%v): %v", v, err)
		}
	}

	select {
	case <-loader.loaded:
	case <-time.After(2 * time.Second):
		t.Fatal("CONFIG_PENDING settle did not reload the simple (non-climate) week profile")
	}
}

// newConfigPendingDP builds the boolean VALUES data point the CCU pushes the
// CONFIG_PENDING transition on, so the callback path has something to route
// the event to.
func newConfigPendingDP(interfaceID, channelAddr string) *generic.DataPoint[bool] {
	return generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    interfaceID,
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterConfigPending),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}
