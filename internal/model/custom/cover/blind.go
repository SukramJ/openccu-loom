// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// BlindKind discriminates between HM (LEVEL_COMBINED) and HmIP
// (COMBINED_PARAMETER) blinds.
type BlindKind int

// BlindKind values.
const (
	BlindKindHM BlindKind = iota
	BlindKindIP
)

// commandLockTimeout mirrors cover.py:37 — the maximum time a Blind
// command waits for an in-flight motion before proceeding without the
// lock.
const commandLockTimeout = 5 * time.Second

// tiltEpsilon is the smallest tilt-level difference treated as a state
// change. The 0..1 range carries at most two decimal digits of precision
// after the CCU's 0..100 integer round-trip, so 1e-9 is safely below
// the resolution limit while guarding against floating-point noise.
const tiltEpsilon = 1e-9

// tiltChanged returns true when |a-b| > tiltEpsilon.
func tiltChanged(a, b float64) bool {
	return math.Abs(a-b) > tiltEpsilon
}

// Blind is a tilting blind. It composes [Cover] (LEVEL = vertical
// position, 1.0 = fully open) and adds a tilt slot (LEVEL_2 = slat
// angle, 1.0 = fully open). Both axes can be commanded individually
// or jointly via the device's COMBINED parameter.
//
// Mirrors `CustomDpBlind` (cover.py:284-551) and `CustomDpIpBlind`
// (cover.py:553-581).
//
// Concurrency: Blind serialises every motion command through
// [commandLock] (channel-based mutex with [commandLockTimeout]) —
// when a second command arrives while a first one is still in flight,
// the second waits up to 5 s for the first to complete. The lock
// also detects the "currently moving" condition: if a non-zero
// target is staged, the blind is sent a STOP first because the
// actuator hardware ignores new coordinates while moving (mirrors
// cover.py:539).
type Blind struct {
	*Cover

	Kind BlindKind

	level2 *generic.Float

	// groupLevel2 is the optional group-channel LEVEL_2 (tilt) slot. It
	// mirrors [Cover.groupLevel] for the tilt axis: the IPCover schema maps
	// GROUP_LEVEL_2 alongside GROUP_LEVEL on the same group/state channel,
	// and the same use_group_channel_for_cover_state toggle governs both —
	// a blind's position and tilt are read from the same physical group
	// channel, so there is no separate toggle for the tilt half.
	groupLevel2 custom.GroupLevelDataPoint

	// commandLock is a channel-of-1 used as a Mutex-with-timeout.
	// Buffer slot full ⇒ held; empty ⇒ free. We initialise it on
	// first use via initOnce.
	commandLock chan struct{}
	initOnce    sync.Once

	// operationMode holds the last observed OPERATION_MODE parameter value (IP
	// blinds only). Guarded by muOp.
	muOp          sync.RWMutex
	operationMode string
	hasOpMode     bool
}

// BlindConfig is the constructor record. Channel must already carry
// LEVEL and LEVEL_2 (and optionally COMBINED_PARAMETER for HmIP).
type BlindConfig struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.CoverCapabilities
	Kind         BlindKind
	// Variant selects the HA device_class for this blind. When zero
	// (VariantShutter), [Blind.HADiscoveryPayload] substitutes
	// VariantBlind. Set to VariantShade for HmIP-HDM.
	Variant CoverVariant
	// Group is the rebased channel-group schema of the profile that
	// materialised this blind. It names the tilt parameter, which is not
	// the same on every family: the HmIP covers drive LEVEL_2, the classic
	// RF jalousie actuators LEVEL_SLATS. The zero value falls back to
	// LEVEL_2, which is what a Blind constructed without a profile relies
	// on.
	Group custom.RebasedChannelGroupConfig
}

// NewBlind constructs a Blind. The cover-side LEVEL/Direction
// subscriptions are wired through [Cover.Subscribe] inside.
func NewBlind(cfg BlindConfig) *Blind {
	cov := New(Config{
		Channel:      cfg.Channel,
		Writer:       cfg.Writer,
		Capabilities: cfg.Capabilities,
		Variant:      cfg.Variant,
		Group:        cfg.Group,
	})
	tiltChannel, tiltParam := custom.ResolveSlotOr(
		cfg.Channel, cfg.Group, hmenum.FieldLevel2, hmenum.ParameterLevel2,
	)
	b := &Blind{
		Cover:  cov,
		Kind:   cfg.Kind,
		level2: custom.FloatField(tiltChannel, tiltParam),
	}
	if cov != nil && cov.Float != nil {
		b.registerBlindServices()
	}
	if b.level2 != nil {
		_ = b.level2.OnConfirmedUpdate(func(_, _ float64) { b.dataVersion.Bump() })
	}
	// Attach a LevelCombined combined DP so the aggregate (level + slats)
	// is surfaced on the event bus and visible via Channel.CombinedDataPoints.
	// The write path remains sendCombined; LevelCombined is read-side only.
	// The aggregate names the tilt parameter this family actually drives.
	// It is a per-channel calculation, so it only applies when the schema
	// put the tilt slot on the blind's own channel — no profile maps it
	// elsewhere today, and a silent cross-channel aggregate would report a
	// value under an address that does not carry it.
	if cfg.Channel != nil && cov != nil && cov.Float != nil && b.level2 != nil && tiltChannel == cfg.Channel {
		lc := combined.NewLevelCombinedWithCentral(
			cfg.Channel.CentralName(),
			cfg.Channel.Address,
			cfg.Writer,
			hmenum.ParameterLevel,
			tiltParam,
			hmenum.ParameterLevelCombined,
		)
		cfg.Channel.AttachCalculatedDataPoint(lc)
	}
	return b
}

// NamePostfix overrides [Cover.NamePostfix] when needed. Plain blinds
// inherit the empty postfix; subtypes can override.
func (b *Blind) NamePostfix() string { return "" }

// TiltPosition returns the current slat-tilt position (0..1) and whether it
// has been observed. Mirrors [Cover.observedLevel]: when the cover is
// configured for group-channel state and a group-channel LEVEL_2 is bound,
// the group channel's tilt takes precedence over the blind's own.
func (b *Blind) TiltPosition() (custom.Position, bool) {
	if b.Cover != nil && b.useGroupChannelForState && b.groupLevel2 != nil {
		if v, ok := b.groupLevel2.Value(); ok {
			if b.Capabilities.InvertedControl {
				v = 1 - v
			}
			return custom.NewPosition(v), true
		}
	}
	if b.level2 == nil {
		return custom.Position{}, false
	}
	v, ok := b.level2.Value()
	if !ok {
		return custom.Position{}, false
	}
	if b.Capabilities.InvertedControl {
		v = 1 - v
	}
	return custom.NewPosition(v), true
}

// SetGroupLevel2 binds an optional group-channel LEVEL_2 (tilt) data point.
// Used by the materializer for sub-blind channels whose canonical tilt
// lives on the group master, mirroring [Cover.SetGroupLevel]. Pass nil to
// clear.
func (b *Blind) SetGroupLevel2(dp custom.GroupLevelDataPoint) {
	b.groupLevel2 = dp
}

// levelForCommand returns the position axis to hold when the caller
// commands only the tilt axis, plus whether the level axis carries an
// optimistic write the CCU has not yet confirmed — a write genuinely in
// flight, not merely a value that happens to be cached. Callers use
// `pending` to decide whether the device is still moving and needs a
// STOP before it accepts new coordinates.
//
// The held level itself comes from [Blind.Position], which already
// blends (highest priority first) an active optimistic write, the
// CCU-unconfirmed value [Blind.sendCombined] stages on every combined
// write, and the last CCU-confirmed observation (see [DataPoint.Value]).
// Staging the just-sent value on every combined write is what lets the
// untouched axis survive a follow-up command issued before the CCU
// echoes back; [DataPoint.OnEventAt] clears that staged value
// unconditionally the instant any CCU value for the axis arrives —
// confirmed or not — so, unlike the boolean staging this replaced, a
// superseded value can never be re-sent.
func (b *Blind) levelForCommand() (level float64, pending bool) {
	if b.Cover != nil && b.Float != nil {
		pending = b.IsOptimistic()
	}
	if pos, ok := b.Position(); ok {
		return pos.Level(), pending
	}
	return closedLevel, pending
}

// tiltForCommand is [Blind.levelForCommand] for the slat axis.
func (b *Blind) tiltForCommand() (tilt float64, pending bool) {
	if b.level2 != nil {
		pending = b.level2.IsOptimistic()
	}
	if pos, ok := b.TiltPosition(); ok {
		return pos.Level(), pending
	}
	return closedLevel, pending
}

// SetPosition commands the vertical position. The slat axis rides along
// unchanged: LEVEL + LEVEL_2 are sent atomically through the device's
// combined-parameter slot, with the tilt value taken from
// [Blind.tiltForCommand].
//
// Always routes through [sendCombined] so the command-processing
// lock and the "currently moving → STOP first" detection apply
// Uniformly.
// single dispatch point regardless of whether tilt is staged
// (cover.py:495).
func (b *Blind) SetPosition(ctx context.Context, target float64, priority hmenum.CommandPriority) error {
	if !b.IsStateChangeArgs(StateChangeArgs{Position: &target}) {
		return nil
	}
	tilt, tiltPending := b.tiltForCommand()
	return b.sendCombined(ctx, target, tilt, tiltPending, priority)
}

// SetTilt commands a new slat-tilt position (0..1, 1.0 = fully open). LEVEL +
// LEVEL_2 are sent atomically, with the position axis taken from
// [Blind.levelForCommand].
func (b *Blind) SetTilt(ctx context.Context, target float64, priority hmenum.CommandPriority) error {
	if !b.IsStateChangeArgs(StateChangeArgs{TiltPosition: &target}) {
		return nil
	}
	if target < 0 {
		target = 0
	}
	if target > 1 {
		target = 1
	}
	if b.level2 == nil {
		return errors.New("blind: SET tilt: channel has no LEVEL_2 data point")
	}
	level, levelPending := b.levelForCommand()
	return b.sendCombined(ctx, level, target, levelPending, priority)
}

// acquireCommandLock blocks until the command-processing lock is
// free or `commandLockTimeout` elapses. Returns a release closure
// when acquired (must be deferred); returns nil + `false` when the
// lock could not be acquired in time — the caller proceeds without
// the lock and emits a warning in production code paths.
//
// — callers will start logging on the false case once the warn-channel
// is wired.
//
//nolint:unparam // `acquired` is part of the public command-lock contract
func (b *Blind) acquireCommandLock(ctx context.Context) (release func(), acquired bool) {
	b.initOnce.Do(func() { b.commandLock = make(chan struct{}, 1) })
	timer := time.NewTimer(commandLockTimeout)
	defer timer.Stop()
	select {
	case b.commandLock <- struct{}{}:
		return func() { <-b.commandLock }, true
	case <-timer.C:
		return func() {}, false
	case <-ctx.Done():
		return func() {}, false
	}
}

// sendCombined writes both axes through the device's combined-parameter
// slot — LEVEL_COMBINED for HM blinds, COMBINED_PARAMETER for HmIP blinds.
// Inversion is applied per-axis from the capability profile.
//
// HmIP wire shape: `COMBINED_PARAMETER = "L2=<tilt_pct>,L=<level_pct>"` as a
// single string write — tilt slot first, both values are integer 0..100
// (position-percent).
//
// HM wire shape: `LEVEL_COMBINED` is a comma-separated hex string
// `"0xHH,0xHH"` where each byte encodes `int(position * 100 * 2)` in the
// 4-digit hex notation `#04x` (e.g. level=1.0, tilt=0.5 → "0xc8,0x64").
// This matches the CCU wire format expected by HM blind actuators.
//
// wasMoving reports that one of the two axes carried a CCU-unconfirmed
// write the caller did not command itself, i.e. the blind is still moving
// towards a coordinate nobody asked to change. Blind actuators ignore new
// coordinates while in motion, so those are stopped first — mirroring
// cover.py:538-540, where STOP fires exactly when the axis fell back to
// `_target_level` / `_target_tilt_level` rather than to the observed
// group level. A command that names both axes never stops.
func (b *Blind) sendCombined(ctx context.Context, level, tilt float64, wasMoving bool, priority hmenum.CommandPriority) error {
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	if tilt < 0 {
		tilt = 0
	}
	if tilt > 1 {
		tilt = 1
	}

	ctx = custom.EnsureContext(ctx)
	release, _ := b.acquireCommandLock(ctx)
	defer release()

	if wasMoving {
		_ = b.Cover.Stop(ctx, priority)
	}

	wireL := level
	wireT := tilt
	if b.Capabilities.InvertedControl {
		wireL = 1 - level
		wireT = 1 - tilt
	}
	switch b.Kind {
	case BlindKindHM:
		s := hmLevelCombined(wireL, wireT)
		if err := b.writer.SetValue(ctx, b.Address(), hmenum.ParameterLevelCombined, s, priority); err != nil {
			return fmt.Errorf("blind: LEVEL_COMBINED: %w", err)
		}
	case BlindKindIP:
		s := fmt.Sprintf("L2=%d,L=%d", int(wireT*100+0.5), int(wireL*100+0.5))
		if err := b.writer.SetValue(ctx, b.Address(), hmenum.ParameterCombinedParameter, s, priority); err != nil {
			return fmt.Errorf("blind: COMBINED_PARAMETER: %w", err)
		}
	}

	// A combined write goes straight to the writer, bypassing the LEVEL /
	// LEVEL_2 data points' own Set() path — so it never arms their
	// optimistic tracker (IsOptimistic stays false; a repeat combined
	// write is never treated as "still moving" by [levelForCommand] /
	// [tiltForCommand] — see TestBlindDoesNotStopWhenNeitherAxisHasAPendingWrite).
	// What it must still do is record what was just told to the CCU, so
	// [Blind.Position] / [Blind.TiltPosition] can hand it back to a
	// follow-up command on the other axis before the echo arrives.
	// [DataPoint.WriteUnconfirmedValue] is exactly that record: a slot
	// [DataPoint.OnEventAt] clears unconditionally on the next CCU value
	// for the axis, so it can never outlive its own confirmation the way
	// the boolean staging this replaced did.
	writeAt := time.Now()
	if b.Float != nil {
		b.WriteUnconfirmedValue(wireL, writeAt)
	}
	if b.level2 != nil {
		b.level2.WriteUnconfirmedValue(wireT, writeAt)
	}
	return nil
}

// Stop overrides [Cover.Stop] to acquire the command-processing lock so
// callers can rely on STOP being serialised with concurrent
// SetPosition/SetTilt commands.
func (b *Blind) Stop(ctx context.Context, priority hmenum.CommandPriority) error {
	ctx = custom.EnsureContext(ctx)
	release, _ := b.acquireCommandLock(ctx)
	defer release()
	return b.Cover.Stop(ctx, priority)
}

// SetCombined commands both axes in a single CCU paramset write using the
// format that matches the [BlindKind].
//
// - BlindKindHM: writes LEVEL_COMBINED as the string "0xHH,0xHH" where each
// byte encodes `int(position * 100 * 2)` in 4-digit hex notation (e.g.
// level=1.0, tilt=0.5 → "0xc8,0x64"). See [hmLevelCombined].
// - BlindKindIP: writes COMBINED_PARAMETER as the string "L2=<tilt_pct>,L=<level_pct>"
// — tilt first, both values are integer 0..100 (position-percent).
func (b *Blind) SetCombined(ctx context.Context, level, tilt float64, priority hmenum.CommandPriority) error {
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	if tilt < 0 {
		tilt = 0
	}
	if tilt > 1 {
		tilt = 1
	}
	// Both axes are named, so neither falls back to a pending target and
	// the STOP guard never fires (cover.py:513-535).
	return b.sendCombined(ctx, level, tilt, false, priority)
}

// Open routes through [Blind.SetPosition] so the combined-parameter wire path
// applies — the embedded [Cover.Open] would otherwise call the inherited
// [Cover.SetPosition] which writes LEVEL on its own (LEVEL-only is the
// non-blind code path).
func (b *Blind) Open(ctx context.Context, priority hmenum.CommandPriority) error {
	open := true
	if !b.IsStateChangeArgs(StateChangeArgs{Open: &open}) {
		return nil
	}
	return b.SetPosition(ctx, 1.0, priority)
}

// Close routes through [Blind.SetPosition] (see [Blind.Open]).
func (b *Blind) Close(ctx context.Context, priority hmenum.CommandPriority) error {
	closing := true
	if !b.IsStateChangeArgs(StateChangeArgs{Close: &closing}) {
		return nil
	}
	return b.SetPosition(ctx, 0.0, priority)
}

// OpenTilt is a convenience for SetTilt(ctx, 1).
func (b *Blind) OpenTilt(ctx context.Context, priority hmenum.CommandPriority) error {
	openTilt := true
	if !b.IsStateChangeArgs(StateChangeArgs{TiltOpen: &openTilt}) {
		return nil
	}
	return b.SetTilt(ctx, 1.0, priority)
}

// CloseTilt is a convenience for SetTilt(ctx, 0).
func (b *Blind) CloseTilt(ctx context.Context, priority hmenum.CommandPriority) error {
	closeTilt := true
	if !b.IsStateChangeArgs(StateChangeArgs{TiltClose: &closeTilt}) {
		return nil
	}
	return b.SetTilt(ctx, 0.0, priority)
}

// StopTilt routes through [Blind.Stop] (which acquires the command-processing
// lock) — the CCU stops both axes jointly when STOP fires.
func (b *Blind) StopTilt(ctx context.Context, priority hmenum.CommandPriority) error {
	return b.Stop(ctx, priority)
}

// Subscribe wires LEVEL_2 and OPERATION_MODE (IP blinds) in addition to the
// [Cover.Subscribe] LEVEL+DIRECTION wiring. Replays the wire DP's currently
// observed value through the same handler so the OPERATION_MODE-cached state
// lands in sync with the CCU at boot, not only on the next push.
func (b *Blind) Subscribe(ch *device.Channel) func() {
	cov := b.Cover.Subscribe(ch)
	var unsubs []func()
	if cov != nil {
		unsubs = append(unsubs, cov)
	}
	applyOpMode := func(next any) {
		if s, ok := next.(string); ok {
			b.OnOperationMode(s)
		}
	}
	if ch != nil {
		// CHANNEL_OPERATION_MODE lives on MASTER for most IP blinds —
		// fall back to MASTER if the VALUES paramset does not carry it.
		if dp := custom.ParamFromAnyParamset(ch, hmenum.ParameterChannelOperationMode); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) {
				applyOpMode(next)
			}))
			custom.ReplayCurrentValue(dp, applyOpMode)
		}
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

// IsStateChange reports whether either the vertical-position or the tilt
// target differs from the last observed state. Pass NaN to indicate "don't
// change this axis".
func (b *Blind) IsStateChange(targetLevel, targetTilt float64) bool {
	if b.Cover.IsStateChange(targetLevel) {
		return true
	}
	if cur, ok := b.TiltPosition(); !ok || tiltChanged(cur.Level(), targetTilt) {
		return true
	}
	return false
}

// IsStateChangeArgs reports whether any of the kwarg-equivalents in
// args would amount to a position-axis or tilt-axis change. Blinds
// recognise all parent Cover axes plus the tilt-specific
// TiltOpen / TiltClose / TiltPosition.
//
// Mirrors `CustomDpBlind.is_state_change(**kwargs)` (cover.py:381-390).
func (b *Blind) IsStateChangeArgs(args StateChangeArgs) bool {
	if b.Cover.IsStateChangeArgs(args) {
		return true
	}
	// Only consult the tilt axis when at least one tilt kwarg was
	// passed — a Position-only call must not be forced through the
	// wire just because LEVEL_2 has not been observed yet.
	wantsTilt := args.TiltOpen != nil || args.TiltClose != nil || args.TiltPosition != nil
	if !wantsTilt {
		return false
	}
	tilt, observed := b.TiltPosition()
	if !observed {
		return true
	}
	if args.TiltOpen != nil && *args.TiltOpen && !tilt.Open() {
		return true
	}
	if args.TiltClose != nil && *args.TiltClose && !tilt.Closed() {
		return true
	}
	if args.TiltPosition != nil && tiltChanged(*args.TiltPosition, tilt.Level()) {
		return true
	}
	return false
}

// Address surfaces the underlying cover's address through the
// embedded *Cover.
func (b *Blind) Address() string { return b.Cover.Address() }

// OperationMode returns the last observed OPERATION_MODE value and whether it
// has been received. Exclusive to IP blinds (HmIP-BBL, HmIP-FBL, HmIP-DRBLI4,
// HmIPW-DRBL4, HmIP-HDM).
func (b *Blind) OperationMode() (string, bool) {
	b.muOp.RLock()
	defer b.muOp.RUnlock()
	return b.operationMode, b.hasOpMode
}

// OnOperationMode records a CCU-emitted OPERATION_MODE update.
// Called from a wire-side subscription when the channel exposes the
// OPERATION_MODE parameter (IP blinds only).
func (b *Blind) OnOperationMode(mode string) {
	b.muOp.Lock()
	b.operationMode = mode
	b.hasOpMode = true
	b.muOp.Unlock()
}

// CurrentChannelTiltPosition returns the raw per-channel LEVEL_2 value
// regardless of any group-channel override.
func (b *Blind) CurrentChannelTiltPosition() (custom.Position, bool) {
	if b.level2 == nil {
		return custom.Position{}, false
	}
	v, ok := b.level2.Value()
	if !ok {
		return custom.Position{}, false
	}
	if b.Capabilities.InvertedControl {
		v = 1 - v
	}
	return custom.NewPosition(v), true
}

// hmLevelCombined encodes two blind-axis positions into the HM wire format
// expected by LEVEL_COMBINED: a comma-separated pair of 4-digit lowercase hex
// values where each byte = int(position * 100 * 2).
//
// Examples:
//   - level=1.0, tilt=1.0 → "0xc8,0xc8"
//   - level=1.0, tilt=0.0 → "0xc8,0x00"
//   - level=0.5, tilt=0.5 → "0x64,0x64"
//
// The product is rounded, not truncated: 0.29, 0.57 and 0.58 land just
// below an exact half-percent step in binary64, so truncation moved the
// blind one 0.5 % step below the commanded position while the HmIP
// branch of the same switch (and internal/client/backends EncodeHMLevel,
// the twin encoder) rounded half-up.
func hmLevelCombined(level, tilt float64) string {
	lByte := int(math.Round(level * 100 * 2))
	tByte := int(math.Round(tilt * 100 * 2))
	return fmt.Sprintf("0x%02x,0x%02x", lByte, tByte)
}
