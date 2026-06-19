// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rega

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFetchAllDeviceDataSkipsEmptyValue guards against re-introducing the
// empty-value -> "0" coercion (reference issue #3228). A not-yet-measured
// numeric data point (e.g. ACTUAL_TEMPERATURE right after a CCU restart)
// reports an empty value over the ReGa object model; the bulk-load script must
// SKIP it (yielding a cache miss so the value stays unset) instead of emitting
// a literal 0 that consumers record as a real reading (e.g. 0 degrees).
func TestFetchAllDeviceDataSkipsEmptyValue(t *testing.T) {
	t.Parallel()

	body, err := loadScript(hmenum.RegaScriptFetchAllDeviceData)
	if err != nil {
		t.Fatalf("loadScript: %v", err)
	}
	norm := regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")

	if strings.Contains(norm, `if (vDPValue == "") { sValue = "0"`) {
		t.Fatal(`fetch_all_device_data.fn must not coerce an empty numeric value to "0" (#3228)`)
	}
	if !strings.Contains(norm, `if (vDPValue == "") { bHasValue = false`) {
		t.Fatal("fetch_all_device_data.fn must skip an empty numeric value (bHasValue = false)")
	}
}
