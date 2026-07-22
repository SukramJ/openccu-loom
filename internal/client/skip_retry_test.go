// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// skip_retry_test.go — skipRetry behaviour:
// IC.SetValue with skipRetry=true must call the backend exactly once even
// when the backend returns a transient error that would normally trigger
// retry. With skipRetry=false and maxAttempts=3 the backend is called up
// to 3 times on a persistent transient error.

package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// countingBackend records the number of SetValue invocations.
// All other methods are no-ops that satisfy backends.Operations.
type countingBackend struct {
	setCallCount atomic.Int64
	setErr       error
}

func (b *countingBackend) incSet() error {
	b.setCallCount.Add(1)
	return b.setErr
}

func (b *countingBackend) SetCallCount() int { return int(b.setCallCount.Load()) }

// --- backends.Operations stub ---
func (b *countingBackend) Kind() backends.Kind { return backends.KindCCU }

func (b *countingBackend) Capabilities() backends.Capabilities       { return backends.Capabilities{} }
func (b *countingBackend) Init(_ context.Context, _, _ string) error { return nil }
func (b *countingBackend) Deinit(_ context.Context, _ string) error  { return nil }
func (b *countingBackend) Ping(_ context.Context, _ string) error    { return nil }
func (b *countingBackend) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (b *countingBackend) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *countingBackend) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandRxMode) error {
	return nil
}

func (b *countingBackend) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return b.incSet()
}

func (b *countingBackend) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, nil
}
func (b *countingBackend) UpdateFirmware(_ context.Context, _ string) error { return nil }
func (b *countingBackend) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (b *countingBackend) GetLinkPeers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (b *countingBackend) AddLink(_ context.Context, _, _, _, _ string) error { return nil }
func (b *countingBackend) RemoveLink(_ context.Context, _, _ string) error    { return nil }
func (b *countingBackend) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (b *countingBackend) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

func (b *countingBackend) ReportValueUsage(_ context.Context, _, _ string, _ int) error { return nil }

func (b *countingBackend) DeleteDevice(_ context.Context, _ string, _ int) error { return nil }

func (b *countingBackend) GetAllPrograms(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (b *countingBackend) SetProgramState(_ context.Context, _ string, _ bool) error { return nil }
func (b *countingBackend) GetSystemUpdateInfo(_ context.Context) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetInboxDevices(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}
func (b *countingBackend) SetSystemVariable(_ context.Context, _ string, _ any) error { return nil }
func (b *countingBackend) CreateSystemVariableBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateSystemVariableEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateSystemVariableFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) DetermineParameter(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}
func (b *countingBackend) GetInstallMode(_ context.Context) (int, error) { return 0, nil }
func (b *countingBackend) SetInstallMode(_ context.Context, _ bool, _, _ int, _ string) error {
	return nil
}

func (b *countingBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (b *countingBackend) GetServiceMessages(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) SuppressServiceMessage(_ context.Context, _, _ string, _ bool) error {
	return nil
}

func (b *countingBackend) GetAlarmMessages(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllRooms(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *countingBackend) GetAllFunctions(_ context.Context) (map[string][]string, error) {
	return nil, nil
}

func (b *countingBackend) RenameDevice(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) RenameChannel(_ context.Context, _ int, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) AcceptDeviceInInbox(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) ExecuteProgram(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetSystemVariable(_ context.Context, _ string) (any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllSystemVariables(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetAllDeviceData(_ context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetDeviceDetails(_ context.Context, _ []string) ([]map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) GetDeviceDescription(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) CreateBackupAndDownload(_ context.Context, _, _ float64) ([]byte, error) {
	return nil, nil
}

func (b *countingBackend) TriggerFirmwareUpdate(_ context.Context) (bool, error) { return false, nil }

func (b *countingBackend) DeleteSystemVariable(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetIseIDByAddress(_ context.Context, _ string) (int, error) { return 0, nil }

func (b *countingBackend) GetLinkInfo(_ context.Context, _, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (b *countingBackend) SetLinkInfo(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}

func (b *countingBackend) GetSuppressedServiceMessages(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (b *countingBackend) HasProgramIDs(_ context.Context, _ string) (bool, error) { return false, nil }
func (b *countingBackend) DownloadFirmware(_ context.Context, _ string) error      { return nil }
func (b *countingBackend) GetMetadata(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}
func (b *countingBackend) SetMetadata(_ context.Context, _, _ string, _ any) error { return nil }

var _ backends.Operations = (*countingBackend)(nil)

// newCountingIC returns an InterfaceClient wired with a fast-retrier
// (maxAttempts=3, no sleep) and a counting backend. The IC uses the
// counting backend; callers pass the same backend to IC.SetValue.
func newCountingIC(t *testing.T, setErr error) (*InterfaceClient, *countingBackend) {
	t.Helper()
	retrier := reliability.NewRetrier(reliability.RetryConfig{
		MaxAttempts: 3,
		Initial:     0, // no sleep — tests run deterministically
	})
	ic, err := New(Config{
		CentralName: "test-skip",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { return nil, nil }),
		Retrier:     retrier,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b := &countingBackend{setErr: setErr}
	return ic, b
}

// TestSetValueSkipRetryCallsBackendOnce verifies that when skipRetry=true
// a failing backend is called exactly once — the Retrier's DoOnce path —
// regardless of the MaxAttempts setting.
func TestSetValueSkipRetryCallsBackendOnce(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("transient network error")
	ic, b := newCountingIC(t, transientErr)

	err := ic.SetValue(
		context.Background(), b,
		"VCU001:1", hmenum.ParameterLevel, 0.5,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset,
		true, // skipRetry
	)
	if err == nil {
		t.Fatal("expected error from backend, got nil")
	}
	if count := b.SetCallCount(); count != 1 {
		t.Errorf("skipRetry=true: backend.SetValue called %d times, want exactly 1", count)
	}
}

// TestSetValueWithRetryCallsBackendMultipleTimes verifies that with the
// default skipRetry=false the retrier does retry on transient errors (up to
// maxAttempts). This is the baseline confirming that DoOnce is NOT the
// default code path.
func TestSetValueWithRetryCallsBackendMultipleTimes(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("transient network error")
	ic, b := newCountingIC(t, transientErr)

	_ = ic.SetValue(
		context.Background(), b,
		"VCU001:1", hmenum.ParameterLevel, 0.5,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset,
		false, // skipRetry — use normal retry
	)
	if count := b.SetCallCount(); count <= 1 {
		t.Errorf("skipRetry=false: backend.SetValue called %d times, want >1 (retried)", count)
	}
}
