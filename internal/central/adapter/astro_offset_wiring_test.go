// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// astroOffsetMasterDP builds the MASTER-paramset ASTRO_OFFSET descriptor a
// device declares for slot 1.
func astroOffsetMasterDP(address string, minV, maxV int) *generic.Integer {
	lo, _ := json.Marshal(minV)
	hi, _ := json.Marshal(maxV)
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      "01_WP_ASTRO_OFFSET",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        lo,
			Max:        hi,
		},
	})
}

// TestAstroOffsetLimitsComeFromTheChannelDescriptor pins the seam rather than
// the helper: the bounds a schedule write is checked against must be the ones
// the channel declares, resolved from the live model.
//
// The CCU's own weekly-program editor carries no constant here — it reads
// ASTRO_OFFSET_MIN / ASTRO_OFFSET_MAX out of the paramset description and
// clamps its input to them.
func TestAstroOffsetLimitsComeFromTheChannelDescriptor(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("ABC0001:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.PutMaster(astroOffsetMasterDP("ABC0001:1", -128, 127))

	got := astroOffsetLimits(ch)
	if !got.Declared {
		t.Fatal("channel declares ASTRO_OFFSET MIN/MAX, but the limits were not resolved")
	}
	if got.Min != -128 || got.Max != 127 {
		t.Errorf("limits = %d..%d, want -128..127", got.Min, got.Max)
	}
}

// TestAstroOffsetLimitsUndeclaredWhenTheChannelCarriesNoDescriptor pins that a
// channel without the parameter reports nothing declared, so the caller falls
// back rather than inventing a range.
func TestAstroOffsetLimitsUndeclaredWhenTheChannelCarriesNoDescriptor(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0002"})
	ch := d.AddChannel("ABC0002:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)

	if got := astroOffsetLimits(ch); got.Declared {
		t.Errorf("no descriptor, but limits reported as declared: %+v", got)
	}
	if got := astroOffsetLimits(nil); got.Declared {
		t.Errorf("nil channel, but limits reported as declared: %+v", got)
	}
}

// astroPutRecorder captures the paramset a save would put on the wire.
type astroPutRecorder struct {
	puts []map[string]any
}

func (w *astroPutRecorder) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	return nil
}

func (w *astroPutRecorder) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	w.puts = append(w.puts, values)
	return nil
}

// TestSaveRejectsAnAstroOffsetTheChannelDoesNotDeclare pins the seam through
// the production save path, not through the helper: a schedule whose offset
// lies outside the channel's declared ASTRO_OFFSET range must not reach the
// wire. A test that called astroOffsetLimits directly would still pass if the
// saver stopped passing the resolved limits along.
func TestSaveRejectsAnAstroOffsetTheChannelDoesNotDeclare(t *testing.T) {
	t.Parallel()

	newSaver := func() (*defaultChannelSaver, *astroPutRecorder) {
		d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0003"})
		ch := d.AddChannel("ABC0003:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
		ch.PutMaster(astroOffsetMasterDP("ABC0003:1", -128, 127))
		w := &astroPutRecorder{}
		ch.SetWriter(w)
		return &defaultChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}, w
	}

	// 200 minutes is inside the old ±720 bound and outside what the device
	// declares, so it separates the two rules.
	sv, w := newSaver()
	err := sv.Save(context.Background(), astroScheduleFor(200))
	if err == nil {
		t.Fatal("an offset outside the declared range reached the wire")
	}
	if len(w.puts) != 0 {
		t.Errorf("rejected save still wrote %d paramset(s)", len(w.puts))
	}

	// A declared offset still goes through.
	sv, w = newSaver()
	if err := sv.Save(context.Background(), astroScheduleFor(120)); err != nil {
		t.Fatalf("an offset inside the declared range was rejected: %v", err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("accepted save wrote %d paramset(s), want 1", len(w.puts))
	}
	if got := w.puts[0]["01_WP_ASTRO_OFFSET"]; got != 120 {
		t.Errorf("wire value = %v, want 120", got)
	}
}

func astroScheduleFor(offset int) *schedule.Simple {
	return &schedule.Simple{
		Entries: map[int]schedule.SimpleEntry{
			1: {
				Weekdays:           []schedule.Weekday{schedule.WeekdayMonday},
				Time:               "00:00",
				Condition:          schedule.ConditionAstro,
				AstroType:          "sunset",
				AstroOffsetMinutes: offset,
				Level:              1,
			},
		},
	}
}
