// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// minSoundfileIndex / maxSoundfileIndex bound the accepted integer range for
// [ConvertSoundfileIndex]. The upper bound is the highest numbered entry the
// device's own SOUNDFILE VALUE_LIST offers (SOUNDFILE_001 .. SOUNDFILE_252);
// a lower bound silently drops the parameter for every file above it, and the
// player then repeats whatever was selected before.
const (
	minSoundfileIndex = 1
	maxSoundfileIndex = 252
)

// ErrInvalidSoundfileIndex is returned when [ConvertSoundfileIndex] receives
// an integer outside the accepted range.
var ErrInvalidSoundfileIndex = fmt.Errorf("siren: soundfile index out of range (1..%d)", maxSoundfileIndex)

// ErrUnknownSoundfile is returned when a caller names a soundfile the
// device does not offer. It is an error rather than a silent skip: the
// player accepts the surrounding parameters either way, so a dropped
// SOUNDFILE plays the previously selected file and looks like success.
// loom:reachable:reason="wrapped and returned by PlaySound in this package when a caller names a tone the device does not offer, and exported so a caller can match it with errors.Is; the analyzer does not follow sentinel-var references"
var ErrUnknownSoundfile = errors.New("siren: unknown soundfile")

// nonPlayableSoundfiles are the SOUNDFILE VALUE_LIST members that are
// link-profile sentinels rather than playable files: OLD_VALUE restores
// the previous selection and DO_NOT_CARE is the "leave untouched" slot.
// Advertising them as tones offers the operator two entries that play
// nothing.
var nonPlayableSoundfiles = map[string]struct{}{
	"OLD_VALUE":   {},
	"DO_NOT_CARE": {},
}

// ConvertSoundfileIndex converts an integer file index to the
// device-wire label "SOUNDFILE_<NNN>". Returns [ErrInvalidSoundfileIndex]
// when the index is out of range.
func ConvertSoundfileIndex(index int) (string, error) {
	if index < minSoundfileIndex || index > maxSoundfileIndex {
		return "", ErrInvalidSoundfileIndex
	}
	return fmt.Sprintf("SOUNDFILE_%03d", index), nil
}

// SoundPlayer is the HmIP-MP3P channel-2 sound-file playback unit. It wraps a
// sound-file selector (1..189) with optional volume, duration and
// repetitions.
type SoundPlayer struct {
	custom.BaseDP

	Address string

	// Capabilities advertises the sound-player preset surface to
	// north-bound consumers. Always carries `SupportsSoundfiles=true`.
	Capabilities custom.SirenCapabilities

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [NewSoundPlayer].
	payload.ServiceRegistry

	// DataVersionTracker tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every confirmed SOUNDFILE or DIRECTION state
	// transition so DataVersionFilter evaluation correctly detects cluster
	// changes for the SoundPlayer's Matter LevelControl mapping.
	hmtypes.DataVersionTracker

	key    hmtypes.DataPointKey
	writer custom.Writer
	level  *generic.Float // LEVEL (0..1 volume)
	// soundfile is SOUNDFILE. HmIP-MP3P carries it twice: read-only on
	// channel 1 and read+write on channel 2, where the sound player sits
	// — so the resolver builds a Select here, not an index sensor.
	soundfile *generic.Select
	// repetitions is REPETITIONS: a write-only ENUM (OPERATIONS=2) whose
	// VALUE_LIST carries the REPETITIONS_nnn labels, so the resolver builds
	// an ActionSelect for it.
	repetitions  *generic.ActionSelect  // REPETITIONS
	direction    *generic.Sensor[int32] // DIRECTION (UP/DOWN; read-only ENUM, index sensor)
	availableSF  []string
	availableRep []string
}

// DataPointKey returns the composite identifier for this custom data
// point. Satisfies [device.AttachableDataPoint] so the materializer
// can attach the SoundPlayer to a channel.
func (sp *SoundPlayer) DataPointKey() hmtypes.DataPointKey { return sp.key }

// Category reports the HA data-point category — clients spawn the
// entity off this value (siren platform, sound-player variant).
func (sp *SoundPlayer) Category() hmenum.DataPointCategory { return hmenum.DataPointCategorySiren }

// SoundPlayerConfig is the constructor record. The channel must
// already carry the LEVEL / SOUNDFILE / REPETITIONS / DURATION_*
// generic data points.
type SoundPlayerConfig struct {
	Channel *device.Channel
	Writer  custom.Writer
}

// NewSoundPlayer constructs a SoundPlayer.
func NewSoundPlayer(cfg SoundPlayerConfig) *SoundPlayer {
	addr := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		addr = cfg.Channel.Address
		if dev := cfg.Channel.Device(); dev != nil {
			key = hmtypes.DataPointKey{
				InterfaceID:    dev.InterfaceID,
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterSoundfile),
			}
		}
	}
	sp := &SoundPlayer{
		Address: addr,
		Capabilities: custom.SirenCapabilities{
			SupportsSoundfiles: true,
		},
		key:         key,
		writer:      cfg.Writer,
		level:       custom.FloatField(cfg.Channel, hmenum.ParameterLevel),
		soundfile:   custom.SelectField(cfg.Channel, hmenum.ParameterSoundfile),
		repetitions: custom.ActionSelectField(cfg.Channel, hmenum.ParameterRepetitions),
		direction:   custom.EnumSensorField(cfg.Channel, hmenum.ParameterDirection),
	}
	if cfg.Channel != nil {
		if dp := cfg.Channel.Parameter(hmenum.ParameterSoundfile); dp != nil {
			for _, v := range dp.ParameterData().ValueList {
				if _, skip := nonPlayableSoundfiles[v]; skip {
					continue
				}
				sp.availableSF = append(sp.availableSF, v)
			}
		}
		if dp := cfg.Channel.Parameter(hmenum.ParameterRepetitions); dp != nil {
			sp.availableRep = append([]string(nil), dp.ParameterData().ValueList...)
		}
	}
	sp.registerSoundPlayerServices()
	return sp
}

// AvailableLights returns nil for SoundPlayer — the HmIP-MP3P has no optical
// alarm capability. Implemented for interface parity with [Siren] and
// [SmokeSiren] so north-bound adapters can call this method uniformly without
// a type assertion.
func (sp *SoundPlayer) AvailableLights() []string { return nil }

// AvailableSoundfiles returns the playable labels of the SOUNDFILE
// VALUE_LIST — the numbered files plus the device's own generators
// (INTERNAL_SOUNDFILE, RANDOM_SOUNDFILE), without the link-profile
// sentinels.
func (sp *SoundPlayer) AvailableSoundfiles() []string {
	return append([]string(nil), sp.availableSF...)
}

// offersSoundfile reports whether label is one the device advertises.
func (sp *SoundPlayer) offersSoundfile(label string) bool {
	if sp == nil || label == "" {
		return false
	}
	for _, v := range sp.availableSF {
		if v == label {
			return true
		}
	}
	return false
}

// soundfileIndexFor resolves a numbered SOUNDFILE label back to its file
// index. ok is false for a label the device does not offer and for the
// non-numbered entries (INTERNAL_SOUNDFILE, RANDOM_SOUNDFILE), which
// carry no index at all and have to travel as labels.
//
// The inverse is arithmetic, not positional: [ConvertSoundfileIndex]
// builds the label as SOUNDFILE_%03d, so the number in the label *is*
// the index. Deriving it from the position in the VALUE_LIST would
// agree only for a gapless list starting at 001 and would otherwise
// play a different file than the operator picked.
func (sp *SoundPlayer) soundfileIndexFor(label string) (int, bool) {
	if !sp.offersSoundfile(label) {
		return 0, false
	}
	digits, found := strings.CutPrefix(label, "SOUNDFILE_")
	if !found {
		return 0, false
	}
	idx, err := strconv.Atoi(digits)
	if err != nil || idx < minSoundfileIndex || idx > maxSoundfileIndex {
		return 0, false
	}
	return idx, true
}

// AvailableRepetitions returns the labels of accepted REPETITIONS
// values (e.g. "NO_REP", "REPETITIONS_5", "INFINITE").
func (sp *SoundPlayer) AvailableRepetitions() []string {
	return append([]string(nil), sp.availableRep...)
}

// CurrentSoundfile returns the last observed SOUNDFILE label and
// whether it has been observed yet.
func (sp *SoundPlayer) CurrentSoundfile() (string, bool) {
	if sp.soundfile == nil {
		return "", false
	}
	return sp.soundfile.Label()
}

// IsPlaying reports whether the unit is currently playing back a sound file.
func (sp *SoundPlayer) IsPlaying() (playing, observed bool) {
	d, ok := custom.EnumLabelValue(sp.direction)
	if !ok {
		return false, false
	}
	return d == "UP" || d == "DOWN", true
}

// IsStateChange reports whether starting or stopping the sound player
// constitutes a state change relative to the last observed state.
// Returns true when the player has not yet been observed (first command
// always goes through).
func (sp *SoundPlayer) IsStateChange(turnOn bool) bool {
	playing, observed := sp.IsPlaying()
	if !observed {
		return true
	}
	return playing != turnOn
}

// maxRepetitionsIndex is the highest allowed value for
// [PlayConfig.RepetitionsIndex].
const maxRepetitionsIndex = 18

// RepetitionsIndexNotSet is the sentinel value for [PlayConfig.RepetitionsIndex]
// that means "do not write the REPETITIONS parameter". Use this when the caller
// wants to trigger playback without overriding the device's current repetitions
// setting. Values in the range -1..18 are valid write targets; any other value
// is treated as RepetitionsIndexNotSet.
const RepetitionsIndexNotSet = -2

// ConvertPlayRepetitionsIndex maps the logical repetitions integer to the
// REPETITIONS wire label expected by the CCU.
//
//   - -1 → infinite repetitions (last entry in the device's REPETITIONS
//     VALUE_LIST, conventionally "INFINITE" or "INFINITE_REPETITIONS")
//   - 0  → no repeat (first entry, conventionally "NO_REP" or
//     "NO_REPETITION")
//   - 1..18 → play N+1 times (second through nineteenth entry)
//
// The concrete label is looked up from availableRep so the returned string
// always matches the device's own VALUE_LIST verbatim. Returns an empty string
// and a non-nil error when the list is empty or index is out of range.
func ConvertPlayRepetitionsIndex(index int, availableRep []string) (string, error) {
	if len(availableRep) == 0 {
		return "", errors.New("siren: ConvertPlayRepetitionsIndex: REPETITIONS list is empty")
	}
	var slot int
	switch {
	case index == 0:
		slot = 0
	case index == -1:
		slot = len(availableRep) - 1
	case index >= 1 && index <= maxRepetitionsIndex:
		slot = index
	default:
		return "", fmt.Errorf("siren: ConvertPlayRepetitionsIndex: index must be -1 (infinite), 0 (none), or 1-%d, got %d", maxRepetitionsIndex, index)
	}
	if slot >= len(availableRep) {
		slot = len(availableRep) - 1
	}
	return availableRep[slot], nil
}

// PlayConfig bundles the optional fields a [PlaySound] call accepts.
type PlayConfig struct {
	// SoundfileIndex is the 1-based file index.
	SoundfileIndex int
	// SoundfileLabel names the wire value verbatim and takes precedence
	// over SoundfileIndex. It is the only way to reach the entries that
	// carry no index — INTERNAL_SOUNDFILE and RANDOM_SOUNDFILE — which
	// is what Home Assistant sends back when the operator picks one out
	// of the advertised `available_tones`.
	SoundfileLabel string
	// Volume is the playback level 0..1.
	Volume float64
	// Duration limits the total playback time. Encoded as
	// DURATION_VALUE / DURATION_UNIT.
	Duration time.Duration
	// RampTime ramps the volume over the given duration (RAMP_TIME).
	RampTime time.Duration
	// RepetitionsIndex controls how often the sound repeats:
	//   -1 → play indefinitely (unlimited repetitions)
	//    0 → play once, no repeat
	//  1..18 → play N+1 times
	// Values outside -1..18 are ignored (no REPETITIONS write).
	// When Loop is true, RepetitionsIndex is overridden to -1 (infinite).
	RepetitionsIndex int
	// Loop requests unlimited repetitions. When true, the effective
	// RepetitionsIndex is forced to -1 regardless of the field above.
	// Convenience shorthand for RepetitionsIndex = -1.
	Loop bool
}

// PlaySound triggers playback as a single atomic put_paramset bundle of every
// populated parameter (SOUNDFILE + REPETITIONS + DURATION_* + RAMP_TIME_* +
// LEVEL). Falls back to sequential SetValue when the writer has no
// PutParamset.
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (sp *SoundPlayer) PlaySound(
	ctx context.Context, cfg PlayConfig, priority hmenum.CommandPriority,
) (err error) {
	if sp.writer == nil {
		return errors.New("soundplayer: writer required")
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(sp.writer), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	const defaultVolume = 0.5
	params := make(map[hmenum.Parameter]any, 6)
	switch {
	case cfg.SoundfileLabel != "":
		if !sp.offersSoundfile(cfg.SoundfileLabel) {
			return fmt.Errorf("%w %q", ErrUnknownSoundfile, cfg.SoundfileLabel)
		}
		params[hmenum.ParameterSoundfile] = cfg.SoundfileLabel
	case cfg.SoundfileIndex >= minSoundfileIndex && cfg.SoundfileIndex <= maxSoundfileIndex:
		sfLabel, err := ConvertSoundfileIndex(cfg.SoundfileIndex)
		if err != nil {
			return fmt.Errorf("soundplayer: play: %w", err)
		}
		params[hmenum.ParameterSoundfile] = sfLabel
	}
	repIdx := cfg.RepetitionsIndex
	if cfg.Loop {
		repIdx = -1
	}
	if repIdx >= -1 && repIdx <= maxRepetitionsIndex {
		if label, err := ConvertPlayRepetitionsIndex(repIdx, sp.availableRep); err == nil {
			params[hmenum.ParameterRepetitions] = label
		}
	}
	// DURATION is only included when the caller explicitly requests it — the
	// device has its own default on-time and overriding it unconditionally
	// changes device behaviour. Mirrors the reference where on_time is only
	// sent when _dp_on_time exists on the channel.
	if cfg.Duration > 0 {
		dv, du := custom.EncodeTimerDuration(cfg.Duration)
		params[hmenum.ParameterDurationUnit] = du
		params[hmenum.ParameterDurationValue] = dv
	}
	if cfg.RampTime > 0 {
		v, u := custom.EncodeTimerDuration(cfg.RampTime)
		params[hmenum.ParameterRampTimeUnit] = u
		params[hmenum.ParameterRampTimeValue] = v
	}
	volume := cfg.Volume
	if volume <= 0 {
		volume = defaultVolume
	}
	params[hmenum.ParameterLevel] = volume
	if err = custom.PutOrSet(ctx, sp.writer, sp.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
		return fmt.Errorf("soundplayer: play: %w", err)
	}
	return nil
}

// StopSound stops playback by atomically writing LEVEL=0 and DURATION=0 as a
// single put_paramset bundle. Both DURATION_VALUE and DURATION_UNIT must be
// written together so the CombinedTimerField on the device side sees a
// consistent pair.
func (sp *SoundPlayer) StopSound(ctx context.Context, priority hmenum.CommandPriority) (err error) {
	if sp.writer == nil {
		return errors.New("soundplayer: writer required")
	}
	ctx = custom.EnsureContext(ctx)
	coll := generic.NewCollector(generic.WriterAsBackend(sp.writer), generic.WithPriority(priority))
	ctx = generic.ContextWithCollector(ctx, coll)
	// Anything staged on the collector only reaches the wire in the
	// flush, so its error is part of this command's result.
	defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	params := map[hmenum.Parameter]any{
		hmenum.ParameterLevel:         float64(0),
		hmenum.ParameterDurationValue: int32(0),
		hmenum.ParameterDurationUnit:  int32(0),
	}
	if err = custom.PutOrSet(ctx, sp.writer, sp.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
		return fmt.Errorf("soundplayer: stop: %w", err)
	}
	return nil
}

// TurnOff is a wrapper around [StopSound] preserving the Siren API.
func (sp *SoundPlayer) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	return sp.StopSound(ctx, priority)
}

// Subscribe wires the channel's sound-player state parameters into
// the SoundPlayer. SoundPlayer has no hot-path aggregate cache (each
// accessor reads directly from the embedded wire DPs) so the
// OnAnyUpdate hooks have no-op bodies — they only need to exist so
// the EventBridge's publishCustomDPState path can re-fire on every
// wire-side change.
//
// Each concrete pointer field is guarded individually — not via an
// interface wrapper — to avoid the typed-nil-via-interface pitfall.
//
// Implements [device.SubscribingDataPoint].
func (sp *SoundPlayer) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	if sp.level != nil {
		unsubs = append(unsubs, sp.level.OnAnyUpdate(func(_, _ any) {}))
	}
	if sp.soundfile != nil {
		// Bump the DataVersion on every confirmed SOUNDFILE change so that
		// Matter DataVersionFilter evaluation picks up playback-state transitions.
		// Mirrors the OnConfirmedUpdate hook in switchdev.New (switch/switch.go).
		unsubs = append(unsubs, sp.soundfile.OnConfirmedUpdate(func(_, _ int32) {
			sp.Bump()
		}))
	}
	if sp.repetitions != nil {
		unsubs = append(unsubs, sp.repetitions.OnAnyUpdate(func(_, _ any) {}))
	}
	if sp.direction != nil {
		// Also bump on DIRECTION changes — UP/DOWN encodes whether the player
		// is currently active, which maps to the Matter LevelControl OnOff bit.
		unsubs = append(unsubs, sp.direction.OnConfirmedUpdate(func(_, _ int32) {
			sp.Bump()
		}))
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}
