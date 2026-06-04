// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func TestRegisterStandardJobsRegistersHeartbeat(t *testing.T) {
	c, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	names, err := RegisterStandardJobs(c, StandardJobs{})
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	// Default registration includes health_heartbeat + check_connection
	// (the latter is always registered when unit.Clients is non-nil,
	// which New always provides).
	found := false
	for _, n := range names {
		if n == "central.health_heartbeat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("default registration = %v, want central.health_heartbeat in list", names)
	}
	foundCC := false
	for _, n := range names {
		if n == "central.check_connection" {
			foundCC = true
		}
	}
	if !foundCC {
		t.Fatalf("default registration = %v, want central.check_connection in list", names)
	}
}

func TestRegisterStandardJobsHonoursNonNilSlots(t *testing.T) {
	c, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := StandardJobs{
		HubConnectivityRefresh: func(context.Context) error { return nil },
		HubMetricsRefresh:      func(context.Context) error { return errors.New("ignored") },
		FirmwareUpdateCheck:    func(context.Context) error { return nil },
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	// The mandatory jobs (heartbeat + check_connection) are always present.
	// Optional jobs appear when their callback is non-nil.
	wantPresent := []string{
		"central.health_heartbeat",
		"central.check_connection",
		"hub.connectivity_refresh",
		"hub.metrics_refresh",
		"central.firmware_check",
	}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, w := range wantPresent {
		if !nameSet[w] {
			t.Fatalf("registered=%v, want %q in list", names, w)
		}
	}
}

func TestRegisterStandardJobsUsesCustomIntervals(t *testing.T) {
	c, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Confirm overrides do not error and the registration succeeds.
	if _, err := RegisterStandardJobs(c, StandardJobs{
		HealthHeartbeatInterval: 5 * time.Second,
		HubConnectivityInterval: 30 * time.Second,
		HubConnectivityRefresh:  func(context.Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterStandardJobsRegistersInstallModeSlot(t *testing.T) {
	c, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	cfg := StandardJobs{
		InstallModeRefresh: func(context.Context) error { calls++; return nil },
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	found := slices.Contains(names, "hub.install_mode_refresh")
	if !found {
		t.Fatalf("install_mode_refresh slot missing in %v", names)
	}
}

func TestRegisterStandardJobsRejectsNilUnit(t *testing.T) {
	if _, err := RegisterStandardJobs(nil, StandardJobs{}); err == nil {
		t.Fatal("nil unit must fail")
	}
}

// --- C-SCHED-1: central.check_connection ---

// makeConnectedClient builds a minimal InterfaceClient + state-machine pair
// in CONNECTED state and registers it with the central's ClientCoordinator.
func makeConnectedClient(t *testing.T, c *Unit, ifaceID string) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName: c.cfg.Name,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	// Advance state machine to CONNECTED via the valid transition path.
	for _, target := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
	} {
		if err := ic.TransitionTo(target, "test", false, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", target, err)
		}
	}
	// Mark the callback as alive.
	ic.NotifyCallback()
	entry := &coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}
	if err := c.Clients.Register(entry); err != nil {
		t.Fatalf("register client: %v", err)
	}
	return ic
}

// findJobRun returns the Run function of the named job from the
// scheduler's registered jobs, or nil when not found.
func findJobRun(c *Unit, name string) func(context.Context) error {
	for _, j := range c.Scheduler.Jobs() {
		if j.Name == name {
			return j.Run
		}
	}
	return nil
}

// TestCheckConnectionJobPublishesConnectionLostOnFailedPing verifies
// that the central.check_connection job emits [hmevent.ConnectionLostEvent]
// for a client whose state has transitioned to Disconnected.
//
// IsCallbackAlive returns true for a zero-timestamp client (no false
// positives during init), so the trigger used here is client state
// Disconnected — one of the three conditions that causes the job to
// fire ConnectionLostEvent.
func TestCheckConnectionJobPublishesConnectionLostOnFailedPing(t *testing.T) {
	c, err := New(Config{Name: "test-cc"})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to ConnectionLostEvent before registering the job.
	var lostCount atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(e hmevent.ConnectionLostEvent) {
		if e.CentralName == "test-cc" && e.InterfaceID == "HmIP-RF" {
			lostCount.Add(1)
		}
	})
	defer unsub()

	// Register a client that is DISCONNECTED — the check_connection job
	// emits ConnectionLostEvent when State() != Connected, regardless of
	// callback freshness.
	ic, err := client.New(client.Config{
		CentralName: c.cfg.Name,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []hmenum.ClientState{
		hmenum.ClientStateInitializing,
		hmenum.ClientStateInitialized,
		hmenum.ClientStateConnecting,
		hmenum.ClientStateConnected,
		hmenum.ClientStateDisconnected,
	} {
		if err := ic.TransitionTo(target, "test", false, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition %s: %v", target, err)
		}
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterStandardJobs(c, StandardJobs{
		CheckConnectionInterval: 10 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	run := findJobRun(c, "central.check_connection")
	if run == nil {
		t.Fatal("central.check_connection job not registered")
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("job run: %v", err)
	}

	if lostCount.Load() != 1 {
		t.Errorf("ConnectionLostEvent count=%d, want 1", lostCount.Load())
	}
}

// TestCheckConnectionJobDoesNotFireOnSuccessfulPing verifies that the
// central.check_connection job does NOT emit [hmevent.ConnectionLostEvent]
// for a client that is CONNECTED and whose callback is alive.
func TestCheckConnectionJobDoesNotFireOnSuccessfulPing(t *testing.T) {
	c, err := New(Config{Name: "test-cc2"})
	if err != nil {
		t.Fatal(err)
	}

	var lostCount atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(e hmevent.ConnectionLostEvent) {
		lostCount.Add(1)
	})
	defer unsub()

	makeConnectedClient(t, c, "HmIP-RF")

	if _, err := RegisterStandardJobs(c, StandardJobs{
		CheckConnectionInterval: 10 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	run := findJobRun(c, "central.check_connection")
	if run == nil {
		t.Fatal("central.check_connection job not registered")
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("job run: %v", err)
	}

	if lostCount.Load() != 0 {
		t.Errorf("ConnectionLostEvent count=%d, want 0 for healthy client", lostCount.Load())
	}
}

// TestCheckConnectionDisabledByNegativeInterval verifies that a negative
// CheckConnectionInterval prevents the central.check_connection job from
// being registered.
func TestCheckConnectionDisabledByNegativeInterval(t *testing.T) {
	c, err := New(Config{Name: "test-cc3"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterStandardJobs(c, StandardJobs{
		CheckConnectionInterval: -1,
	}); err != nil {
		t.Fatal(err)
	}

	if run := findJobRun(c, "central.check_connection"); run != nil {
		t.Error("central.check_connection should not be registered when interval is negative")
	}
}

// TestStandardJobsFirmwareDeliveryInterval verifies that the firmware-delivery
// check fires with a 1-hour default, not the erroneous 1-minute value.
func TestStandardJobsFirmwareDeliveryInterval(t *testing.T) {
	if defaultFirmwareDeliverySlot != time.Hour {
		t.Errorf("defaultFirmwareDeliverySlot=%v, want 1h", defaultFirmwareDeliverySlot)
	}
}

// TestStandardJobsRefreshClientDataDefault verifies that RegisterStandardJobs
// wires a default RefreshClientData implementation when none is supplied, and
// that it publishes DataRefreshTriggeredEvent / DataRefreshCompletedEvent.
// Closes.
func TestStandardJobsRefreshClientDataDefault(t *testing.T) {
	c, err := New(Config{Name: "test-g19"})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to both bookend events before wiring jobs.
	var triggered, completed atomic.Int32
	unsubT := events.Subscribe(c.EventBus, func(e hmevent.DataRefreshTriggeredEvent) {
		if e.CentralName == "test-g19" {
			triggered.Add(1)
		}
	})
	defer unsubT()
	unsubC := events.Subscribe(c.EventBus, func(e hmevent.DataRefreshCompletedEvent) {
		if e.CentralName == "test-g19" {
			completed.Add(1)
		}
	})
	defer unsubC()

	// Wire jobs without providing RefreshClientData — the default should apply.
	names, err := RegisterStandardJobs(c, StandardJobs{})
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}

	found := false
	for _, n := range names {
		if n == "central.refresh_client_data" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected central.refresh_client_data in registered jobs, got %v", names)
	}

	run := findJobRun(c, "central.refresh_client_data")
	if run == nil {
		t.Fatal("central.refresh_client_data job not found in scheduler")
	}

	// Drive the state machine to RUNNING so gatedRun passes.
	// Starting → Initializing → Running.
	for _, state := range []hmenum.CentralState{
		hmenum.CentralStateInitializing,
		hmenum.CentralStateRunning,
	} {
		if err := c.StateMachine.TransitionTo(state, hmenum.FailureReasonNone); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}

	// Run the job once; LoadAndRefreshDataPointData returns
	// ErrNotWired (no fn wired) — the default swallows the error in
	// the event fields but still publishes both bookend events.
	_ = run(context.Background())

	if triggered.Load() == 0 {
		t.Error("DataRefreshTriggeredEvent not published by default RefreshClientData")
	}
	if completed.Load() == 0 {
		t.Error("DataRefreshCompletedEvent not published by default RefreshClientData")
	}
}

// TestIsOperational_NilUnit verifies isOperational returns false for nil.
func TestIsOperational_NilUnit(t *testing.T) {
	if isOperational(nil) {
		t.Error("nil unit must return false")
	}
}

// TestIsOperational_DegradedState verifies DEGRADED state is considered operational.
func TestIsOperational_DegradedState(t *testing.T) {
	c := newTestCentral(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Start(ctx)
	_ = c.StateMachine.ForceTransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone)
	if !isOperational(c) {
		t.Error("DEGRADED state must be operational")
	}
}

// TestIsOperational_StoppedState verifies a pre-started central is not operational.
func TestIsOperational_StoppedState(t *testing.T) {
	c := newTestCentral(t)
	if isOperational(c) {
		t.Error("pre-started central must not be operational")
	}
}

// TestHasConnectionIssue_NilUnit verifies hasConnectionIssue returns false for nil.
func TestHasConnectionIssue_NilUnit(t *testing.T) {
	if hasConnectionIssue(nil) {
		t.Error("nil unit must return false")
	}
}

// TestHasConnectionIssue_NilClients verifies hasConnectionIssue returns false
// when the Clients coordinator is nil.
func TestHasConnectionIssue_NilClients(t *testing.T) {
	c := newTestCentral(t)
	c.Clients = nil
	if hasConnectionIssue(c) {
		t.Error("nil Clients must return false")
	}
}

// TestHasConnectionIssue_NoClients verifies an empty client registry reports no issue.
func TestHasConnectionIssue_NoClients(t *testing.T) {
	c := newTestCentral(t)
	if hasConnectionIssue(c) {
		t.Error("empty clients must not report connection issue")
	}
}

// TestGatedRun_NotOperational_ReturnsNil verifies gatedRun skips fn when not operational.
func TestGatedRun_NotOperational_ReturnsNil(t *testing.T) {
	c := newTestCentral(t)
	called := false
	fn := gatedRun(c, false, func(_ context.Context) error {
		called = true
		return nil
	})
	_ = fn(context.Background())
	if called {
		t.Error("gatedRun must not call fn when not operational")
	}
}

// TestGatedRun_Operational_CallsFn verifies gatedRun calls fn when operational.
func TestGatedRun_Operational_CallsFn(t *testing.T) {
	c := newTestCentral(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Start(ctx)

	called := false
	fn := gatedRun(c, false, func(_ context.Context) error {
		called = true
		return nil
	})
	_ = fn(context.Background())
	if !called {
		t.Error("gatedRun must call fn when operational")
	}
}

// advanceCentralToRunning walks the central state machine from its current
// state to RUNNING via the valid transition path.
func advanceCentralToRunning(t *testing.T, c *Unit) {
	t.Helper()
	if err := c.StateMachine.TransitionTo(hmenum.CentralStateInitializing, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Initializing: %v", err)
	}
	if err := c.StateMachine.TransitionTo(hmenum.CentralStateRunning, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Running: %v", err)
	}
}

// TestRefreshClientDataJobRegisters verifies that the RefreshClientData
// slot is registered when a non-nil function is provided.
func TestRefreshClientDataJobRegisters(t *testing.T) {
	c, err := New(Config{Name: "test-rcd"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cfg := StandardJobs{
		RefreshClientData: func(context.Context) error { calls.Add(1); return nil },
	}
	names, err := RegisterStandardJobs(c, cfg)
	if err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "central.refresh_client_data" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refresh_client_data not in registered jobs: %v", names)
	}
}

// TestRefreshClientDataJobGatedByState verifies that the RefreshClientData
// job does NOT fire when the central state is not RUNNING or DEGRADED.
func TestRefreshClientDataJobGatedByState(t *testing.T) {
	c, err := New(Config{Name: "test-rcd-gate"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	cfg := StandardJobs{
		RefreshClientData:         func(context.Context) error { calls.Add(1); return nil },
		RefreshClientDataInterval: 10 * time.Second,
	}
	if _, err := RegisterStandardJobs(c, cfg); err != nil {
		t.Fatalf("RegisterStandardJobs: %v", err)
	}
	run := findJobRun(c, "central.refresh_client_data")
	if run == nil {
		t.Fatal("refresh_client_data job not found")
	}

	if err := run(context.Background()); err != nil {
		t.Fatalf("job run in non-operational state: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("refresh_client_data fired in STARTING state, calls=%d", calls.Load())
	}

	advanceCentralToRunning(t, c)
	if err := run(context.Background()); err != nil {
		t.Fatalf("job run in RUNNING state: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("refresh_client_data expected 1 call in RUNNING, got %d", calls.Load())
	}
}

// TestNonConnectionJobGatedByConnectionIssue verifies that a hub refresh
// job is skipped when at least one client is not CONNECTED.
func TestNonConnectionJobGatedByConnectionIssue(t *testing.T) {
	c, err := New(Config{Name: "test-cig"})
	if err != nil {
		t.Fatal(err)
	}
	advanceCentralToRunning(t, c)

	ic, err := client.New(client.Config{
		CentralName: c.cfg.Name,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	cfg := StandardJobs{
		HubConnectivityRefresh:  func(context.Context) error { calls.Add(1); return nil },
		HubConnectivityInterval: 10 * time.Second,
	}
	if _, err := RegisterStandardJobs(c, cfg); err != nil {
		t.Fatal(err)
	}
	run := findJobRun(c, "hub.connectivity_refresh")
	if run == nil {
		t.Fatal("hub.connectivity_refresh job not found")
	}

	if err := run(context.Background()); err != nil {
		t.Fatalf("job run: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("hub.connectivity_refresh must not fire when connection issue present, calls=%d", calls.Load())
	}
}

// TestNonConnectionJobFiresWhenAllConnected verifies that hub refresh
// jobs fire when all clients are CONNECTED.
func TestNonConnectionJobFiresWhenAllConnected(t *testing.T) {
	c, err := New(Config{Name: "test-cig2"})
	if err != nil {
		t.Fatal(err)
	}
	advanceCentralToRunning(t, c)
	makeConnectedClient(t, c, "HmIP-RF")

	var calls atomic.Int32
	cfg := StandardJobs{
		HubConnectivityRefresh:  func(context.Context) error { calls.Add(1); return nil },
		HubConnectivityInterval: 10 * time.Second,
	}
	if _, err := RegisterStandardJobs(c, cfg); err != nil {
		t.Fatal(err)
	}
	run := findJobRun(c, "hub.connectivity_refresh")
	if run == nil {
		t.Fatal("hub.connectivity_refresh job not found")
	}

	if err := run(context.Background()); err != nil {
		t.Fatalf("job run: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("hub.connectivity_refresh expected 1 call when all connected, got %d", calls.Load())
	}
}

// TestIsOperationalReturnsTrueOnlyForRunningOrDegraded verifies the
// isOperational helper covers RUNNING and DEGRADED but no other state.
func TestIsOperationalReturnsTrueOnlyForRunningOrDegraded(t *testing.T) {
	c, err := New(Config{Name: "test-op"})
	if err != nil {
		t.Fatal(err)
	}

	if isOperational(c) {
		t.Error("isOperational must be false in STARTING state")
	}

	advanceCentralToRunning(t, c)
	if !isOperational(c) {
		t.Error("isOperational must be true in RUNNING state")
	}

	if err := c.StateMachine.TransitionTo(hmenum.CentralStateDegraded, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Degraded: %v", err)
	}
	if !isOperational(c) {
		t.Error("isOperational must be true in DEGRADED state")
	}

	if err := c.StateMachine.TransitionTo(hmenum.CentralStateStopped, hmenum.FailureReasonNone); err != nil {
		t.Fatalf("transition to Stopped: %v", err)
	}
	if isOperational(c) {
		t.Error("isOperational must be false in STOPPED state")
	}
}

// TestHasConnectionIssueReturnsFalseWithNoClients verifies hasConnectionIssue
// is false when no clients are registered.
func TestHasConnectionIssueReturnsFalseWithNoClients(t *testing.T) {
	c, err := New(Config{Name: "test-hci"})
	if err != nil {
		t.Fatal(err)
	}
	if hasConnectionIssue(c) {
		t.Error("hasConnectionIssue must be false with no registered clients")
	}
}

// TestHasConnectionIssueTrueWhenClientDisconnected verifies that one
// disconnected client causes hasConnectionIssue to return true.
func TestHasConnectionIssueTrueWhenClientDisconnected(t *testing.T) {
	c, err := New(Config{Name: "test-hci2"})
	if err != nil {
		t.Fatal(err)
	}

	ic, err := client.New(client.Config{
		CentralName: c.cfg.Name,
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Client:      ic,
	}); err != nil {
		t.Fatal(err)
	}

	if !hasConnectionIssue(c) {
		t.Error("hasConnectionIssue must be true when a client is not CONNECTED")
	}
}
