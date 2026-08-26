// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFetchAllDeviceDataLoads verifies the bulk-load script is embedded and
// non-empty. The script runs on the CCU and cannot be executed in Go.
func TestFetchAllDeviceDataLoads(t *testing.T) {
	t.Parallel()

	body, err := loadScript(hmenum.RegaScriptFetchAllDeviceData)
	if err != nil {
		t.Fatalf("loadScript: %v", err)
	}
	if body == "" {
		t.Fatal("fetch_all_device_data.fn must not be empty")
	}
}

// TestFetchAllDeviceDataV25Safeguards pins the two v2.5 source-level safeguards
// against the post-restart "0" placeholder (reference issue #3228):
//
//  1. VirtualDevices data points are gated on a valid LastTimestamp(), so heating
//     groups that expose a Timestamp() but no real reading after a CCU restart
//     stay out of the bulk result instead of emitting a placeholder 0.
//  2. An empty value is coerced to 0 only when it is a genuine string script
//     variable (VarType() == 4); a real numeric 0 has a numeric VarType and is
//     preserved, so legitimate zero readings are not conflated with not-yet-
//     measured ones (the flaw of the bare vDPValue == "" check).
func TestFetchAllDeviceDataV25Safeguards(t *testing.T) {
	t.Parallel()

	body, err := loadScript(hmenum.RegaScriptFetchAllDeviceData)
	if err != nil {
		t.Fatalf("loadScript: %v", err)
	}
	norm := regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")

	if !strings.Contains(norm, `(!oDP.LastTimestamp()) && (sUse_Interface == "VirtualDevices")`) {
		t.Fatal("VirtualDevices data points must be gated on a valid LastTimestamp() (#3228)")
	}
	if !strings.Contains(norm, "vDP_Value.VarType()") {
		t.Fatal("empty-value detection must use VarType() (#3228)")
	}
	if !strings.Contains(norm, `(iDP_Value_VarType == 4) && (vDP_Value == "")`) {
		t.Fatal("an empty value must be coerced to 0 only for a string VarType (#3228)")
	}
}
