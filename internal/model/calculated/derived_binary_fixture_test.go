// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newWindowOpenSensorForTest builds a WINDOW_OPEN sensor the way
// [BuildCalculatedDataPoints] builds it for a rotary-handle device: the
// label sets come from the registry row rather than from a copy in the
// test, so a row whose vocabulary moves takes these tests with it.
//
// The model is named, not indexed: which registry slot carries
// WINDOW_OPEN is not a property the registry declares.
func newWindowOpenSensorForTest(t *testing.T) *DerivedBinarySensor {
	t.Helper()
	for _, m := range LookupDerivedBinaryMappings("HmIP-SRH") {
		if m.CalculatedParameter != hmenum.CalculatedParameterWindowOpen {
			continue
		}
		return NewDerivedBinarySensorWithIdentity(
			"", "", m.CalculatedParameter, m.SourceParameter, m.OnValues, m.OffValues,
		)
	}
	t.Fatal("no WINDOW_OPEN mapping registered for HmIP-SRH")
	return nil
}
