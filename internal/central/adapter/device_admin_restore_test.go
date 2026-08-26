// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// restoreRecordingOperations wraps fakeOperations (defined in
// device_admin_unpair_test.go) so the restore tests can record every
// RestoreConfigToDevice call — and, crucially, assert it was never made
// for interfaces the interface gate must reject before any wire call.
type restoreRecordingOperations struct {
	*fakeOperations
	restoreCalls []string
	restoreErr   error
}

func (f *restoreRecordingOperations) RestoreConfigToDevice(_ context.Context, address string) error {
	f.restoreCalls = append(f.restoreCalls, address)
	return f.restoreErr
}

// buildRestoreFixture wires a single device on the given interface into a
// fresh central + registry + ValueWriter. RestoreDeviceConfig only reads
// ModelRegistry and the ValueWriter, so — unlike buildUnpairFixture — no
// DeviceRegistry/DescRegistry/ParamsetReg seeding is needed.
func buildRestoreFixture(
	t *testing.T, iface hmenum.Interface, restoreErr error,
) (domain *DeviceAdminDomain, fake *restoreRecordingOperations) {
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
		InterfaceID: string(iface),
		Interface:   iface,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Name:        "Flur",
	})
	c.ModelRegistry.Put(dev)

	fake = &restoreRecordingOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		restoreErr:     restoreErr,
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.ParseWireInterfaceID(string(iface)), fake)

	domain = NewDeviceAdminDomain(reg, w)
	return domain, fake
}

// TestRestoreDeviceConfigHmIPRFCallsBackend verifies a HmIP-RF device (the
// HMIPServer wire method) reaches the backend's RestoreConfigToDevice.
func TestRestoreDeviceConfigHmIPRFCallsBackend(t *testing.T) {
	t.Parallel()
	domain, fake := buildRestoreFixture(t, hmenum.InterfaceHmIPRF, nil)

	if err := domain.RestoreDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("RestoreDeviceConfig: %v", err)
	}
	if len(fake.restoreCalls) != 1 || fake.restoreCalls[0] != "0001ABCD" {
		t.Errorf("restoreCalls=%v, want [0001ABCD]", fake.restoreCalls)
	}
}

// TestRestoreDeviceConfigBidCosRFCallsBackend mirrors the HmIP-RF case for
// the rfd wire method.
func TestRestoreDeviceConfigBidCosRFCallsBackend(t *testing.T) {
	t.Parallel()
	domain, fake := buildRestoreFixture(t, hmenum.InterfaceBidCosRF, nil)

	if err := domain.RestoreDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("RestoreDeviceConfig: %v", err)
	}
	if len(fake.restoreCalls) != 1 || fake.restoreCalls[0] != "0001ABCD" {
		t.Errorf("restoreCalls=%v, want [0001ABCD]", fake.restoreCalls)
	}
}

// TestRestoreDeviceConfigBidCosWiredRejectedBeforeWireCall verifies hs485d
// (BidCos-Wired) devices are rejected by the interface gate in
// DeviceAdminDomain.RestoreDeviceConfig before any XML-RPC call is
// attempted — the backend's RestoreConfigToDevice must never be invoked.
func TestRestoreDeviceConfigBidCosWiredRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	domain, fake := buildRestoreFixture(t, hmenum.InterfaceBidCosWired, nil)

	err := domain.RestoreDeviceConfig(context.Background(), "0001ABCD")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if len(fake.restoreCalls) != 0 {
		t.Errorf("restoreCalls=%v, want none — BidCos-Wired must be rejected before the wire call", fake.restoreCalls)
	}
}

// TestRestoreDeviceConfigCUxDRejectedBeforeWireCall mirrors the
// BidCos-Wired case for CUxD virtual devices.
func TestRestoreDeviceConfigCUxDRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	domain, fake := buildRestoreFixture(t, hmenum.InterfaceCUxD, nil)

	err := domain.RestoreDeviceConfig(context.Background(), "0001ABCD")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if len(fake.restoreCalls) != 0 {
		t.Errorf("restoreCalls=%v, want none — CUxD must be rejected before the wire call", fake.restoreCalls)
	}
}

// TestRestoreDeviceConfigUnknownDeviceReturnsErrNoDeviceBackend verifies an
// address absent from every central's ModelRegistry surfaces
// ErrNoDeviceBackend rather than a nil-pointer panic or a silent no-op.
func TestRestoreDeviceConfigUnknownDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain, _ := buildRestoreFixture(t, hmenum.InterfaceHmIPRF, nil)

	err := domain.RestoreDeviceConfig(context.Background(), "UNKNOWN")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestRestoreDeviceConfigNilRegistryOrWriterReturnsErrNoDeviceBackend
// verifies the domain guards against an un-wired registry or writer
// (both are required before any registry walk is attempted).
func TestRestoreDeviceConfigNilRegistryOrWriterReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()

	domainNilReg := NewDeviceAdminDomain(nil, client.NewValueWriter())
	if err := domainNilReg.RestoreDeviceConfig(context.Background(), "0001ABCD"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Errorf("nil registry: expected ErrNoDeviceBackend, got %v", err)
	}

	reg := central.NewRegistry()
	domainNilWriter := NewDeviceAdminDomain(reg, nil)
	if err := domainNilWriter.RestoreDeviceConfig(context.Background(), "0001ABCD"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Errorf("nil writer: expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestRestoreDeviceConfigPropagatesBackendError verifies a backend-level
// failure (e.g. a CCU XML-RPC fault) on a supported interface is
// propagated to the caller rather than swallowed.
func TestRestoreDeviceConfigPropagatesBackendError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	domain, fake := buildRestoreFixture(t, hmenum.InterfaceHmIPRF, wantErr)

	err := domain.RestoreDeviceConfig(context.Background(), "0001ABCD")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
	if len(fake.restoreCalls) != 1 {
		t.Errorf("restoreCalls=%v, want exactly one call", fake.restoreCalls)
	}
}
