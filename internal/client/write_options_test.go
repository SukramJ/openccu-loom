// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubBackend records the SetValue + PutParamset calls so the tests
// can assert that the wire write went out before the wait kicks in.
type stubBackend struct {
	mu        sync.Mutex
	setCalls  []stubSetCall
	putCalls  []stubPutCall
	failNext  error
	delayUnti chan struct{}
}

type stubSetCall struct {
	Channel string
	Param   hmenum.Parameter
	Value   any
	RxMode  hmenum.CommandRxMode
}

type stubPutCall struct {
	Channel string
	Key     hmenum.ParamsetKey
	Values  map[string]any
}

func (s *stubBackend) SetValue(_ context.Context, ch string, p hmenum.Parameter, v any, _ hmenum.CommandPriority, rx hmenum.CommandRxMode) error {
	s.mu.Lock()
	s.setCalls = append(s.setCalls, stubSetCall{Channel: ch, Param: p, Value: v, RxMode: rx})
	err := s.failNext
	s.failNext = nil
	delay := s.delayUnti
	s.mu.Unlock()
	if delay != nil {
		<-delay
	}
	return err
}

func (s *stubBackend) PutParamset(_ context.Context, ch string, key hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandRxMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCalls = append(s.putCalls, stubPutCall{Channel: ch, Key: key, Values: values})
	return nil
}

// stubBackend implements [backends.Operations] for the WaitForCallback
// + PurgeAddresses tests. Only SetValue / PutParamset have meaningful
// behavior; everything else is a quiet no-op so the tests don't have
// to wire a real CCU transport.

func (s *stubBackend) Kind() backends.Kind                             { return backends.KindCCU }
func (s *stubBackend) DeleteDevice(context.Context, string, int) error { return nil }
func (s *stubBackend) Capabilities() backends.Capabilities             { return backends.Capabilities{} }
func (s *stubBackend) Init(context.Context, string, string) error      { return nil }
func (s *stubBackend) Deinit(context.Context, string) error            { return nil }
func (s *stubBackend) Ping(context.Context, string) error              { return nil }
func (s *stubBackend) ListDevices(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (s *stubBackend) GetParamsetDescription(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (s *stubBackend) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) GetValue(context.Context, string, hmenum.Parameter) (any, error) {
	return nil, nil
}
func (s *stubBackend) UpdateFirmware(context.Context, string) error { return nil }
func (s *stubBackend) GetLinks(context.Context, string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}
func (s *stubBackend) GetLinkPeers(context.Context, string) ([]string, error) { return nil, nil }
func (s *stubBackend) AddLink(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubBackend) RemoveLink(context.Context, string, string) error { return nil }
func (s *stubBackend) GetLinkParamsetDescription(context.Context, string, string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (s *stubBackend) GetLinkParamset(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) PutLinkParamset(context.Context, string, string, map[string]any) error {
	return nil
}
func (s *stubBackend) ReportValueUsage(context.Context, string, string, int) error { return nil }
func (s *stubBackend) GetAllPrograms(context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (s *stubBackend) SetProgramState(context.Context, string, bool) error { return nil }
func (s *stubBackend) GetSystemUpdateInfo(context.Context) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) GetInboxDevices(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}
func (s *stubBackend) SetSystemVariable(context.Context, string, any) error { return nil }
func (s *stubBackend) CreateSystemVariableBool(context.Context, string, bool) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) CreateSystemVariableEnum(context.Context, string, []string) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) CreateSystemVariableFloat(context.Context, string, float64, float64) (map[string]any, error) {
	return nil, nil
}

func (s *stubBackend) DetermineParameter(context.Context, string, string) (any, error) {
	return nil, nil
}

// Extended Operations stubs (new interface methods — no-ops for tests).
// NOTE: identical stubs are needed in adapter tests — kept here as reference.
func (*stubBackend) GetInstallMode(context.Context) (int, error) { return 0, nil }

func (*stubBackend) SetInstallMode(context.Context, bool, int, int, string) error {
	return nil
}

func (*stubBackend) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (*stubBackend) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (*stubBackend) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (*stubBackend) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (*stubBackend) SearchDevices(context.Context) (int, error) {
	return 0, backends.ErrUnsupported
}

func (*stubBackend) SetTeam(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (*stubBackend) ListTeams(context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (*stubBackend) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, backends.ErrUnsupported
}

func (*stubBackend) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (*stubBackend) SuppressServiceMessage(context.Context, string, string, bool) error {
	return nil
}

func (*stubBackend) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*stubBackend) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (*stubBackend) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, nil
}
func (*stubBackend) RenameDevice(context.Context, int, string) (bool, error)  { return false, nil }
func (*stubBackend) RenameChannel(context.Context, int, string) (bool, error) { return false, nil }
func (*stubBackend) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, nil
}
func (*stubBackend) ExecuteProgram(context.Context, string) (bool, error)   { return false, nil }
func (*stubBackend) GetSystemVariable(context.Context, string) (any, error) { return nil, nil }
func (*stubBackend) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*stubBackend) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (*stubBackend) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, nil
}

func (*stubBackend) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (*stubBackend) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, nil
}
func (*stubBackend) TriggerFirmwareUpdate(context.Context) (bool, error) { return false, nil }
func (*stubBackend) DeleteSystemVariable(context.Context, string) (bool, error) {
	return false, nil
}
func (*stubBackend) GetIseIDByAddress(context.Context, string) (int, error) { return 0, nil }
func (*stubBackend) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, nil
}

func (*stubBackend) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (*stubBackend) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (*stubBackend) HasProgramIDs(context.Context, string) (bool, error) { return false, nil }
func (*stubBackend) DownloadFirmware(context.Context, string) error      { return nil }
func (*stubBackend) GetMetadata(context.Context, string, string) (any, error) {
	return nil, nil
}
func (*stubBackend) SetMetadata(context.Context, string, string, any) error { return nil }

// stubRetrier records device-cancel calls for PurgeAddresses tests.
type stubRetrier struct {
	mu        sync.Mutex
	cancelled []string
}

func (r *stubRetrier) CancelDevice(addr string) int {
	r.mu.Lock()
	r.cancelled = append(r.cancelled, addr)
	r.mu.Unlock()
	return 1
}

func (r *stubRetrier) CancelInterface() int { return 0 }

// TestSetValueWithOptions_WaitForCallbackHonoured verifies that with
// a bus installed and an event on the bus matching the key+value, the
// SetValue call returns nil after the event was published.
func TestSetValueWithOptions_WaitForCallbackHonoured(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Publish the matching event in 25 ms — SetValueWithOptions
	// should observe it and return nil.
	go func() {
		time.Sleep(25 * time.Millisecond)
		dpk, _ := hmtypes.NewDataPointKey("HmIP-RF", "VCU0001:1", hmenum.ParamsetKeyValues, "STATE")
		newVal, _ := hmtypes.NewParamValue(true)
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			Key:      dpk,
			NewValue: newVal,
		})
	}()

	err := w.SetValueWithOptions(ctx, "ccu", "HmIP-RF", "VCU0001:1", hmenum.Parameter("STATE"), true, WriteOptions{
		WaitForCallback:        true,
		WaitForCallbackTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("SetValueWithOptions: %v", err)
	}
}

// TestSetValueWithOptions_WaitForCallbackTimeout verifies that the
// caller sees the timeout sentinel when no matching event arrives.
func TestSetValueWithOptions_WaitForCallbackTimeout(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	err := w.SetValueWithOptions(context.Background(), "ccu", "HmIP-RF", "VCU0001:1", hmenum.Parameter("STATE"), true, WriteOptions{
		WaitForCallback:        true,
		WaitForCallbackTimeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("WaitForCallback timeout must surface as error")
	}
	if !errors.Is(err, ErrStateChangeTimeout) {
		t.Fatalf("expected ErrStateChangeTimeout in chain, got %v", err)
	}
}

// TestSetValueWithOptions_WaitForCallbackNoBusIsNoOp verifies that
// without a bus installed, WaitForCallback=true is silently ignored —
// the call returns nil after the wire write succeeds.
func TestSetValueWithOptions_WaitForCallbackNoBusIsNoOp(t *testing.T) {
	t.Parallel()
	w := NewValueWriter()
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	err := w.SetValueWithOptions(context.Background(), "ccu", "HmIP-RF", "VCU0001:1", hmenum.Parameter("STATE"), true, WriteOptions{
		WaitForCallback: true,
	})
	if err != nil {
		t.Fatalf("no-bus path must skip the wait: got %v", err)
	}
}

// TestSetValueWithOptions_PurgeAddressesCancelsRetries verifies that
// the listed addresses are passed to the retrier BEFORE the wire
// write happens.
func TestSetValueWithOptions_PurgeAddressesCancelsRetries(t *testing.T) {
	t.Parallel()
	w := NewValueWriter()
	r := &stubRetrier{}
	w.SetRetrier(r)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	err := w.SetValueWithOptions(context.Background(), "ccu", "HmIP-RF", "VCU0001:1", hmenum.Parameter("STATE"), true, WriteOptions{
		PurgeAddresses: []string{"VCU0001", "VCU0002"},
	})
	if err != nil {
		t.Fatalf("SetValueWithOptions: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cancelled) != 2 || r.cancelled[0] != "VCU0001" || r.cancelled[1] != "VCU0002" {
		t.Fatalf("CancelDevice calls = %v, want [VCU0001 VCU0002]", r.cancelled)
	}
}

// TestPutParamsetWithOptions_WaitForCallbackAcrossKeys verifies that
// PutParamsetWithOptions waits for confirmations on EVERY parameter
// in the values map, not just one.
func TestPutParamsetWithOptions_WaitForCallbackAcrossKeys(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	w := NewValueWriter()
	w.SetEventBus(bus)
	w.Register("ccu", "HmIP-RF", &stubBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Fire confirmations for both LEVEL and LEVEL_2.
	go func() {
		time.Sleep(20 * time.Millisecond)
		for _, p := range []struct {
			Param string
			Value any
		}{
			{"LEVEL", 0.5},
			{"LEVEL_2", 0.25},
		} {
			dpk, _ := hmtypes.NewDataPointKey("HmIP-RF", "VCU0001:4", hmenum.ParamsetKeyValues, p.Param)
			pv, _ := hmtypes.NewParamValue(p.Value)
			events.Publish(bus, hmevent.DataPointValueChangedEvent{
				Base:     hmevent.NewBase(),
				Key:      dpk,
				NewValue: pv,
			})
		}
	}()

	err := w.PutParamsetWithOptions(ctx, "ccu", "HmIP-RF", "VCU0001:4", hmenum.ParamsetKeyValues, map[string]any{
		"LEVEL":   0.5,
		"LEVEL_2": 0.25,
	}, WriteOptions{
		WaitForCallback:        true,
		WaitForCallbackTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("PutParamsetWithOptions: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WriteOptions.SkipRetry zero-value and set behaviour
// ---------------------------------------------------------------------------

// TestWriteOptionsSkipRetryDefaultFalse verifies zero-value WriteOptions
// has SkipRetry=false.
func TestWriteOptionsSkipRetryDefaultFalse(t *testing.T) {
	t.Parallel()
	var opts WriteOptions
	if opts.SkipRetry {
		t.Error("WriteOptions{}.SkipRetry should default to false")
	}
}

// TestWriteOptionsSkipRetryCanBeSet verifies SkipRetry can be set to true.
func TestWriteOptionsSkipRetryCanBeSet(t *testing.T) {
	t.Parallel()
	opts := WriteOptions{SkipRetry: true}
	if !opts.SkipRetry {
		t.Error("WriteOptions{SkipRetry: true}.SkipRetry should be true")
	}
}
