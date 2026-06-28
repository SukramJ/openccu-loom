// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// hub_wiring_refresh_hooks_test.go verifies that WireHub's SetRefreshHooks
// call installs all seven refresh closures and that each load function
// correctly fetches from the ReGa runner fake and updates the hub model.

package adapter

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
)

// ============================================================
// SetRefreshHooks — all 7 slots are installed
// ============================================================

// TestSetRefreshHooksAllSlotsInstalled verifies that WireHub installs all
// seven refresh hooks and that each fires exactly once when called through
// the HubCoordinator's Refresh* methods.
func TestSetRefreshHooksAllSlotsInstalled(t *testing.T) {
	t.Parallel()

	// Counters incremented when each hook is invoked.
	var (
		programs        atomic.Int32
		sysvars         atomic.Int32
		inbox           atomic.Int32
		serviceMessages atomic.Int32
		alarmMessages   atomic.Int32
		systemUpdate    atomic.Int32
		installMode     atomic.Int32
	)

	hub := coordinators.NewHubCoordinator("ccu-test", nil)
	hub.SetRefreshHooks(coordinators.RefreshHooks{
		Programs:        func(_ context.Context) error { programs.Add(1); return nil },
		Sysvars:         func(_ context.Context) error { sysvars.Add(1); return nil },
		Inbox:           func(_ context.Context) error { inbox.Add(1); return nil },
		ServiceMessages: func(_ context.Context) error { serviceMessages.Add(1); return nil },
		AlarmMessages:   func(_ context.Context) error { alarmMessages.Add(1); return nil },
		SystemUpdate:    func(_ context.Context) error { systemUpdate.Add(1); return nil },
		InstallMode:     func(_ context.Context) error { installMode.Add(1); return nil },
	})

	ctx := context.Background()
	if err := hub.RefreshPrograms(ctx); err != nil {
		t.Fatalf("RefreshPrograms: %v", err)
	}
	if err := hub.RefreshSysvars(ctx); err != nil {
		t.Fatalf("RefreshSysvars: %v", err)
	}
	if err := hub.RefreshInbox(ctx); err != nil {
		t.Fatalf("RefreshInbox: %v", err)
	}
	if err := hub.RefreshServiceMessages(ctx); err != nil {
		t.Fatalf("RefreshServiceMessages: %v", err)
	}
	if err := hub.RefreshAlarmMessages(ctx); err != nil {
		t.Fatalf("RefreshAlarmMessages: %v", err)
	}
	if err := hub.RefreshSystemUpdate(ctx); err != nil {
		t.Fatalf("RefreshSystemUpdate: %v", err)
	}
	if err := hub.RefreshInstallMode(ctx); err != nil {
		t.Fatalf("RefreshInstallMode: %v", err)
	}

	checks := []struct {
		name string
		got  int32
	}{
		{"programs", programs.Load()},
		{"sysvars", sysvars.Load()},
		{"inbox", inbox.Load()},
		{"serviceMessages", serviceMessages.Load()},
		{"alarmMessages", alarmMessages.Load()},
		{"systemUpdate", systemUpdate.Load()},
		{"installMode", installMode.Load()},
	}
	for _, c := range checks {
		if c.got != 1 {
			t.Errorf("hook %q called %d times, want 1", c.name, c.got)
		}
	}
}

// ============================================================
// loadAlarmMessages — ReGa runner fake
// ============================================================

func TestLoadAlarmMessagesPopulatesHub(t *testing.T) {
	t.Parallel()

	r := newRegaRunnerFor(
		t,
		`[{"id":"a1","name":"Alarm One","device_name":"Dev A","address":"ABC:1"},`+
			`{"id":"a2","name":"Alarm Two","device_name":"Dev B","address":"DEF:2"}]`,
	)
	h := hubmodel.NewHub("test-central")

	if err := loadAlarmMessages(context.Background(), r, h, nil, "en"); err != nil {
		t.Fatalf("loadAlarmMessages: %v", err)
	}
	got := h.Messages.List()
	if len(got) != 2 {
		t.Fatalf("alarm messages count = %d, want 2", len(got))
	}
}

func TestLoadAlarmMessagesNilRunner(t *testing.T) {
	t.Parallel()
	h := hubmodel.NewHub("test-central")
	// A nil runner must be a no-op, not a panic.
	if err := loadAlarmMessages(context.Background(), nil, h, nil, "en"); err != nil {
		t.Errorf("loadAlarmMessages(nil runner) = %v, want nil", err)
	}
}

func TestLoadAlarmMessagesNilHub(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerFor(t, `[]`)
	// Must not panic.
	if err := loadAlarmMessages(context.Background(), r, nil, nil, "en"); err != nil {
		t.Errorf("loadAlarmMessages(nil hub) = %v, want nil", err)
	}
}

// ============================================================
// loadSystemUpdate — ReGa runner fake
// ============================================================

// newRegaRunnerFor builds a rega.Runner backed by a JSON-RPC fake that
// routes ReGa.runScript calls by returning scriptJSON as the result string.
// The CCU returns the script stdout as a raw JSON string in the result field.
func newRegaRunnerFor(t *testing.T, scriptJSON string) *rega.Runner {
	t.Helper()
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript": func(_ map[string]any) any {
			// The result must be a string (the CCU's script stdout).
			return scriptJSON
		},
	})
	jc := newJSONRPCClient(t, srv.URL)
	r, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return r
}

func TestLoadSystemUpdatePopulatesHub(t *testing.T) {
	t.Parallel()

	r := newRegaRunnerFor(
		t,
		`{"current_firmware":"3.55.10","available_firmware":"3.57.0","update_available":true,"check_script_available":true}`,
	)
	h := hubmodel.NewHub("test-central")

	if err := loadSystemUpdate(context.Background(), r, h); err != nil {
		t.Fatalf("loadSystemUpdate: %v", err)
	}
	info, observed := h.Update.UpdateInfo()
	if !observed {
		t.Fatal("Update.observed must be true after loadSystemUpdate")
	}
	if info.CurrentFirmware != "3.55.10" {
		t.Errorf("CurrentFirmware = %q, want 3.55.10", info.CurrentFirmware)
	}
	if !info.UpdateAvailable {
		t.Error("UpdateAvailable must be true when available_firmware != current_firmware")
	}
}

func TestLoadSystemUpdateNoAvailableVersion(t *testing.T) {
	t.Parallel()

	r := newRegaRunnerFor(
		t,
		`{"current_firmware":"3.57.0","available_firmware":"","update_available":false,"check_script_available":true}`,
	)
	h := hubmodel.NewHub("test-central")

	if err := loadSystemUpdate(context.Background(), r, h); err != nil {
		t.Fatalf("loadSystemUpdate: %v", err)
	}
	info, _ := h.Update.UpdateInfo()
	if info.UpdateAvailable {
		t.Error("UpdateAvailable must be false when available_firmware is empty")
	}
}

// TestLoadSystemUpdateKeepsLastKnownFirmware pins the transient-exec
// guard: ReGaHss occasionally returns an empty current_firmware (the
// `grep VERSION= /VERSION` system.Exec yields no output) while the
// rest of the payload is valid. A previously observed non-empty
// firmware version must survive such a refresh instead of regressing
// the surface to an empty string.
func TestLoadSystemUpdateKeepsLastKnownFirmware(t *testing.T) {
	t.Parallel()

	h := hubmodel.NewHub("test-central")

	good := newRegaRunnerFor(
		t,
		`{"current_firmware":"3.87.6.20260509","available_firmware":"","update_available":false,"check_script_available":true}`,
	)
	if err := loadSystemUpdate(context.Background(), good, h); err != nil {
		t.Fatalf("loadSystemUpdate (good): %v", err)
	}

	empty := newRegaRunnerFor(
		t,
		`{"current_firmware":"","available_firmware":"","update_available":false,"check_script_available":true}`,
	)
	if err := loadSystemUpdate(context.Background(), empty, h); err != nil {
		t.Fatalf("loadSystemUpdate (empty): %v", err)
	}

	info, observed := h.Update.UpdateInfo()
	if !observed {
		t.Fatal("Update.observed must stay true")
	}
	if info.CurrentFirmware != "3.87.6.20260509" {
		t.Errorf("CurrentFirmware = %q, want last known 3.87.6.20260509", info.CurrentFirmware)
	}
}

// TestLoadSystemUpdateEmptyFirstObservation pins that a first-ever
// refresh delivering an empty current_firmware is still recorded
// (observed=true, empty value) — there is no previous value to keep,
// and the next scheduled refresh delivers the real one.
func TestLoadSystemUpdateEmptyFirstObservation(t *testing.T) {
	t.Parallel()

	r := newRegaRunnerFor(
		t,
		`{"current_firmware":"","available_firmware":"","update_available":false,"check_script_available":true}`,
	)
	h := hubmodel.NewHub("test-central")
	if err := loadSystemUpdate(context.Background(), r, h); err != nil {
		t.Fatalf("loadSystemUpdate: %v", err)
	}
	info, observed := h.Update.UpdateInfo()
	if !observed {
		t.Fatal("Update.observed must be true after a successful refresh")
	}
	if info.CurrentFirmware != "" {
		t.Errorf("CurrentFirmware = %q, want empty on first observation", info.CurrentFirmware)
	}
}

// ============================================================
// runInitialSystemUpdateLoad — boot-time fetch
// ============================================================

// recordingSystemUpdateRefresher captures RefreshSystemUpdate calls.
type recordingSystemUpdateRefresher struct {
	calls int
	err   error
}

func (r *recordingSystemUpdateRefresher) RefreshSystemUpdate(_ context.Context) error {
	r.calls++
	return r.err
}

// TestRunInitialSystemUpdateLoadInvokesRefresher pins the boot-time
// one-shot: the reference stack's scheduler runs every job immediately
// at start, so the Go wiring must trigger the system-update fetch once
// without waiting for the 60-minute hub.system_update_refresh slot.
func TestRunInitialSystemUpdateLoadInvokesRefresher(t *testing.T) {
	t.Parallel()
	rec := &recordingSystemUpdateRefresher{}
	runInitialSystemUpdateLoad(rec, "test-central", slog.New(slog.DiscardHandler))
	if rec.calls != 1 {
		t.Errorf("RefreshSystemUpdate calls = %d, want 1", rec.calls)
	}
}

// TestRunInitialSystemUpdateLoadToleratesFailure pins that a failing
// boot-time fetch is logged and swallowed — the scheduled refresh
// retries later; boot must not be affected.
func TestRunInitialSystemUpdateLoadToleratesFailure(t *testing.T) {
	t.Parallel()
	rec := &recordingSystemUpdateRefresher{err: context.DeadlineExceeded}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("runInitialSystemUpdateLoad panicked on refresh error: %v", r)
		}
	}()
	runInitialSystemUpdateLoad(rec, "test-central", slog.New(slog.DiscardHandler))
	if rec.calls != 1 {
		t.Errorf("RefreshSystemUpdate calls = %d, want 1", rec.calls)
	}
	// Nil refresher and nil logger must be no-ops, not panics.
	runInitialSystemUpdateLoad(nil, "test-central", nil)
	runInitialSystemUpdateLoad(&recordingSystemUpdateRefresher{err: context.Canceled}, "test-central", nil)
}

// ============================================================
// loadInbox — central-wide ReGa query
// ============================================================

func newMinimalUnit(t *testing.T, ifaceIDs ...string) *central.Unit {
	t.Helper()
	unit, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	for _, id := range ifaceIDs {
		_ = unit.Clients.Register(&coordinators.ClientEntry{
			InterfaceID: id,
		})
	}
	return unit
}

func newJSONRPCClientFor(t *testing.T, routes map[string]func(map[string]any) any) *jsonrpc.Client {
	t.Helper()
	srv := newJSONRPCFake(t, routes)
	return newJSONRPCClient(t, srv.URL)
}

func TestLoadInboxPopulatesUnit(t *testing.T) {
	t.Parallel()

	r := newRegaRunnerFor(
		t,
		`[{"id":"d1","address":"001A2B3C","name":"PSM","type":"HmIP-PSM","interface":"HmIP-RF"},`+
			`{"id":"d2","address":"456D7E8F","name":"Switch","type":"HM-LC-Sw1-FM","interface":"BidCos-RF"}]`,
	)
	unit := newMinimalUnit(t, "HmIP-RF", "BidCos-RF")

	if err := loadInbox(context.Background(), r, unit); err != nil {
		t.Fatalf("loadInbox: %v", err)
	}
	devices := unit.HubModel.Inbox.List()
	if len(devices) != 2 {
		t.Fatalf("inbox device count = %d, want 2", len(devices))
	}
}

func TestLoadInboxNilRunner(t *testing.T) {
	t.Parallel()
	unit := newMinimalUnit(t)
	// A nil runner must be a no-op, not a panic.
	if err := loadInbox(context.Background(), nil, unit); err != nil {
		t.Errorf("loadInbox(nil runner) = %v, want nil", err)
	}
}

func TestLoadInboxNilUnit(t *testing.T) {
	t.Parallel()
	r := newRegaRunnerFor(t, `[]`)
	if err := loadInbox(context.Background(), r, nil); err != nil {
		t.Errorf("loadInbox(nil unit) = %v, want nil", err)
	}
}

// ============================================================
// loadServiceMessages — ReGa runner fake, deduplication by ID
// ============================================================

func TestLoadServiceMessagesDeduplicated(t *testing.T) {
	t.Parallel()

	// The ReGa script returns two entries with the same id — dedup must
	// keep only one in the hub model.
	r := newRegaRunnerFor(
		t,
		`[{"id":"sm-01","name":"Low Battery","address":"HEQ0001:1","device_name":"Sensor"},`+
			`{"id":"sm-01","name":"Low Battery","address":"HEQ0001:1","device_name":"Sensor"}]`,
	)
	unit := newMinimalUnit(t, "HmIP-RF", "BidCos-RF")

	if err := loadServiceMessages(context.Background(), r, unit, nil, "en"); err != nil {
		t.Fatalf("loadServiceMessages: %v", err)
	}
	msgs := unit.HubModel.ServiceMessages.List()
	if len(msgs) != 1 {
		t.Fatalf("service message count = %d after dedup, want 1", len(msgs))
	}
}

func TestLoadServiceMessagesNilRunner(t *testing.T) {
	t.Parallel()
	unit := newMinimalUnit(t)
	// A nil runner must be a no-op, not a panic.
	if err := loadServiceMessages(context.Background(), nil, unit, nil, "en"); err != nil {
		t.Errorf("loadServiceMessages(nil runner) = %v, want nil", err)
	}
}

// ============================================================
// loadInstallMode — polls per registered install-mode DP
// ============================================================

func TestLoadInstallModeUpdatesDP(t *testing.T) {
	t.Parallel()

	var pollCount atomic.Int32
	jc := newJSONRPCClientFor(t, map[string]func(map[string]any) any{
		"Interface.getInstallMode": func(params map[string]any) any {
			pollCount.Add(1)
			iface, _ := params["interface"].(string)
			if iface == "HmIP-RF" {
				return 30 // 30 seconds remaining
			}
			return 0
		},
	})

	unit := newMinimalUnit(t, "HmIP-RF")

	// Register an InstallMode DP for the interface. loadInstallMode reads
	// directly from unit.HubModel; the hub coordinator is needed only for
	// PublishInstallModeRefreshed (which requires a live event bus from
	// the already-wired unit.Hub).
	dp := hubmodel.NewInstallMode("HmIP-RF", nil)
	unit.HubModel.PutInstallMode(dp)

	if err := loadInstallMode(context.Background(), jc, unit); err != nil {
		t.Fatalf("loadInstallMode: %v", err)
	}
	if got := pollCount.Load(); got != 1 {
		t.Errorf("Interface.getInstallMode poll count = %d, want 1", got)
	}
	enabled, remaining, observed := dp.InstallState()
	if !observed {
		t.Fatal("install mode DP must be observed after loadInstallMode")
	}
	if !enabled {
		t.Error("install mode must be enabled when CCU returned 30 s")
	}
	if remaining <= 0 || remaining > 30*time.Second {
		t.Errorf("remaining = %v, want ~30s", remaining)
	}
}

func TestLoadInstallModeNilUnit(t *testing.T) {
	t.Parallel()
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{})
	jc := newJSONRPCClient(t, srv.URL)
	if err := loadInstallMode(context.Background(), jc, nil); err != nil {
		t.Errorf("loadInstallMode(nil) = %v, want nil", err)
	}
}

// ============================================================
// stringField — helper coverage
// ============================================================

func TestStringFieldPresent(t *testing.T) {
	t.Parallel()
	m := map[string]any{"key": "value"}
	if got := stringField(m, "key"); got != "value" {
		t.Errorf("stringField = %q, want value", got)
	}
}

func TestStringFieldMissing(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	if got := stringField(m, "absent"); got != "" {
		t.Errorf("stringField missing = %q, want empty", got)
	}
}

func TestStringFieldWrongType(t *testing.T) {
	t.Parallel()
	m := map[string]any{"count": 42}
	if got := stringField(m, "count"); got != "" {
		t.Errorf("stringField wrong type = %q, want empty", got)
	}
}
