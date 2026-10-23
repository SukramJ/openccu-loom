// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"fmt"
	"time"

	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: SoundPlayer participates in the Matter source
// surface (ADR 0012) as a Speaker (0x0022) device; soundRamp is the
// device port behind go-fabric's native level source, which the kit
// builds on the declared ramp.
var (
	_ interfaces.MatterDeviceSource   = (*SoundPlayer)(nil)
	_ interfaces.MatterChangeNotifier = (*SoundPlayer)(nil)
	_ levelcontroldef.NativeRamp      = soundRamp{}
	_ levelcontroldef.NativeMover     = soundRamp{}
	_ levelcontroldef.NativeSwitcher  = soundRamp{}
)

// matterDeviceTypeSpeaker is the Matter Speaker device type. It mandates
// OnOff and LevelControl servers and nothing else — matter.js
// packages/model/src/standard/elements/speaker.element.ts:12-19 lists both
// requirements with conformance "M" and declares no feature requirement on
// either, so the OnOff cluster here advertises an empty FeatureMap rather
// than the LT baseline the OnOffPlugInUnit projections carry.
const matterDeviceTypeSpeaker = uint16(fabricdevice.SpeakerID)

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (sp *SoundPlayer) MatterDeviceType() uint16 { return matterDeviceTypeSpeaker }

// MatterDevice implements [interfaces.MatterDeviceSource]: a Speaker
// with OnOff and LevelControl declared, built once per player.
//
// OnOff is go-fabric's server without the LT feature: the Speaker device
// type (speaker.element.ts:18-19) reads OnOff as mute / unmute, mandates no
// Lighting feature, and so the cluster carries OnOff, Off, On and Toggle
// only. The HmIP-MP3P carries no mute parameter, and it does not need
// one: the whole of its audio output is one LEVEL knob, and
// [SoundPlayer.StopSound] silences the player by writing LEVEL=0. Muted
// and silent are therefore the same device state here, and both halves
// of the projection are backed by real wire values — the attribute
// follows DIRECTION (UP/DOWN = a file is being played, so sound is coming
// out) through the confirmed-update hook and [generic.SetMatterOnOff],
// and the Sink is the profile's own play and stop paths. Nothing is
// remembered or invented on either side. What the cluster deliberately
// does not carry is *which* file plays: that is SOUNDFILE, which has no
// Matter cluster; On resumes with whatever the device last selected.
//
// LevelControl runs on the hand-off path of go-fabric's server over
// [soundRamp] (levelcontroldef.Declare{Ramp}): the kit's native source
// keeps Options and OnLevel — Matter's own bookkeeping, with no
// HmIP-MP3P parameter behind either — applies the ExecuteIfOff gate,
// turns each command into one target level, and hands a "with On/Off"
// command that starts or stops the player to the ramp as one switch.
// The OO feature is selected as the server advertised it before, so the
// kit couples the "with On/Off" commands to the OnOff server
// (notes/parity/by_design.md BD-Matter-LevelControl-NativeRamp).
func (sp *SoundPlayer) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return sp.matterDevice.Device(func() (*fabricdevice.Device, error) {
		playing, _ := sp.IsPlaying()
		d, err := generic.NewMatterDevice(sp, matterDeviceTypeSpeaker, opts,
			generic.MatterOnOffDeclaration(false, playing, func(ctx context.Context, on bool) error {
				if on {
					return sp.matterPlay(ctx)
				}
				return sp.StopSound(ctx, matterDispatchPriority)
			}),
			levelcontroldef.Declare{Ramp: soundRamp{sp: sp}, Features: levelcontroldef.FeatureOnOff})
		if err != nil {
			return nil, err
		}
		if sp.direction != nil {
			_ = sp.direction.OnConfirmedUpdate(func(_, _ int32) {
				playing, _ := sp.IsPlaying()
				generic.SetMatterOnOff(d, playing)
			})
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if playing, observed := sp.IsPlaying(); observed {
			generic.SetMatterOnOff(d, playing)
		}
		return d, nil
	})
}

// MatterEligibility marks SoundPlayer as partially mappable. Volume and the
// audible/silent state map onto the Speaker device type in full; the sound
// file and the repetition count — the two things that decide *what* plays —
// have no Matter cluster and stay MQTT-only, per ADR 0012 §5 ("out of Matter
// scope").
func (sp *SoundPlayer) MatterEligibility() interfaces.MatterEligibilityVerdict {
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: matterDeviceTypeSpeaker,
		Clusters:   []uint32{onoffdef.ClusterID, levelcontroldef.ClusterID},
		Reason:     "Speaker covers volume (LevelControl) and audible/silent (OnOff); soundfile and repetition selection have no Matter cluster and stay MQTT-only.",
	}
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]. LEVEL
// carries the volume the LevelControl cluster reports and DIRECTION carries
// whether the player is audible, which is the OnOff attribute; fan both in so
// a file started or a volume turned at the device — or by a CCU program —
// dirty-marks the endpoint and reaches a controller's Subscribe rather than
// waiting for its next read. The library LevelControl server forwards this
// same notifier for its own cluster.
func (sp *SoundPlayer) OnMatterValueChanged(cb func()) func() {
	if sp == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		sp.level.OnMatterValueChanged(cb),
		sp.direction.OnMatterValueChanged(cb),
	)
}

// matterPlay starts playback at the volume the device currently reports, or
// lets [SoundPlayer.PlaySound] apply its own default when nothing has been
// observed. Passing the observed level back is what keeps On/Off from acting
// as a volume reset; passing zero when the level is unknown avoids inventing
// one here, where the profile already has an answer.
func (sp *SoundPlayer) matterPlay(ctx context.Context) error {
	volume, ok := sp.matterVolume()
	if !ok {
		volume = 0
	}
	return sp.playVolume(ctx, volume)
}

// playVolume triggers playback with volume and nothing else: no SOUNDFILE (the
// device keeps its selection), no REPETITIONS ([RepetitionsIndexNotSet]), no
// duration and no ramp. A Matter controller has no vocabulary for any of them.
func (sp *SoundPlayer) playVolume(ctx context.Context, volume float64) error {
	return sp.PlaySound(ctx, PlayConfig{
		Volume:           volume,
		RepetitionsIndex: RepetitionsIndexNotSet,
	}, matterDispatchPriority)
}

// ─── LevelControl (0x0008), device port ──────────────────────────────

// matterVolume reports the observed LEVEL as the float the write paths take.
func (sp *SoundPlayer) matterVolume() (float64, bool) {
	if sp == nil || sp.level == nil {
		return 0, false
	}
	return sp.level.Value()
}

// soundRamp is the [levelcontroldef.NativeRamp] of a [SoundPlayer]: the
// player's LEVEL knob, which is its volume. The HmIP-MP3P has no
// controller-driven volume ramp — RAMP_TIME accompanies a play command,
// not a volume change — so every level is applied at once.
type soundRamp struct{ sp *SoundPlayer }

// Level implements [levelcontroldef.NativeRamp]: the HM 0..1 LEVEL on the
// 0..254 scale. known=false while LEVEL has never been observed, which the
// server encodes as TLV null — CurrentLevel carries quality X (matter.js
// level-control.element.ts:29-32), so an unread volume is reported as
// unknown rather than as the real level 0.
func (r soundRamp) Level() (uint8, bool) {
	v, ok := r.sp.matterVolume()
	if !ok {
		return 0, false
	}
	return levelcontroldef.LevelFromUnit(v), true
}

// Ramp implements [levelcontroldef.NativeRamp]: LEVEL alone, at once (d is
// ignored, see [soundRamp]), never starting or stopping playback. A "with
// On/Off" command reaches it only while the player is already playing and
// the target lies above the minimum; starting and stopping go through
// [soundRamp.SwitchOn] and [soundRamp.SwitchOff].
func (r soundRamp) Ramp(ctx context.Context, level uint8, _ time.Duration, _ bool) error {
	return r.sp.setVolume(ctx, levelcontroldef.UnitFromLevel(level))
}

// SwitchOn implements [levelcontroldef.NativeSwitcher]: a "with On/Off"
// command that starts the player starts it at the target volume in one
// write, the profile's own play path. d is ignored, see [soundRamp]. The
// OnOff attribute is not set here: it follows DIRECTION once the device
// confirms playback (see [SoundPlayer.matterOnOffServer]).
func (r soundRamp) SwitchOn(ctx context.Context, level uint8, _ time.Duration) error {
	return r.sp.playVolume(ctx, levelcontroldef.UnitFromLevel(level))
}

// SwitchOff implements [levelcontroldef.NativeSwitcher]: a "with On/Off"
// command that ends at the minimum is silence, so it stops the player in
// one write — [SoundPlayer.StopSound], which carries LEVEL=0 with the
// cleared DURATION pair. As with SwitchOn, the OnOff attribute follows
// the device's DIRECTION confirmation.
func (r soundRamp) SwitchOff(ctx context.Context, _ time.Duration) error {
	return r.sp.StopSound(ctx, matterDispatchPriority)
}

// Stop implements [levelcontroldef.NativeRamp]. No ramp is ever running (see
// [soundRamp.Ramp]), so there is nothing to halt. It is NOT the OnOff Off
// path — stopping a level transition and silencing the speaker are
// different operations, and mapping this one onto StopSound would silence
// a speaker whenever a controller ended a fade.
func (soundRamp) Stop(context.Context) error { return nil }

// MoveLevel implements [levelcontroldef.NativeMover]: Move and MoveWithOnOff
// keep the volume where it is. The source's default would jump a speaker
// to full volume or to silence, because a device that cannot ramp reaches
// the bound at once; matter.js lets an implementation replace that
// default, moveLogic (LevelControlServer.ts:378-403), and a continuous
// move has no reading on this player.
func (soundRamp) MoveLevel(context.Context, levelcontroldef.MoveRequest, bool) error { return nil }

// OnMatterValueChanged forwards [SoundPlayer.OnMatterValueChanged], so the
// LevelControl server dirty-marks on a volume or playback change at the
// device, as it did while the player was its source.
func (r soundRamp) OnMatterValueChanged(cb func()) func() {
	return r.sp.OnMatterValueChanged(cb)
}

func (sp *SoundPlayer) setVolume(ctx context.Context, volume float64) error {
	if sp == nil || sp.level == nil {
		return fmt.Errorf("soundplayer: set volume: channel has no %s data point", hmenum.ParameterLevel)
	}
	return sp.level.Set(custom.EnsureContext(ctx), volume, matterDispatchPriority)
}
