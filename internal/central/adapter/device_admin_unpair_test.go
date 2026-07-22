// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// fakeOperations is a minimal backends.Operations stub. Every method
// that is not DeleteDevice is a no-op so we don't need to maintain a
// long list of forwarding calls — only the call under test matters.
type fakeOperations struct {
	deleteDeviceCalls []string
	deleteDeviceFlags []int
	deleteDeviceErr   error
	kind              backends.Kind
}

func (f *fakeOperations) Kind() backends.Kind                       { return f.kind }
func (f *fakeOperations) Capabilities() backends.Capabilities       { return backends.Capabilities{} }
func (f *fakeOperations) Init(_ context.Context, _, _ string) error { return nil }
func (f *fakeOperations) Deinit(_ context.Context, _ string) error  { return nil }
func (f *fakeOperations) Ping(_ context.Context, _ string) error    { return nil }
func (f *fakeOperations) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	return nil, nil
}

func (f *fakeOperations) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (f *fakeOperations) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandRxMode) error {
	return nil
}

func (f *fakeOperations) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	return nil
}

func (f *fakeOperations) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	return nil, nil
}
func (f *fakeOperations) UpdateFirmware(_ context.Context, _ string) error { return nil }
func (f *fakeOperations) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return nil, nil
}

func (f *fakeOperations) GetLinkPeers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeOperations) AddLink(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeOperations) RemoveLink(_ context.Context, _, _ string) error    { return nil }
func (f *fakeOperations) GetLinkParamsetDescription(_ context.Context, _, _ string) (map[string]hmproto.ParameterData, error) {
	return nil, nil
}

func (f *fakeOperations) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

func (f *fakeOperations) ReportValueUsage(_ context.Context, _, _ string, _ int) error { return nil }

func (f *fakeOperations) DeleteDevice(_ context.Context, address string, flags int) error {
	f.deleteDeviceCalls = append(f.deleteDeviceCalls, address)
	f.deleteDeviceFlags = append(f.deleteDeviceFlags, flags)
	return f.deleteDeviceErr
}

func (f *fakeOperations) GetAllPrograms(_ context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (f *fakeOperations) SetProgramState(_ context.Context, _ string, _ bool) error { return nil }
func (f *fakeOperations) GetSystemUpdateInfo(_ context.Context) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) GetInboxDevices(_ context.Context, _ string) ([]map[string]any, error) {
	return nil, nil
}
func (f *fakeOperations) SetSystemVariable(_ context.Context, _ string, _ any) error { return nil }
func (f *fakeOperations) CreateSystemVariableBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) CreateSystemVariableEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) CreateSystemVariableFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	return nil, nil
}

func (f *fakeOperations) DetermineParameter(_ context.Context, _, _ string) (any, error) {
	return nil, nil
}

// Extended Operations stubs (new interface methods — no-ops in tests).
func (*fakeOperations) GetInstallMode(context.Context) (int, error) { return 0, nil }

func (*fakeOperations) SetInstallMode(context.Context, bool, int, int, string) error {
	return nil
}

func (*fakeOperations) SetInstallModeLocal(context.Context, int, string, string) error {
	return backends.ErrUnsupported
}

func (*fakeOperations) RestoreConfigToDevice(context.Context, string) error {
	return backends.ErrUnsupported
}

func (*fakeOperations) ListReplaceableDevices(context.Context, string) ([]hmproto.DeviceDescription, error) {
	return nil, backends.ErrUnsupported
}

func (*fakeOperations) ReplaceDevice(context.Context, string, string) error {
	return backends.ErrUnsupported
}

func (*fakeOperations) GetServiceMessages(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) SuppressServiceMessage(context.Context, string, string, bool) error {
	return nil
}

func (*fakeOperations) GetAlarmMessages(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) GetAllRooms(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (*fakeOperations) GetAllFunctions(context.Context) (map[string][]string, error) {
	return nil, nil
}

func (*fakeOperations) RenameDevice(context.Context, int, string) (bool, error) {
	return false, nil
}

func (*fakeOperations) RenameChannel(context.Context, int, string) (bool, error) {
	return false, nil
}

func (*fakeOperations) AcceptDeviceInInbox(context.Context, string) (bool, error) {
	return false, nil
}
func (*fakeOperations) ExecuteProgram(context.Context, string) (bool, error)   { return false, nil }
func (*fakeOperations) GetSystemVariable(context.Context, string) (any, error) { return nil, nil }
func (*fakeOperations) GetAllSystemVariables(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) GetAllDeviceData(context.Context) (map[string]map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) GetDeviceDetails(context.Context, []string) ([]map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) GetDeviceDescription(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) CreateBackupAndDownload(context.Context, float64, float64) ([]byte, error) {
	return nil, nil
}
func (*fakeOperations) TriggerFirmwareUpdate(context.Context) (bool, error) { return false, nil }
func (*fakeOperations) DeleteSystemVariable(context.Context, string) (bool, error) {
	return false, nil
}
func (*fakeOperations) GetIseIDByAddress(context.Context, string) (int, error) { return 0, nil }
func (*fakeOperations) GetLinkInfo(context.Context, string, string, string) (map[string]any, error) {
	return nil, nil
}

func (*fakeOperations) SetLinkInfo(context.Context, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (*fakeOperations) GetSuppressedServiceMessages(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func (*fakeOperations) HasProgramIDs(context.Context, string) (bool, error)     { return false, nil }
func (*fakeOperations) DownloadFirmware(context.Context, string) error          { return nil }
func (*fakeOperations) GetMetadata(_ context.Context, _, _ string) (any, error) { return nil, nil }
func (*fakeOperations) SetMetadata(_ context.Context, _, _ string, _ any) error { return nil }

// buildUnpairFixture creates a central with one device seeded into every
// relevant registry, wires a fake backend into a ValueWriter, and
// returns everything the caller needs to assert on.
func buildUnpairFixture(t *testing.T, backendErr error) (
	domain *DeviceAdminDomain,
	centralUnit *central.Unit,
	dev *device.Device,
	fake *fakeOperations,
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

	dev = device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Flur",
	})
	c.ModelRegistry.Put(dev)
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmenum.InterfaceHmIPRF,
		Address:   "0001ABCD",
		Model:     "HmIP-STH",
	})
	c.DescRegistry.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "0001ABCD", Type: "HmIP-STH"})
	c.ParamsetReg.Put(hmenum.InterfaceHmIPRF, "0001ABCD", hmenum.ParamsetKeyValues, hmproto.Paramset{})

	fake = &fakeOperations{kind: backends.KindCCU, deleteDeviceErr: backendErr}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	domain = NewDeviceAdminDomain(reg, w)
	centralUnit = c
	return domain, centralUnit, dev, fake
}

func TestUnpairDeviceHappyPath(t *testing.T) {
	t.Parallel()
	domain, c, _, fake := buildUnpairFixture(t, nil)

	if err := domain.UnpairDevice(context.Background(), "0001ABCD", false, false); err != nil {
		t.Fatalf("UnpairDevice: %v", err)
	}
	// Backend received the DeleteDevice call with flags=0 (plain unpair).
	if len(fake.deleteDeviceCalls) != 1 || fake.deleteDeviceCalls[0] != "0001ABCD" {
		t.Errorf("deleteDeviceCalls=%v, want [0001ABCD]", fake.deleteDeviceCalls)
	}
	if len(fake.deleteDeviceFlags) != 1 || fake.deleteDeviceFlags[0] != 0 {
		t.Errorf("deleteDeviceFlags=%v, want [0]", fake.deleteDeviceFlags)
	}
	// All in-memory caches must be cleared.
	if _, ok := c.ModelRegistry.Get("0001ABCD"); ok {
		t.Error("device still in ModelRegistry after unpair")
	}
	if _, ok := c.DeviceRegistry.Get(hmenum.InterfaceHmIPRF, "0001ABCD"); ok {
		t.Error("device still in DeviceRegistry after unpair")
	}
	if _, ok := c.DescRegistry.Get(hmenum.InterfaceHmIPRF, "0001ABCD"); ok {
		t.Error("description still in DescRegistry after unpair")
	}
	if c.ParamsetReg.Len() != 0 {
		t.Errorf("paramsets still present after unpair, len=%d", c.ParamsetReg.Len())
	}
}

func TestUnpairDeviceUnknownDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain, _, _, _ := buildUnpairFixture(t, nil)

	err := domain.UnpairDevice(context.Background(), "UNKNOWN", false, false)
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestUnpairDeviceForwardsResetAndForceFlags verifies the reset/force options
// are combined into the CCU delete bitmask handed to the backend.
func TestUnpairDeviceForwardsResetAndForceFlags(t *testing.T) {
	t.Parallel()
	domain, _, _, fake := buildUnpairFixture(t, nil)

	if err := domain.UnpairDevice(context.Background(), "0001ABCD", true, true); err != nil {
		t.Fatalf("UnpairDevice: %v", err)
	}
	want := backends.DeleteFlagReset | backends.DeleteFlagForce
	if len(fake.deleteDeviceFlags) != 1 || fake.deleteDeviceFlags[0] != want {
		t.Errorf("deleteDeviceFlags=%v, want [%d]", fake.deleteDeviceFlags, want)
	}
}

// TestUnpairDeviceFlagCombinationsAreDistinct verifies reset and force map
// onto their own dedicated bitmask bits rather than being conflated — each
// option independently controls its own bit, so a caller asking for only
// one of them must not accidentally set the other.
func TestUnpairDeviceFlagCombinationsAreDistinct(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reset bool
		force bool
		want  int
	}{
		{name: "neither", reset: false, force: false, want: 0},
		{name: "reset only", reset: true, force: false, want: backends.DeleteFlagReset},
		{name: "force only", reset: false, force: true, want: backends.DeleteFlagForce},
		{name: "both", reset: true, force: true, want: backends.DeleteFlagReset | backends.DeleteFlagForce},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			domain, _, _, fake := buildUnpairFixture(t, nil)

			if err := domain.UnpairDevice(context.Background(), "0001ABCD", tt.reset, tt.force); err != nil {
				t.Fatalf("UnpairDevice: %v", err)
			}
			if len(fake.deleteDeviceFlags) != 1 || fake.deleteDeviceFlags[0] != tt.want {
				t.Errorf("deleteDeviceFlags=%v, want [%d]", fake.deleteDeviceFlags, tt.want)
			}
		})
	}
}

func TestUnpairDeviceBackendUnsupportedPropagatesAndDoesNotClearRegistries(t *testing.T) {
	t.Parallel()
	domain, c, _, _ := buildUnpairFixture(t, backends.ErrUnsupported)

	err := domain.UnpairDevice(context.Background(), "0001ABCD", false, false)
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported propagation, got %v", err)
	}
	// Registries must NOT have been touched.
	if _, ok := c.ModelRegistry.Get("0001ABCD"); !ok {
		t.Error("ModelRegistry must not be cleared when backend returns ErrUnsupported")
	}
	if _, ok := c.DeviceRegistry.Get(hmenum.InterfaceHmIPRF, "0001ABCD"); !ok {
		t.Error("DeviceRegistry must not be cleared when backend returns ErrUnsupported")
	}
}

func TestUnpairDeviceNilRegistryOrWriterReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()

	// nil registry
	domainNilReg := NewDeviceAdminDomain(nil, client.NewValueWriter())
	if err := domainNilReg.UnpairDevice(context.Background(), "0001ABCD", false, false); !errors.Is(err, ErrNoDeviceBackend) {
		t.Errorf("nil registry: expected ErrNoDeviceBackend, got %v", err)
	}

	// nil writer
	reg := central.NewRegistry()
	domainNilWriter := NewDeviceAdminDomain(reg, nil)
	if err := domainNilWriter.UnpairDevice(context.Background(), "0001ABCD", false, false); !errors.Is(err, ErrNoDeviceBackend) {
		t.Errorf("nil writer: expected ErrNoDeviceBackend, got %v", err)
	}
}
