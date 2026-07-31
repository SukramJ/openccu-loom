// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// rebootOps is a backends.Operations that also implements the ccuRebooter
// capability. It records the reboot call and returns a configurable error.
type rebootOps struct {
	fakeOperations

	rebootCalls int
	rebootErr   error
}

func (r *rebootOps) RebootCCU(_ context.Context) (bool, error) {
	r.rebootCalls++
	if r.rebootErr != nil {
		return false, r.rebootErr
	}
	return true, nil
}

// buildCCUMaintenanceFixture wires a central named centralName with the given
// backend registered as its primary interface, and returns the domain.
func buildCCUMaintenanceFixture(t *testing.T, centralName string, ops backends.Operations) *CCUMaintenanceDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	w := clientpkg.NewValueWriter()
	w.Register(centralName, "HmIP-RF", ops)
	ic := newTestInterfaceClient(t, centralName, "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return NewCCUMaintenanceDomain(reg, w)
}

func TestCCUMaintenanceRebootCCUSuccess(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("RebootCCU: %v", err)
	}
	if ops.rebootCalls != 1 {
		t.Fatalf("expected 1 reboot call, got %d", ops.rebootCalls)
	}
}

func TestCCUMaintenanceRebootCCUUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.RebootCCU(context.Background(), "does-not-exist")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.rebootCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceRebootCCUUnsupportedBackend(t *testing.T) {
	t.Parallel()
	// A plain fakeOperations does not implement ccuRebooter (no RebootCCU).
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.RebootCCU(context.Background(), "ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenanceRebootCCUPropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &rebootOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		rebootErr:      errors.New("ccu unreachable"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); err == nil {
		t.Fatal("expected the backend reboot error to propagate")
	}
}

func TestCCUMaintenanceRebootCCUNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.RebootCCU(context.Background(), "ccu-01"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

// positionOps is a backends.Operations that also implements the
// ccuPositionSetter capability. It records the position it was asked to
// write and returns a configurable error.
type positionOps struct {
	fakeOperations

	setCalls int
	lastLon  float64
	lastLat  float64
	setErr   error
}

func (p *positionOps) SetCCUPosition(_ context.Context, longitude, latitude float64) error {
	p.setCalls++
	p.lastLon, p.lastLat = longitude, latitude
	return p.setErr
}

func TestCCUMaintenanceSetCCUPositionSuccessPatchesSystemInfo(t *testing.T) {
	t.Parallel()
	ops := &positionOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// An unrelated field must survive the patch untouched — PatchSystemPosition
	// only touches Longitude/Latitude.
	c.SetSystemInformation(central.SystemInfo{Hostname: "ccu-01.local"})
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", ops)
	ic := newTestInterfaceClient(t, "ccu-01", "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dom := NewCCUMaintenanceDomain(reg, w)

	if err := dom.SetCCUPosition(context.Background(), "ccu-01", 10.222946, 53.551086); err != nil {
		t.Fatalf("SetCCUPosition: %v", err)
	}
	if ops.setCalls != 1 || ops.lastLon != 10.222946 || ops.lastLat != 53.551086 {
		t.Fatalf("backend not called with the expected position: calls=%d lon=%g lat=%g",
			ops.setCalls, ops.lastLon, ops.lastLat)
	}

	info := c.SystemInformation()
	if info.Longitude != 10.222946 || info.Latitude != 53.551086 {
		t.Errorf("cached SystemInfo not patched: got %g/%g", info.Longitude, info.Latitude)
	}
	if info.Hostname != "ccu-01.local" {
		t.Errorf("unrelated field Hostname changed by the patch: got %q", info.Hostname)
	}
}

func TestCCUMaintenanceSetCCUPositionUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &positionOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.SetCCUPosition(context.Background(), "does-not-exist", 10, 50)
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.setCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceSetCCUPositionUnsupportedBackend(t *testing.T) {
	t.Parallel()
	// A plain fakeOperations does not implement ccuPositionSetter.
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.SetCCUPosition(context.Background(), "ccu-01", 10, 50)
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenanceSetCCUPositionPropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &positionOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		setErr:         hmerr.ErrValidation,
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.SetCCUPosition(context.Background(), "ccu-01", 200, 50)
	if !errors.Is(err, hmerr.ErrValidation) {
		t.Fatalf("want the backend's validation error to propagate, got %v", err)
	}
}

func TestCCUMaintenanceSetCCUPositionNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.SetCCUPosition(context.Background(), "ccu-01", 10, 50); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

// hostControlOps is a backends.Operations that also implements the
// ccuHostController capability. It records each of the three power/boot
// actions in its own counter so a test can prove the shared
// ccuHostActionHandler dispatch never cross-wires the actions — a
// copy-paste error in that shared table would otherwise be silent.
type hostControlOps struct {
	fakeOperations

	poweroffCalls     int
	safeModeCalls     int
	recoveryModeCalls int

	poweroffErr     error
	safeModeErr     error
	recoveryModeErr error
}

func (h *hostControlOps) PoweroffCCU(_ context.Context) (bool, error) {
	h.poweroffCalls++
	if h.poweroffErr != nil {
		return false, h.poweroffErr
	}
	return true, nil
}

func (h *hostControlOps) EnterSafeMode(_ context.Context) error {
	h.safeModeCalls++
	return h.safeModeErr
}

func (h *hostControlOps) EnterRecoveryMode(_ context.Context) error {
	h.recoveryModeCalls++
	return h.recoveryModeErr
}

func TestCCUMaintenancePoweroffCCUSuccess(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.PoweroffCCU(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("PoweroffCCU: %v", err)
	}
	if ops.poweroffCalls != 1 {
		t.Fatalf("expected 1 poweroff call, got %d", ops.poweroffCalls)
	}
}

func TestCCUMaintenancePoweroffCCUUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.PoweroffCCU(context.Background(), "does-not-exist")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.poweroffCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenancePoweroffCCUUnsupportedBackend(t *testing.T) {
	t.Parallel()
	// A plain fakeOperations does not implement ccuHostController.
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.PoweroffCCU(context.Background(), "ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenancePoweroffCCUPropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		poweroffErr:    errors.New("ccu unreachable"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.PoweroffCCU(context.Background(), "ccu-01"); err == nil {
		t.Fatal("expected the backend poweroff error to propagate")
	}
}

func TestCCUMaintenancePoweroffCCUNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.PoweroffCCU(context.Background(), "ccu-01"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

func TestCCUMaintenanceEnterSafeModeSuccess(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.EnterSafeMode(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("EnterSafeMode: %v", err)
	}
	if ops.safeModeCalls != 1 {
		t.Fatalf("expected 1 safe-mode call, got %d", ops.safeModeCalls)
	}
}

func TestCCUMaintenanceEnterSafeModeUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.EnterSafeMode(context.Background(), "does-not-exist")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.safeModeCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceEnterSafeModeUnsupportedBackend(t *testing.T) {
	t.Parallel()
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.EnterSafeMode(context.Background(), "ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenanceEnterSafeModePropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		safeModeErr:    errors.New("ccu rejected safe mode"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.EnterSafeMode(context.Background(), "ccu-01"); err == nil {
		t.Fatal("expected the backend safe-mode error to propagate")
	}
}

func TestCCUMaintenanceEnterSafeModeNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.EnterSafeMode(context.Background(), "ccu-01"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

func TestCCUMaintenanceEnterRecoveryModeSuccess(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.EnterRecoveryMode(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("EnterRecoveryMode: %v", err)
	}
	if ops.recoveryModeCalls != 1 {
		t.Fatalf("expected 1 recovery-mode call, got %d", ops.recoveryModeCalls)
	}
}

func TestCCUMaintenanceEnterRecoveryModeUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.EnterRecoveryMode(context.Background(), "does-not-exist")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.recoveryModeCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceEnterRecoveryModeUnsupportedBackend(t *testing.T) {
	t.Parallel()
	ops := &fakeOperations{kind: backends.KindCUxD}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.EnterRecoveryMode(context.Background(), "ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("want backends.ErrUnsupported, got %v", err)
	}
}

func TestCCUMaintenanceEnterRecoveryModePropagatesBackendError(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{
		fakeOperations:  fakeOperations{kind: backends.KindCCU},
		recoveryModeErr: errors.New("ccu unreachable"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.EnterRecoveryMode(context.Background(), "ccu-01"); err == nil {
		t.Fatal("expected the backend recovery-mode error to propagate")
	}
}

func TestCCUMaintenanceEnterRecoveryModeNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.EnterRecoveryMode(context.Background(), "ccu-01"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

// TestCCUMaintenanceHostActionsDoNotCrossWire drives all three host
// actions against one backend in sequence and checks after each call that
// only its own counter advanced. hostControllerFor resolves the same
// backend for all three actions, so a mix-up in the caller-side dispatch
// (e.g. PostCCUSafeMode wired to ccuActionPoweroff) would be invisible if
// each action were only ever tested in isolation.
func TestCCUMaintenanceHostActionsDoNotCrossWire(t *testing.T) {
	t.Parallel()
	ops := &hostControlOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)

	if err := dom.PoweroffCCU(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("PoweroffCCU: %v", err)
	}
	if ops.poweroffCalls != 1 || ops.safeModeCalls != 0 || ops.recoveryModeCalls != 0 {
		t.Fatalf("after PoweroffCCU: poweroff=%d safeMode=%d recoveryMode=%d, want 1/0/0",
			ops.poweroffCalls, ops.safeModeCalls, ops.recoveryModeCalls)
	}

	if err := dom.EnterSafeMode(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("EnterSafeMode: %v", err)
	}
	if ops.poweroffCalls != 1 || ops.safeModeCalls != 1 || ops.recoveryModeCalls != 0 {
		t.Fatalf("after EnterSafeMode: poweroff=%d safeMode=%d recoveryMode=%d, want 1/1/0",
			ops.poweroffCalls, ops.safeModeCalls, ops.recoveryModeCalls)
	}

	if err := dom.EnterRecoveryMode(context.Background(), "ccu-01"); err != nil {
		t.Fatalf("EnterRecoveryMode: %v", err)
	}
	if ops.poweroffCalls != 1 || ops.safeModeCalls != 1 || ops.recoveryModeCalls != 1 {
		t.Fatalf("after EnterRecoveryMode: poweroff=%d safeMode=%d recoveryMode=%d, want 1/1/1",
			ops.poweroffCalls, ops.safeModeCalls, ops.recoveryModeCalls)
	}
}

// downloadOps records the firmware-download URL passed to the primary
// backend and returns a configurable error.
type downloadOps struct {
	fakeOperations

	downloadCalls int
	lastURL       string
	downloadErr   error
}

func (d *downloadOps) DownloadFirmware(_ context.Context, url string) error {
	d.downloadCalls++
	d.lastURL = url
	return d.downloadErr
}

func TestCCUMaintenanceDownloadFirmwareSuccess(t *testing.T) {
	t.Parallel()
	ops := &downloadOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.DownloadFirmware(context.Background(), "ccu-01", "https://x/fw.tgz"); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
	if ops.downloadCalls != 1 || ops.lastURL != "https://x/fw.tgz" {
		t.Fatalf("expected one download of the url, got calls=%d url=%q", ops.downloadCalls, ops.lastURL)
	}
}

func TestCCUMaintenanceDownloadFirmwareSingleCentralDefault(t *testing.T) {
	t.Parallel()
	ops := &downloadOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	// Empty central resolves to the sole registered central.
	if err := dom.DownloadFirmware(context.Background(), "", "https://x/fw.tgz"); err != nil {
		t.Fatalf("DownloadFirmware: %v", err)
	}
	if ops.downloadCalls != 1 {
		t.Fatalf("expected the sole central to be used, got calls=%d", ops.downloadCalls)
	}
}

func TestCCUMaintenanceDownloadFirmwareUnknownCentral(t *testing.T) {
	t.Parallel()
	ops := &downloadOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	err := dom.DownloadFirmware(context.Background(), "nope", "https://x/fw.tgz")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
	if ops.downloadCalls != 0 {
		t.Fatalf("backend must not be called for an unknown central")
	}
}

func TestCCUMaintenanceDownloadFirmwarePropagatesError(t *testing.T) {
	t.Parallel()
	ops := &downloadOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		downloadErr:    errors.New("ccu unreachable"),
	}
	dom := buildCCUMaintenanceFixture(t, "ccu-01", ops)
	if err := dom.DownloadFirmware(context.Background(), "ccu-01", "https://x/fw.tgz"); err == nil {
		t.Fatal("expected the backend download error to propagate")
	}
}

func TestCCUMaintenanceDownloadFirmwareNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewCCUMaintenanceDomain(nil, nil)
	if err := dom.DownloadFirmware(context.Background(), "ccu-01", "https://x/fw.tgz"); !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral, got %v", err)
	}
}

// TestCCUMaintenanceDownloadFirmwareAmbiguousWithoutCentralName pins the
// resolveCentral rule that the empty-central convenience only applies to a
// single-CCU deployment: with two centrals registered, an empty name must
// not silently pick one — it is ambiguous and must fail closed.
func TestCCUMaintenanceDownloadFirmwareAmbiguousWithoutCentralName(t *testing.T) {
	t.Parallel()
	w := clientpkg.NewValueWriter()
	reg := central.NewRegistry()
	opsByCentral := map[string]*downloadOps{}
	for _, name := range []string{"ccu-01", "ccu-02"} {
		c, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%s): %v", name, err)
		}
		ops := &downloadOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
		opsByCentral[name] = ops
		w.Register(name, "HmIP-RF", ops)
		ic := newTestInterfaceClient(t, name, "HmIP-RF", 5)
		if err := c.Clients.Register(&coordinators.ClientEntry{
			InterfaceID: "HmIP-RF",
			Interface:   hmenum.InterfaceHmIPRF,
			Client:      ic,
		}); err != nil {
			t.Fatalf("Clients.Register(%s): %v", name, err)
		}
		if err := reg.Register(c); err != nil {
			t.Fatalf("reg.Register(%s): %v", name, err)
		}
	}
	dom := NewCCUMaintenanceDomain(reg, w)

	err := dom.DownloadFirmware(context.Background(), "", "https://x/fw.tgz")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Fatalf("want hmerr.ErrUnknownCentral for an ambiguous default, got %v", err)
	}
	for name, ops := range opsByCentral {
		if ops.downloadCalls != 0 {
			t.Fatalf("backend for %s must not be called on an ambiguous default", name)
		}
	}
}
