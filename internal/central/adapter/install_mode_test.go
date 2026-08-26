// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// installFakeOps extends fakeOperations with SetInstallMode call recording.
type installFakeOps struct {
	fakeOperations
	calls []installModeCall
}

type installModeCall struct {
	on      bool
	dur     int
	mode    int
	address string
}

func (f *installFakeOps) SetInstallMode(_ context.Context, on bool, duration, mode int, deviceAddress string) error {
	f.calls = append(f.calls, installModeCall{on: on, dur: duration, mode: mode, address: deviceAddress})
	return nil
}

// buildInstallModeFixture creates a central with one ValueWriter that
// has a fake backend registered exactly like production wiring does:
// under the canonical wire ID (central-prefixed, see [WireInterfaceID]
// and the writer.Register call in ccu_wiring.go) — NOT under the bare
// interface type. The install-mode writer must bridge that difference.
func buildInstallModeFixture(t *testing.T, centralName string, iface hmenum.Interface) (
	*central.Unit,
	*clientpkg.ValueWriter,
	*installFakeOps,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	fake := &installFakeOps{}
	w := clientpkg.NewValueWriter()
	w.Register(centralName, hmtypes.NewWireInterfaceID(centralName, iface), fake)
	return c, w, fake
}

// ---------------------------------------------------------------------------
// installModeWriter.SetInstallMode
// ---------------------------------------------------------------------------

func TestInstallModeWriter_SetInstallMode_Enable(t *testing.T) {
	t.Parallel()
	c, w, fake := buildInstallModeFixture(t, "ccu-01", hmenum.InterfaceHmIPRF)
	writer := &installModeWriter{unit: c, writer: w}

	if err := writer.SetInstallMode(context.Background(), "HmIP-RF", true, 60*time.Second); err != nil {
		t.Fatalf("SetInstallMode: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	got := fake.calls[0]
	if !got.on {
		t.Errorf("on = false, want true")
	}
	if got.dur != 60 {
		t.Errorf("duration = %d, want 60", got.dur)
	}
	if got.mode != installModeNormal {
		t.Errorf("mode = %d, want %d (installModeNormal)", got.mode, installModeNormal)
	}
	if got.address != "" {
		t.Errorf("address = %q, want empty", got.address)
	}
}

func TestInstallModeWriter_SetInstallMode_Disable(t *testing.T) {
	t.Parallel()
	c, w, fake := buildInstallModeFixture(t, "ccu-01", hmenum.InterfaceHmIPRF)
	writer := &installModeWriter{unit: c, writer: w}

	if err := writer.SetInstallMode(context.Background(), "HmIP-RF", false, 0); err != nil {
		t.Fatalf("SetInstallMode disable: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	if fake.calls[0].on {
		t.Errorf("on = true, want false for disable")
	}
}

func TestInstallModeWriter_SetInstallMode_UnknownInterface(t *testing.T) {
	t.Parallel()
	c, w, _ := buildInstallModeFixture(t, "ccu-01", hmenum.InterfaceHmIPRF)
	writer := &installModeWriter{unit: c, writer: w}

	err := writer.SetInstallMode(context.Background(), "NoSuchIface", true, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for unknown interface, got nil")
	}
}

// ---------------------------------------------------------------------------
// installModeWriter.SetInstallModeForDevice
// ---------------------------------------------------------------------------

func TestInstallModeWriter_SetInstallModeForDevice(t *testing.T) {
	t.Parallel()
	c, w, fake := buildInstallModeFixture(t, "ccu-01", hmenum.InterfaceBidCosRF)
	writer := &installModeWriter{unit: c, writer: w}

	if err := writer.SetInstallModeForDevice(context.Background(), "BidCos-RF", 60*time.Second, "ABC123"); err != nil {
		t.Fatalf("SetInstallModeForDevice: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	got := fake.calls[0]
	if !got.on {
		t.Errorf("on = false, want true (device install mode always opens)")
	}
	if got.mode != installModeNormal {
		t.Errorf("mode = %d, want %d (installModeNormal)", got.mode, installModeNormal)
	}
	if got.address != "ABC123" {
		t.Errorf("address = %q, want ABC123", got.address)
	}
}

func TestInstallModeWriter_SetInstallModeForDevice_UnknownInterface(t *testing.T) {
	t.Parallel()
	c, w, _ := buildInstallModeFixture(t, "ccu-01", hmenum.InterfaceBidCosRF)
	writer := &installModeWriter{unit: c, writer: w}

	err := writer.SetInstallModeForDevice(context.Background(), "NoSuchIface", 30*time.Second, "ABC123")
	if err == nil {
		t.Fatal("expected error for unknown interface, got nil")
	}
}

// ---------------------------------------------------------------------------
// WireInstallModeDPs
// ---------------------------------------------------------------------------

// buildWireFixture creates a central with two interface clients and a
// ValueWriter so WireInstallModeDPs can be called. One client is a
// pairing-capable radio (HmIP-RF), the other is not (VirtualDevices).
func buildWireFixture(t *testing.T) (*central.Unit, *clientpkg.ValueWriter) {
	t.Helper()

	const centralName = "ccu-wire"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	icPairing := newTestInterfaceClient(t, centralName, "HmIP-RF", 5)
	icNoPairing := newTestInterfaceClient(t, centralName, "VirtualDevices", 5)

	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      icPairing,
	}); err != nil {
		t.Fatalf("register HmIP-RF client: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "VirtualDevices",
		Interface:   hmenum.InterfaceVirtualDevices,
		Client:      icNoPairing,
	}); err != nil {
		t.Fatalf("register VirtualDevices client: %v", err)
	}

	w := clientpkg.NewValueWriter()
	w.Register(centralName, hmtypes.NewWireInterfaceID(centralName, hmenum.InterfaceHmIPRF), &installFakeOps{})
	w.Register(centralName, hmtypes.NewWireInterfaceID(centralName, hmenum.InterfaceVirtualDevices), &installFakeOps{})

	return c, w
}

func TestWireInstallModeDPs_RegistersPairingCapableOnly(t *testing.T) {
	t.Parallel()
	c, w := buildWireFixture(t)

	WireInstallModeDPs(c, w)

	dps := c.HubModel.InstallModeDPs()
	if len(dps) != 1 {
		t.Fatalf("InstallModeDPs len=%d, want 1 (HmIP-RF only)", len(dps))
	}
	if got := dps[0].InterfaceID; got != "HmIP-RF" {
		t.Errorf("InstallModeDP InterfaceID = %q, want HmIP-RF", got)
	}
}

func TestWireInstallModeDPs_NilSafe(t *testing.T) {
	t.Parallel()
	// nil unit must not panic.
	WireInstallModeDPs(nil, nil)
	WireInstallModeDPs(nil, clientpkg.NewValueWriter())

	// non-nil unit but nil writer must not panic.
	c, err := central.New(central.Config{Name: "ccu-nil"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	WireInstallModeDPs(c, nil)
}
