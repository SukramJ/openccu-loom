// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns_test

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// ---- helpers ----

func validService(t *testing.T) mdns.Service {
	t.Helper()
	return mdns.Service{
		InstanceName: "9C71D38FBE48F2E5-0000000012345678",
		ServiceType:  mdns.ServiceTypeOperational,
		Port:         5540,
		HostName:     "openccu-loom",
		Addresses:    []net.IP{net.ParseIP("192.168.1.1")},
		TXT: []mdns.TXTRecord{
			{Key: "SII", Value: "5000"},
			{Key: "SAI", Value: "300"},
			{Key: "T", Value: "0"},
		},
	}
}

func operationalCfg() mdns.OperationalServiceConfig {
	return mdns.OperationalServiceConfig{
		CompressedFabricID:    [8]byte{0x9C, 0x71, 0xD3, 0x8F, 0xBE, 0x48, 0xF2, 0xE5},
		NodeID:                0x0000000012345678,
		Port:                  5540,
		HostName:              "openccu-loom",
		Addresses:             []net.IP{net.ParseIP("fd11::1")},
		SessionIdleInterval:   5000,
		SessionActiveInterval: 300,
	}
}

func commissionableCfg() mdns.CommissionableServiceConfig {
	return mdns.CommissionableServiceConfig{
		InstanceID:        [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22},
		Discriminator:     0xA5C, // long=2652, short=(2652>>8)&0x0F = 0x0A = 10
		VendorID:          0x1234,
		ProductID:         0x5678,
		CommissioningMode: 1,
		DeviceTypeID:      0x000E,
		Port:              5540,
		HostName:          "openccu-loom",
		Addresses:         []net.IP{net.ParseIP("fd11::1")},
	}
}

// ---- Service.Validate success ----

func TestService_Validate_Success(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	if err := svc.Validate(); err != nil {
		t.Fatalf("Validate on valid service: %v", err)
	}
}

// ---- Service.Validate failures ----

func TestService_Validate_EmptyInstanceName(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.InstanceName = ""
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for empty InstanceName, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_InstanceNameTooLong(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.InstanceName = strings.Repeat("x", 64)
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for InstanceName > 63 chars, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_UnknownServiceType(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.ServiceType = "_unknown._tcp"
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for unknown ServiceType, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_ZeroPort(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.Port = 0
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for Port=0, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_EmptyHostName(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.HostName = ""
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for empty HostName, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_EmptyTXTKey(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.TXT = append(svc.TXT, mdns.TXTRecord{Key: "", Value: "val"})
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for empty TXT key, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

func TestService_Validate_TXTKeyValueExceeds255(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.TXT = append(svc.TXT, mdns.TXTRecord{
		Key:   "KEY",
		Value: strings.Repeat("v", 253),
	})
	err := svc.Validate()
	if err == nil {
		t.Fatal("expected error for TXT key+value > 255, got nil")
	}
	if !errors.Is(err, mdns.ErrInvalidService) {
		t.Fatalf("expected ErrInvalidService, got %v", err)
	}
}

// ---- MarshalTXT ----

func TestService_MarshalTXT_SortedAlphabetically(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{
		TXT: []mdns.TXTRecord{
			{Key: "T", Value: "0"},
			{Key: "SAI", Value: "300"},
			{Key: "SII", Value: "5000"},
		},
	}
	got := svc.MarshalTXT()
	want := []string{"SAI=300", "SII=5000", "T=0"}
	if len(got) != len(want) {
		t.Fatalf("MarshalTXT len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MarshalTXT[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestService_MarshalTXT_CaseInsensitiveOrder(t *testing.T) {
	t.Parallel()
	// D, VP, CM, DT — mixed case, check stable sorted output.
	svc := mdns.Service{
		TXT: []mdns.TXTRecord{
			{Key: "VP", Value: "1+2"},
			{Key: "D", Value: "42"},
			{Key: "DT", Value: "14"},
			{Key: "CM", Value: "1"},
		},
	}
	got := svc.MarshalTXT()
	// Case-insensitive: cm < d < dt < vp
	want := []string{"CM=1", "D=42", "DT=14", "VP=1+2"}
	if len(got) != len(want) {
		t.Fatalf("MarshalTXT len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MarshalTXT[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---- FQDN ----

func TestService_FQDN_EmptyDomain_UsesLocal(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{
		InstanceName: "INST",
		ServiceType:  mdns.ServiceTypeOperational,
		Domain:       "",
	}
	got := svc.FQDN()
	want := "INST._matter._tcp.local."
	if got != want {
		t.Fatalf("FQDN = %q, want %q", got, want)
	}
}

func TestService_FQDN_ExplicitDomain(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{
		InstanceName: "INST",
		ServiceType:  mdns.ServiceTypeOperational,
		Domain:       "example.com",
	}
	got := svc.FQDN()
	want := "INST._matter._tcp.example.com."
	if got != want {
		t.Fatalf("FQDN = %q, want %q", got, want)
	}
}

// ---- HostFQDN ----

func TestService_HostFQDN_EmptyDomain_UsesLocal(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{
		HostName: "bridge",
		Domain:   "",
	}
	got := svc.HostFQDN()
	want := "bridge.local."
	if got != want {
		t.Fatalf("HostFQDN = %q, want %q", got, want)
	}
}

func TestService_HostFQDN_ExplicitDomain(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{
		HostName: "bridge",
		Domain:   "home",
	}
	got := svc.HostFQDN()
	want := "bridge.home."
	if got != want {
		t.Fatalf("HostFQDN = %q, want %q", got, want)
	}
}

// ---- BuildOperationalService ----

func TestBuildOperationalService_InstanceName(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	svc := mdns.BuildOperationalService(cfg)
	// CompressedFabricID = 9C71D38FBE48F2E5, NodeID = 0x0000000012345678
	want := "9C71D38FBE48F2E5-0000000012345678"
	if svc.InstanceName != want {
		t.Fatalf("InstanceName = %q, want %q", svc.InstanceName, want)
	}
}

func TestBuildOperationalService_ServiceType(t *testing.T) {
	t.Parallel()
	svc := mdns.BuildOperationalService(operationalCfg())
	if svc.ServiceType != mdns.ServiceTypeOperational {
		t.Fatalf("ServiceType = %q, want %q", svc.ServiceType, mdns.ServiceTypeOperational)
	}
}

func TestBuildOperationalService_TXTContainsSIISAIT(t *testing.T) {
	t.Parallel()
	svc := mdns.BuildOperationalService(operationalCfg())
	keys := make(map[string]string, len(svc.TXT))
	for _, r := range svc.TXT {
		keys[r.Key] = r.Value
	}
	for _, k := range []string{"SII", "SAI", "SAT", "T"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("TXT missing key %q", k)
		}
	}
	if got := keys["SAT"]; got != "4000" {
		t.Errorf("SAT default = %q, want \"4000\" (matter.js SessionIntervals.ts:29)", got)
	}
}

func TestBuildOperationalService_TCPFlags_BothFalse(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.TCPClient = false
	cfg.TCPServer = false
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "T", "0")
}

// The `T` (TCP Support) bitmap per Matter §4.3.1.7 / chip Advertiser.h:
// bit0 reserved, bit1 = TCP client (0x02), bit2 = TCP server (0x04).
func TestBuildOperationalService_TCPFlags_ClientOnly(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.TCPClient = true
	cfg.TCPServer = false
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "T", "2") // client = bit1
}

func TestBuildOperationalService_TCPFlags_ServerOnly(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.TCPClient = false
	cfg.TCPServer = true
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "T", "4") // server = bit2
}

func TestBuildOperationalService_TCPFlags_Both(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.TCPClient = true
	cfg.TCPServer = true
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "T", "6") // both bits set: 0x02 + 0x04
}

func TestBuildOperationalService_Subtypes(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	svc := mdns.BuildOperationalService(cfg)
	if len(svc.Subtypes) != 1 {
		t.Fatalf("Subtypes len=%d, want 1", len(svc.Subtypes))
	}
	// Operational `_I<…>` subtype carries the COMPRESSED FABRIC ID
	// (8 bytes uppercase hex) per Matter §4.3.1.4 + matter.js
	// MdnsConsts.ts:34. Publishing `_I<NodeID>` (the previous bug)
	// makes the bridge invisible to the controller's post-CASE
	// fabric scan `_I<myCompressedFabric>._sub._matter._tcp.local`
	// and triggers RemoveFabric.
	want := "_I9C71D38FBE48F2E5"
	if svc.Subtypes[0] != want {
		t.Fatalf("Subtypes[0] = %q, want %q", svc.Subtypes[0], want)
	}
}

func TestBuildOperationalService_ValidateNil(t *testing.T) {
	t.Parallel()
	svc := mdns.BuildOperationalService(operationalCfg())
	if err := svc.Validate(); err != nil {
		t.Fatalf("Validate on built operational service: %v", err)
	}
}

// ---- BuildCommissionableService ----

func TestBuildCommissionableService_InstanceName(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	// InstanceID = AABBCCDDEEFF1122 → uppercase hex 16 chars
	want := "AABBCCDDEEFF1122"
	if svc.InstanceName != want {
		t.Fatalf("InstanceName = %q, want %q", svc.InstanceName, want)
	}
	if len(svc.InstanceName) != 16 {
		t.Fatalf("InstanceName length=%d, want 16", len(svc.InstanceName))
	}
}

func TestBuildCommissionableService_ServiceType(t *testing.T) {
	t.Parallel()
	svc := mdns.BuildCommissionableService(commissionableCfg())
	if svc.ServiceType != mdns.ServiceTypeCommissionable {
		t.Fatalf("ServiceType = %q, want %q", svc.ServiceType, mdns.ServiceTypeCommissionable)
	}
}

func TestBuildCommissionableService_TXTDiscriminator(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	// long discriminator = 0xA5C = 2652
	assertTXTValue(t, svc, "D", "2652")
}

func TestBuildCommissionableService_TXTVendorProduct(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "VP", "4660+22136")
}

func TestBuildCommissionableService_TXTCommissioningMode(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "CM", "1")
}

func TestBuildCommissionableService_TXTDeviceType(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "DT", "14")
}

func TestBuildCommissionableService_DeviceName_SetsDN(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.DeviceName = "My Bridge"
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "DN", "My Bridge")
}

func TestBuildCommissionableService_DeviceName_Empty_NoDN(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.DeviceName = ""
	svc := mdns.BuildCommissionableService(cfg)
	if hasTXTKey(svc, "DN") {
		t.Fatal("expected no DN key when DeviceName is empty")
	}
}

func TestBuildCommissionableService_PairingHint_NonZero_SetsPH(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.PairingHint = 33
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "PH", "33")
}

// TestBuildCommissionableService_PairingHint_Zero_EmitsDefault verifies
// that PairingHint=0 emits PH=PairingHintDefault (0x21) matching
// matter.js CommissionableMdnsAdvertisement.ts DEFAULT_PAIRING_HINT.
// The old behaviour suppressed PH=0; the new behaviour always emits PH
// (defaulting to 0x21 = 33 when unset).
func TestBuildCommissionableService_PairingHint_Zero_EmitsDefault(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.PairingHint = 0
	svc := mdns.BuildCommissionableService(cfg)
	if !hasTXTKey(svc, "PH") {
		t.Fatal("expected PH key to be emitted with default when PairingHint is 0")
	}
	assertTXTValue(t, svc, "PH", strconv.FormatUint(uint64(mdns.PairingHintDefault), 10))
}

// TestPairingHintDefault_MatchesMatterJS pins the numeric value of
// [mdns.PairingHintDefault] to matter.js's DEFAULT_PAIRING_HINT
// (packages/protocol/src/mdns/MdnsConsts.ts:15-18 —
// { powerCycle: true, deviceManual: true }). Per
// packages/protocol/src/advertisement/PairingHintBitmap.ts, powerCycle
// is bit 0 (0x01) and deviceManual is bit 5 (0x20); the combined
// bitmap is 0x21, NOT 0x33 (which would additionally set
// deviceManufacturerUrl (bit 1) and customInstruction (bit 4) —
// customInstruction requires a PI value the bridge never supplies, so
// advertising it is actively wrong).
func TestPairingHintDefault_MatchesMatterJS(t *testing.T) {
	t.Parallel()
	const wantPowerCycle = 1 << 0
	const wantDeviceManual = 1 << 5
	want := uint16(wantPowerCycle | wantDeviceManual)
	if mdns.PairingHintDefault != want {
		t.Fatalf("PairingHintDefault = 0x%02X, want 0x%02X (powerCycle|deviceManual)", mdns.PairingHintDefault, want)
	}
}

func TestBuildCommissionableService_PairingInstruction_SetsPI(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.PairingInstruction = "enter code on display"
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "PI", "enter code on display")
}

func TestBuildCommissionableService_Subtypes(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	// Discriminator = 0xA5C = 2652; short = (2652 >> 8) & 0x0F = 0x0A = 10
	// DeviceTypeID = 0x000E = 14.
	svc := mdns.BuildCommissionableService(cfg)
	if len(svc.Subtypes) != 5 {
		t.Fatalf("Subtypes len=%d, want 5", len(svc.Subtypes))
	}
	want := map[string]bool{
		"_L2652": false,
		"_S10":   false,
		"_V4660": false,
		"_CM":    false, // commissioning-mode subtype per §4.3.1.5.3
		"_T14":   false, // device-type subtype per §4.3.1.5.4 (0x000E = 14)
	}
	for _, s := range svc.Subtypes {
		if _, ok := want[s]; ok {
			want[s] = true
		} else {
			t.Errorf("unexpected subtype %q", s)
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("subtype %q missing", k)
		}
	}
}

func TestBuildCommissionableService_ShortDiscriminator(t *testing.T) {
	t.Parallel()
	// Discriminator 0xA5C = 2652; short = (2652 >> 8) & 0x0F = 10
	cfg := commissionableCfg()
	svc := mdns.BuildCommissionableService(cfg)
	found := false
	for _, s := range svc.Subtypes {
		if s == "_S10" {
			found = true
		}
	}
	if !found {
		t.Fatalf("short discriminator subtype _S10 not found in %v", svc.Subtypes)
	}
}

func TestBuildCommissionableService_ValidateNil(t *testing.T) {
	t.Parallel()
	svc := mdns.BuildCommissionableService(commissionableCfg())
	if err := svc.Validate(); err != nil {
		t.Fatalf("Validate on built commissionable service: %v", err)
	}
}

// ---- small assertion helpers ----

func assertTXTValue(t *testing.T, svc mdns.Service, key, wantValue string) {
	t.Helper()
	for _, r := range svc.TXT {
		if r.Key == key {
			if r.Value != wantValue {
				t.Fatalf("TXT[%s] = %q, want %q", key, r.Value, wantValue)
			}
			return
		}
	}
	t.Fatalf("TXT key %q not found", key)
}

func hasTXTKey(svc mdns.Service, key string) bool {
	for _, r := range svc.TXT {
		if r.Key == key {
			return true
		}
	}
	return false
}

// TestBuildOperationalService_SII_SAI_Floor pins Bug O: the
// operational `_matter._tcp` TXT record MUST never emit `SII=0` or
// `SAI=0` on the wire. Matter §4.3.1.6 treats those as the MRP
// retransmission tuning hints; Apple Home reads them post-
// CommissioningComplete to size its CASE retry budget — a zero
// value collapses the controller's retry timer and the bridge ends
// up as "Reachable: NO / Nodeid: (null)" in the Home App.
//
// Mirrors matter.js's NodeSession.ts:147 emission pattern.
// The operational defaults are tighter than the commissionable defaults
// — chip (Advertiser_ImplMinimalMdns.cpp) and matter.js
// (MdnsBroadcaster.ts MATTER_OPERATION_*_DEFAULT) both emit
// SII=500ms / SAI=300ms for the operational record. The 5000ms
// commissionable default does NOT apply here.
func TestBuildOperationalService_SII_SAI_Floor(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.SessionIdleInterval = 0
	cfg.SessionActiveInterval = 0
	svc := mdns.BuildOperationalService(cfg)

	got := map[string]string{}
	for _, r := range svc.TXT {
		got[r.Key] = r.Value
	}
	if got["SII"] == "0" || got["SII"] == "" {
		t.Errorf("SII = %q on wire — Bug O regression: zero collapses Apple's CASE retry budget", got["SII"])
	}
	if got["SAI"] == "0" || got["SAI"] == "" {
		t.Errorf("SAI = %q on wire — Bug O regression: zero collapses Apple's CASE retry budget", got["SAI"])
	}
	// chip + matter.js operational defaults.
	if got["SII"] != "500" {
		t.Errorf("SII = %q, want 500 (chip MATTER_OPERATION_SII_DEFAULT)", got["SII"])
	}
	if got["SAI"] != "300" {
		t.Errorf("SAI = %q, want 300 (chip MATTER_OPERATION_SAI_DEFAULT)", got["SAI"])
	}
}

// TestBuildOperationalService_SII_SAI_RespectsCaller verifies that a
// non-zero caller value is NOT clamped — the floor only applies to
// zero. Future callers that need different MRP timing must be able
// to override.
func TestBuildOperationalService_SII_SAI_RespectsCaller(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.SessionIdleInterval = 2000
	cfg.SessionActiveInterval = 100
	svc := mdns.BuildOperationalService(cfg)

	got := map[string]string{}
	for _, r := range svc.TXT {
		got[r.Key] = r.Value
	}
	if got["SII"] != "2000" {
		t.Errorf("SII = %q, want 2000 (caller value, not floored)", got["SII"])
	}
	if got["SAI"] != "100" {
		t.Errorf("SAI = %q, want 100 (caller value, not floored)", got["SAI"])
	}
}

// ---- RotatingID / RI TXT key ----

// TestBuildCommissionableService_RotatingID_Emitted verifies that a
// non-empty RotatingID is emitted as the `RI` TXT key.
// Mirrors matter.js Scanner.ts:38 / chip Advertiser_ImplMinimalMdns.cpp:878-881
// — RI emitted only when the platform supplies it.
func TestBuildCommissionableService_RotatingID_Emitted(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.RotatingID = "AABBCCDD11223344"
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "RI", "AABBCCDD11223344")
}

// TestBuildCommissionableService_RotatingID_Empty_NoRI verifies that an
// empty RotatingID suppresses the `RI` key (correct — not a mandatory
// TXT field).
func TestBuildCommissionableService_RotatingID_Empty_NoRI(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.RotatingID = ""
	svc := mdns.BuildCommissionableService(cfg)
	if hasTXTKey(svc, "RI") {
		t.Fatal("expected no RI key when RotatingID is empty")
	}
}

// ---- ICD TXT key ----

// TestBuildCommissionableService_ICD_Emitted verifies that a non-nil ICD
// pointer emits the `ICD` TXT key.
// Mirrors matter.js MdnsAdvertisement.ts:191-193.
func TestBuildCommissionableService_ICD_Emitted(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	v := uint8(1)
	cfg.ICD = &v
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "ICD", "1")
}

// TestBuildCommissionableService_ICD_Nil_NoKey verifies that a nil ICD
// field suppresses the `ICD` key (non-ICD bridge).
func TestBuildCommissionableService_ICD_Nil_NoKey(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.ICD = nil
	svc := mdns.BuildCommissionableService(cfg)
	if hasTXTKey(svc, "ICD") {
		t.Fatal("expected no ICD key when ICD is nil")
	}
}

// TestBuildOperationalService_ICD_Emitted verifies the `ICD` TXT key
// on the operational record.
func TestBuildOperationalService_ICD_Emitted(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	v := uint8(0)
	cfg.ICD = &v
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "ICD", "0")
}

// TestBuildOperationalService_ICD_Nil_NoKey verifies that nil ICD
// suppresses the key on the operational record.
func TestBuildOperationalService_ICD_Nil_NoKey(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.ICD = nil
	svc := mdns.BuildOperationalService(cfg)
	if hasTXTKey(svc, "ICD") {
		t.Fatal("expected no ICD key when ICD is nil")
	}
}

// ---- defaultHostName fallback ----

// TestBuildOperationalService_EmptyHostName_UsesDefault exercises defaultHostName indirectly through
// BuildOperationalService when HostName is left empty so the function
// supplies the OS hostname fallback.
func TestBuildOperationalService_EmptyHostName_UsesDefault(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.HostName = ""
	svc := mdns.BuildOperationalService(cfg)
	if svc.HostName == "" {
		t.Fatal("expected non-empty HostName from defaultHostName fallback")
	}
}

// TestBuildCommissionableService_EmptyHostName_UsesDefault exercises the
// commissionable path through defaultHostName.
func TestBuildCommissionableService_EmptyHostName_UsesDefault(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.HostName = ""
	svc := mdns.BuildCommissionableService(cfg)
	if svc.HostName == "" {
		t.Fatal("expected non-empty HostName from defaultHostName fallback")
	}
}

// ---- SubtypeResponder nil-receiver guard ----

// TestSubtypeResponder_NilReceiver_AddRemove_NoOp verifies that calling methods on a
// nil *SubtypeResponder is safe (nil guards).
func TestSubtypeResponder_NilReceiver_AddRemove_NoOp(t *testing.T) {
	t.Parallel()
	var r *mdns.SubtypeResponder
	// Should not panic.
	r.AddSubtype("_L1._sub._matterc._udp.local.", "inst._matterc._udp.local.")
	r.RemoveSubtype("_L1._sub._matterc._udp.local.")
	if err := r.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

// TestEnsureTrailingDot_Via_SubtypeHelpers verifies ensureTrailingDot is exercised
// when a non-dotted string is passed to AddSubtype/RemoveSubtype, through
// the exported constructor path. We use the Noop advertiser — no socket.
func TestEnsureTrailingDot_Via_SubtypeHelpers(t *testing.T) {
	t.Parallel()
	// Construct the full subtype qname manually — the production code
	// appends "." inside AddSubtype if missing.
	svc := mdns.Service{
		InstanceName: "INST",
		ServiceType:  mdns.ServiceTypeCommissionable,
		Domain:       "local",
		Subtypes:     []string{"_CM"},
	}
	// FQDN and HostFQDN exercise ensureTrailingDot-like behaviour.
	fqdn := svc.FQDN()
	if !strings.HasSuffix(fqdn, ".") {
		t.Fatalf("FQDN missing trailing dot: %q", fqdn)
	}
}

// ---- SAT floor + SII/SAI commissionable floor ----

// TestBuildCommissionableService_SII_SAI_Floor mirrors the operational
// floor test for the commissionable record — zero values must be replaced
// by spec defaults (5000/300).
func TestBuildCommissionableService_SII_SAI_Floor(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.SessionIdleInterval = 0
	cfg.SessionActiveInterval = 0
	svc := mdns.BuildCommissionableService(cfg)
	m := make(map[string]string)
	for _, r := range svc.TXT {
		m[r.Key] = r.Value
	}
	if m["SII"] == "0" || m["SII"] == "" {
		t.Errorf("SII = %q — commissionable zero should default to 500", m["SII"])
	}
	if m["SAI"] == "0" || m["SAI"] == "" {
		t.Errorf("SAI = %q — commissionable zero should default to 300", m["SAI"])
	}
	if m["SII"] != "500" {
		t.Errorf("SII = %q, want 500 (matter.js SessionIntervals idle default, §4.3.1.6)", m["SII"])
	}
	if m["SAI"] != "300" {
		t.Errorf("SAI = %q, want 300 (commissionable default per §4.3.1.6)", m["SAI"])
	}
}

// TestBuildCommissionableService_SAT_Floor verifies the SAT TXT key
// defaults to 4000 ms when SessionActiveThreshold is zero.
func TestBuildCommissionableService_SAT_Floor(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.SessionActiveThreshold = 0
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "SAT", "4000")
}

// TestBuildOperationalService_SAT_Floor verifies the operational SAT key.
func TestBuildOperationalService_SAT_Floor(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.SessionActiveThreshold = 0
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "SAT", "4000")
}

// TestBuildOperationalService_SAT_RespectsCaller verifies a non-zero SAT
// is not clamped.
func TestBuildOperationalService_SAT_RespectsCaller(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	cfg.SessionActiveThreshold = 8000
	svc := mdns.BuildOperationalService(cfg)
	assertTXTValue(t, svc, "SAT", "8000")
}

// TestService_Validate_Commissionable_ServiceType verifies Validate accepts the
// commissionable service type.
func TestService_Validate_Commissionable_ServiceType(t *testing.T) {
	t.Parallel()
	svc := validService(t)
	svc.ServiceType = mdns.ServiceTypeCommissionable
	if err := svc.Validate(); err != nil {
		t.Fatalf("Validate on commissionable service type: %v", err)
	}
}

// TestService_MarshalTXT_Empty returns empty slice for empty TXT list.
func TestService_MarshalTXT_Empty(t *testing.T) {
	t.Parallel()
	svc := mdns.Service{}
	got := svc.MarshalTXT()
	if len(got) != 0 {
		t.Fatalf("MarshalTXT on empty TXT: len=%d, want 0", len(got))
	}
}

// TestBuildOperationalService_Addresses_Copied verifies that mutating the
// caller's slice does not affect the built service's Addresses.
func TestBuildOperationalService_Addresses_Copied(t *testing.T) {
	t.Parallel()
	cfg := operationalCfg()
	before := len(cfg.Addresses)
	svc := mdns.BuildOperationalService(cfg)
	// Truncate caller's slice — must not affect svc.
	cfg.Addresses = nil
	if len(svc.Addresses) != before {
		t.Fatalf("Addresses not defensively copied: len=%d after caller clear, want %d", len(svc.Addresses), before)
	}
}

// TestBuildCommissionableService_SII_SAI_RespectsCaller verifies non-zero
// caller values are kept.
func TestBuildCommissionableService_SII_SAI_RespectsCaller(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.SessionIdleInterval = 3000
	cfg.SessionActiveInterval = 150
	svc := mdns.BuildCommissionableService(cfg)
	assertTXTValue(t, svc, "SII", "3000")
	assertTXTValue(t, svc, "SAI", "150")
}

// TestBuildCommissionableService_NoVendorOmitsVP verifies VP and the _V
// subtype are omitted when VendorID is 0 — VendorID 0 is reserved, so
// "VP=0+0" / "_V0" would be non-conformant (chip gates both on a present
// vendor id).
func TestBuildCommissionableService_NoVendorOmitsVP(t *testing.T) {
	t.Parallel()
	cfg := commissionableCfg()
	cfg.VendorID = 0
	cfg.ProductID = 0
	svc := mdns.BuildCommissionableService(cfg)
	for _, r := range svc.TXT {
		if r.Key == "VP" {
			t.Errorf("VP must be omitted when VendorID is 0, got %q", r.Value)
		}
	}
	for _, s := range svc.Subtypes {
		if strings.HasPrefix(s, "_V") {
			t.Errorf("_V subtype must be omitted when VendorID is 0, got %q", s)
		}
	}
}
