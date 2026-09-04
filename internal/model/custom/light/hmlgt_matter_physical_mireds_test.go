// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import "testing"

// Both ColorControl projections answer ColorTempPhysicalMin/MaxMireds
// for the same kind of light, so they must resolve it by the same rule:
// the light's own Kelvin bounds, reciprocated. ctColorServer already
// does; rgbwColorServer returned the fixed pair, which is a second
// decision site for one datum.
//
// The bounds below are set on the model directly. They are a test input
// for the projection rule, NOT a claim about any device: the fleet's
// COLOR_TEMPERATURE descriptors declare 1000-10200 K, whose reciprocals
// clamp onto the same numbers the fixed pair carries, so no in-fleet
// descriptor can tell the two rules apart.
func TestHmLgtRGBWPhysicalMiredsFollowTheLightsKelvinBounds(t *testing.T) {
	t.Parallel()

	r := &RGBWLight{MinKelvin: 2700, MaxKelvin: 6000}
	s := rgbwColorServer{l: r}

	wantMin := kelvinToMireds(r.MaxKelvin) // higher Kelvin → lower mireds
	wantMax := kelvinToMireds(r.MinKelvin)

	for _, tc := range []struct {
		name   string
		attrID uint32
		want   uint16
	}{
		{"ColorTempPhysicalMinMireds", matterAttrColorColorTempPhysicalMinMir, wantMin},
		{"ColorTempPhysicalMaxMireds", matterAttrColorColorTempPhysicalMaxMir, wantMax},
		{"CoupleColorTempToLevelMinMireds", matterAttrColorCoupleColorTempToLevelMinMir, wantMin},
	} {
		got, ok := s.MatterRead(tc.attrID)
		if !ok {
			t.Fatalf("%s: attribute not readable", tc.name)
		}
		v, isU16 := got.(uint16)
		if !isU16 {
			t.Fatalf("%s: read %T, want uint16", tc.name, got)
		}
		if v != tc.want {
			t.Errorf("%s = %d mireds for a %d..%d K light, want %d — the RGBW projection is not using the light's own bounds",
				tc.name, v, r.MinKelvin, r.MaxKelvin, tc.want)
		}
	}
}
