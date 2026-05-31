// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central_test

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestCentralUnitInfoPayloadCarriesHACanonicalKeys pins the contract that
// [CentralUnit.Info] marshals to HA-canonical keys (`sw_version`,
// `serial_number`, `configuration_url`) so the JSON output can flow
// straight into the MQTT-Discovery hub-device block without renaming.
func TestCentralUnitInfoPayloadCarriesHACanonicalKeys(t *testing.T) {
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
	info, _ := unit.Info().(*payload.CentralUnitInfo)
	if info == nil {
		t.Fatal("Info() returned nil — expected *payload.CentralUnitInfo")
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

// TestCentralUnitInfoPayloadOmitsEmptyFields pins that pre-bootstrap
// (SystemInfo with zero-values) the JSON wire output contains only
// the always-stamped `name`. Empty strings must not surface as keys —
// HA would render "Unknown" for them.
func TestCentralUnitInfoPayloadOmitsEmptyFields(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "GoOtto"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, _ := unit.Info().(*payload.CentralUnitInfo)
	if info == nil {
		t.Fatal("Info() returned nil — expected *payload.CentralUnitInfo")
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
