// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// eventbridge_helpers_test.go covers the pure-logic helpers in
// eventbridge.go: parseChannel, deviceAddrAndChannel, inferInterface,
// lookupDeviceObject, lookupDevice (package-level), lookupChannel,
// lookupCalculatedUnit, isCalculatedParameter, isReachabilityParameter,
// datapointNameDataOf, firstPressParameter.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// parseChannel
// ============================================================

func TestParseChannelNormal(t *testing.T) {
	t.Parallel()
	addr, n := parseChannel("DEV001:3")
	if addr != "DEV001:3" || n != 3 {
		t.Errorf("parseChannel = (%q, %d), want (DEV001:3, 3)", addr, n)
	}
}

func TestParseChannelNoColon(t *testing.T) {
	t.Parallel()
	addr, n := parseChannel("DEV001")
	if addr != "DEV001" || n != 0 {
		t.Errorf("parseChannel no colon = (%q, %d), want (DEV001, 0)", addr, n)
	}
}

func TestParseChannelNonNumericSuffix(t *testing.T) {
	t.Parallel()
	addr, n := parseChannel("DEV:abc")
	if n != 0 {
		t.Errorf("parseChannel non-numeric = (%q, %d), want n=0", addr, n)
	}
}

// ============================================================
// deviceAddrAndChannel
// ============================================================

func TestDeviceAddrAndChannelNormal(t *testing.T) {
	t.Parallel()
	addr, ch := deviceAddrAndChannel("DEV001:2")
	if addr != "DEV001" || ch != 2 {
		t.Errorf("deviceAddrAndChannel = (%q, %d), want (DEV001, 2)", addr, ch)
	}
}

func TestDeviceAddrAndChannelNoColon(t *testing.T) {
	t.Parallel()
	addr, ch := deviceAddrAndChannel("DEV001")
	if addr != "DEV001" || ch != 0 {
		t.Errorf("deviceAddrAndChannel no colon = (%q, %d), want (DEV001, 0)", addr, ch)
	}
}

func TestDeviceAddrAndChannelNonNumeric(t *testing.T) {
	t.Parallel()
	addr, ch := deviceAddrAndChannel("DEV:xyz")
	if addr != "DEV" || ch != 0 {
		t.Errorf("deviceAddrAndChannel non-numeric = (%q, %d), want (DEV, 0)", addr, ch)
	}
}

// ============================================================
// inferInterface
// ============================================================

func TestInferInterfaceFromKey(t *testing.T) {
	t.Parallel()
	key := hmtypes.DataPointKey{InterfaceID: "HmIP-RF"}
	if got := inferInterface(key); got != "HmIP-RF" {
		t.Errorf("inferInterface = %q, want HmIP-RF", got)
	}
}

func TestInferInterfaceEmptyKey(t *testing.T) {
	t.Parallel()
	key := hmtypes.DataPointKey{}
	if got := inferInterface(key); got != "" {
		t.Errorf("inferInterface empty = %q, want empty", got)
	}
}

// ============================================================
// lookupDeviceObject (package-level)
// ============================================================

func TestLookupDeviceObjectNilRegistry(t *testing.T) {
	t.Parallel()
	if got := lookupDeviceObject(nil, "ccu-01", "DEV001"); got != nil {
		t.Errorf("lookupDeviceObject nil registry = %v, want nil", got)
	}
}

func TestLookupDeviceObjectNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	if got := lookupDeviceObject(reg, "ccu-01", "NOSUCH"); got != nil {
		t.Errorf("lookupDeviceObject not found = %v, want nil", got)
	}
}

// ============================================================
// lookupDevice (package-level, returns model+name)
// ============================================================

func TestLookupDeviceNilRegistry(t *testing.T) {
	t.Parallel()
	m, n := lookupDevice(nil, "ccu-01", "DEV001")
	if m != "" || n != "" {
		t.Errorf("lookupDevice nil registry = (%q, %q), want empty", m, n)
	}
}

func TestLookupDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	m, n := lookupDevice(reg, "ccu-01", "NOSUCH")
	if m != "" || n != "" {
		t.Errorf("lookupDevice not found = (%q, %q), want empty", m, n)
	}
}

// ============================================================
// lookupChannel (package-level, inside eventbridge.go)
// ============================================================

func TestLookupChannelEventBridgeNilRegistry(t *testing.T) {
	t.Parallel()
	if got := lookupChannel(nil, "ccu-01", "DEV001", 1); got != nil {
		t.Errorf("lookupChannel nil registry = %v, want nil", got)
	}
}

func TestLookupChannelEventBridgeNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	if got := lookupChannel(reg, "ccu-01", "NOSUCH", 1); got != nil {
		t.Errorf("lookupChannel not found = %v, want nil", got)
	}
}

// ============================================================
// lookupCalculatedUnit
// ============================================================

func TestLookupCalculatedUnitNilChannel(t *testing.T) {
	t.Parallel()
	_, ok := lookupCalculatedUnit(nil, "DEW_POINT")
	if ok {
		t.Error("lookupCalculatedUnit nil channel must return false")
	}
}

func TestLookupCalculatedUnitNoCalcDPs(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("DEV001:1", 1, "CLIMATE_CONTROL", hmenum.ParamsetKeyValues)
	_, ok := lookupCalculatedUnit(ch, "DEW_POINT")
	if ok {
		t.Error("lookupCalculatedUnit empty channel must return false")
	}
}

// ============================================================
// isCalculatedParameter
// ============================================================

func TestIsCalculatedParameterNilChannel(t *testing.T) {
	t.Parallel()
	if isCalculatedParameter(nil, "DEW_POINT") {
		t.Error("isCalculatedParameter nil channel must return false")
	}
}

func TestIsCalculatedParameterEmptyChannel(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "D:1", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("D:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// No calculated DPs on the channel → false
	if isCalculatedParameter(ch, "DEW_POINT") {
		t.Error("isCalculatedParameter empty channel must return false")
	}
}

// ============================================================
// isReachabilityParameter
// ============================================================

func TestIsReachabilityParameterTrue(t *testing.T) {
	t.Parallel()
	cases := []string{"UNREACH", "STICKY_UNREACH"}
	for _, tc := range cases {
		if !isReachabilityParameter(tc) {
			t.Errorf("isReachabilityParameter(%q) = false, want true", tc)
		}
	}
}

func TestIsReachabilityParameterFalse(t *testing.T) {
	t.Parallel()
	if isReachabilityParameter("TEMPERATURE") {
		t.Error("isReachabilityParameter(TEMPERATURE) = true, want false")
	}
}

// ============================================================
// datapointNameDataOf
// ============================================================

func TestDatapointNameDataOfNilDP(t *testing.T) {
	t.Parallel()
	_, ok := datapointNameDataOf(nil)
	if ok {
		t.Error("datapointNameDataOf nil must return false")
	}
}

func TestDatapointNameDataOfNoInterface(t *testing.T) {
	t.Parallel()
	// An arbitrary struct that does not implement nameDataProvider
	_, ok := datapointNameDataOf(struct{}{})
	if ok {
		t.Error("datapointNameDataOf plain struct must return false")
	}
}
