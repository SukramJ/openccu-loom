// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putCoverFloatDP attaches a writable FLOAT wire data point to ch.
func putCoverFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// newRfBlindDevice builds a classic RF jalousie actuator through the real
// registry, carrying the tilt parameter under the name the model uses.
func newRfBlindDevice(t *testing.T, model string, tiltParam hmenum.Parameter) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "BidCos-RF",
		Interface:    hmenum.InterfaceBidCosRF,
		Address:      "LEQ0987654",
		Model:        model,
		ProductGroup: hmenum.ProductGroupHM,
	})
	dev.AddChannel("LEQ0987654:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch := dev.AddChannel("LEQ0987654:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	putCoverFloatDP(ch, hmenum.ParameterLevel)
	if tiltParam != "" {
		putCoverFloatDP(ch, tiltParam)
	}
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return ch
}

// TestRfJalousieWithSlatsBindsItsTiltAxis is the regression guard for a
// jalousie actuator that lost its tilt axis entirely.
//
// The RfCover profile maps FieldLevel2 → LEVEL_SLATS, but the constructor
// promoted a plain Cover to a Blind only when LEVEL_2 was present, and the
// Blind then bound its tilt slot to LEVEL_2 as well. The classic RF jalousie
// actuators carry LEVEL + LEVEL_SLATS and no LEVEL_2, so they materialised as
// a plain cover: no tilt on any north-bound surface, and no way to set one.
func TestRfJalousieWithSlatsBindsItsTiltAxis(t *testing.T) {
	t.Parallel()
	ch := newRfBlindDevice(t, "HM-LC-Ja1PBU-FM", hmenum.ParameterLevelSlats)

	cdp := ch.CustomDataPoint()
	blind, ok := cdp.(*cover.Blind)
	if !ok {
		t.Fatalf("custom data point is %T, want *cover.Blind — the tilt axis is gone", cdp)
	}

	slats, ok := ch.Parameter(hmenum.ParameterLevelSlats).(*generic.Float)
	if !ok {
		t.Fatal("LEVEL_SLATS is not a float data point")
	}
	slats.OnEvent(0.25)

	tilt, ok := blind.TiltPosition()
	if !ok {
		t.Fatal("TiltPosition() reported nothing after LEVEL_SLATS was fed — the slot is unbound")
	}
	if got := tilt.Level(); got != 0.25 {
		t.Errorf("TiltPosition().Level() = %v, want 0.25", got)
	}
}

// TestRfCoverWithoutATiltParameterStaysAPlainCover is the negative control
// for the promotion: the RF actuators that carry neither LEVEL_2 nor
// LEVEL_SLATS are plain covers and must stay that way. Without this, widening
// the promotion to "anything the schema maps" would turn every RF cover into
// a blind with a tilt axis it cannot drive.
func TestRfCoverWithoutATiltParameterStaysAPlainCover(t *testing.T) {
	t.Parallel()
	ch := newRfBlindDevice(t, "HM-LC-Bl1-FM", "")

	cdp := ch.CustomDataPoint()
	if _, isBlind := cdp.(*cover.Blind); isBlind {
		t.Fatalf("custom data point is a *cover.Blind; a device with no tilt parameter must stay a plain cover")
	}
	if _, isCover := cdp.(*cover.Cover); !isCover {
		t.Fatalf("custom data point is %T, want *cover.Cover", cdp)
	}
}
