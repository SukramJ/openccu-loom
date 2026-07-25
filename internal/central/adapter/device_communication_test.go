// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// comTestRecordingOperations wraps fakeOperations (defined in
// device_admin_unpair_test.go) so the communication-test tests can script
// TestDevice's result and record every call — crucially, assert it was
// never made for an interface the gate must reject before any wire call.
// fakeOperations already implements TestDevice (returning ErrUnsupported),
// which satisfies the comTester assertion on its own; this wrapper only
// adds the ability to script a canned result for the happy-path case.
type comTestRecordingOperations struct {
	*fakeOperations

	calls  []string
	result hmapi.CommunicationTestResult
	err    error
}

func (f *comTestRecordingOperations) TestDevice(_ context.Context, address string, _, _ float64) (hmapi.CommunicationTestResult, error) {
	f.calls = append(f.calls, address)
	if f.err != nil {
		return hmapi.CommunicationTestResult{}, f.err
	}
	return f.result, nil
}

// TestTestDeviceCommunicationRadioInterfaceCallsBackend verifies a
// BidCos-RF device reaches the backend's TestDevice and returns its
// result verbatim.
func TestTestDeviceCommunicationRadioInterfaceCallsBackend(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF", Interface: hmenum.InterfaceBidCosRF, Address: "ABC0001", Model: "HM-Sec-SC",
	})
	unit.ModelRegistry.Put(dev)

	want := hmapi.CommunicationTestResult{Passed: true, DurationMs: 42}
	fake := &comTestRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}, result: want}
	w := client.NewValueWriter()
	w.Register("ccu-01", "BidCos-RF", fake)

	domain := NewDeviceAdminDomain(reg, w)
	got, err := domain.TestDeviceCommunication(context.Background(), "ABC0001")
	if err != nil {
		t.Fatalf("TestDeviceCommunication: %v", err)
	}
	if got != want {
		t.Fatalf("result=%+v, want %+v", got, want)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "ABC0001" {
		t.Fatalf("backend TestDevice calls=%v, want [ABC0001]", fake.calls)
	}
}

// TestTestDeviceCommunicationUnsupportedInterfaceRejectedBeforeWireCall
// verifies VirtualDevices and CUxD devices are rejected by the interface
// gate before any wire call — the backend's TestDevice must never be
// invoked for either.
func TestTestDeviceCommunicationUnsupportedInterfaceRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		interfaceID string
		iface       hmenum.Interface
	}{
		{"VirtualDevices", "VirtualDevices", hmenum.InterfaceVirtualDevices},
		{"CUxD", "CUxD", hmenum.InterfaceCUxD},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			unit, reg := newReplaceUnit(t, "ccu-01")
			dev := device.New(device.Config{
				InterfaceID: tc.interfaceID, Interface: tc.iface, Address: "ABC0002", Model: "HM-Sec-SC",
			})
			unit.ModelRegistry.Put(dev)

			fake := &comTestRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
			w := client.NewValueWriter()
			w.Register("ccu-01", tc.interfaceID, fake)

			domain := NewDeviceAdminDomain(reg, w)
			_, err := domain.TestDeviceCommunication(context.Background(), "ABC0002")
			if !errors.Is(err, backends.ErrUnsupported) {
				t.Fatalf("expected ErrUnsupported, got %v", err)
			}
			if len(fake.calls) != 0 {
				t.Errorf("backend TestDevice must never be called for %s, got %v", tc.name, fake.calls)
			}
		})
	}
}

// TestTestDeviceCommunicationUnknownDeviceReturnsErrNoDeviceBackend
// verifies an address absent from every central's ModelRegistry surfaces
// ErrNoDeviceBackend rather than a nil-pointer panic or silent no-op.
func TestTestDeviceCommunicationUnknownDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	_, reg := newReplaceUnit(t, "ccu-01")
	domain := NewDeviceAdminDomain(reg, client.NewValueWriter())

	_, err := domain.TestDeviceCommunication(context.Background(), "UNKNOWN")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestTestDeviceCommunicationNilRegistryReturnsErrNoDeviceBackend verifies
// the un-wired-registry guard shared with ReplaceDevice/SearchWiredDevices.
func TestTestDeviceCommunicationNilRegistryReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain := NewDeviceAdminDomain(nil, client.NewValueWriter())

	_, err := domain.TestDeviceCommunication(context.Background(), "ABC0001")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestTestDeviceCommunicationNoBackendRegisteredReturnsErrNoDeviceBackend
// verifies a known device whose interface supports the test, but for which
// no backend was ever registered under its InterfaceID, surfaces
// ErrNoDeviceBackend rather than a nil dereference.
func TestTestDeviceCommunicationNoBackendRegisteredReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF", Interface: hmenum.InterfaceBidCosRF, Address: "ABC0003", Model: "HM-Sec-SC",
	})
	unit.ModelRegistry.Put(dev)

	domain := NewDeviceAdminDomain(reg, client.NewValueWriter())
	_, err := domain.TestDeviceCommunication(context.Background(), "ABC0003")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestTestDeviceCommunicationBackendErrorPropagates verifies a wire-level
// failure from the backend (e.g. a device that never answers the radio
// test frame) is returned unwrapped, not swallowed.
func TestTestDeviceCommunicationBackendErrorPropagates(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF", Interface: hmenum.InterfaceBidCosRF, Address: "ABC0004", Model: "HM-Sec-SC",
	})
	unit.ModelRegistry.Put(dev)

	sentinel := errors.New("ccu unreachable")
	fake := &comTestRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}, err: sentinel}
	w := client.NewValueWriter()
	w.Register("ccu-01", "BidCos-RF", fake)

	domain := NewDeviceAdminDomain(reg, w)
	_, err := domain.TestDeviceCommunication(context.Background(), "ABC0004")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
