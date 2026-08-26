// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// replaceRecordingOperations wraps fakeOperations (defined in
// device_admin_unpair_test.go) so the replace tests can script
// ListReplaceableDevices / ReplaceDevice responses and record every
// ReplaceDevice call — crucially, assert it was never made for an
// interface the gate must reject before any wire call.
type replaceRecordingOperations struct {
	*fakeOperations

	listResult []hmproto.DeviceDescription
	listErr    error

	replaceCalls [][2]string
	replaceErr   error
}

func (f *replaceRecordingOperations) ListReplaceableDevices(_ context.Context, _ string) ([]hmproto.DeviceDescription, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResult, nil
}

func (f *replaceRecordingOperations) ReplaceDevice(_ context.Context, oldAddress, newAddress string) error {
	f.replaceCalls = append(f.replaceCalls, [2]string{oldAddress, newAddress})
	return f.replaceErr
}

// newReplaceUnit builds a Unit named name and registers it into a fresh
// registry, mirroring newTestCentralNamed (pingpong_wiring_test.go) but
// bundling the registry construction so every test starts from a clean pair.
func newReplaceUnit(t *testing.T, name string) (unit *central.Unit, reg *central.Registry) {
	t.Helper()
	unit = newTestCentralNamed(t, name)
	reg = central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register(%s): %v", name, err)
	}
	return unit, reg
}

// registerReplaceClient wires a client entry for iface onto unit
// (mirrors registerCentralWithClient in groups_test.go) so
// DeviceAdminDomain.replaceInterfaces walks it. The entry's InterfaceID is
// the canonical wire id the production wiring registers, so the resolution
// the domain performs is the one the daemon performs.
func registerReplaceClient(t *testing.T, unit *central.Unit, iface hmenum.Interface) {
	t.Helper()
	ifaceID := WireInterfaceID(unit.Name(), iface)
	ic := newTestInterfaceClient(t, unit.Name(), string(iface), 5)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: ifaceID,
		Interface:   iface,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register(%s): %v", ifaceID, err)
	}
}

// ---------------------------------------------------------------------------
// ReplaceCandidates
// ---------------------------------------------------------------------------

// TestReplaceCandidatesFiltersKnownDevicesAndChannels verifies that the
// candidate list keeps only device-level rows (Parent == "") the daemon
// already models — a channel row and a device the ModelRegistry has never
// seen (not yet accepted) are both dropped.
func TestReplaceCandidatesFiltersKnownDevicesAndChannels(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	registerReplaceClient(t, unit, hmenum.InterfaceBidCosRF)

	fake := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	fake.listResult = []hmproto.DeviceDescription{
		{Address: "CAND001", Type: "HM-Sec-SC"},
		{Address: "CAND001:1", Parent: "CAND001", Type: "SHUTTER_CONTACT"},
		{Address: "CAND002", Type: "HM-LC-Sw1"}, // never accepted — must be dropped
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), fake)

	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), Interface: hmenum.InterfaceBidCosRF,
		Address: "CAND001", Model: "HM-Sec-SC", Name: "Fenster",
	}))

	domain := NewDeviceAdminDomain(reg, w)
	out, err := domain.ReplaceCandidates(context.Background(), "", "NEW001")
	if err != nil {
		t.Fatalf("ReplaceCandidates: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("candidates=%+v, want exactly 1 (channel row + unaccepted device filtered)", out)
	}
	if out[0].Address != "CAND001" || out[0].Interface != "BidCos-RF" || out[0].Central != "ccu-01" {
		t.Errorf("candidate=%+v", out[0])
	}
}

// TestReplaceCandidatesModelMatchesFlag verifies ModelMatches is true only
// for the candidate whose Type equals the inbox model of the new device.
func TestReplaceCandidatesModelMatchesFlag(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	registerReplaceClient(t, unit, hmenum.InterfaceBidCosRF)

	fake := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	fake.listResult = []hmproto.DeviceDescription{
		{Address: "CAND001", Type: "HM-Sec-SC"},
		{Address: "CAND002", Type: "HM-LC-Sw1"},
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), fake)

	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), Interface: hmenum.InterfaceBidCosRF, Address: "CAND001", Model: "HM-Sec-SC",
	}))
	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), Interface: hmenum.InterfaceBidCosRF, Address: "CAND002", Model: "HM-LC-Sw1",
	}))
	// The new (inbox) device is HM-Sec-SC: CAND001 is an exact-model match,
	// CAND002 is a CCU-approved but cross-type compatible swap.
	unit.HubModel.Inbox.Replace([]hub.InboxDevice{{Address: "NEW001", Model: "HM-Sec-SC"}})

	domain := NewDeviceAdminDomain(reg, w)
	out, err := domain.ReplaceCandidates(context.Background(), "", "NEW001")
	if err != nil {
		t.Fatalf("ReplaceCandidates: %v", err)
	}
	matches := make(map[string]bool, len(out))
	for _, c := range out {
		matches[c.Address] = c.ModelMatches
	}
	if !matches["CAND001"] {
		t.Errorf("CAND001 should be ModelMatches=true, got candidates=%+v", out)
	}
	if matches["CAND002"] {
		t.Errorf("CAND002 should be ModelMatches=false, got candidates=%+v", out)
	}
}

// TestReplaceCandidatesTolerantOfPerInterfaceListError verifies a
// wrong-interface-serial fault from one replace-capable interface does not
// abort the whole lookup — candidates from a second interface still come
// back.
func TestReplaceCandidatesTolerantOfPerInterfaceListError(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	registerReplaceClient(t, unit, hmenum.InterfaceBidCosRF)
	registerReplaceClient(t, unit, hmenum.InterfaceBidCosWired)

	failing := &replaceRecordingOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		listErr:        errors.New("wrong-interface serial"),
	}
	ok := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	ok.listResult = []hmproto.DeviceDescription{{Address: "CAND001", Type: "HM-Sec-SC"}}

	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), failing)
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosWired), ok)

	unit.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosWired), Interface: hmenum.InterfaceBidCosWired, Address: "CAND001", Model: "HM-Sec-SC",
	}))

	domain := NewDeviceAdminDomain(reg, w)
	out, err := domain.ReplaceCandidates(context.Background(), "", "NEW001")
	if err != nil {
		t.Fatalf("a per-interface list error must be tolerated, got: %v", err)
	}
	if len(out) != 1 || out[0].Address != "CAND001" || out[0].Interface != "BidCos-Wired" {
		t.Fatalf("candidates=%+v, want exactly the BidCos-Wired candidate", out)
	}
}

// TestReplaceCandidatesCentralResolution exercises the three central
// resolution outcomes: an explicit unknown name, the sole-central fallback,
// and ambiguity once a second central is registered.
func TestReplaceCandidatesCentralResolution(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")
	registerReplaceClient(t, unit, hmenum.InterfaceBidCosRF)
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}})
	domain := NewDeviceAdminDomain(reg, w)

	if _, err := domain.ReplaceCandidates(context.Background(), "", "NEW001"); err != nil {
		t.Fatalf("sole central should resolve with an empty name, got: %v", err)
	}

	if _, err := domain.ReplaceCandidates(context.Background(), "does-not-exist", "NEW001"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("explicit unknown central: expected ErrUnknownCentral, got %v", err)
	}

	// Registering a second central makes the empty-name fallback ambiguous.
	second := newTestCentralNamed(t, "ccu-02")
	if err := reg.Register(second); err != nil {
		t.Fatalf("reg.Register(ccu-02): %v", err)
	}
	if _, err := domain.ReplaceCandidates(context.Background(), "", "NEW001"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("ambiguous central (2 registered, no name given): expected ErrUnknownCentral, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReplaceDevice
// ---------------------------------------------------------------------------

// TestReplaceDeviceEligibleInterfaceCallsBackend verifies a BidCos-RF
// device reaches the backend's ReplaceDevice call.
func TestReplaceDeviceEligibleInterfaceCallsBackend(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	dev := device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), Interface: hmenum.InterfaceBidCosRF, Address: "OLD001", Model: "HM-Sec-SC",
	})
	unit.ModelRegistry.Put(dev)
	unit.DeviceRegistry.Put(registry.DeviceEntry{Interface: hmtypes.ParseWireInterfaceID(dev.InterfaceID), Address: "OLD001", Model: "HM-Sec-SC"})

	fake := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), fake)

	domain := NewDeviceAdminDomain(reg, w)
	if err := domain.ReplaceDevice(context.Background(), "", "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}
	if len(fake.replaceCalls) != 1 || fake.replaceCalls[0] != [2]string{"OLD001", "NEW001"} {
		t.Fatalf("replaceCalls=%v, want [[OLD001 NEW001]]", fake.replaceCalls)
	}
}

// TestReplaceDeviceIneligibleInterfaceRejectedBeforeWireCall verifies an
// HmIP device (HMIPServer throws NotImplementedException for replaceDevice)
// is rejected by the interface gate before any wire call is attempted — the
// backend's ReplaceDevice must never be invoked.
func TestReplaceDeviceIneligibleInterfaceRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	dev := device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF), Interface: hmenum.InterfaceHmIPRF, Address: "OLD001", Model: "HmIP-STH",
	})
	unit.ModelRegistry.Put(dev)
	unit.DeviceRegistry.Put(registry.DeviceEntry{Interface: hmtypes.ParseWireInterfaceID(dev.InterfaceID), Address: "OLD001", Model: "HmIP-STH"})

	fake := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF), fake)

	domain := NewDeviceAdminDomain(reg, w)
	err := domain.ReplaceDevice(context.Background(), "", "OLD001", "NEW001")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if len(fake.replaceCalls) != 0 {
		t.Errorf("backend ReplaceDevice must never be called for an ineligible interface, got %v", fake.replaceCalls)
	}
}

// TestReplaceDeviceUnknownOldDeviceReturnsErrNoDeviceBackend verifies an
// address absent from every central's ModelRegistry surfaces
// ErrNoDeviceBackend rather than a nil-pointer panic or silent no-op.
func TestReplaceDeviceUnknownOldDeviceReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	_, reg := newReplaceUnit(t, "ccu-01")
	domain := NewDeviceAdminDomain(reg, client.NewValueWriter())

	err := domain.ReplaceDevice(context.Background(), "", "UNKNOWN", "NEW001")
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestReplaceDeviceSucceedsWhenEagerRefreshFails verifies that a failure
// of the best-effort eager model refresh does NOT surface the
// already-committed (irreversible) CCU swap as an error. The old device is
// present in the ModelRegistry (so the pre-checks pass) but absent from the
// coordinator's device registry, so coordinators.ReplaceDevice returns
// "old device not found" — which must be logged and swallowed, not
// returned. The backend swap must still have been issued.
func TestReplaceDeviceSucceedsWhenEagerRefreshFails(t *testing.T) {
	t.Parallel()
	unit, reg := newReplaceUnit(t, "ccu-01")

	dev := device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), Interface: hmenum.InterfaceBidCosRF, Address: "OLD001", Model: "HM-Sec-SC",
	})
	unit.ModelRegistry.Put(dev)
	// Deliberately do NOT register OLD001 in unit.DeviceRegistry, so the
	// coordinator's eager ReplaceDevice fails to find the old device.

	fake := &replaceRecordingOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF), fake)

	domain := NewDeviceAdminDomain(reg, w)
	if err := domain.ReplaceDevice(context.Background(), "", "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice must succeed despite eager-refresh failure, got: %v", err)
	}
	if len(fake.replaceCalls) != 1 || fake.replaceCalls[0] != [2]string{"OLD001", "NEW001"} {
		t.Fatalf("backend swap must still fire: replaceCalls=%v, want [[OLD001 NEW001]]", fake.replaceCalls)
	}
}

// TestReplaceDeviceNilRegistryReturnsErrNoDeviceBackend verifies both
// ReplaceCandidates and ReplaceDevice guard against an un-wired registry.
func TestReplaceDeviceNilRegistryReturnsErrNoDeviceBackend(t *testing.T) {
	t.Parallel()
	domain := NewDeviceAdminDomain(nil, client.NewValueWriter())

	if err := domain.ReplaceDevice(context.Background(), "", "OLD001", "NEW001"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("ReplaceDevice: expected ErrNoDeviceBackend, got %v", err)
	}
	if _, err := domain.ReplaceCandidates(context.Background(), "", "NEW001"); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("ReplaceCandidates: expected ErrNoDeviceBackend, got %v", err)
	}
}
