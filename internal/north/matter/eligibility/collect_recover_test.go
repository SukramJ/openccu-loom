// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package eligibility_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// panickingCustomDP models a structurally-incomplete device — e.g. a custom
// light whose LEVEL data point never materialised, leaving a nil embedded
// pointer that a promoted accessor dereferences. Its DataPointKey has no
// parameter, so the candidate-label helper falls through to Name(), which
// panics like the real nil-embed deref.
type panickingCustomDP struct{}

func (p *panickingCustomDP) DataPointKey() hmtypes.DataPointKey { return hmtypes.DataPointKey{} }

func (p *panickingCustomDP) Name() string {
	panic("simulated nil embedded-pointer dereference in an incomplete custom device")
}

// TestCollectCandidates_SkipsPanickingDevice guarantees that one
// structurally-broken device does not crash the whole exposable enumeration:
// it is skipped, and every healthy device in the same batch still surfaces.
//
// Regression for a custom colour light whose LEVEL data point never
// materialised (a nil embedded *generic.Float): its promoted Name() panicked
// inside CollectCandidates, so GET /api/v1/matter/exposable returned 500 and
// the Matter bridge came up with no bridged endpoints at all.
func TestCollectCandidates_SkipsPanickingDevice(t *testing.T) {
	t.Parallel()

	broken := device.New(device.Config{Address: "BROKEN:0", Model: "HmIP-LSC"})
	bch := broken.AddChannel("BROKEN:1", 1, "RGBW", hmenum.ParamsetKeyValues)
	bch.SetCustomDataPoint(&panickingCustomDP{})

	healthy := device.New(device.Config{Address: "OK:0", Name: "Test Switch", Model: "HmIP-PS"})
	hch := healthy.AddChannel("OK:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	hch.SetCustomDataPoint(&mappableDP{
		key:     hmtypes.DataPointKey{Parameter: "STATE"},
		devType: 0x0100,
		cluster: 0x0006,
	})

	var got []eligibility.Candidate
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CollectCandidates panicked on a broken device instead of skipping it: %v", r)
			}
		}()
		got = eligibility.CollectCandidates("central", []*device.Device{broken, healthy}, false)
	}()

	if len(got) != 1 {
		t.Fatalf("want exactly 1 candidate (broken skipped, healthy kept), got %d: %+v", len(got), got)
	}
	if got[0].Key.DeviceAddress != "OK:0" {
		t.Fatalf("healthy candidate missing after skipping the broken one; got device %q", got[0].Key.DeviceAddress)
	}
}
