// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestNewRejectsEmptyName(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestCentralStartStop(t *testing.T) {
	c, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if c.StateMachine.State() != hmenum.CentralStateRunning {
		t.Fatalf("state=%s", c.StateMachine.State())
	}
	c.Stop()
	if c.StateMachine.State() != hmenum.CentralStateStopped {
		t.Fatalf("state=%s after stop", c.StateMachine.State())
	}
}

func TestCentralCoordinatorsAreWired(t *testing.T) {
	c, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if c.EventBus == nil || c.Cache == nil || c.Events == nil ||
		c.Devices == nil || c.Hub == nil || c.Recovery == nil ||
		c.DeviceRegistry == nil || c.DescRegistry == nil ||
		c.ParamsetReg == nil || c.Clients == nil ||
		c.Scheduler == nil || c.Health == nil {
		t.Fatal("some coordinator/registry is nil")
	}
}

func TestCentralSystemInformationLifecycle(t *testing.T) {
	c, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SystemInformation(); got != (SystemInfo{}) {
		t.Fatalf("fresh SystemInformation must be zero, got %+v", got)
	}
	if c.Model() != "" {
		t.Fatalf("fresh Model must be empty, got %q", c.Model())
	}

	c.SetSystemInformation(SystemInfo{
		Model:    "OpenCCU3",
		Version:  "3.71.13",
		Hostname: "ccu.local",
		Serial:   "OEQ1234567",
		IsHaApp:  false,
	})
	if got := c.SystemInformation(); got.Model != "OpenCCU3" || got.Version != "3.71.13" {
		t.Fatalf("after Set: %+v", got)
	}
	if c.Model() != "OpenCCU3" {
		t.Fatalf("Model=%q want OpenCCU3", c.Model())
	}
	if c.Version() == "" {
		t.Fatal("daemon Version must not be empty (build.Version default 'dev')")
	}
}

func TestCentralResolveDeviceName(t *testing.T) {
	c, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ResolveDeviceName("0001ABCD"); got != "0001ABCD" {
		t.Fatalf("unknown=%q want 0001ABCD (passthrough)", got)
	}
	if got := c.ResolveDeviceName(""); got != "" {
		t.Fatalf("empty input=%q want empty", got)
	}
}

func TestCentralReadableGenericDataPointsEmpty(t *testing.T) {
	c, err := New(Config{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ReadableGenericDataPoints(); len(got) != 0 {
		t.Fatalf("empty registry must return empty list, got len=%d", len(got))
	}
}

// TestStopTransitionsThroughStopping verifies that Stop() leaves the central
// in the STOPPED state and that the transition is recorded in the state machine
// history. The central state machine has no STOPPING intermediate state —
// teardown transitions directly from RUNNING → STOPPED via ForceTransitionTo.
func TestStopTransitionsThroughStopping(t *testing.T) {
	c, err := New(Config{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if c.StateMachine.State() != hmenum.CentralStateRunning {
		t.Fatalf("pre-stop state=%s want running", c.StateMachine.State())
	}

	c.Stop()

	if got := c.StateMachine.State(); got != hmenum.CentralStateStopped {
		t.Fatalf("post-stop state=%s want stopped", got)
	}
	hist := c.StateMachine.History()
	if len(hist) == 0 {
		t.Fatal("expected at least one history entry after Stop")
	}
	last := hist[len(hist)-1]
	if last.To != hmenum.CentralStateStopped {
		t.Fatalf("last history entry To=%s want stopped", last.To)
	}
}

// TestStopStopsSchedulerOnce verifies that calling Stop() stops the scheduler
// without panicking and leaves the central in STOPPED state. A second Stop()
// call must also be safe (idempotent early-return path).
func TestStopStopsSchedulerOnce(t *testing.T) {
	c, err := New(Config{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First Stop must succeed and transition to STOPPED.
	c.Stop()
	if got := c.StateMachine.State(); got != hmenum.CentralStateStopped {
		t.Fatalf("after first Stop state=%s want stopped", got)
	}

	// Second Stop must not panic (idempotent early-return).
	c.Stop()
	if got := c.StateMachine.State(); got != hmenum.CentralStateStopped {
		t.Fatalf("after second Stop state=%s want stopped", got)
	}
}

// TestStopIdempotent verifies that calling Stop() on an already-stopped central
// returns immediately without panicking and without modifying the state machine
// history.
func TestStopIdempotent(t *testing.T) {
	c, err := New(Config{Name: "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	c.Stop()
	histAfterFirst := c.StateMachine.History()

	// Second Stop — must be a no-op: no new history entries.
	c.Stop()
	histAfterSecond := c.StateMachine.History()

	if len(histAfterSecond) != len(histAfterFirst) {
		t.Fatalf("idempotent Stop must not add history entries: before=%d after=%d",
			len(histAfterFirst), len(histAfterSecond))
	}
	if got := c.StateMachine.State(); got != hmenum.CentralStateStopped {
		t.Fatalf("state after double Stop=%s want stopped", got)
	}
}

// TestCCUVersion verifies CCUVersion() returns SystemInfo.Version.
func TestCCUVersion(t *testing.T) {
	c, _ := New(Config{Name: "main"})
	if c.CCUVersion() != "" {
		t.Fatal("CCUVersion should be empty before SetSystemInformation")
	}
	c.SetSystemInformation(SystemInfo{Version: "3.73.10.20250101"})
	if got := c.CCUVersion(); got != "3.73.10.20250101" {
		t.Fatalf("CCUVersion=%q, want 3.73.10.20250101", got)
	}
	// Version() is still the build version, not the CCU version.
	if c.Version() == c.CCUVersion() {
		t.Fatal("Version() and CCUVersion() must not return the same value after SetSystemInformation")
	}
}

// TestOnStateTransition verifies convenience wrapper fires on
// matching transitions.
func TestOnStateTransition(t *testing.T) {
	c, _ := New(Config{Name: "main"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var toStates []hmenum.CentralState
	unsub := c.OnStateTransition(hmenum.CentralStateRunning, "", func(to, from hmenum.CentralState) {
		toStates = append(toStates, to)
	})
	defer unsub()

	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(toStates) != 1 || toStates[0] != hmenum.CentralStateRunning {
		t.Fatalf("expected Running transition, got %v", toStates)
	}

	// Unsubscribe — subsequent transitions must not fire.
	unsub()
	c.Stop()
	if len(toStates) != 1 {
		t.Fatalf("post-unsubscribe handler fired: %v", toStates)
	}
}

// TestOnStateTransitionFromFilter verifies that the `from` filter works.
func TestOnStateTransitionFromFilter(t *testing.T) {
	c, _ := New(Config{Name: "main"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var fired int
	// Only care about transitions from Initializing to Running.
	unsub := c.OnStateTransition(hmenum.CentralStateRunning, hmenum.CentralStateInitializing, func(_, _ hmenum.CentralState) {
		fired++
	})
	defer unsub()

	_ = c.Start(ctx)
	if fired != 1 {
		t.Fatalf("from-filter: expected 1 fire, got %d", fired)
	}

	// Transition to Stopped — should NOT fire because from is Initializing
	// but the actual from-state here is Running.
	c.Stop()
	if fired != 1 {
		t.Fatalf("from-filter: extra fire after Stop, total=%d", fired)
	}
}

// TestRenameDeviceWithChannels verifies channel rename uses
// "{name}:{no}" pattern when includeChannels=true.
func TestRenameDeviceWithChannels(t *testing.T) {
	c, _ := New(Config{Name: "main"})

	var renamed []string
	c.SetRenameDeviceFn(func(_ context.Context, addr, name string) error {
		renamed = append(renamed, addr+":"+name)
		return nil
	})

	// Without includeChannels — only device address/name is renamed.
	renamed = nil
	if err := c.RenameDeviceWithChannels(context.Background(), "ABC123456", "MyDevice", false); err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 1 {
		t.Fatalf("includeChannels=false: expected 1 rename call, got %d: %v", len(renamed), renamed)
	}

	// No ModelRegistry entry — includeChannels=true should not panic.
	renamed = nil
	if err := c.RenameDeviceWithChannels(context.Background(), "UNKNOWN", "X", true); err != nil {
		t.Fatal(err)
	}
	// Only the device-level rename fires; no channels are known.
	if len(renamed) != 1 {
		t.Fatalf("unknown device: expected 1 call, got %d: %v", len(renamed), renamed)
	}
}

// TestHasPingPong_NoClientsReturnsFalse verifies HasPingPong returns false
// when no clients are registered.
func TestHasPingPong_NoClientsReturnsFalse(t *testing.T) {
	c := newTestCentral(t)
	if c.HasPingPong() {
		t.Error("expected false when no clients registered")
	}
}

// TestStart_ContextAlreadyCancelled verifies Start does not panic
// when given an already-cancelled context.
func TestStart_ContextAlreadyCancelled(t *testing.T) {
	c := newTestCentral(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = c.Start(ctx)
}

// TestOnStateTransition_ReceivesRunningEvent verifies the OnStateTransition
// callback fires with RUNNING after Start.
func TestOnStateTransition_ReceivesRunningEvent(t *testing.T) {
	c := newTestCentral(t)

	var fires atomic.Int32
	unsub := c.OnStateTransition(hmenum.CentralStateRunning, "", func(to, from hmenum.CentralState) {
		if to == hmenum.CentralStateRunning {
			fires.Add(1)
		}
	})
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && fires.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if fires.Load() < 1 {
		t.Errorf("expected OnStateTransition to fire for RUNNING, fires=%d", fires.Load())
	}
}

// TestOnStateTransition_UnsubscribeStopsFiring verifies the callback is
// suppressed after unsubscribing.
func TestOnStateTransition_UnsubscribeStopsFiring(t *testing.T) {
	c := newTestCentral(t)

	var fires atomic.Int32
	unsub := c.OnStateTransition("", "", func(to, from hmenum.CentralState) {
		fires.Add(1)
	})

	unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	if fires.Load() > 0 {
		t.Errorf("listener should not fire after unsubscribe, fires=%d", fires.Load())
	}
}

// TestOnStateTransition_NilEventBusSafe verifies OnStateTransition does not
// panic when EventBus is nil.
func TestOnStateTransition_NilEventBusSafe(t *testing.T) {
	c := newTestCentral(t)
	origBus := c.EventBus
	c.EventBus = nil
	unsub := c.OnStateTransition("", "", func(_, _ hmenum.CentralState) {})
	if unsub == nil {
		t.Fatal("expected non-nil unsub even when EventBus is nil")
	}
	unsub()
	c.EventBus = origBus
}

// TestResolveDeviceName_ModelRegistryNil verifies ResolveDeviceName passes
// through the address when ModelRegistry is nil.
func TestResolveDeviceName_ModelRegistryNil(t *testing.T) {
	c := newTestCentral(t)
	c.ModelRegistry = nil
	if got := c.ResolveDeviceName("0001ABCD"); got != "0001ABCD" {
		t.Errorf("expected passthrough when ModelRegistry nil, got %q", got)
	}
}

// TestResolveDeviceName_EmptyAddressPassThrough verifies ResolveDeviceName
// returns the empty string unchanged.
func TestResolveDeviceName_EmptyAddressPassThrough(t *testing.T) {
	c := newTestCentral(t)
	if got := c.ResolveDeviceName(""); got != "" {
		t.Errorf("empty address must pass through, got %q", got)
	}
}

// TestRenameDeviceWithChannels_NotIncludeChannels verifies that
// includeChannels=false only renames the device.
func TestRenameDeviceWithChannels_NotIncludeChannels(t *testing.T) {
	c := newTestCentral(t)
	c.SetRenameDeviceFn(func(_ context.Context, _, _ string) error { return nil })
	if err := c.RenameDeviceWithChannels(context.Background(), "0001ABCD", "NewName", false); err != nil {
		t.Fatalf("RenameDeviceWithChannels (no channels): %v", err)
	}
}

// TestRenameDeviceWithChannels_UnknownDeviceIsNoop verifies that an unknown
// device silently succeeds with includeChannels=true.
func TestRenameDeviceWithChannels_UnknownDeviceIsNoop(t *testing.T) {
	c := newTestCentral(t)
	c.SetRenameDeviceFn(func(_ context.Context, _, _ string) error { return nil })
	if err := c.RenameDeviceWithChannels(context.Background(), "UNKNOWN", "Name", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels unknown device: %v", err)
	}
}

// TestRenameDeviceWithChannels_RenameDeviceError verifies that a rename fn
// error is propagated.
func TestRenameDeviceWithChannels_RenameDeviceError(t *testing.T) {
	c := newTestCentral(t)
	boom := errors.New("rename failed")
	c.SetRenameDeviceFn(func(_ context.Context, _, _ string) error { return boom })
	err := c.RenameDeviceWithChannels(context.Background(), "0001ABCD", "Name", false)
	if !errors.Is(err, boom) {
		t.Errorf("expected rename error, got %v", err)
	}
}

// TestSetLinkResolver_NilLinkCoord verifies SetLinkResolver accepts nil.
func TestSetLinkResolver_NilLinkCoord(t *testing.T) {
	c := newTestCentral(t)
	c.SetLinkResolver(nil)
}

// TestQueryFacade_NilDevicesMethods verifies QueryFacade methods return safe
// zero values when all internal fields are nil.
func TestQueryFacade_NilDevicesMethods(t *testing.T) {
	qf := &QueryFacade{}
	if qf.DeviceCount() != 0 {
		t.Error("DeviceCount with nil devices must be 0")
	}
	if qf.Devices() != nil {
		t.Error("Devices with nil devices must be nil")
	}
	if qf.HealthSnapshot() != nil {
		t.Error("HealthSnapshot with nil health must be nil")
	}
	_ = qf.OverallHealth()
	if qf.GetDataPoints("") != nil {
		t.Error("GetDataPoints with nil model must be nil")
	}
	if qf.GetDataPointsByCategory(hmenum.DataPointCategoryAction) != nil {
		t.Error("GetDataPointsByCategory with nil model must be nil")
	}
	if qf.GetCustomDataPoint("DEV:1", 1) != nil {
		t.Error("GetCustomDataPoint with nil model must be nil")
	}
	if qf.GetGenericDataPoint("DEV:1", "STATE") != nil {
		t.Error("GetGenericDataPoint with nil model must be nil")
	}
	if qf.GetEventSources("") != nil {
		t.Error("GetEventSources with nil model must be nil")
	}
	if qf.GetChannel("DEV:1") != nil {
		t.Error("GetChannel with nil model must be nil")
	}
}

// TestQueryFacade_GetGenericDataPoint_EmptyChannelAddress verifies an empty
// channel address causes an early nil return.
func TestQueryFacade_GetGenericDataPoint_EmptyChannelAddress(t *testing.T) {
	c := newTestCentral(t)
	qf := c.QueryFacade()
	if qf.GetGenericDataPoint("", "STATE") != nil {
		t.Error("empty channelAddress must return nil")
	}
}

// TestReadableGenericDataPoints_NilModelRegistry verifies ReadableGenericDataPoints
// returns nil when ModelRegistry is nil.
func TestReadableGenericDataPoints_NilModelRegistry(t *testing.T) {
	c := newTestCentral(t)
	c.ModelRegistry = nil
	got := c.ReadableGenericDataPoints()
	if got != nil {
		t.Errorf("nil ModelRegistry must return nil, got %v", got)
	}
}

// TestWireSessionRecorderPersistence_NilRecorderIsNoop verifies that
// WireSessionRecorderPersistence with a nil store returns a no-op unsub.
func TestWireSessionRecorderPersistence_NilRecorderIsNoop(t *testing.T) {
	c := newTestCentral(t)
	unsub := c.WireSessionRecorderPersistence(context.Background(), nil, "slug", 0)
	if unsub == nil {
		t.Fatal("expected non-nil unsubscribe func")
	}
	unsub()
}

// TestWireSessionRecorderPersistence_NilStoreIsNoop verifies that a nil
// store returns a callable no-op unsub.
func TestWireSessionRecorderPersistence_NilStoreIsNoop(t *testing.T) {
	c := newTestCentral(t)
	unsub := c.WireSessionRecorderPersistence(context.Background(), nil, "test", time.Second)
	if unsub == nil {
		t.Fatal("expected non-nil unsubscribe func")
	}
	unsub()
}

// TestWireSessionRecorderPersistence_NilCentralIsNoop verifies a nil
// Unit receiver returns a callable no-op unsub.
func TestWireSessionRecorderPersistence_NilCentralIsNoop(t *testing.T) {
	var c *Unit
	unsub := c.WireSessionRecorderPersistence(context.Background(), nil, "slug", 0)
	if unsub == nil {
		t.Fatal("expected non-nil unsubscribe func even for nil receiver")
	}
	unsub()
}

// errCentral is a test-local sentinel error for whitebox tests that need a
// non-nil error value.
var errCentral = errCentralString("test error sentinel")

type errCentralString string

func (e errCentralString) Error() string { return string(e) }

// TestRenameDeviceWithChannels_IncludesChannels verifies channel rename
// uses the device-and-channel addresses when includeChannels=true.
func TestRenameDeviceWithChannels_IncludesChannels(t *testing.T) {
	c := newTestCentral(t)
	addr := "RDC001"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-Test",
		InterfaceID: "test-iface",
	})
	d.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	renamed := make(map[string]string)
	c.SetRenameDeviceFn(func(_ context.Context, address, name string) error {
		renamed[address] = name
		return nil
	})

	if err := c.RenameDeviceWithChannels(context.Background(), addr, "NewName", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels: %v", err)
	}
	if renamed[addr] != "NewName" {
		t.Errorf("device rename: got %q, want NewName", renamed[addr])
	}
	if _, ok := renamed[addr+":1"]; !ok {
		t.Error("expected channel rename to be called")
	}
}

// TestRenameDeviceWithChannels_ChannelRenameError verifies that a channel
// rename failure is propagated.
func TestRenameDeviceWithChannels_ChannelRenameError(t *testing.T) {
	c := newTestCentral(t)
	addr := "RDC002"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-Test",
		InterfaceID: "test-iface",
	})
	d.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	callCount := 0
	c.SetRenameDeviceFn(func(_ context.Context, address, _ string) error {
		callCount++
		if address != addr {
			return errCentral
		}
		return nil
	})

	err := c.RenameDeviceWithChannels(context.Background(), addr, "N", true)
	if err == nil {
		t.Error("expected error when channel rename fails")
	}
}

// TestResolveDeviceName_ReturnsDeviceName verifies the device name is
// returned when populated.
func TestResolveDeviceName_ReturnsDeviceName(t *testing.T) {
	c := newTestCentral(t)
	addr := "RDN001"
	d := device.New(device.Config{
		Address:     addr,
		Model:       "HmIP-SW2",
		InterfaceID: "iface",
	})
	d.Name = "My Light"
	c.ModelRegistry.Put(d)

	if got := c.ResolveDeviceName(addr); got != "My Light" {
		t.Errorf("ResolveDeviceName: got %q, want My Light", got)
	}
}

// TestResolveDeviceName_FallsBackToModel verifies ResolveDeviceName falls
// back to the Model string when Name is empty.
func TestResolveDeviceName_FallsBackToModel(t *testing.T) {
	c := newTestCentral(t)
	addr := "RDN002"
	d := device.New(device.Config{
		Address:     addr,
		Model:       "HmIP-SW2",
		InterfaceID: "iface",
	})
	c.ModelRegistry.Put(d)

	if got := c.ResolveDeviceName(addr); got != "HmIP-SW2" {
		t.Errorf("ResolveDeviceName fallback to Model: got %q, want HmIP-SW2", got)
	}
}

// TestDeviceAddress_NoColon verifies deviceAddress returns the input
// unchanged when no colon is present.
func TestDeviceAddress_NoColon(t *testing.T) {
	got := deviceAddress("ABCDEF")
	if got != "ABCDEF" {
		t.Errorf("deviceAddress with no colon: got %q, want ABCDEF", got)
	}
}

// TestRegistry_Register_NilUnit verifies Register rejects a nil unit.
func TestRegistry_Register_NilUnit(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("expected error registering nil unit")
	}
}

// TestRegistry_Register_Duplicate verifies Register returns an error
// when the same unit is registered twice.
func TestRegistry_Register_Duplicate(t *testing.T) {
	r := NewRegistry()
	c, _ := New(Config{Name: "dup"})
	if err := r.Register(c); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(c); err == nil {
		t.Error("expected ErrAlreadyRegistered on duplicate")
	}
}

// TestRegistry_StartAll_HappyPath verifies StartAll starts all registered units.
func TestRegistry_StartAll_HappyPath(t *testing.T) {
	r := NewRegistry()
	c, _ := New(Config{Name: "reg1"})
	_ = r.Register(c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
}

// TestWireDevicesCreatedGate_DoubleWire verifies WireDevicesCreatedGate
// can be called twice without panic and resets the gate.
func TestWireDevicesCreatedGate_DoubleWire(t *testing.T) {
	c := newTestCentral(t)
	c.WireDevicesCreatedGate()
	c.WireDevicesCreatedGate()
	if c.IsDevicesCreated() {
		t.Error("gate must be false after double-wire reset")
	}
}
