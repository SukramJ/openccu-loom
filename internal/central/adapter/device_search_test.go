// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// searchRecordingOperations wraps fakeOperations (defined in
// device_admin_unpair_test.go) so the search tests can script the
// SearchDevices count/error and record every call — crucially, assert it
// was never made for an interface the gate must reject before any wire
// call.
type searchRecordingOperations struct {
	*fakeOperations

	calls int
	found int
	err   error
}

func (f *searchRecordingOperations) SearchDevices(context.Context) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.found, nil
}

// TestSearchWiredDevicesBidCosWiredCallsBackend verifies a BidCos-Wired
// scan reaches the backend registered under the canonical wire ID
// ([WireInterfaceID], central-prefixed) and returns the found count.
func TestSearchWiredDevicesBidCosWiredCallsBackend(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	fake := &searchRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}, found: 3}
	w := client.NewValueWriter()
	w.Register(unit.Name(), hmtypes.NewWireInterfaceID(unit.Name(), hmenum.InterfaceBidCosWired), fake)

	domain := NewDeviceAdminDomain(reg, w)
	count, err := domain.SearchWiredDevices(context.Background(), "", "BidCos-Wired")
	if err != nil {
		t.Fatalf("SearchWiredDevices: %v", err)
	}
	if count != 3 {
		t.Fatalf("count=%d, want 3", count)
	}
	if fake.calls != 1 {
		t.Fatalf("backend SearchDevices calls=%d, want 1", fake.calls)
	}
}

// TestSearchWiredDevicesNonWiredInterfaceRejectedBeforeWireCall verifies
// a non-BidCos-Wired interface (e.g. BidCos-RF, which pairs via
// setInstallMode + addDevice rather than a bus scan) is rejected by the
// interface gate before any wire call is attempted.
func TestSearchWiredDevicesNonWiredInterfaceRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	fake := &searchRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}, found: 9}
	w := client.NewValueWriter()
	w.Register(unit.Name(), hmtypes.NewWireInterfaceID(unit.Name(), hmenum.InterfaceBidCosRF), fake)

	domain := NewDeviceAdminDomain(reg, w)
	_, err := domain.SearchWiredDevices(context.Background(), "", "BidCos-RF")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("backend SearchDevices must never be called for a non-wired interface, got %d calls", fake.calls)
	}
}

// TestSearchWiredDevicesUnknownCentralReturnsErrUnknownCentral verifies an
// explicit, unregistered central name surfaces ErrUnknownCentral rather
// than falling back to the sole central.
func TestSearchWiredDevicesUnknownCentralReturnsErrUnknownCentral(t *testing.T) {
	t.Parallel()
	_, reg := newReplaceUnit(t, "ccu-01")
	domain := NewDeviceAdminDomain(reg, client.NewValueWriter())

	_, err := domain.SearchWiredDevices(context.Background(), "does-not-exist", "BidCos-Wired")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("expected ErrUnknownCentral, got %v", err)
	}
}

// TestSearchWiredDevicesNoBackendRegisteredReturnsErrUnknownCentral
// verifies that a resolved central with no backend registered under the
// wire-scoped interface ID (e.g. the interface was never wired for that
// central) surfaces ErrUnknownCentral — distinct from the nil-registry
// case below.
func TestSearchWiredDevicesNoBackendRegisteredReturnsErrUnknownCentral(t *testing.T) {
	t.Parallel()
	_, reg := newReplaceUnit(t, "ccu-01")
	domain := NewDeviceAdminDomain(reg, client.NewValueWriter())

	_, err := domain.SearchWiredDevices(context.Background(), "", "BidCos-Wired")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("expected ErrUnknownCentral, got %v", err)
	}
}

// TestSearchWiredDevicesNilRegistryReturnsErrNoDeviceBackend verifies the
// un-wired-registry guard shared with ReplaceDevice/ReplaceCandidates.
func TestSearchWiredDevicesNilRegistryReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain := NewDeviceAdminDomain(nil, client.NewValueWriter())

	_, err := domain.SearchWiredDevices(context.Background(), "", "BidCos-Wired")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestSearchWiredDevicesBackendErrorPropagates verifies a wire-level
// failure from the backend (e.g. hs485d unreachable) is returned
// unwrapped rather than swallowed, and the best-effort inbox refresh does
// not turn it into a success.
func TestSearchWiredDevicesBackendErrorPropagates(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	sentinel := errors.New("hs485d unreachable")
	fake := &searchRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}, err: sentinel}
	w := client.NewValueWriter()
	w.Register(unit.Name(), hmtypes.NewWireInterfaceID(unit.Name(), hmenum.InterfaceBidCosWired), fake)

	domain := NewDeviceAdminDomain(reg, w)
	count, err := domain.SearchWiredDevices(context.Background(), "", "BidCos-Wired")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if count != 0 {
		t.Fatalf("count=%d, want 0 on error", count)
	}
}
