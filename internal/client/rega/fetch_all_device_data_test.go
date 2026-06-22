// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
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
