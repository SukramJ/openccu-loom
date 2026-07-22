// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- paramset-specific fake operations --------------------------

// paramsetFakeOps is a minimal backends.Operations stub with optional
// callback fields for the methods tests need to intercept. All other
// methods are no-ops. This lets individual tests hook only the calls they
// care about without implementing the full interface each time.
type paramsetFakeOps struct {
	getParamsetFn            func(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	putParamsetFn            func(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error
	getParamsetDescriptionFn func(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error)
	listDevicesFn            func(ctx context.Context) ([]hmproto.DeviceDescription, error)
}

func (f *paramsetFakeOps) Kind() backends.Kind { return backends.KindCCU }
func (f *paramsetFakeOps) Capabilities() backends.Capabilities {
	return backends.CapabilityFor(backends.KindCCU)
}
func (f *paramsetFakeOps) Init(_ context.Context, _, _ string) error { return nil }
func (f *paramsetFakeOps) Deinit(_ context.Context, _ string) error  { return nil }
func (f *paramsetFakeOps) Ping(_ context.Context, _ string) error    { return nil }
func (f *paramsetFakeOps) ListDevices(ctx context.Context) ([]hmproto.DeviceDescription, error) {
	if f.listDevicesFn != nil {
		return f.listDevicesFn(ctx)
	}
	return nil, nil
}

func (f *paramsetFakeOps) GetParamsetDescription(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	if f.getParamsetDescriptionFn != nil {
		return f.getParamsetDescriptionFn(ctx, address, key)
	}
	return nil, nil
}

func (f *paramsetFakeOps) GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error) {
	if f.getParamsetFn != nil {
		return f.getParamsetFn(ctx, address, key)
	}
	return map[string]any{}, nil
}

func (f *paramsetFakeOps) PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandRxMode) error {
	if f.putParamsetFn != nil {
		return f.putParamsetFn(ctx, address, key, values)
	}
	return nil
}

func (f *paramsetFakeOps) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return nil
}

func (f *paramsetFakeOps) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, nil
}
func (f *paramsetFakeOps) UpdateFirmware(_ context.Context, _ string) error { return nil }
func (f *paramsetFakeOps) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (f *paramsetFakeOps) GetLinkPeers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *paramsetFakeOps) AddLink(_ context.Context, _, _, _, _ string) error { return nil }
func (f *paramsetFakeOps) RemoveLink(_ context.Context, _, _ string) error    { return nil }
func (f *paramsetFakeOps) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (f *paramsetFakeOps) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (f *paramsetFakeOps) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

func (f *paramsetFakeOps) ReportValueUsage(_ context.Context, _, _ string, _ int) error { return nil }

func (f *paramsetFakeOps) DeleteDevice(_ context.Context, _ string, _ int) error { return nil }

func (f *paramsetFakeOps) GetAllPrograms(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (f *paramsetFakeOps) SetProgramState(_ context.Context, _ string, _ bool) error { return nil }
func (f *paramsetFakeOps) GetSystemUpdateInfo(_ context.Context) (map[string]any, error) {
	return nil, nil
}

func (f *paramsetFakeOps) GetInboxDevices(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}
func (f *paramsetFakeOps) SetSystemVariable(_ context.Context, _ string, _ any) error { return nil }
func (f *paramsetFakeOps) CreateSystemVariableBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	return nil, nil
}

func (f *paramsetFakeOps) CreateSystemVariableEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	return nil, nil
}

func (f *paramsetFakeOps) CreateSystemVariableFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	return nil, nil
}

func (f *paramsetFakeOps) DetermineParameter(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}

// Extended Operations stubs (new interface methods — no-ops in tests).
func (*paramsetFakeOps) GetInstallMode(context.Context) (int, error) { return 0, nil }

func (*paramsetFakeOps) SetInstallMode(context.Context, bool, int, int, string) error {
	return nil
}

func (*paramsetFakeOps) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (*paramsetFakeOps) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (*paramsetFakeOps) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (*paramsetFakeOps) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (*paramsetFakeOps) SearchDevices(context.Context) (int, error) {
	return 0, backends.ErrUnsupported
}

func (*paramsetFakeOps) TestDevice(context.Context, string, float64, float64) (hmapi.CommunicationTestResult, error) {
	return hmapi.CommunicationTestResult{}, backends.ErrUnsupported
}

func (*paramsetFakeOps) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) SuppressServiceMessage(context.Context, string, string, bool) error {
	return nil
}
func (*paramsetFakeOps) GetMetadata(_ context.Context, _, _ string) (any, error) { return nil, nil }
func (*paramsetFakeOps) SetMetadata(_ context.Context, _, _ string, _ any) error { return nil }
func (*paramsetFakeOps) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (*paramsetFakeOps) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (*paramsetFakeOps) RenameDevice(context.Context, int, string) (bool, error) {
	return false, nil
}

func (*paramsetFakeOps) RenameChannel(context.Context, int, string) (bool, error) {
	return false, nil
}

func (*paramsetFakeOps) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, nil
}

func (*paramsetFakeOps) ExecuteProgram(context.Context, string) (bool, error)   { return false, nil }
func (*paramsetFakeOps) GetSystemVariable(context.Context, string) (any, error) { return nil, nil }
func (*paramsetFakeOps) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, nil
}
func (*paramsetFakeOps) TriggerFirmwareUpdate(context.Context) (bool, error) { return false, nil }
func (*paramsetFakeOps) DeleteSystemVariable(context.Context, string) (bool, error) {
	return false, nil
}
func (*paramsetFakeOps) GetIseIDByAddress(context.Context, string) (int, error) { return 0, nil }
func (*paramsetFakeOps) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, nil
}

func (*paramsetFakeOps) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (*paramsetFakeOps) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (*paramsetFakeOps) HasProgramIDs(context.Context, string) (bool, error) { return false, nil }
func (*paramsetFakeOps) DownloadFirmware(context.Context, string) error      { return nil }

var _ backends.Operations = (*paramsetFakeOps)(nil)

// ---------- recording channel writer -----------------------------------

// recordingChannelWriter records every SetValue and PutParamset call.
// Satisfies device.ChannelWriter. Safe for concurrent use.
type recordingChannelWriter struct {
	mu       sync.Mutex
	setCalls []recordedSet
	putCalls []recordedPut
	setErr   error
	putErr   error
}

type recordedSet struct {
	address   string
	parameter hmenum.Parameter
	value     any
	priority  hmenum.CommandPriority
}

type recordedPut struct {
	address     string
	paramsetKey hmenum.ParamsetKey
	values      map[string]any
	priority    hmenum.CommandPriority
}

func (r *recordingChannelWriter) SetValue(
	_ context.Context,
	address string,
	param hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.setErr != nil {
		return r.setErr
	}
	r.setCalls = append(r.setCalls, recordedSet{address, param, value, priority})
	return nil
}

func (r *recordingChannelWriter) PutParamset(
	_ context.Context,
	address string,
	key hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.putErr != nil {
		return r.putErr
	}
	r.putCalls = append(r.putCalls, recordedPut{address, key, values, priority})
	return nil
}

func (r *recordingChannelWriter) putCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.putCalls)
}

func (r *recordingChannelWriter) setCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.setCalls)
}

// ---------- test fixture helpers ---------------------------------------

// buildParamsetFixture creates a central with one device + one channel
// that has a float writable DP for hmenum.ParameterLevel in VALUES and
// a float writable DP for hmenum.ParameterLevel in MASTER. The
// recordingChannelWriter is installed on the channel.
//
// Returns:
// - domain: wired ParamsetsDomain with a real ValueWriter + fakeOperations
// - chw: the recording writer installed on the channel
// - fakeOps: the fakeOperations registered in the ValueWriter
func buildParamsetFixture(t *testing.T) (
	domain *ParamsetsDomain,
	chw *recordingChannelWriter,
	fakeOps *paramsetFakeOps,
) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Wohnzimmer",
	})
	ch := dev.AddChannel("0001ABCD:1", 1, "LEVEL", hmenum.ParamsetKeyValues)

	// VALUES data point for LEVEL.
	dpValues := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("1.0"),
		},
	})
	dpValues.OnEvent(0.5)
	ch.Put(dpValues)

	// MASTER data point for LEVEL.
	dpMaster := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("1.0"),
		},
	})
	dpMaster.OnEvent(0.3)
	ch.PutMaster(dpMaster)

	// Install a recording writer on the channel.
	chw = &recordingChannelWriter{}
	ch.SetWriter(chw)

	c.ModelRegistry.Put(dev)

	// Wire a paramsetFakeOps into a ValueWriter so resolve() succeeds.
	fakeOps = &paramsetFakeOps{}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fakeOps)

	domain = NewParamsetsDomain(reg, w)
	return domain, chw, fakeOps
}

// ---------- GetParamset -----------------------------------------------

// TestGetParamsetReturnsChannelCachedValues verifies that GetParamset
// returns the channel's cached snapshot (from OnEvent seeding) rather
// than hitting the backend when the channel has observed values.
func TestGetParamsetReturnsChannelCachedValues(t *testing.T) {
	t.Parallel()

	domain, _, fakeOps := buildParamsetFixture(t)

	// Arm the backend so we can detect unexpected hits.
	getParamsetCalls := 0
	fakeOps.getParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		getParamsetCalls++
		return map[string]any{string(hmenum.ParameterLevel): float64(0.99)}, nil
	}

	result, err := domain.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamset: %v", err)
	}
	// The channel was seeded with 0.5 for LEVEL.
	if v, ok := result[string(hmenum.ParameterLevel)]; !ok || v != float64(0.5) {
		t.Fatalf("GetParamset result LEVEL: got %v, want 0.5", v)
	}
	// The backend must NOT have been called.
	if getParamsetCalls != 0 {
		t.Fatalf("backend GetParamset called %d times, want 0 (channel cache should have been used)", getParamsetCalls)
	}
}

// TestGetParamsetFallsBackToBackendWhenCacheEmpty verifies that when the
// channel has no observed values, GetParamset tries Refresh (which
// calls the backend) and returns the result.
func TestGetParamsetFallsBackToBackendWhenCacheEmpty(t *testing.T) {
	t.Parallel()

	// Build without seeding data points so the cache is empty.
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	ch := dev.AddChannel("0001ABCD:1", 1, "LEVEL", hmenum.ParamsetKeyValues)
	// DP exists but has never been observed.
	dpValues := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("1.0"),
		},
	})
	ch.Put(dpValues)
	// Install a refresher that returns a known value.
	knownResult := func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
		return map[string]any{string(hmenum.ParameterLevel): float64(0.77)}, nil
	}
	fakeRefresher := &paramsetFakeOps{getParamsetFn: knownResult}
	ch.SetRefresher(fakeRefresher)
	c.ModelRegistry.Put(dev)

	fakeOps := &paramsetFakeOps{getParamsetFn: knownResult}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fakeOps)

	domain := NewParamsetsDomain(reg, w)
	result, err := domain.GetParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("GetParamset: %v", err)
	}
	// After Refresh, the cache is populated and returned.
	if v, ok := result[string(hmenum.ParameterLevel)]; !ok {
		t.Fatalf("GetParamset result LEVEL missing")
	} else if v != float64(0.77) {
		t.Fatalf("GetParamset result LEVEL: got %v, want 0.77", v)
	}
}

// ---------- PutParamset channel routing --------------------------------

// TestPutParamsetRoutesViaChannelSetMany verifies that PutParamset
// dispatches through Channel.SetMany (which calls PutParamset on the
// channel's ChannelWriter) when a matching channel exists in the
// registry.
func TestPutParamsetRoutesViaChannelSetMany(t *testing.T) {
	t.Parallel()

	domain, chw, _ := buildParamsetFixture(t)

	values := map[string]any{string(hmenum.ParameterLevel): float64(0.8)}
	if err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, values); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	// Channel has only one parameter (LEVEL) — collector dispatches via
	// SetValue (single-param path), not PutParamset.
	if chw.setCallCount() != 1 {
		t.Fatalf("channel writer SetValue calls=%d, want 1", chw.setCallCount())
	}
}

// TestPutParamsetRoutesManyViaChannelPutParamset verifies that when two
// parameters are written at once, SetMany creates an internal collector
// that dispatches via PutParamset on the channel writer.
func TestPutParamsetRoutesManyViaChannelPutParamset(t *testing.T) {
	t.Parallel()

	// Build a fixture with two VALUE DPs so the collector uses PutParamset.
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	ch := dev.AddChannel("0001ABCD:1", 1, "LEVEL", hmenum.ParamsetKeyValues)
	for _, p := range []hmenum.Parameter{hmenum.ParameterLevel, hmenum.ParameterLevel2} {
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "0001ABCD:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
				Min:        json.RawMessage("0.0"),
				Max:        json.RawMessage("1.0"),
			},
		})
		dp.OnEvent(0.5)
		ch.Put(dp)
	}
	chw := &recordingChannelWriter{}
	ch.SetWriter(chw)
	c.ModelRegistry.Put(dev)

	fakeOps := &paramsetFakeOps{}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fakeOps)
	domain := NewParamsetsDomain(reg, w)

	values := map[string]any{
		string(hmenum.ParameterLevel):  float64(0.8),
		string(hmenum.ParameterLevel2): float64(0.4),
	}
	if err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, values); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	// Two params → PutParamset on the channel writer.
	if chw.putCallCount() != 1 {
		t.Fatalf("channel writer PutParamset calls=%d, want 1", chw.putCallCount())
	}
	if chw.setCallCount() != 0 {
		t.Fatalf("channel writer SetValue calls=%d, want 0", chw.setCallCount())
	}
}

// TestPutParamsetFallsBackToBackendWhenNoChannel verifies that when the
// device address cannot be resolved to a channel, PutParamset falls
// through to the direct backend call.
func TestPutParamsetFallsBackToBackendWhenNoChannel(t *testing.T) {
	t.Parallel()

	// Build a domain with a device but NO channel under "0001ABCD:1".
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	// Do NOT add a channel for "0001ABCD:1".
	c.ModelRegistry.Put(dev)

	putCalled := false
	fakeOps := &paramsetFakeOps{
		putParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any) error {
			putCalled = true
			return nil
		},
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fakeOps)
	domain := NewParamsetsDomain(reg, w)

	values := map[string]any{string(hmenum.ParameterLevel): float64(0.5)}
	if err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, values); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if !putCalled {
		t.Fatal("expected backend PutParamset to be called (legacy fallback), but it was not")
	}
}

// ---------- Visibility gate fires before Channel.SetMany ---------------

// TestPutParamsetVisibilityGateFiresBeforeChannelSetMany asserts that
// the VisibilityGate check runs BEFORE Channel.SetMany is invoked.
// When the gate rejects a parameter, the channel writer must NOT be
// called at all (defense-in-depth).
func TestPutParamsetVisibilityGateFiresBeforeChannelSetMany(t *testing.T) {
	t.Parallel()

	domain, chw, _ := buildParamsetFixture(t)

	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterLevel)
	domain.SetVisibilityGate(gate)

	values := map[string]any{string(hmenum.ParameterLevel): float64(0.8)}
	err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues, values)
	if !errors.Is(err, hmerr.ErrParameterHidden) {
		t.Fatalf("want ErrParameterHidden, got %v", err)
	}
	// The channel writer must NOT have been called.
	if chw.setCallCount() != 0 || chw.putCallCount() != 0 {
		t.Fatalf("channel writer must not be called when gate rejects: set=%d put=%d",
			chw.setCallCount(), chw.putCallCount())
	}
}

// ============================================================
// NewUISchemaAdapter constructor
// ============================================================

func TestNewUISchemaAdapterNilArgs(t *testing.T) {
	t.Parallel()
	a := NewUISchemaAdapter(nil, nil, nil, nil, nil)
	if a == nil {
		t.Fatal("NewUISchemaAdapter must return non-nil adapter")
	}
}

// ============================================================
// ParamsetsDomain GetLinkParamset nil-registry
// ============================================================

func TestParamsetsDomainGetLinkParamsetNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	_, err := p.GetLinkParamset(context.Background(), "DEV:1", "DEV2:1")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// ParamsetsDomain PutLinkParamset nil-registry
// ============================================================

func TestParamsetsDomainPutLinkParamsetNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	err := p.PutLinkParamset(context.Background(), "DEV:1", "DEV2:1", map[string]any{"PARAM": 1})
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// ParamsetsDomain GetLinkFormSchema nil-registry
// ============================================================

func TestParamsetsDomainGetLinkFormSchemaNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	_, err := p.GetLinkFormSchema(context.Background(), "HmIP-RF", "DEV:1", "DEV2:1")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// ParamsetsDomain GetParamset nil-registry
// ============================================================

func TestParamsetsDomainGetParamsetNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	_, err := p.GetParamset(context.Background(), "DEV:1", hmenum.ParamsetKeyValues)
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// ParamsetsDomain PutParamset nil-registry
// ============================================================

func TestParamsetsDomainPutParamsetNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	err := p.PutParamset(context.Background(), "DEV:1", hmenum.ParamsetKeyValues, map[string]any{"PARAM": true})
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// SchedulesDomain nil-registry guards (resolve)
// ============================================================

func TestSchedulesDomainFindScheduleChannelWithRegistry(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	// Empty registry → device not found error
	_, err := s.FindScheduleChannel(context.Background(), "NOSUCHDEV")
	if err == nil {
		t.Fatal("expected error — device not found in empty registry")
	}
}

// ============================================================
// SchedulesDomain.detectScheduleDomain with non-nil registry
// ============================================================

func TestSchedulesDomainDetectScheduleDomainNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	// Device not found → returns ""
	got := s.detectScheduleDomain("NOSUCHDEV", 1)
	if got != "" {
		t.Errorf("detectScheduleDomain not found = %q, want empty", got)
	}
}

// ============================================================
// ParamsetsDomain resolveChannel nil-registry
// ============================================================

func TestParamsetsDomainResolveChannelNilRegistry(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	ch := p.resolveChannel("DEV:1")
	if ch != nil {
		t.Errorf("resolveChannel nil registry = %v, want nil", ch)
	}
}
