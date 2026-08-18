// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newFleetSoundPlayer builds the HmIP-MP3P sound-player channel from the
// device's own VALUES paramset descriptor (testdata, extracted verbatim
// from the simulator's embedded description for VCU1543608:2).
//
// The hand-built rig in siren_test.go offers three soundfiles. Every
// question about which tones actually reach the device is unanswerable
// on such a list — the real SOUNDFILE VALUE_LIST has 256 entries, of
// which four are not numbered files at all.
func newFleetSoundPlayer(t *testing.T) (*SoundPlayer, *stubWriter) {
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
	sf, ok := descriptors[string(hmenum.ParameterSoundfile)]
	if !ok || len(sf.ValueList) < 200 {
		t.Fatalf("descriptor carries %d SOUNDFILE entries — the fixture is not the sound-player channel",
			len(sf.ValueList))
	}

	w := &stubWriter{}
	dev := device.New(device.Config{
		Address: "VCU1543608", InterfaceID: "HmIP-RF",
		Interface: hmenum.InterfaceHmIPRF, Model: "HmIP-MP3P",
	})
	ch := dev.AddChannel(channelAddress, 2, "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	key := func(p hmenum.Parameter) hmtypes.DataPointKey {
		return hmtypes.DataPointKey{
			InterfaceID:    dev.InterfaceID,
			ChannelAddress: channelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		}
	}
	ch.Put(generic.NewSelect(generic.Spec{
		Key: key(hmenum.ParameterSoundfile), Descriptor: sf, Writer: w,
	}))
	ch.Put(generic.NewFloat(generic.Spec{
		Key:        key(hmenum.ParameterLevel),
		Descriptor: descriptors[string(hmenum.ParameterLevel)],
		Writer:     w,
	}))
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key:        key(hmenum.ParameterRepetitions),
		Descriptor: descriptors[string(hmenum.ParameterRepetitions)],
		Writer:     w,
	}))

	return NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: w}), w
}

// resetStubWriter drops every recorded write so the next command can be
// inspected on its own.
func resetStubWriter(w *stubWriter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = nil
}

// TestEveryAdvertisedToneReachesTheDevice drives the real turn_on
// service with every tone the player advertises to Home Assistant and
// asserts each one is written to SOUNDFILE.
//
// A tone that does not reach the wire is invisible: the command still
// succeeds because volume, duration and repetitions are written anyway,
// and the device simply repeats whatever file was selected before.
func TestEveryAdvertisedToneReachesTheDevice(t *testing.T) {
	t.Parallel()

	sp, rec := newFleetSoundPlayer(t)
	tones := sp.AvailableSoundfiles()
	if len(tones) < 200 {
		t.Fatalf("the player advertises %d tones — the fixture is not the real VALUE_LIST", len(tones))
	}
	for _, tone := range tones {
		resetStubWriter(rec)
		if err := sp.Invoke(context.Background(), "turn_on",
			map[string]any{"tone": tone}, hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("turn_on(tone=%s): %v", tone, err)
			continue
		}
		if got := lastStringWrite(rec, hmenum.ParameterSoundfile); got != tone {
			t.Errorf("tone %q reached the wire as %q — the daemon advertises this tone and then drops "+
				"the SOUNDFILE parameter, so the device replays the previous file", tone, got)
		}
	}
}

// TestAdvertisedTonesExcludeTheLinkProfileSentinels pins that the two
// VALUE_LIST members that are not playable files stay out of the tone
// list. OLD_VALUE restores the previous selection and DO_NOT_CARE means
// "leave this parameter alone"; both belong to the link-profile encoding
// and would be dead entries in the operator's tone picker.
func TestAdvertisedTonesExcludeTheLinkProfileSentinels(t *testing.T) {
	t.Parallel()

	sp, _ := newFleetSoundPlayer(t)
	tones := sp.AvailableSoundfiles()
	for _, sentinel := range []string{"OLD_VALUE", "DO_NOT_CARE"} {
		if slices.Contains(tones, sentinel) {
			t.Errorf("%s is advertised as a playable tone", sentinel)
		}
	}
	// The device's own generators are playable and must survive.
	for _, want := range []string{"INTERNAL_SOUNDFILE", "RANDOM_SOUNDFILE", "SOUNDFILE_001", "SOUNDFILE_252"} {
		if !slices.Contains(tones, want) {
			t.Errorf("%s is missing from the advertised tones", want)
		}
	}
}

// TestPlaySoundRejectsAToneTheDeviceDoesNotOffer pins that an unknown
// label is an error rather than a silently omitted parameter.
func TestPlaySoundRejectsAToneTheDeviceDoesNotOffer(t *testing.T) {
	t.Parallel()

	sp, rec := newFleetSoundPlayer(t)
	err := sp.PlaySound(context.Background(),
		PlayConfig{SoundfileLabel: "SOUNDFILE_999", RepetitionsIndex: RepetitionsIndexNotSet},
		hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrUnknownSoundfile) {
		t.Errorf("PlaySound with an unknown tone returned %v, want %v", err, ErrUnknownSoundfile)
	}
	if got := lastStringWrite(rec, hmenum.ParameterSoundfile); got != "" {
		t.Errorf("an unknown tone reached the wire as %q", got)
	}
}

// TestPlaySoundAcceptsEveryNumberedFileTheDeviceOffers drives the
// numeric form — the one the alarm chirp output uses — across the full
// range of numbered files the device's VALUE_LIST carries.
func TestPlaySoundAcceptsEveryNumberedFileTheDeviceOffers(t *testing.T) {
	t.Parallel()

	sp, rec := newFleetSoundPlayer(t)
	for _, tone := range sp.AvailableSoundfiles() {
		idx, ok := sp.soundfileIndexFor(tone)
		if !ok {
			continue // INTERNAL_SOUNDFILE / RANDOM_SOUNDFILE carry no index
		}
		resetStubWriter(rec)
		if err := sp.PlaySound(context.Background(),
			PlayConfig{SoundfileIndex: idx, RepetitionsIndex: RepetitionsIndexNotSet},
			hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("PlaySound(index=%d): %v", idx, err)
			continue
		}
		if got := lastStringWrite(rec, hmenum.ParameterSoundfile); got != tone {
			t.Errorf("file index %d reached the wire as %q, want %q", idx, got, tone)
		}
	}
}
