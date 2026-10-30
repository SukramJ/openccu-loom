// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"slices"
	"testing"

	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// discardCoverWriter accepts every write; the tests below only compose.
type discardCoverWriter struct{}

func (discardCoverWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}

// addGarageDrive puts a garage drive on dev's second channel. It projects
// as a Closure (0x0230), a device type go-fabric's levels below 1.5.1 do
// not know (go-fabric schema.DeviceTypeLevels).
func addGarageDrive(dev *device.Device) {
	ch := dev.AddChannel("BWTH0001:2", 2, "GARAGE_DOOR", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(cover.NewGarage(cover.GarageConfig{Channel: ch, Writer: discardCoverWriter{}}))
}

// bridgesClosure reports whether the bundle's bridge serves a Closure
// endpoint.
func bridgesClosure(bundle *matterBridgeBundle) bool {
	for _, ep := range bundle.node.Bridge().Topology().Bridged() {
		if ep.DeviceType == uint16(fabricdevice.ClosureID) {
			return true
		}
	}
	return false
}

// TestMatterSpecLevelFollowsTheConfig pins north.matter.spec_level through
// the daemon's Matter start path, one case per value: unset bridges the
// garage drive as a Closure; 1.4.2 withholds it. The thermostat stays
// bridged in both — the rig fails otherwise — which holds only while the
// node and every device carry the same policies: go-fabric's node refuses
// a device composed under other ones (node.ErrPolicyMismatch), so a level
// that reached only one side would drop the thermostat too.
func TestMatterSpecLevelFollowsTheConfig(t *testing.T) {
	for _, tc := range []struct {
		level   string
		closure bool
	}{{"", true}, {"1.4.2", false}} {
		t.Run("level="+tc.level, func(t *testing.T) {
			_, bundle, _ := bootThermostatBridgeWith(t, false, false, nil, thermostatBridgeExtras{
				configure: func(m *config.NorthMatter) { m.SpecLevel = tc.level },
				populate:  addGarageDrive,
			})
			if got := bridgesClosure(bundle); got != tc.closure {
				t.Errorf("Closure endpoint bridged = %v, want %v", got, tc.closure)
			}
			if tc.level != "" {
				if got := bundle.node.Surface().Level; string(got) != tc.level {
					t.Errorf("node surface level = %q, want %q", got, tc.level)
				}
			}
		})
	}
}

// TestMatterControllerProfileReachesTheNode pins
// north.matter.controller_profile through the daemon's Matter start path:
// the node reports the selected profile, and apple-home's one register
// row (Thermostat MSCH, Reported) only warns, so the week programs stay
// served. A profile that reached only the node or only the devices would
// leave the thermostat unbridged and fail the rig.
func TestMatterControllerProfileReachesTheNode(t *testing.T) {
	ctx, bundle, ep := bootThermostatBridgeWith(t, false, true, nil, thermostatBridgeExtras{
		configure: func(m *config.NorthMatter) { m.ControllerProfile = "apple-home" },
	})
	if got := bundle.node.Surface().Profiles; !slices.Equal(got, []string{"apple-home"}) {
		t.Errorf("node surface profiles = %v, want [apple-home]", got)
	}
	st, fm := readThermostat(ctx, bundle, ep, 0xFFFC)
	if st != im.StatusSuccess {
		t.Fatalf("FeatureMap read = %v", st)
	}
	if toUint32(fm)&uint32(thermostatspec.FeatureMatterScheduleConfiguration) == 0 {
		t.Errorf("FeatureMap 0x%X: MSCH removed under apple-home, whose row only warns", toUint32(fm))
	}
}
