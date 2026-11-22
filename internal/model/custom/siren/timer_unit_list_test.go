// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unitOrdinal is the wire ordinal of label: its position in the list the
// channel declares, read from the list rather than assumed.
func unitOrdinal(t *testing.T, list []string, label string) int32 {
	t.Helper()
	i := slices.Index(list, label)
	if i < 0 {
		t.Fatalf("VALUE_LIST %v has no %q", list, label)
	}
	return int32(i) //nolint:gosec // a four-entry list
}

func assertStubPair(t *testing.T, w *stubWriter, valueParam, unitParam hmenum.Parameter, wantValue, wantUnit int32) {
	t.Helper()
	v, vok := w.has(valueParam)
	u, uok := w.has(unitParam)
	if !vok || !uok || v != wantValue || u != wantUnit {
		t.Errorf("%s/%s = (%v, %v), want (%d, %d)", valueParam, unitParam, v, u, wantValue, wantUnit)
	}
}

// asir2DurationUnits is DURATION_UNIT's VALUE_LIST on HmIP-ASIR-2 channel 3
// in godevccu's captured paramset descriptions: a write-only ENUM without
// 10MS, next to a write-only INTEGER DURATION_VALUE (MAX 16343).
var asir2DurationUnits = []string{"S", "M", "H"}

func newDurationSiren(t *testing.T, w Writer) *Siren {
	t.Helper()
	const addr = "VCU8249617:3"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU8249617", Model: "HmIP-ASIR-2"})
	ch := d.AddChannel(addr, 3, "SIREN", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)}
	}
	ch.Put(generic.NewInteger(generic.Spec{
		Key:        key(hmenum.ParameterDurationValue),
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsWrite, Default: []byte("0")},
		Writer:     w,
	}))
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: key(hmenum.ParameterDurationUnit),
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsWrite,
			ValueList: asir2DurationUnits, Default: []byte(`"S"`),
		},
		Writer: w,
	}))
	return New(Config{Channel: ch, Writer: w, Capabilities: custom.SirenCapabilities{SupportsDuration: true}})
}

// TestSirenDurationRoundsWithinItsChannelsUnitList drives Siren.TurnOn on
// the HmIP-ASIR-2 duration pair. The siren's list ends at H, so a
// sub-second duration rounds to one second instead of collapsing to a zero
// count, and a promoted duration rounds to its nearest minute.
func TestSirenDurationRoundsWithinItsChannelsUnitList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d         time.Duration
		wantValue int32
		wantUnit  string
	}{
		{500 * time.Millisecond, 1, "S"},
		{1500 * time.Millisecond, 2, "S"},
		{16373 * time.Second, 273, "M"},
	}
	for _, c := range cases {
		w := &stubWriter{}
		s := newDurationSiren(t, w)
		if err := s.TurnOn(context.Background(), OnConfig{Duration: c.d}, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("TurnOn(%v): %v", c.d, err)
		}
		u, _ := w.has(hmenum.ParameterDurationUnit)
		if ord, ok := u.(int32); ok && int(ord) >= len(asir2DurationUnits) {
			t.Errorf("TurnOn(%v) wrote DURATION_UNIT %d, outside the channel's list %v", c.d, ord, asir2DurationUnits)
		}
		assertStubPair(t, w, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit,
			c.wantValue, unitOrdinal(t, asir2DurationUnits, c.wantUnit))
	}
}

// newFleetSoundPlayerWithTimers builds the HmIP-MP3P sound-player channel
// from the device's own VALUES descriptor (testdata, VCU1543608:2) with
// both timer pairs materialised from that descriptor, so the unit lists
// the encoder sees are the captured ones.
func newFleetSoundPlayerWithTimers(t *testing.T) (sp *SoundPlayer, w *stubWriter, units map[hmenum.Parameter][]string) {
	t.Helper()
	const channelAddress = "VCU1543608:2"
	raw, err := os.ReadFile("testdata/hmip_mp3p_sound_receiver_values.json")
	if err != nil {
		t.Fatalf("read sound-player descriptor: %v", err)
	}
	var descriptors map[string]hmproto.ParameterData
	if err := json.Unmarshal(raw, &descriptors); err != nil {
		t.Fatalf("decode sound-player descriptor: %v", err)
	}
	w = &stubWriter{}
	dev := device.New(device.Config{
		Address: "VCU1543608", InterfaceID: "HmIP-RF",
		Interface: hmenum.InterfaceHmIPRF, Model: "HmIP-MP3P",
	})
	ch := dev.AddChannel(channelAddress, 2, "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{
			InterfaceID: dev.InterfaceID, ChannelAddress: channelAddress,
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p),
		}
	}
	desc := func(p hmenum.Parameter) hmproto.ParameterData {
		d, ok := descriptors[string(p)]
		if !ok {
			t.Fatalf("descriptor has no %s", p)
		}
		return d
	}
	ch.Put(generic.NewSelect(generic.Spec{Key: key(hmenum.ParameterSoundfile), Descriptor: desc(hmenum.ParameterSoundfile), Writer: w}))
	ch.Put(generic.NewFloat(generic.Spec{Key: key(hmenum.ParameterLevel), Descriptor: desc(hmenum.ParameterLevel), Writer: w}))
	ch.Put(generic.NewActionSelect(generic.Spec{Key: key(hmenum.ParameterRepetitions), Descriptor: desc(hmenum.ParameterRepetitions), Writer: w}))
	units = map[hmenum.Parameter][]string{}
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterRampTimeValue} {
		ch.Put(generic.NewInteger(generic.Spec{Key: key(p), Descriptor: desc(p), Writer: w}))
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationUnit, hmenum.ParameterRampTimeUnit} {
		d := desc(p)
		units[p] = d.ValueList
		ch.Put(generic.NewActionSelect(generic.Spec{Key: key(p), Descriptor: d, Writer: w}))
	}
	return NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: w}), w, units
}

// TestSoundPlayerTimersUseTheCapturedUnitLists drives PlaySound on the
// captured HmIP-MP3P sound channel, whose DURATION_UNIT and RAMP_TIME_UNIT
// both declare 10MS: a sub-second play time and ramp go out as hundredths
// of a second.
func TestSoundPlayerTimersUseTheCapturedUnitLists(t *testing.T) {
	t.Parallel()
	sp, w, units := newFleetSoundPlayerWithTimers(t)
	err := sp.PlaySound(context.Background(), PlayConfig{
		SoundfileIndex: 1, Volume: 0.5,
		Duration: 500 * time.Millisecond, RampTime: 1500 * time.Millisecond,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("PlaySound: %v", err)
	}
	assertStubPair(t, w, hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit,
		50, unitOrdinal(t, units[hmenum.ParameterDurationUnit], "10MS"))
	assertStubPair(t, w, hmenum.ParameterRampTimeValue, hmenum.ParameterRampTimeUnit,
		150, unitOrdinal(t, units[hmenum.ParameterRampTimeUnit], "10MS"))
}
