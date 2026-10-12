// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"fmt"
	"sync"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"
	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: SoundPlayer participates in the Matter source
// surface (ADR 0012) as a Speaker (0x0022) endpoint and is the narrow host
// port behind the library's LevelControl server.
var (
	_ interfaces.MatterEndpointSource = (*SoundPlayer)(nil)
	_ interfaces.MatterChangeNotifier = (*SoundPlayer)(nil)
	_ levelcontrol.LevelSource        = (*SoundPlayer)(nil)
)

// matterDeviceTypeSpeaker is the Matter Speaker device type. It mandates
// OnOff and LevelControl servers and nothing else — matter.js
// packages/model/src/standard/elements/speaker.element.ts:12-19 lists both
// requirements with conformance "M" and declares no feature requirement on
// either, so the OnOff cluster here advertises an empty FeatureMap rather
// than the LT baseline the OnOffPlugInUnit projections carry.
const matterDeviceTypeSpeaker uint16 = 0x0022

// speakerLevelState holds the two LevelControl attributes that describe the
// cluster rather than the device: Options and OnLevel. Neither has an
// HmIP-MP3P parameter behind it — they are Matter's own bookkeeping, which
// matter.js likewise keeps in cluster state — so they live on the data point
// and survive the server reconstruction that every topology assembly and
// every eligibility query performs.
type speakerLevelState struct {
	mu      sync.RWMutex
	options uint8
	// onLevel is nil for the spec's null reading, "OnLevel has no effect".
	onLevel *uint8
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (sp *SoundPlayer) MatterDeviceType() uint16 { return matterDeviceTypeSpeaker }

// MatterClusterServers implements [interfaces.MatterEndpointSource]:
// OnOff and LevelControl, both go-fabric servers built once per player
// (the bridge keeps a server it already serves across reassemblies; a
// server rebuilt each time would lose its data version and timers).
func (sp *SoundPlayer) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{
		sp.matterOnOffServer(),
		sp.matterLevelServer(),
	}
}

// matterLevelServer builds the LevelControl server once, over the
// player's [levelcontrol.LevelSource] port.
func (sp *SoundPlayer) matterLevelServer() *levelcontrol.Server {
	sp.matterLevelOnce.Do(func() {
		sp.matterLevelSrv = levelcontrol.NewServer(levelcontrol.Config{
			Source:      sp,
			DataVersion: &sp.DataVersionTracker,
		})
	})
	return sp.matterLevelSrv
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
		Clusters:   []uint32{onoff.ClusterID, levelcontrol.ClusterID},
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

// ─── OnOff (0x0006) ──────────────────────────────────────────────────

// matterOnOffServer builds the player's OnOff server once, go-fabric's
// [onoff.Server] without the LT feature (ADR 0084 phase 2): the Speaker
// device type (speaker.ts:20-22) reads OnOff as mute / unmute, mandates
// no Lighting feature, and so the cluster carries OnOff, Off, On and
// Toggle only.
//
// The HmIP-MP3P carries no mute parameter, and it does not need one: the
// whole of its audio output is one LEVEL knob, and [SoundPlayer.StopSound]
// silences the player by writing LEVEL=0. Muted and silent are therefore
// the same device state here, and both halves of the projection are
// backed by real wire values — the attribute follows DIRECTION (UP/DOWN =
// a file is being played, so sound is coming out) through the confirmed-
// update hook and [onoff.Server.Set], and the Sink is the profile's own
// play and stop paths. Nothing is remembered or invented on either side.
//
// What the cluster deliberately does not carry is *which* file plays.
// That is SOUNDFILE, which has no Matter cluster; On resumes with
// whatever the device last selected.
func (sp *SoundPlayer) matterOnOffServer() *onoff.Server {
	return sp.matterOnOff.Server(func() *onoff.Server {
		playing, _ := sp.IsPlaying()
		srv := generic.MustMatterOnOffServer(generic.MatterOnOffConfig{
			Lighting:    false,
			DataVersion: &sp.DataVersionTracker,
			Initial:     playing,
			Sink: func(ctx context.Context, on bool) error {
				if on {
					return sp.matterPlay(ctx)
				}
				return sp.StopSound(ctx, matterDispatchPriority)
			},
		})
		if sp.direction != nil {
			_ = sp.direction.OnConfirmedUpdate(func(_, _ int32) {
				playing, _ := sp.IsPlaying()
				srv.Set(playing)
			})
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if playing, observed := sp.IsPlaying(); observed {
			srv.Set(playing)
		}
		return srv
	})
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

// ─── LevelControl (0x0008), host port ────────────────────────────────

// matterVolume reports the observed LEVEL. Separate from [CurrentLevel] so the
// float stays available to the command paths, which write floats.
func (sp *SoundPlayer) matterVolume() (float64, bool) {
	if sp == nil || sp.level == nil {
		return 0, false
	}
	return sp.level.Value()
}

// CurrentLevel implements [levelcontrol.LevelSource]: the HM 0..1 LEVEL scaled
// onto Matter's level byte. known=false while LEVEL has never been observed,
// which the server encodes as TLV null — CurrentLevel carries quality X
// (matter.js level-control.element.ts:29-30), so an unread volume is reported
// as unknown rather than as the real level 0.
func (sp *SoundPlayer) CurrentLevel() (uint8, bool) {
	v, ok := sp.matterVolume()
	if !ok {
		return 0, false
	}
	return volumeToMatterLevel(v), true
}

// Options implements [levelcontrol.LevelSource].
func (sp *SoundPlayer) Options() uint8 {
	if sp == nil {
		return 0
	}
	sp.matterLevel.mu.RLock()
	defer sp.matterLevel.mu.RUnlock()
	return sp.matterLevel.options
}

// SetOptions implements [levelcontrol.LevelSource]. The server has already
// checked the bitmap against the advertised FeatureMap.
func (sp *SoundPlayer) SetOptions(_ context.Context, options uint8) error {
	sp.matterLevel.mu.Lock()
	defer sp.matterLevel.mu.Unlock()
	sp.matterLevel.options = options
	return nil
}

// OnLevel implements [levelcontrol.LevelSource]. Unset means null, which the
// spec reads as "OnLevel has no effect" — and it has none here: On resumes at
// the volume the device reports (see [SoundPlayer.matterPlay]).
func (sp *SoundPlayer) OnLevel() (uint8, bool) {
	if sp == nil {
		return 0, false
	}
	sp.matterLevel.mu.RLock()
	defer sp.matterLevel.mu.RUnlock()
	if sp.matterLevel.onLevel == nil {
		return 0, false
	}
	return *sp.matterLevel.onLevel, true
}

// SetOnLevel implements [levelcontrol.LevelSource]. The value is stored so a
// controller reads back what it wrote; it is not consulted on On, because the
// device resumes at its own LEVEL and overriding that would make the Matter
// path behave differently from every other surface on the same data point.
func (sp *SoundPlayer) SetOnLevel(_ context.Context, level *uint8) error {
	sp.matterLevel.mu.Lock()
	defer sp.matterLevel.mu.Unlock()
	if level == nil {
		sp.matterLevel.onLevel = nil
		return nil
	}
	v := *level
	sp.matterLevel.onLevel = &v
	return nil
}

// MoveToLevel implements [levelcontrol.LevelSource]: a volume change that must
// not touch OnOff, so it writes LEVEL alone and never starts or stops playback.
func (sp *SoundPlayer) MoveToLevel(ctx context.Context, req levelcontrol.MoveToLevelRequest) error {
	if !sp.matterExecutionAllowed(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	return sp.setVolume(ctx, matterLevelToVolume(req.Level))
}

// MoveToLevelWithOnOff implements [levelcontrol.LevelSource]: the coupled
// variant, which drives the endpoint's OnOff state along with the level. Level
// 0 is silence, so it stops the player; any other level starts it at that
// volume, which the profile does as one atomic write.
func (sp *SoundPlayer) MoveToLevelWithOnOff(ctx context.Context, req levelcontrol.MoveToLevelRequest) error {
	if req.Level == levelcontrol.LevelMin {
		return sp.StopSound(ctx, matterDispatchPriority)
	}
	return sp.playVolume(ctx, matterLevelToVolume(req.Level))
}

// Move implements [levelcontrol.LevelSource]. The HmIP-MP3P has no
// controller-driven continuous volume ramp: RAMP_TIME accompanies a play
// command and runs to a target the caller names, which is a transition, not an
// open-ended move at a rate. A zero rate is already refused by the server
// (matter.js LevelControlServer.ts:305-308), so what reaches here is a valid
// command with nothing behind it and returns Success without acting — the same
// call the dimmer projection makes in internal/model/custom/light/matter.go.
func (sp *SoundPlayer) Move(_ context.Context, _ levelcontrol.MoveRequest) error { return nil }

// MoveWithOnOff implements [levelcontrol.LevelSource]. See [SoundPlayer.Move]:
// there is no move to couple OnOff to.
func (sp *SoundPlayer) MoveWithOnOff(_ context.Context, _ levelcontrol.MoveRequest) error {
	return nil
}

// Step implements [levelcontrol.LevelSource]: one discrete volume step from the
// level the device reports, clamped to the cluster's range. Plain Step must not
// touch OnOff, so it writes LEVEL alone.
func (sp *SoundPlayer) Step(ctx context.Context, req levelcontrol.StepRequest) error {
	if !sp.matterExecutionAllowed(req.OptionsMask, req.OptionsOverride) {
		return nil
	}
	next, ok := sp.steppedLevel(req)
	if !ok {
		return nil
	}
	return sp.setVolume(ctx, matterLevelToVolume(next))
}

// StepWithOnOff implements [levelcontrol.LevelSource]: the coupled variant. A
// step that lands on the minimum level is silence, which turns the player off
// (Matter §1.6.7.6, chip LevelControlCluster.cpp compares the post-clamp
// target); any other target starts playback at that volume.
func (sp *SoundPlayer) StepWithOnOff(ctx context.Context, req levelcontrol.StepRequest) error {
	next, ok := sp.steppedLevel(req)
	if !ok {
		return nil
	}
	if next == levelcontrol.LevelMin {
		return sp.StopSound(ctx, matterDispatchPriority)
	}
	return sp.playVolume(ctx, matterLevelToVolume(next))
}

// Stop implements [levelcontrol.LevelSource]. Stop halts an in-flight Move or
// Step transition; this projection starts none (see [SoundPlayer.Move]), so
// there is nothing to halt and the command succeeds. It is NOT the OnOff Off
// path — stopping a level transition and silencing the speaker are different
// operations, and mapping this one onto StopSound would silence a speaker
// whenever a controller ended a fade.
func (sp *SoundPlayer) Stop(_ context.Context, _ levelcontrol.StopRequest) error { return nil }

// StopWithOnOff implements [levelcontrol.LevelSource]. See [SoundPlayer.Stop].
func (sp *SoundPlayer) StopWithOnOff(_ context.Context, _ levelcontrol.StopRequest) error {
	return nil
}

// steppedLevel resolves a Step request against the level the device reports.
// ok is false while LEVEL has never been observed: a step is relative, and
// stepping from a level nothing confirmed would write a volume derived from a
// guess.
func (sp *SoundPlayer) steppedLevel(req levelcontrol.StepRequest) (uint8, bool) {
	current, known := sp.CurrentLevel()
	if !known {
		return 0, false
	}
	if req.StepMode == levelcontrol.StepModeDown {
		if int(current)-int(req.StepSize) < int(levelcontrol.LevelMin) {
			return levelcontrol.LevelMin, true
		}
		return current - req.StepSize, true
	}
	if int(current)+int(req.StepSize) > int(levelcontrol.LevelMax) {
		return levelcontrol.LevelMax, true
	}
	return current + req.StepSize, true
}

// matterExecutionAllowed applies the ExecuteIfOff gate the plain (non-On/Off)
// commands consult. The bitmap arithmetic belongs to the cluster and comes from
// [levelcontrol.EffectiveOptions]; the state it is weighed against belongs to
// the endpoint's OnOff cluster, which the level server cannot see — so it is
// read here, from the same [SoundPlayer.IsPlaying] the OnOff attribute reports.
// Mirrors matter.js LevelControlServer.ts:729-736 #optionsAllowExecution.
func (sp *SoundPlayer) matterExecutionAllowed(mask, override uint8) bool {
	if levelcontrol.EffectiveOptions(sp.Options(), mask, override)&levelcontrol.OptionExecuteIfOff != 0 {
		return true
	}
	playing, _ := sp.IsPlaying()
	return playing
}

// setVolume writes LEVEL without touching playback.
func (sp *SoundPlayer) setVolume(ctx context.Context, volume float64) error {
	if sp == nil || sp.level == nil {
		return fmt.Errorf("soundplayer: set volume: channel has no %s data point", hmenum.ParameterLevel)
	}
	return sp.level.Set(custom.EnsureContext(ctx), volume, matterDispatchPriority)
}

// volumeToMatterLevel encodes the HM 0..1 LEVEL onto the Matter level byte.
// The ceiling is [levelcontrol.LevelMax] (254) and not 255: Matter reserves
// 0xFF as the null sentinel of a nullable uint8, and CurrentLevel is nullable.
func volumeToMatterLevel(v float64) uint8 {
	scaled := custom.NewBrightness(v).Level() * float64(levelcontrol.LevelMax)
	return uint8(scaled + 0.5)
}

// matterLevelToVolume decodes a Matter level byte back onto the HM 0..1 range.
func matterLevelToVolume(level uint8) float64 {
	if level >= levelcontrol.LevelMax {
		return 1.0
	}
	return float64(level) / float64(levelcontrol.LevelMax)
}
