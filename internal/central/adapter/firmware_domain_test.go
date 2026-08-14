// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── nil-guard ───────────────────────────────────────────────────────────────

func TestNewFirmwareDomainNilGuards(t *testing.T) {
	t.Parallel()
	d := NewFirmwareDomain(nil, nil)
	if err := d.RefreshFirmwareData(context.Background()); err == nil {
		t.Fatal("expected error when registry and writer are nil")
	}
}

// ─── writerDescFetcher ───────────────────────────────────────────────────────

// listRecordingOps embeds fakeOperations and records ListDevices calls with
// configurable return values.
type listRecordingOps struct {
	fakeOperations
	calls      int
	returnDesc []hmproto.DeviceDescription
	returnErr  error
}

func (f *listRecordingOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	f.calls++
	return f.returnDesc, f.returnErr
}

func TestWriterDescFetcherListDevices_HappyPath(t *testing.T) {
	t.Parallel()

	want := []hmproto.DeviceDescription{
		{Address: "0002ABCD", Type: "HmIP-PSM"},
	}
	fake := &listRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDesc:     want,
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	f := &writerDescFetcher{writer: w, central: "ccu-01"}
	got, err := f.ListDevices(context.Background(), wireHmIPRF)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("expected 1 backend call, got %d", fake.calls)
	}
	if len(got) != len(want) || got[0].Address != want[0].Address {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWriterDescFetcherListDevices_MissingBackend(t *testing.T) {
	t.Parallel()

	// Writer has no backend registered for ccu-01/HmIP-RF.
	w := clientpkg.NewValueWriter()
	f := &writerDescFetcher{writer: w, central: "ccu-01"}

	_, err := f.ListDevices(context.Background(), wireHmIPRF)
	if err == nil {
		t.Fatal("expected error when backend is not registered")
	}
	if !strings.Contains(err.Error(), "ccu-01") || !strings.Contains(err.Error(), "HmIP-RF") {
		t.Errorf("error %q must mention central and interface", err.Error())
	}
}

// ─── RefreshFirmwareData happy path ──────────────────────────────────────────

// buildFirmwareDomainFixture creates a central with one device and a fake
// backend registered under "HmIP-RF", returning the FirmwareDomain and the
// fake for call inspection.
func buildFirmwareDomainFixture(t *testing.T) (*FirmwareDomain, *listRecordingOps) {
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
		Address:     "0002ABCD",
		Model:       "HmIP-PSM",
		Name:        "Socket",
	})
	c.ModelRegistry.Put(dev)

	// Seed the description registry so the coordinator's GetInterfaceIDs()
	// returns HmIP-RF and the firmware sweep reaches the backend.
	c.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{
		Address: "0002ABCD", Type: "HmIP-PSM",
	})
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: wireHmIPRF,
		Address:   "0002ABCD",
		Model:     "HmIP-PSM",
	})

	fake := &listRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDesc: []hmproto.DeviceDescription{
			{Address: "0002ABCD", Type: "HmIP-PSM", Children: []string{"0002ABCD:0"}},
		},
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	return NewFirmwareDomain(reg, w), fake
}

func TestFirmwareDomainRefreshFirmwareData_HappyPath(t *testing.T) {
	t.Parallel()

	d, fake := buildFirmwareDomainFixture(t)
	if err := d.RefreshFirmwareData(context.Background()); err != nil {
		t.Fatalf("RefreshFirmwareData: %v", err)
	}
	if fake.calls < 1 {
		t.Errorf("expected backend ListDevices to be invoked, got %d calls", fake.calls)
	}
}

// ─── applyFirmwareFromDescriptions ───────────────────────────────────────────

// TestApplyFirmwareFromDescriptions_UpdatesFromFreshDescription verifies that
// the current/available firmware version and the update lifecycle state move
// from the (just refreshed) description registry onto the live device model,
// while the Updatable capability bound at materialisation time is preserved
// untouched.
func TestApplyFirmwareFromDescriptions_UpdatesFromFreshDescription(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-apply"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0004ABCD",
		Model:       "HmIP-PSM",
		Firmware: device.FirmwareInfo{
			Current:     "1.2.2",
			Updatable:   true,
			UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate,
		},
	})
	c.ModelRegistry.Put(dev)

	c.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{
		Address:             "0004ABCD",
		Type:                "HmIP-PSM",
		Firmware:            "1.4.10",
		AvailableFirmware:   "1.4.10",
		FirmwareUpdateState: "UP_TO_DATE",
	})

	applyFirmwareFromDescriptions(c)

	info := dev.Firmware().Info()
	if info.Current != "1.4.10" {
		t.Errorf("Current = %q, want 1.4.10", info.Current)
	}
	if info.Available != "1.4.10" {
		t.Errorf("Available = %q, want 1.4.10", info.Available)
	}
	if info.UpdateState != hmenum.DeviceFirmwareStateUpToDate {
		t.Errorf("UpdateState = %q, want UP_TO_DATE", info.UpdateState)
	}
	if !info.Updatable {
		t.Error("Updatable must stay true — it is bound at materialisation time, not refreshed here")
	}
}

// TestApplyFirmwareFromDescriptions_DeviceWithoutDescriptionUntouched verifies
// that a live device with no matching entry in the description registry keeps
// its previously observed firmware info unchanged.
func TestApplyFirmwareFromDescriptions_DeviceWithoutDescriptionUntouched(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-apply-notfound"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0005ABCD",
		Model:       "HmIP-PSM",
		Firmware: device.FirmwareInfo{
			Current:     "9.9.9",
			Updatable:   true,
			UpdateState: hmenum.DeviceFirmwareStateUpToDate,
		},
	})
	c.ModelRegistry.Put(dev)
	// No description registered for 0005ABCD.

	applyFirmwareFromDescriptions(c)

	info := dev.Firmware().Info()
	if info.Current != "9.9.9" || info.UpdateState != hmenum.DeviceFirmwareStateUpToDate {
		t.Errorf("firmware info changed for a device without a description: %+v", info)
	}
}

// ─── RefreshCentralFirmwareDataByState ───────────────────────────────────────

// TestRefreshCentralFirmwareDataByState_NilGuards verifies the nil-unit and
// nil-writer guards return an error without touching anything else.
func TestRefreshCentralFirmwareDataByState_NilGuards(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-bystate-nil"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	w := clientpkg.NewValueWriter()

	if err := RefreshCentralFirmwareDataByState(context.Background(), nil, w, nil); err == nil {
		t.Fatal("expected error for nil unit")
	}
	if err := RefreshCentralFirmwareDataByState(context.Background(), c, nil, nil); err == nil {
		t.Fatal("expected error for nil writer")
	}
}

// TestRefreshCentralFirmwareDataByState_StateGateShortCircuits verifies that
// when no live device on an interface sits in one of the requested firmware
// states, the per-interface state gate skips the description re-pull
// entirely — the fake backend must observe zero ListDevices calls.
func TestRefreshCentralFirmwareDataByState_StateGateShortCircuits(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-bystate-gate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0006ABCD",
		Model:       "HmIP-PSM",
		Firmware:    device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateUpToDate},
	})
	c.ModelRegistry.Put(dev)

	// Seed the description registry so GetInterfaceIDs() returns HmIP-RF and
	// the outer per-interface loop actually runs.
	c.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{Address: "0006ABCD"})

	fake := &listRecordingOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-bystate-gate", "HmIP-RF", fake)

	// The requested states do not include UP_TO_DATE, so no device matches.
	err = RefreshCentralFirmwareDataByState(context.Background(), c, w,
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateDeliverFirmwareImage})
	if err != nil {
		t.Fatalf("RefreshCentralFirmwareDataByState: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("ListDevices called %d times, want 0 (state gate should short-circuit)", fake.calls)
	}
}

// TestRefreshCentralFirmwareDataByState_NoInterfacesNoFetch verifies that an
// empty description registry (no interfaces known yet) skips the fetch loop
// entirely and returns nil without error.
func TestRefreshCentralFirmwareDataByState_NoInterfacesNoFetch(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-bystate-noiface"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	w := clientpkg.NewValueWriter()

	err = RefreshCentralFirmwareDataByState(context.Background(), c, w,
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateDeliverFirmwareImage})
	if err != nil {
		t.Fatalf("RefreshCentralFirmwareDataByState: %v", err)
	}
}

// ─── modelFirmwareStateReader ────────────────────────────────────────────────

// TestModelFirmwareStateReader_DeviceFirmwareStates verifies that
// DeviceFirmwareStates returns only the devices matching the requested
// interface, each mapped to its current firmware update state. The requested
// identifier is the canonical wire id the description registry hands the
// coordinator, not the bare interface.
func TestModelFirmwareStateReader_DeviceFirmwareStates(t *testing.T) {
	t.Parallel()

	reg := registry.NewModelRegistry()
	hmipWireID := WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF)

	hmipDev := device.New(device.Config{
		InterfaceID: hmipWireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0007ABCD",
		Firmware:    device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
	})
	bidcosDev := device.New(device.Config{
		InterfaceID: WireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF),
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "0008ABCD",
		Firmware:    device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateUpToDate},
	})
	reg.Put(hmipDev)
	reg.Put(bidcosDev)

	reader := modelFirmwareStateReader{reg: reg}
	states := reader.DeviceFirmwareStates(hmtypes.ParseWireInterfaceID(hmipWireID))

	if len(states) != 1 {
		t.Fatalf("states = %v, want exactly 1 entry for HmIP-RF", states)
	}
	got, ok := states["0007ABCD"]
	if !ok {
		t.Fatal("expected 0007ABCD in the result")
	}
	if got != hmenum.DeviceFirmwareStateReadyForUpdate {
		t.Errorf("state = %q, want READY_FOR_UPDATE", got)
	}
	if _, present := states["0008ABCD"]; present {
		t.Error("BidCos device must not appear in the HmIP-RF result set")
	}
}

// ─── wire-id keying ──────────────────────────────────────────────────────────

// TestApplyFirmwareFromDescriptionsResolvesByWireInterfaceID pins the key the
// description registry is actually populated with. A device carries two
// identifier spaces — the bare `Interface` for the operator surfaces and the
// canonical `InterfaceID` (`<central>-<iface>`) the registries are keyed by —
// and they only differ once the central has a name. Looking the description up
// under the bare interface misses every entry, so a CCU-side firmware change
// never reaches the model and the SPA / MQTT update entity keeps the values the
// device was materialised with at boot.
func TestApplyFirmwareFromDescriptionsResolvesByWireInterfaceID(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-wire"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)

	dev := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0009ABCD",
		Model:       "HmIP-PSM",
		Firmware: device.FirmwareInfo{
			Current:     "1.2.2",
			Updatable:   true,
			UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate,
		},
	})
	c.ModelRegistry.Put(dev)

	c.DescRegistry.Put(hmtypes.ParseWireInterfaceID(wireID), hmproto.DeviceDescription{
		Address:             "0009ABCD",
		Type:                "HmIP-PSM",
		Firmware:            "1.4.10",
		AvailableFirmware:   "1.4.12",
		FirmwareUpdateState: "READY_FOR_UPDATE",
	})

	applyFirmwareFromDescriptions(c)

	info := dev.Firmware().Info()
	if info.Current != "1.4.10" || info.Available != "1.4.12" {
		t.Errorf("firmware = %+v, want current 1.4.10 / available 1.4.12", info)
	}
}

// TestRefreshCentralFirmwareDataByStateReachesBackendOnNamedCentral drives the
// state-gated sweep through its real entry point on a central whose name makes
// the wire id differ from the bare interface. The gate has to see the device's
// READY_FOR_UPDATE state, otherwise the description re-pull never reaches the
// CCU at all.
func TestRefreshCentralFirmwareDataByStateReachesBackendOnNamedCentral(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-bystate-wire"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)

	c.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "000BABCD",
		Model:       "HmIP-PSM",
		Firmware:    device.FirmwareInfo{UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
	}))
	c.DescRegistry.Put(hmtypes.ParseWireInterfaceID(wireID), hmproto.DeviceDescription{Address: "000BABCD"})

	fake := &listRecordingOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := clientpkg.NewValueWriter()
	w.Register(c.Name(), wireID, fake)

	if err := RefreshCentralFirmwareDataByState(context.Background(), c, w,
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateReadyForUpdate}); err != nil {
		t.Fatalf("RefreshCentralFirmwareDataByState: %v", err)
	}
	if fake.calls == 0 {
		t.Fatal("the state gate must match the device, so the description re-pull reaches the backend")
	}
}
