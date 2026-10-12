// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"testing"

	windowcoveringspec "github.com/SukramJ/go-fabric/cluster/spec/windowcovering"
)

// TestExtractGoToPercentageAcceptsBothBridgeShapes pins that the decoder
// reads the generated request structs (the bridge's decode while
// cluster/spec/windowcovering is linked) and the tag-keyed map (its generic
// fallback without a linked definition) to the same value, so the command
// does not depend on which definition packages the binary links.
func TestExtractGoToPercentageAcceptsBothBridgeShapes(t *testing.T) {
	cases := []struct {
		name   string
		fields any
	}{
		{"GoToLiftPercentage struct", windowcoveringspec.GoToLiftPercentageRequest{LiftPercent100thsValue: 4200}},
		{"GoToTiltPercentage struct", windowcoveringspec.GoToTiltPercentageRequest{TiltPercent100thsValue: 4200}},
		{"tag map", map[uint8]any{0: uint64(4200)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pct, err := extractGoToPercentage(tc.fields)
			if err != nil {
				t.Fatalf("extractGoToPercentage: %v", err)
			}
			if pct != 4200 {
				t.Fatalf("extractGoToPercentage = %d, want 4200", pct)
			}
		})
	}
}
