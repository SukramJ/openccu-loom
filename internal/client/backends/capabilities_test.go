// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Capability profile tests: CapabilityFor, UpdateCapabilitiesForVersion,
// Kind.String(), and the capability matrix for each backend kind.

package backends

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// CCU capability profile
// ---------------------------------------------------------------------------

func TestCCUCapabilityFlags(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"Backup", caps.Backup, true},
		{"GetAllPrograms", caps.GetAllPrograms, true},
		{"Rooms", caps.Rooms, true},
		{"Functions", caps.Functions, true},
		{"PingPong", caps.PingPong, true},
		{"RPCCallback", caps.RPCCallback, true},
		{"InstallMode", caps.InstallMode, true},
		{"InboxDevices", caps.InboxDevices, true},
		{"ServiceMessages", caps.ServiceMessages, true},
		{"AlarmMessages", caps.AlarmMessages, true},
		{"LinkOperations", caps.LinkOperations, true},
		{"Rename", caps.Rename, true},
		{"IseIDLookup", caps.IseIDLookup, true},
		{"ExecuteProgram", caps.ExecuteProgram, true},
		{"FirmwareUpdate", caps.FirmwareUpdate, true},
		{"DeleteSystemVariable", caps.DeleteSystemVariable, true},
		{"CreateSystemVariable", caps.CreateSystemVariable, true},
		{"CommunicationTest", caps.CommunicationTest, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("CCU Capabilities.%s = %v; want %v", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestCapabilityFor_CCU_HasMetadataTrue verifies that KindCCU advertises
// Metadata capability so the object-metadata path is available.
func TestCapabilityFor_CCU_HasMetadataTrue(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	if !caps.Metadata {
		t.Error("KindCCU: Metadata should be true")
	}
}

// TestCapabilityFor_InstallModeLocal verifies that the keyserver-less HmIP
// LOCAL teach-in capability is advertised only for KindCCU — CUxD and
// Homegear have no HmIP JSON-RPC surface to serve it.
func TestCapabilityFor_InstallModeLocal(t *testing.T) {
	t.Parallel()
	if !CapabilityFor(KindCCU).InstallModeLocal {
		t.Error("KindCCU: InstallModeLocal should be true")
	}
	if CapabilityFor(KindCUxD).InstallModeLocal {
		t.Error("KindCUxD: InstallModeLocal should be false")
	}
	if CapabilityFor(KindHomegear).InstallModeLocal {
		t.Error("KindHomegear: InstallModeLocal should be false")
	}
}

// TestCapabilityFor_ReplaceDevice verifies that the guided device-replace
// capability is advertised only for KindCCU — CUxD and Homegear have no
// listReplaceableDevices/replaceDevice wire method.
func TestCapabilityFor_ReplaceDevice(t *testing.T) {
	t.Parallel()
	if !CapabilityFor(KindCCU).ReplaceDevice {
		t.Error("KindCCU: ReplaceDevice should be true")
	}
	if CapabilityFor(KindCUxD).ReplaceDevice {
		t.Error("KindCUxD: ReplaceDevice should be false")
	}
	if CapabilityFor(KindHomegear).ReplaceDevice {
		t.Error("KindHomegear: ReplaceDevice should be false")
	}
}

// TestCapabilityFor_SearchDevices verifies that the wired-bus scan
// capability is advertised only for KindCCU — CUxD and Homegear have no
// searchDevices wire method (and the interface-level gate on top of it
// restricts KindCCU further, to BidCos-Wired only).
func TestCapabilityFor_SearchDevices(t *testing.T) {
	t.Parallel()
	if !CapabilityFor(KindCCU).SearchDevices {
		t.Error("KindCCU: SearchDevices should be true")
	}
	if CapabilityFor(KindCUxD).SearchDevices {
		t.Error("KindCUxD: SearchDevices should be false")
	}
	if CapabilityFor(KindHomegear).SearchDevices {
		t.Error("KindHomegear: SearchDevices should be false")
	}
}

// TestCapabilityFor_CommunicationTest verifies that the per-device
// communication/function test capability is advertised only for KindCCU —
// CUxD and Homegear have no ReGa runner to drive the start/poll scripts.
func TestCapabilityFor_CommunicationTest(t *testing.T) {
	t.Parallel()
	if !CapabilityFor(KindCCU).CommunicationTest {
		t.Error("KindCCU: CommunicationTest should be true")
	}
	if CapabilityFor(KindCUxD).CommunicationTest {
		t.Error("KindCUxD: CommunicationTest should be false")
	}
	if CapabilityFor(KindHomegear).CommunicationTest {
		t.Error("KindHomegear: CommunicationTest should be false")
	}
}

func TestCapabilityFor_TeamAssignment(t *testing.T) {
	t.Parallel()
	if !CapabilityFor(KindCCU).TeamAssignment {
		t.Error("KindCCU: TeamAssignment should be true")
	}
	if CapabilityFor(KindCUxD).TeamAssignment {
		t.Error("KindCUxD: TeamAssignment should be false")
	}
	if CapabilityFor(KindHomegear).TeamAssignment {
		t.Error("KindHomegear: TeamAssignment should be false")
	}
}

// ---------------------------------------------------------------------------
// Homegear capability profile
// ---------------------------------------------------------------------------

func TestHomegearCapabilityFlags(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindHomegear)

	// Capabilities that Homegear does NOT have.
	absent := []struct {
		name string
		got  bool
	}{
		{"Backup", caps.Backup},
		{"GetAllPrograms", caps.GetAllPrograms},
		{"Rooms", caps.Rooms},
		{"Functions", caps.Functions},
		{"InstallMode", caps.InstallMode},
		{"InboxDevices", caps.InboxDevices},
		{"ServiceMessages", caps.ServiceMessages},
		{"AlarmMessages", caps.AlarmMessages},
		{"IseIDLookup", caps.IseIDLookup},
		{"ExecuteProgram", caps.ExecuteProgram},
		{"FirmwareUpdate", caps.FirmwareUpdate},
		{"HasSystemUpdate", caps.HasSystemUpdate},
		{"PingPong", caps.PingPong},
		{"Metadata", caps.Metadata},
	}
	for _, tc := range absent {
		t.Run("absent_"+tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got {
				t.Errorf("Homegear Capabilities.%s = true; want false", tc.name)
			}
		})
	}

	// Capabilities that Homegear DOES have.
	present := []struct {
		name string
		got  bool
	}{
		{"RPCCallback", caps.RPCCallback},
		{"LinkOperations", caps.LinkOperations},
	}
	for _, tc := range present {
		t.Run("present_"+tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.got {
				t.Errorf("Homegear Capabilities.%s = false; want true", tc.name)
			}
		})
	}
}

// TestCapabilityFor_Homegear_MetadataFalseAndPingPongFalse verifies the
// two flags that differ from the CCU backend.
func TestCapabilityFor_Homegear_MetadataFalseAndPingPongFalse(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindHomegear)
	if caps.Metadata {
		t.Error("KindHomegear: Metadata should be false")
	}
	if caps.PingPong {
		t.Error("KindHomegear: PingPong should be false")
	}
}

// ---------------------------------------------------------------------------
// CUxD capability profile
// ---------------------------------------------------------------------------

func TestCUxDNoProgramsNoRooms(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCUxD)

	if caps.GetAllPrograms {
		t.Error("CUxD Capabilities.GetAllPrograms = true; want false")
	}
	if caps.Rooms {
		t.Error("CUxD Capabilities.Rooms = true; want false")
	}
	if caps.Backup {
		t.Error("CUxD Capabilities.Backup = true; want false")
	}
	if caps.InstallMode {
		t.Error("CUxD Capabilities.InstallMode = true; want false")
	}
	if !caps.PingPong {
		t.Error("CUxD Capabilities.PingPong = false; want true")
	}
	if !caps.RPCCallback {
		t.Error("CUxD Capabilities.RPCCallback = false; want true")
	}
}

// ---------------------------------------------------------------------------
// CapabilityFor round-trip — each known Kind produces a distinct profile
// ---------------------------------------------------------------------------

func TestCapabilityForDistinctProfiles(t *testing.T) {
	t.Parallel()

	ccu := CapabilityFor(KindCCU)
	hg := CapabilityFor(KindHomegear)
	cuxd := CapabilityFor(KindCUxD)

	// CCU has Backup; Homegear does not.
	if ccu.Backup == hg.Backup {
		t.Error("CCU and Homegear should differ on Backup capability")
	}
	// CUxD has no Backup; CCU does.
	if cuxd.Backup {
		t.Error("CUxD should not have Backup")
	}
}

// ---------------------------------------------------------------------------
// Kind.String() covers every defined Kind value
// ---------------------------------------------------------------------------

func TestKindStringNonEmpty(t *testing.T) {
	t.Parallel()
	kinds := []Kind{KindCCU, KindCUxD, KindHomegear}
	for _, k := range kinds {
		t.Run(k.String(), func(t *testing.T) {
			t.Parallel()
			if k.String() == "" || k.String() == "unknown" {
				t.Errorf("Kind(%d).String() = %q; want non-empty non-unknown", k, k.String())
			}
		})
	}
}

func TestKindStringCoversAllKnownKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind Kind
		want string
	}{
		{KindCCU, "ccu"},
		{KindCUxD, "cuxd"},
		{KindHomegear, "homegear"},
		{Kind(99), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := tc.kind.String()
			if got != tc.want {
				t.Fatalf("Kind(%d).String()=%q, want %q", int(tc.kind), got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpdateCapabilitiesForVersion adjusts HasSystemUpdate
// ---------------------------------------------------------------------------

func TestVersionBoundaryTable(t *testing.T) {
	t.Parallel()
	ccu := CapabilityFor(KindCCU)

	tests := []struct {
		version string
		wantSU  bool
	}{
		{"3.49.10.20210601", true},
		{"3.50.0.0", true},
		{"3.48.99.0", false},
		{"3.0.0.0", false},
		{"2.99.0.0", false},
		{"", true}, // empty string: no adjustment
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			got := UpdateCapabilitiesForVersion(ccu, tc.version)
			if got.HasSystemUpdate != tc.wantSU {
				t.Errorf("version %q: HasSystemUpdate = %v; want %v",
					tc.version, got.HasSystemUpdate, tc.wantSU)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CCU capability matrix — GetAllPrograms, GetAllSysvars, RequiresPeriodicRefresh
// ---------------------------------------------------------------------------

func TestCcuCapabilitiesGetAllProgramsAndSysvars(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	if !caps.GetAllPrograms {
		t.Error("KindCCU: GetAllPrograms must be true")
	}
	if !caps.GetAllSysvars {
		t.Error("KindCCU: GetAllSysvars must be true")
	}
	if caps.RequiresPeriodicRefresh {
		t.Error("KindCCU: RequiresPeriodicRefresh must be false (CCU pushes events)")
	}
}

func TestHomegearCapabilitiesGetAllProgramsAndSysvars(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindHomegear)
	// Homegear has no JSON-RPC / ReGa surface — programs and sysvars
	// surface as ErrUnsupported (SPECIFICATION §9.2).
	if caps.GetAllPrograms {
		t.Error("KindHomegear: GetAllPrograms must be false (no ReGa/JSON-RPC)")
	}
	if caps.GetAllSysvars {
		t.Error("KindHomegear: GetAllSysvars must be false (no ReGa/JSON-RPC)")
	}
	if caps.RequiresPeriodicRefresh {
		t.Error("KindHomegear: RequiresPeriodicRefresh must be false (Homegear pushes events)")
	}
}

func TestCuxdCapabilitiesRequiresPeriodicRefresh(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCUxD)
	if caps.RequiresPeriodicRefresh {
		t.Error("KindCUxD: RequiresPeriodicRefresh must be false (CUxD pushes via BIN-RPC callback)")
	}
}

// ---------------------------------------------------------------------------
// CapabilityFor consistency — advertised capability must match implementation
// ---------------------------------------------------------------------------

func TestCapabilityFirmwareUpdateConsistency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// CCU with JSON-RPC wired → FirmwareUpdate cap=true, method succeeds.
	ccuCaps := CapabilityFor(KindCCU)
	if !ccuCaps.FirmwareUpdate {
		t.Error("KindCCU: advertises FirmwareUpdate=false but spec says true")
	}
	ccuB := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil)
	if err := ccuB.UpdateFirmware(ctx, "0001"); err != nil {
		t.Errorf("CCU UpdateFirmware with JSON wired: %v", err)
	}

	// CUxD → cap=false, method always returns ErrUnsupported.
	cuxdCaps := CapabilityFor(KindCUxD)
	if cuxdCaps.FirmwareUpdate {
		t.Error("KindCUxD: advertises FirmwareUpdate=true but implementation refuses")
	}
	cuxdB := NewCuxdBackend(&fakeCaller{}, nil)
	if err := cuxdB.UpdateFirmware(ctx, "CUX0001"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("CUxD UpdateFirmware: want ErrUnsupported, got %v", err)
	}

	// Homegear → cap=false, method always returns ErrUnsupported.
	hgCaps := CapabilityFor(KindHomegear)
	if hgCaps.FirmwareUpdate {
		t.Error("KindHomegear: advertises FirmwareUpdate=true but implementation refuses")
	}
	hgB := NewHomegearBackend(&fakeCaller{}, nil)
	if err := hgB.UpdateFirmware(ctx, "HG0001"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Homegear UpdateFirmware: want ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Version-based capability adjustment (UpdateCapabilitiesForVersion)
// ---------------------------------------------------------------------------

// TestVersionOldFirmwareDisablesSystemUpdate verifies that firmware versions
// before 3.49 have HasSystemUpdate == false.
func TestVersionOldFirmwareDisablesSystemUpdate(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	if !caps.HasSystemUpdate {
		t.Fatal("pre-condition: KindCCU base caps must have HasSystemUpdate=true")
	}
	oldCaps := UpdateCapabilitiesForVersion(caps, "3.47.10.20190101")
	if oldCaps.HasSystemUpdate {
		t.Error("firmware 3.47: HasSystemUpdate must be false (requires >= 3.49)")
	}
}

// TestVersionNewFirmwareKeepsSystemUpdate verifies that firmware >= 3.49
// retains HasSystemUpdate == true.
func TestVersionNewFirmwareKeepsSystemUpdate(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	updatedCaps := UpdateCapabilitiesForVersion(caps, "3.55.10.20210601")
	if !updatedCaps.HasSystemUpdate {
		t.Error("firmware 3.55: HasSystemUpdate must remain true (>= 3.49)")
	}
}

// TestVersionExactBoundaryInclusive verifies that firmware exactly 3.49
// retains HasSystemUpdate == true (boundary is inclusive: >= 3.49).
func TestVersionExactBoundaryInclusive(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	updatedCaps := UpdateCapabilitiesForVersion(caps, "3.49.0.20200101")
	if !updatedCaps.HasSystemUpdate {
		t.Error("firmware 3.49.0: HasSystemUpdate must be true (boundary inclusive)")
	}
}

// TestVersionEmptyStringNoAdjustment verifies that an empty version string
// leaves capabilities unchanged.
func TestVersionEmptyStringNoAdjustment(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)
	adjusted := UpdateCapabilitiesForVersion(caps, "")
	if adjusted != caps {
		t.Error("empty version string must not adjust any capability")
	}
}

// ---------------------------------------------------------------------------
// Full capability matrix verification per backend kind
// ---------------------------------------------------------------------------

// TestCCUCapabilityMatrix verifies the full CCU capability matrix.
func TestCCUCapabilityMatrix(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCCU)

	must := map[string]bool{
		"RPCCallback":            caps.RPCCallback,
		"PingPong":               caps.PingPong,
		"ListDevices":            caps.ListDevices,
		"GetAllPrograms":         caps.GetAllPrograms,
		"GetAllSysvars":          caps.GetAllSysvars,
		"FirmwareUpdate":         caps.FirmwareUpdate,
		"ConfigRestore":          caps.ConfigRestore,
		"CommunicationTest":      caps.CommunicationTest,
		"AlarmMessages":          caps.AlarmMessages,
		"Backup":                 caps.Backup,
		"CreateSystemVariable":   caps.CreateSystemVariable,
		"DeleteDevice":           caps.DeleteDevice,
		"DeleteSystemVariable":   caps.DeleteSystemVariable,
		"ExecuteProgram":         caps.ExecuteProgram,
		"HasSystemUpdate":        caps.HasSystemUpdate,
		"InboxDevices":           caps.InboxDevices,
		"InstallMode":            caps.InstallMode,
		"LinkOperations":         caps.LinkOperations,
		"ServiceMessages":        caps.ServiceMessages,
		"SetProgramState":        caps.SetProgramState,
		"SetSystemVariable":      caps.SetSystemVariable,
		"SuppressServiceMessage": caps.SuppressServiceMessage,
		"ValueListRead":          caps.ValueListRead,
		"VirtualKey":             caps.VirtualKey,
		"Functions":              caps.Functions,
		"Rooms":                  caps.Rooms,
		"Rename":                 caps.Rename,
		"IseIDLookup":            caps.IseIDLookup,
	}
	for name, v := range must {
		if !v {
			t.Errorf("KindCCU capability %s must be true", name)
		}
	}
	if caps.RequiresPeriodicRefresh {
		t.Error("KindCCU: RequiresPeriodicRefresh must be false")
	}
}

// TestHomegearCapabilityMatrix verifies the full Homegear capability matrix.
func TestHomegearCapabilityMatrix(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindHomegear)

	must := map[string]bool{
		"RPCCallback":    caps.RPCCallback,
		"ListDevices":    caps.ListDevices,
		"SetSystemVar":   caps.SetSystemVariable,
		"DeleteSysVar":   caps.DeleteSystemVariable,
		"ValueListRead":  caps.ValueListRead,
		"LinkOperations": caps.LinkOperations,
		"DeleteDevice":   caps.DeleteDevice,
	}
	mustNot := map[string]bool{
		"GetAllPrograms":  caps.GetAllPrograms,
		"GetAllSysvars":   caps.GetAllSysvars,
		"FirmwareUpdate":  caps.FirmwareUpdate,
		"ConfigRestore":   caps.ConfigRestore,
		"Backup":          caps.Backup,
		"InstallMode":     caps.InstallMode,
		"HasSystemUpdate": caps.HasSystemUpdate,
		"PingPong":        caps.PingPong,
		"Metadata":        caps.Metadata,
	}
	for name, v := range must {
		if !v {
			t.Errorf("Homegear: %s must be true", name)
		}
	}
	for name, v := range mustNot {
		if v {
			t.Errorf("Homegear: %s must be false", name)
		}
	}
}

// TestCUxDCapabilityMatrix verifies the CUxD-specific capability set.
func TestCUxDCapabilityMatrix(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindCUxD)

	if !caps.RPCCallback {
		t.Error("KindCUxD: RPCCallback must be true")
	}
	if !caps.PingPong {
		t.Error("KindCUxD: PingPong must be true")
	}
	if !caps.ListDevices {
		t.Error("KindCUxD: ListDevices must be true")
	}
	if !caps.LinkOperations {
		t.Error("KindCUxD: LinkOperations must be true")
	}

	absent := map[string]bool{
		"GetAllPrograms":  caps.GetAllPrograms,
		"GetAllSysvars":   caps.GetAllSysvars,
		"FirmwareUpdate":  caps.FirmwareUpdate,
		"ConfigRestore":   caps.ConfigRestore,
		"Backup":          caps.Backup,
		"AlarmMessages":   caps.AlarmMessages,
		"InstallMode":     caps.InstallMode,
		"HasSystemUpdate": caps.HasSystemUpdate,
	}
	for name, v := range absent {
		if v {
			t.Errorf("KindCUxD: %s must be false", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Every backend Kind string is non-empty
// ---------------------------------------------------------------------------

func TestEveryMVPBackendKindStringIsNonEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		b    Operations
	}{
		{"CCU", NewCcuBackend(&fakeCaller{}, nil, nil)},
		{"CUxD", NewCuxdBackend(&fakeCaller{}, nil)},
		{"Homegear", NewHomegearBackend(&fakeCaller{}, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tc.b.Kind().String()
			if s == "" || s == "unknown" {
				t.Fatalf("%s: Kind().String()=%q, want non-empty recognisable name", tc.name, s)
			}
		})
	}
}
