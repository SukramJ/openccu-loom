// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"slices"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
)

// TestSirenOnOffAdvertisesLighting pins the LT (Lighting) feature on the
// siren's OnOff cluster: OnOffPlugInUnit (0x010A) mandates it
// (on-off-plug-in-unit.element.ts:27-30), and with it the four LT-gated
// attributes and three LT-gated commands. A FeatureMap of 0 once left them
// unimplemented although the device type requires them.
func TestSirenOnOffAdvertisesLighting(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	if v, ok := srv.MatterRead(0xFFFC); !ok || v != onoff.FeatureLighting {
		t.Errorf("FeatureMap = (%v, %v), want (0x%02X, true)", v, ok, onoff.FeatureLighting)
	}
	if v, ok := srv.MatterRead(0xFFFD); !ok || v != onoff.Revision() {
		t.Errorf("ClusterRevision = (%v, %v), want (%v, true)", v, ok, onoff.Revision())
	}
	if got, want := srv.MatterAttributes(), onoff.LightingAttributes(); !slices.Equal(got, want) {
		t.Errorf("MatterAttributes() = %#x, want the LT set %#x", got, want)
	}
	if got, want := srv.MatterAcceptedCommands(), onoff.LightingCommands(); !slices.Equal(got, want) {
		t.Errorf("MatterAcceptedCommands() = %#x, want the LT set %#x", got, want)
	}
	if n := len(srv.MatterGeneratedCommands()); n != 0 {
		t.Errorf("MatterGeneratedCommands() lists %d command(s); OnOff commands carry no response payload", n)
	}
}
