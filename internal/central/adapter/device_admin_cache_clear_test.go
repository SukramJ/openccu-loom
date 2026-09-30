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

// cacheClearRecordingOperations wraps fakeOperations so the tests can record
// every ClearConfigCache call — and assert none was made for an interface
// the gate must reject before the wire.
type cacheClearRecordingOperations struct {
	*fakeOperations
	clearCalls []string
	clearErr   error
}

func (f *cacheClearRecordingOperations) ClearConfigCache(_ context.Context, address string) error {
	f.clearCalls = append(f.clearCalls, address)
	return f.clearErr
}

func buildCacheClearFixture(
	t *testing.T, iface hmenum.Interface, clearErr error,
) (domain *DeviceAdminDomain, fake *cacheClearRecordingOperations) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	c.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: string(iface),
		Interface:   iface,
		Address:     "0001ABCD",
		Model:       "HM-LC-Sw1-FM",
		Name:        "Flur",
	}))
	fake = &cacheClearRecordingOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		clearErr:       clearErr,
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.ParseWireInterfaceID(string(iface)), fake)
	return NewDeviceAdminDomain(reg, w), fake
}

// TestClearConfigCacheBidCosCallsBackend verifies both BidCos daemons (rfd,
// hs485d) reach the backend's ClearConfigCache with the device address.
func TestClearConfigCacheBidCosCallsBackend(t *testing.T) {
	t.Parallel()
	for _, iface := range []hmenum.Interface{hmenum.InterfaceBidCosRF, hmenum.InterfaceBidCosWired} {
		domain, fake := buildCacheClearFixture(t, iface, nil)
		if err := domain.ClearConfigCache(context.Background(), "0001ABCD"); err != nil {
			t.Fatalf("%s: ClearConfigCache: %v", iface, err)
		}
		if len(fake.clearCalls) != 1 || fake.clearCalls[0] != "0001ABCD" {
			t.Errorf("%s: clearCalls=%v, want [0001ABCD]", iface, fake.clearCalls)
		}
	}
}

// TestClearConfigCacheUnsupportedInterfacesRejectedBeforeWireCall verifies
// HmIP-RF, CUxD and VirtualDevices are refused by the interface gate with
// ErrUnsupported and never reach the wire.
func TestClearConfigCacheUnsupportedInterfacesRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceHmIPRF, hmenum.InterfaceCUxD, hmenum.InterfaceVirtualDevices,
	} {
		domain, fake := buildCacheClearFixture(t, iface, nil)
		err := domain.ClearConfigCache(context.Background(), "0001ABCD")
		if !errors.Is(err, backends.ErrUnsupported) {
			t.Fatalf("%s: expected ErrUnsupported, got %v", iface, err)
		}
		if len(fake.clearCalls) != 0 {
			t.Errorf("%s: clearCalls=%v, want none", iface, fake.clearCalls)
		}
	}
}

// TestClearConfigCacheUnknownDeviceReturnsErrNoDeviceBackend verifies an
// address no central models surfaces ErrNoDeviceBackend.
func TestClearConfigCacheUnknownDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain, _ := buildCacheClearFixture(t, hmenum.InterfaceBidCosRF, nil)
	if err := domain.ClearConfigCache(context.Background(), "UNKNOWN"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
	if err := NewDeviceAdminDomain(nil, nil).ClearConfigCache(context.Background(), "0001ABCD"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("unwired: expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestClearConfigCachePropagatesBackendError verifies a backend fault on a
// supported interface reaches the caller.
func TestClearConfigCachePropagatesBackendError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	domain, _ := buildCacheClearFixture(t, hmenum.InterfaceBidCosRF, wantErr)
	if err := domain.ClearConfigCache(context.Background(), "0001ABCD"); !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
}
