// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// TestW2CfgValidateRejectsAnUnusableMatterPasscode pins that the config
// tier refuses a setup code the PASE builder will later refuse anyway.
//
// The two ends disagreed: [NorthMatterCommissioning.Passcode] documents
// the legal range, but no validator enforced it, so a save of 12345678
// answered 200, the REST setup endpoint minted a QR and a manual code
// from it, and the boot path then failed to build the PASE adapter and
// logged one warning — a bridge that advertises, accepts no pairing, and
// says nothing on the surface the operator used.
//
// The legal set is chip's IsValidSetupPIN (mirrored in go-fabric's
// secure/setup), not a range re-derived here.
func TestW2CfgValidateRejectsAnUnusableMatterPasscode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		passcode uint32
		wantErr  bool
	}{
		{"zero disables PASE", 0, false},
		{"a usable code", 20202021, false},
		{"the lowest legal code", 1, false},
		{"the highest legal code", 99999998, false},
		{"above the 27-bit range", 99999999, true},
		{"a sequential trivial code", 12345678, true},
		{"a descending trivial code", 87654321, true},
		{"a repeated-digit trivial code", 11111111, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.North.Matter.Commissioning.Passcode = tc.passcode
			err := cfg.Validate()
			if tc.wantErr {
				assertRejected(t, err, "north.matter.commissioning.passcode")
				return
			}
			assertAccepted(t, err)
		})
	}
}
