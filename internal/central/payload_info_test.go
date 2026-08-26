// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestCentralInfoPayloadCarriesHACanonicalKeys pins the contract that
// [Unit.Info] marshals to HA-canonical keys (`sw_version`,
// `serial_number`, `configuration_url`) so the JSON output can flow
// straight into the MQTT-Discovery hub-device block without renaming.
func TestCentralInfoPayloadCarriesHACanonicalKeys(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	unit.SetSystemInformation(central.SystemInfo{
		Model:    "HmIP-CCU3",
		Version:  "3.71.13",
		Hostname: "ccu3",
		Serial:   "OEQ1234567",
		URL:      "http://172.18.X.XX",
		IsHaApp:  false,
	})
	info, _ := unit.Info().(*payload.CentralInfo)
	if info == nil {
		t.Fatal("Info() returned nil — expected *payload.CentralInfo")
	}

	if info.Name != "GoOtto" {
		t.Errorf("Name = %q, want GoOtto", info.Name)
	}
	if info.Model != "HmIP-CCU3" {
		t.Errorf("Model = %q, want HmIP-CCU3", info.Model)
	}
	if info.SWVersion != "3.71.13" {
		t.Errorf("SWVersion = %q, want 3.71.13", info.SWVersion)
	}
	if info.Hostname != "ccu3" {
		t.Errorf("Hostname = %q, want ccu3", info.Hostname)
	}
	if info.SerialNumber != "OEQ1234567" {
		t.Errorf("SerialNumber = %q, want OEQ1234567", info.SerialNumber)
	}
	if info.ConfigurationURL != "http://172.18.X.XX" {
		t.Errorf("ConfigurationURL = %q, want http://172.18.X.XX", info.ConfigurationURL)
	}

	// Wire-format check: serialised JSON must carry the HA-canonical
	// keys, never the bare struct names.
	buf, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(buf)
	for _, want := range []string{
		`"sw_version"`,
		`"serial_number"`,
		`"configuration_url"`,
	} {
		if !contains(wire, want) {
			t.Errorf("wire JSON missing %s: %s", want, wire)
		}
	}
	for _, forbidden := range []string{`"version"`, `"serial"`, `"url"`} {
		if contains(wire, forbidden) {
			t.Errorf("wire JSON leaks bare key %s: %s", forbidden, wire)
		}
	}
}

// TestCentralInfoPayloadOmitsEmptyFields pins that pre-bootstrap
// (SystemInfo with zero-values) the JSON wire output contains only
// the always-stamped `name`. Empty strings must not surface as keys —
// HA would render "Unknown" for them.
func TestCentralInfoPayloadOmitsEmptyFields(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, _ := unit.Info().(*payload.CentralInfo)
	if info == nil {
		t.Fatal("Info() returned nil — expected *payload.CentralInfo")
	}
	if info.Name != "GoOtto" {
		t.Errorf("Name = %q, want GoOtto", info.Name)
	}
	buf, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(buf)
	for _, forbidden := range []string{
		`"model"`,
		`"sw_version"`,
		`"serial_number"`,
		`"configuration_url"`,
		`"hostname"`,
		`"is_ha_app"`,
	} {
		if contains(wire, forbidden) {
			t.Errorf("zero-value field %s leaked into pre-bootstrap wire JSON: %s", forbidden, wire)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestCentralInfoPayloadOmitsCCUSecurityFlags pins that the CCU security
// posture stays out of the MQTT-Discovery hub-device block. AuthEnabled and
// HTTPSRedirectEnabled are deliberately untagged in [central.SystemInfo]:
// they are a status-page concern, and leaking them into the discovery
// payload would change a published wire contract. A `payload:"info"` tag
// added to either field in a future edit fails here.
func TestCentralInfoPayloadOmitsCCUSecurityFlags(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	unit.SetSystemInformation(central.SystemInfo{
		Model:                "HmIP-CCU3",
		Serial:               "OEQ1234567",
		AuthEnabled:          true,
		HTTPSRedirectEnabled: true,
	})

	buf, err := json.Marshal(unit.Info())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"auth_enabled", "AuthEnabled",
		"https_redirect_enabled", "HTTPSRedirectEnabled",
	} {
		if _, present := decoded[key]; present {
			t.Errorf("discovery payload carries %q — the CCU security flags must stay untagged: %s", key, buf)
		}
	}
	// Sanity: the tagged neighbours are still there, so the assertion above
	// is not passing merely because the payload is empty.
	if _, present := decoded["serial_number"]; !present {
		t.Errorf("expected serial_number in payload, got %s", buf)
	}
}

// TestCCUInterfacesCacheReturnsCopy pins the copy-on-read/copy-on-write
// contract of the CCU-reported interface list: a caller that keeps or sorts
// the returned slice must not be able to reach into the cache, and neither
// must a caller that mutates the slice it handed to the setter.
func TestCCUInterfacesCacheReturnsCopy(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := unit.CCUInterfaces(); len(got) != 0 {
		t.Errorf("fresh unit reports %d interfaces, want 0", len(got))
	}

	source := []central.CCUInterface{
		{Type: "HmIP-RF", Address: "HmIP-RF", Port: 2010, URL: "http://127.0.0.1:2010"},
		{Type: "BidCos-RF", Address: "BidCos-RF", Port: 2001},
	}
	unit.SetCCUInterfaces(source)

	// Mutating the caller's slice after the set must not affect the cache.
	source[0].Address = "mutated"
	got := unit.CCUInterfaces()
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].Address != "HmIP-RF" {
		t.Errorf("cache observed caller mutation: Address = %q", got[0].Address)
	}
	if got[0].Port != 2010 || got[1].Port != 2001 {
		t.Errorf("ports = %d/%d, want 2010/2001", got[0].Port, got[1].Port)
	}

	// Mutating the returned slice must not affect the next read either.
	got[1].Type = "clobbered"
	if again := unit.CCUInterfaces(); again[1].Type != "BidCos-RF" {
		t.Errorf("cache observed reader mutation: Type = %q", again[1].Type)
	}
}
