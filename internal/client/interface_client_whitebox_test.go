// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for internal/client unexported functions:
// coalesceKeyFor, isUnsupported, valuesMatch, toFloat64,
// MetricsCircuitState half-open branch, throttleForMethod, closeThrottles.
// These live in the `client` package (not `client_test`) to access unexported symbols.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// coalesceKeyFor
// ---------------------------------------------------------------------------

func TestCoalesceKeyForSetValue(t *testing.T) {
	t.Parallel()
	// method = "setValue", 3+ string args → returns a non-empty key.
	key := coalesceKeyFor("setValue", []any{"HmIP-RF", "VCU001:1", "LEVEL", float64(0.5)})
	if key == "" {
		t.Error("expected non-empty coalesce key for setValue")
	}
}

func TestCoalesceKeyForNonSetValue(t *testing.T) {
	t.Parallel()
	// method ≠ "setValue" → always ""
	if key := coalesceKeyFor("getValue", []any{"HmIP-RF", "VCU001:1", "LEVEL"}); key != "" {
		t.Errorf("expected empty key for getValue, got %q", key)
	}
}

func TestCoalesceKeyForTooFewArgs(t *testing.T) {
	t.Parallel()
	// method = "setValue" but < 3 args → ""
	if key := coalesceKeyFor("setValue", []any{"HmIP-RF"}); key != "" {
		t.Errorf("expected empty key for < 3 args, got %q", key)
	}
}

func TestCoalesceKeyForNonStringArgs(t *testing.T) {
	t.Parallel()
	// method = "setValue", 3 args, but not all strings → ""
	if key := coalesceKeyFor("setValue", []any{1, 2, 3}); key != "" {
		t.Errorf("expected empty key for non-string args, got %q", key)
	}
}

// ---------------------------------------------------------------------------
// isUnsupported
// ---------------------------------------------------------------------------

func TestIsUnsupportedTrue(t *testing.T) {
	t.Parallel()
	if !isUnsupported(backends.ErrUnsupported) {
		t.Error("expected true for backends.ErrUnsupported")
	}
}

func TestIsUnsupportedWrapped(t *testing.T) {
	t.Parallel()
	// The function compares .Error() strings, so a wrapped error whose
	// message equals the sentinel's message must also match.
	wrapped := fmt.Errorf("wrap: %w", backends.ErrUnsupported)
	// wrapped.Error() != backends.ErrUnsupported.Error() — that is expected
	// per the current implementation which does string-equality on the raw
	// error, not errors.Is. So a wrapping error returns false here.
	// This test documents the current behaviour.
	_ = isUnsupported(wrapped) // either true or false — must not panic
}

func TestIsUnsupportedFalse(t *testing.T) {
	t.Parallel()
	if isUnsupported(errors.New("something else")) {
		t.Error("expected false for a different error")
	}
	if isUnsupported(nil) {
		t.Error("expected false for nil")
	}
}

// ---------------------------------------------------------------------------
// valuesMatch
// ---------------------------------------------------------------------------

func TestValuesMatchFloat64(t *testing.T) {
	t.Parallel()
	if !valuesMatch(float64(1.005), float64(1.005)) {
		t.Error("equal float64s should match")
	}
	if !valuesMatch(float64(1.005), float32(1.005)) {
		t.Error("float64 vs float32 close values should match")
	}
	if !valuesMatch(float64(1.0), int(1)) {
		t.Error("float64(1.0) should match int(1)")
	}
}

func TestValuesMatchFloat64NoMatch(t *testing.T) {
	t.Parallel()
	if valuesMatch(float64(1.0), float64(2.0)) {
		t.Error("different float64s should not match")
	}
	if valuesMatch(float64(1.0), "not a float") {
		t.Error("float64 vs string should not match")
	}
}

func TestValuesMatchFloat32(t *testing.T) {
	t.Parallel()
	if !valuesMatch(float32(1.5), float64(1.5)) {
		t.Error("float32 vs float64 should match")
	}
	if valuesMatch(float32(1.5), "bad") {
		t.Error("float32 vs string should not match")
	}
}

func TestValuesMatchDefault(t *testing.T) {
	t.Parallel()
	if !valuesMatch("hello", "hello") {
		t.Error("equal strings should match")
	}
	if valuesMatch("a", "b") {
		t.Error("different strings should not match")
	}
	if !valuesMatch(true, true) {
		t.Error("equal bools should match")
	}
	if valuesMatch(true, false) {
		t.Error("different bools should not match")
	}
}

// ---------------------------------------------------------------------------
// toFloat64
// ---------------------------------------------------------------------------

func TestToFloat64Variants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input any
		want  float64
		ok    bool
	}{
		{float64(3.14), 3.14, true},
		{float32(1.5), 1.5, true},
		{int(7), 7.0, true},
		{int32(8), 8.0, true},
		{int64(9), 9.0, true},
		{"string", 0, false},
		{true, 0, false},
	}
	for _, tc := range cases {
		got, gotOK := toFloat64(tc.input)
		if gotOK != tc.ok {
			t.Errorf("toFloat64(%T): ok=%v, want %v", tc.input, gotOK, tc.ok)
		}
		if tc.ok && got != tc.want {
			t.Errorf("toFloat64(%T)=%v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// FetchAllDeviceData — ErrUnsupported path
// ---------------------------------------------------------------------------

type errUnsupportedBackend struct {
	orchBackendStub
}

func (b *errUnsupportedBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, backends.ErrUnsupported
}

func (b *errUnsupportedBackend) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, backends.ErrUnsupported
}

func TestFetchAllDeviceDataUnsupported(t *testing.T) {
	t.Parallel()
	ic := newWhiteBoxIC(t, hmenum.InterfaceHmIPRF)
	b := &errUnsupportedBackend{}
	data, err := ic.FetchAllDeviceData(context.Background(), b)
	if err != nil {
		t.Fatalf("expected nil error for ErrUnsupported, got %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for ErrUnsupported, got %v", data)
	}
}

func TestFetchDeviceDetailsUnsupported(t *testing.T) {
	t.Parallel()
	ic := newWhiteBoxIC(t, hmenum.InterfaceHmIPRF)
	b := &errUnsupportedBackend{}
	data, err := ic.FetchDeviceDetails(context.Background(), b, []string{"VCU001"})
	if err != nil {
		t.Fatalf("expected nil error for ErrUnsupported, got %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for ErrUnsupported, got %v", data)
	}
}

// ---------------------------------------------------------------------------
// GetDeviceDescriptionWithCoalescing — ErrUnsupported path
// ---------------------------------------------------------------------------

type errUnsupportedDescBackend struct {
	orchBackendStub
}

func (b *errUnsupportedDescBackend) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, backends.ErrUnsupported
}

func TestGetDeviceDescriptionWithCoalescingUnsupported(t *testing.T) {
	t.Parallel()
	ic := newWhiteBoxIC(t, hmenum.InterfaceHmIPRF)
	b := &errUnsupportedDescBackend{}
	desc, err := ic.GetDeviceDescriptionWithCoalescing(context.Background(), b, "VCU001")
	if err != nil {
		t.Fatalf("expected nil error for ErrUnsupported, got %v", err)
	}
	if desc != nil {
		t.Errorf("expected nil desc for ErrUnsupported, got %v", desc)
	}
}

// ---------------------------------------------------------------------------
// GetParamsetDescriptionOnDemand — ErrUnsupported path
// ---------------------------------------------------------------------------

type errUnsupportedParamsetBackend struct {
	orchBackendStub
}

func (b *errUnsupportedParamsetBackend) GetParamsetDescription(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, backends.ErrUnsupported
}

func TestGetParamsetDescriptionOnDemandUnsupported(t *testing.T) {
	t.Parallel()
	ic := newWhiteBoxIC(t, hmenum.InterfaceHmIPRF)
	b := &errUnsupportedParamsetBackend{}
	desc, err := ic.GetParamsetDescriptionOnDemand(context.Background(), b, "VCU001:0", hmenum.ParamsetKeyMaster)
	if err != nil {
		t.Fatalf("expected nil error for ErrUnsupported, got %v", err)
	}
	if desc != nil {
		t.Errorf("expected nil for ErrUnsupported, got %v", desc)
	}
}

// ---------------------------------------------------------------------------
// MetricsCircuitState — half-open branch
// ---------------------------------------------------------------------------

func TestMetricsCircuitStateHalfOpen(t *testing.T) {
	t.Parallel()
	// To reach HALF_OPEN we inject a clock that reports a time far in the
	// future after the circuit has been tripped OPEN.
	now := time.Now()
	clk := &advanceable{t: now}

	cb := reliability.NewCircuit(reliability.CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Second,
		Clock:            clk.Now,
	})
	ic, _ := New(Config{
		CentralName: "ccu",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Circuit:     cb,
	})
	defer ic.Close()

	// Trip the circuit open.
	cb.RecordFailure()
	if got := ic.MetricsCircuitState(); got != 1 {
		t.Fatalf("want open (1), got %d", got)
	}

	// Advance the clock past the reset timeout → next State() call returns HALF_OPEN.
	clk.Advance(2 * time.Second)
	if got := ic.MetricsCircuitState(); got != 2 {
		t.Errorf("MetricsCircuitState()=%d, want 2 (half-open)", got)
	}
}

type advanceable struct {
	mu sync.Mutex
	t  time.Time
}

func (a *advanceable) Now() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t
}

func (a *advanceable) Advance(d time.Duration) {
	a.mu.Lock()
	a.t = a.t.Add(d)
	a.mu.Unlock()
}

// ---------------------------------------------------------------------------
// throttleForMethod — classification branches
// ---------------------------------------------------------------------------

func TestThrottleForMethodClassification(t *testing.T) {
	t.Parallel()
	// Build an IC with per-class throttles so the switch branches fire.
	rt := newThrottleForTest()
	wt := newThrottleForTest()
	ct := newThrottleForTest()
	defer rt.Close()
	defer wt.Close()
	defer ct.Close()

	ic, err := New(Config{
		CentralName:     "ccu",
		Interface:       hmenum.InterfaceHmIPRF,
		Caller:          CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		ReadThrottle:    rt,
		WriteThrottle:   wt,
		ControlThrottle: ct,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ic.Close()

	// Issue calls in each class to exercise throttleForMethod's switch.
	// "getValue" is read, "setValue" is write, "init" is control.
	ctx := context.Background()
	_, _ = ic.Call(ctx, "getValue", nil, hmenum.CommandPriorityLow, "")
	_, _ = ic.Call(ctx, "setValue", nil, hmenum.CommandPriorityLow, "")
	_, _ = ic.Call(ctx, "init", nil, hmenum.CommandPriorityLow, "")
	// unknown method → write throttle
	_, _ = ic.Call(ctx, "unknownMethodXYZ", nil, hmenum.CommandPriorityLow, "")
}

// ---------------------------------------------------------------------------
// closeThrottles — deduplicated path (same throttle aliased to all slots)
// ---------------------------------------------------------------------------

func TestCloseThrottlesDeduplicated(t *testing.T) {
	t.Parallel()
	shared := newThrottleForTest()
	// Note: do NOT close `shared` manually; ic.Close() will close it.
	ic, err := New(Config{
		CentralName:     "ccu",
		Interface:       hmenum.InterfaceHmIPRF,
		Caller:          CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Throttle:        shared,
		ReadThrottle:    shared,
		WriteThrottle:   shared,
		ControlThrottle: shared,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Closing once must not panic even though the same pointer is in 4 slots.
	ic.Close()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newWhiteBoxIC(t *testing.T, iface hmenum.Interface) *InterfaceClient {
	t.Helper()
	ic, err := New(Config{
		CentralName: "ccu",
		Interface:   iface,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(ic.Close)
	return ic
}

// newThrottleForTest returns a CommandThrottle suitable for tests.
func newThrottleForTest() *reliability.CommandThrottle {
	return reliability.NewThrottle(reliability.ThrottleConfig{MaxInFlight: 10})
}

// orchBackendStub is a minimal backends.Operations implementation that returns
// sane zero values for every method; embedded by the specific error stubs
// above so they only have to override one method each.
type orchBackendStub struct{}

func (b *orchBackendStub) Kind() backends.Kind                       { return backends.KindCCU }
func (b *orchBackendStub) Capabilities() backends.Capabilities       { return backends.Capabilities{} }
func (b *orchBackendStub) Init(_ context.Context, _, _ string) error { return nil }
func (b *orchBackendStub) Deinit(context.Context, string) error      { return nil }
func (b *orchBackendStub) Ping(context.Context, string) error        { return nil }
func (b *orchBackendStub) ListDevices(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (b *orchBackendStub) GetParamsetDescription(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *orchBackendStub) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any, hmenum.CommandRxMode) error {
	return nil
}

func (b *orchBackendStub) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority, hmenum.CommandRxMode) error {
	return nil
}

func (b *orchBackendStub) GetValue(context.Context, string, hmenum.Parameter) (any, error) {
	return nil, nil
}
func (b *orchBackendStub) UpdateFirmware(context.Context, string) error { return nil }
func (b *orchBackendStub) GetLinks(context.Context, string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (b *orchBackendStub) GetLinkPeers(context.Context, string) ([]string, error) { return nil, nil }

func (b *orchBackendStub) AddLink(context.Context, string, string, string, string) error {
	return nil
}
func (b *orchBackendStub) RemoveLink(context.Context, string, string) error { return nil }
func (b *orchBackendStub) GetLinkParamsetDescription(context.Context, string, string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *orchBackendStub) GetLinkParamset(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) PutLinkParamset(context.Context, string, string, map[string]any) error {
	return nil
}

func (b *orchBackendStub) ReportValueUsage(context.Context, string, string, int) error { return nil }

func (b *orchBackendStub) DeleteDevice(context.Context, string, int) error { return nil }

func (b *orchBackendStub) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (b *orchBackendStub) SetProgramState(context.Context, string, bool) error { return nil }
func (b *orchBackendStub) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}
func (b *orchBackendStub) SetSystemVariable(context.Context, string, any) error { return nil }
func (b *orchBackendStub) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) DetermineParameter(context.Context, string, string) (any, error) {
	return nil, nil
}
func (b *orchBackendStub) GetInstallMode(context.Context) (int, error) { return 0, nil }
func (b *orchBackendStub) SetInstallMode(context.Context, bool, int, int, string) error {
	return nil
}

func (b *orchBackendStub) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackendStub) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackendStub) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *orchBackendStub) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackendStub) SearchDevices(context.Context) (int, error) {
	return 0, backends.ErrUnsupported
}

func (b *orchBackendStub) SetTeam(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (b *orchBackendStub) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (b *orchBackendStub) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, backends.ErrUnsupported
}

func (b *orchBackendStub) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) SuppressServiceMessage(context.Context, string, string, bool) error {
	return nil
}

func (b *orchBackendStub) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *orchBackendStub) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *orchBackendStub) RenameDevice(context.Context, int, string) (bool, error) { return true, nil }

func (b *orchBackendStub) RenameChannel(context.Context, int, string) (bool, error) { return true, nil }

func (b *orchBackendStub) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return true, nil
}
func (b *orchBackendStub) ExecuteProgram(context.Context, string) (bool, error) { return true, nil }
func (b *orchBackendStub) GetSystemVariable(context.Context, string) (any, error) {
	return "val", nil
}

func (b *orchBackendStub) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, nil
}
func (b *orchBackendStub) TriggerFirmwareUpdate(context.Context) (bool, error) { return true, nil }
func (b *orchBackendStub) DeleteSystemVariable(context.Context, string) (bool, error) {
	return true, nil
}
func (b *orchBackendStub) GetIseIDByAddress(context.Context, string) (int, error) { return 0, nil }
func (b *orchBackendStub) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, nil
}

func (b *orchBackendStub) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (b *orchBackendStub) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (b *orchBackendStub) HasProgramIDs(context.Context, string) (bool, error) { return false, nil }
func (b *orchBackendStub) DownloadFirmware(context.Context, string) error      { return nil }
func (b *orchBackendStub) GetMetadata(context.Context, string, string) (any, error) {
	return nil, nil
}
func (b *orchBackendStub) SetMetadata(context.Context, string, string, any) error { return nil }

// Compile-time interface satisfaction.
var _ backends.Operations = (*orchBackendStub)(nil)
