// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unIgnoreOrderProfileName is a throwaway [hmenum.DeviceProfile] registered
// only for this test — it never collides with a real device type because
// no CCU ever reports "TESTUNIGNOREORDER" as a TYPE.
const unIgnoreOrderProfileName = hmenum.DeviceProfile("TestUnIgnoreOrderProfile")

// unIgnoreOrderDeviceType is the fake device TYPE registered with the
// profile above.
const unIgnoreOrderDeviceType = "TESTUNIGNOREORDER"

// fakeAttachableDataPoint satisfies [device.AttachableDataPoint] with the
// bare minimum so the materializer's SetCustomDataPoint hook succeeds
// without pulling in a real custom-DP sub-package.
type fakeAttachableDataPoint struct {
	key hmtypes.DataPointKey
}

func (f *fakeAttachableDataPoint) DataPointKey() hmtypes.DataPointKey { return f.key }

// registerUnIgnoreOrderProfile installs a minimal custom-DP profile into
// [custom.DefaultRegistry] — the same registry [DevicePipeline] uses in
// production — with `AllowUndefinedGenericDataPoints: false`. That flag is
// what makes [custom.SuppressUndefinedGenericDataPoints] force-mark every
// undefined VALUES parameter NoCreate, which is the pass whose ordering
// relative to the un-ignore marks this test exists to pin.
func registerUnIgnoreOrderProfile(t *testing.T) {
	t.Helper()
	reg := custom.DefaultRegistry()
	profile := custom.Profile{
		Name:       unIgnoreOrderProfileName,
		DeviceType: unIgnoreOrderDeviceType,
		Category:   hmenum.DataPointCategorySwitch,
		Channels:   []custom.ChannelRoleAssignment{{Channel: 1, Role: custom.ChannelRolePrimary}},
		Config: &custom.ProfileConfig{
			ProfileType: unIgnoreOrderProfileName,
			ChannelGroup: custom.ChannelGroupConfig{
				PrimaryChannel:                  0,
				PrimaryChannelSet:               true,
				AllowUndefinedGenericDataPoints: false,
			},
		},
	}
	if err := reg.Register(profile); err != nil {
		t.Fatalf("register fake profile: %v", err)
	}
	ctor := func(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
		return &fakeAttachableDataPoint{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(unIgnoreOrderProfileName)}}, nil
	}
	if err := reg.RegisterConstructor(unIgnoreOrderProfileName, ctor); err != nil {
		t.Fatalf("register fake constructor: %v", err)
	}
}

// TestFinishIngestAppliesUnIgnoreBeforeCustomDPSuppression proves the effect
// the pipeline's un-ignore withdrawal exists to produce: an operator's
// `un_ignore` entry for a parameter that a custom-DP device's suppression
// pass would otherwise force to NoCreate must leave that data point
// un-suppressed once the full ingest pipeline has run.
//
// SetForcedUsage has no clearing counterpart (see
// [custom.SuppressUndefinedGenericDataPoints]), so this only holds when the
// un-ignore mark is stamped on the data point BEFORE the suppression walk
// runs — the pass consults [device.ParameterDataPoint.IsUnIgnored] to decide
// whether to skip a candidate. Reordering [DevicePipeline.finishIngest] so
// the suppression pass runs first reproduces the withdrawn feature: this
// test then fails with the un-ignored parameter still forced NoCreate.
func TestFinishIngestAppliesUnIgnoreBeforeCustomDPSuppression(t *testing.T) {
	registerUnIgnoreOrderProfile(t)

	c, _ := central.New(central.Config{Name: "ccu-unignore-order"})
	p := NewDevicePipeline(c)

	gate := newProductionVisibilityGate()
	gate.Parameter().LoadUnIgnore([]visibility.UnIgnoreEntry{
		{Parameter: "SPECIAL_PARAM", IsSimple: true},
	})
	p.WithVisibility(gate)

	b := backendWithParams("UNIGNOREDEV01", unIgnoreOrderDeviceType, "SOME_CHANNEL", map[string]hmproto.ParameterData{
		"SPECIAL_PARAM": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("UNIGNOREDEV01")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("UNIGNOREDEV01:1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	dp := ch.Parameter(hmenum.Parameter("SPECIAL_PARAM"))
	if dp == nil {
		t.Fatal("SPECIAL_PARAM must be stored as a DP — every wire parameter becomes a DP")
	}
	f, ok := dp.(interface {
		ForcedUsage() (hmenum.DataPointUsage, bool)
	})
	if !ok {
		t.Fatal("SPECIAL_PARAM DP does not expose ForcedUsage()")
	}
	if usage, set := f.ForcedUsage(); set && usage == hmenum.DataPointUsageNoCreate {
		t.Fatalf(
			"SPECIAL_PARAM stayed forced to NoCreate after ApplyUnIgnoredMarks ran; "+
				"the custom-DP suppression pass ran before the un-ignore mark was stamped "+
				"(forced_usage=%v set=%v)", usage, set,
		)
	}
}
