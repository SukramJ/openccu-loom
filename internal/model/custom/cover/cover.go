// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package cover implements the position-based cover (shutter / blind)
// custom data point. LEVEL is held as a typed reference to the
// channel's existing *generic.Float — there is exactly one instance
// per (channel, parameter), and Cover provides a typed view onto it.
package cover

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Writer is an alias for [custom.Writer].
type Writer = custom.Writer

// CoverVariant selects the HA device_class emitted in the MQTT
// discovery payload. It mirrors the HA CoverDeviceClass enum
// (homeassistant.components.cover.CoverDeviceClass) and the subset
// Used.
// (entity_helpers/descriptions/covers.py:CoverEntityDescription).
//
// Values are ordered roughly by prevalence in the Homematic
// device catalogue.
//
//nolint:revive // stuttering intentional — unqualified Variant collides
type CoverVariant int

// CoverVariant values. VariantShutter is the zero value so a plain
// Cover constructed without an explicit variant defaults to "shutter".
const (
	VariantShutter CoverVariant = iota // HA: "shutter" (default for RF/IP roller blinds)
	VariantBlind                       // HA: "blind"   (tilting: HmIP-BBL, HmIP-FBL, …)
	VariantAwning                      // HA: "awning"  (Markise — outward-rolling shade)
	VariantCurtain                     // HA: "curtain" (Vorhang — fabric curtain track)
	VariantDamper                      // HA: "damper"  (Klappe — ventilation damper)
	VariantShade                       // HA: "shade"   (HmIP-HDM roller shade)
	VariantWindow                      // HA: "window"  (HM-Sec-Win window actuator)
	VariantGarage                      // HA: "garage"  (handled by *Garage, here for completeness)
)

// VariantString returns the lowercase HA device_class string for each
// variant.
func VariantString(v CoverVariant) string {
	switch v {
	case VariantBlind:
		return "blind"
	case VariantAwning:
		return "awning"
	case VariantCurtain:
		return "curtain"
	case VariantDamper:
		return "damper"
	case VariantShade:
		return "shade"
	case VariantWindow:
		return "window"
	case VariantGarage:
		return "garage"
	default: // VariantShutter
		return "shutter"
	}
}

// CoverDirection describes the cover's current motion as reported by
// The CCU's DIRECTION parameter. Values mirror
// `_DIRECTION_*` integers in `model/custom/cover.py`.
// Stuttering accepted (see nolint): many callers import only this
// package and the unqualified `Direction` would collide with the
// far more common motion-direction usages in adapter/event code.
//
//nolint:revive // see comment block above
type CoverDirection int

// CoverDirection values.
const (
	DirectionNone    CoverDirection = 0
	DirectionUp      CoverDirection = 1
	DirectionDown    CoverDirection = 2
	DirectionUnknown CoverDirection = -1
)

// Config is the constructor record. Channel must already carry the
// LEVEL data point (and optionally LEVEL_2 for slat-style covers).
// Writer is used for the STOP action that has no embedded primitive.
type Config struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.CoverCapabilities

	// Variant selects the HA device_class emitted in the MQTT discovery payload.
	// Defaults to VariantShutter when zero.
	Variant CoverVariant

	// WindowDrive activates the HM-Sec-Win level remap.
	//
	// - The fully-closed wire level is `-0.005` (not `0.0`). - SetPosition(0.0)
	// writes `-0.005` to LEVEL. - SetPosition(p) for 0 < p ≤ 0.01 writes `0.0`
	// (the slightly- open wire-level used to keep the gasket free of strain). -
	// CurrentPosition reads `-0.005` as 0 (closed) and `0.0` as 0.01 (slightly
	// open).
	//
	// HM-Sec-Win is the only profile that flags WindowDrive=true. Default false
	// matches every other cover.
	WindowDrive bool
}

// Cover is a cover (shutter / blind) device. The embedded [*generic.Float] is
// the channel's LEVEL data point — it is not a duplicate. Cover layers
// position semantics and an optional "InvertedControl" quirk on top.
//
// `groupLevel` is the optional group-channel LEVEL slot. It is set for
// sub-cover channels where the master channel of the group holds the
// canonical position; the sub-channel mirrors the master's LEVEL so HA can
// show a consistent state across all channels of a group.
type Cover struct {
	*generic.Float
	custom.BaseDP

	// ServiceRegistry is Cover's own write-half registry. It shadows
	// the registry promoted from *generic.Float so cover-level service
	// methods (open / close / stop / set_position) live on the cover
	// instance and not on the shared underlying LEVEL data point —
	// otherwise two Cover wrappers around the same channel would
	// double-register and panic. The generic.Float keeps its own
	// promoted-but-shadowed registry available to direct callers that
	// reach the LEVEL DP through Channel.Parameter.
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful MatterInvoke so
	// DataVersionFilter evaluation correctly detects cluster changes.
	dataVersion cluster.DataVersionTracker

	address      string
	writer       Writer
	Capabilities custom.CoverCapabilities
	Variant      CoverVariant

	// windowDrive flips SetPosition / Position into the HM-Sec-Win
	// remap mode. Set by Config.WindowDrive at construction.
	windowDrive bool

	groupLevel              *generic.Float
	useGroupChannelForState bool

	directionMu sync.RWMutex
	direction   CoverDirection
	hasDir      bool
}

// Window-drive level constants.
const (
	wdClosedLevel = -0.005 // wire-level "fully closed"
	closedLevel   = 0.0    // wire-level used by HM-Sec-Win for "slightly open"
)

// New constructs a Cover. The channel's LEVEL data point becomes the
// embedded *generic.Float; if the channel carries no LEVEL DP, the
// returned Cover reports an unobserved position and write attempts
// fail with a missing-data-point error.
func New(cfg Config) *Cover {
	address := ""
	if cfg.Channel != nil {
		address = cfg.Channel.Address
	}
	c := &Cover{
		Float:        custom.FloatField(cfg.Channel, hmenum.ParameterLevel),
		address:      address,
		writer:       cfg.Writer,
		Capabilities: cfg.Capabilities,
		Variant:      cfg.Variant,
		windowDrive:  cfg.WindowDrive,
		direction:    DirectionUnknown,
	}
	if c.Float != nil {
		c.registerCoverServices()
		// Matter §10.6.5: DataVersion advances on every CCU-confirmed attribute change.
		_ = c.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	return c
}

// Address returns the channel address the Cover writes to.
func (c *Cover) Address() string { return c.address }

// IsRefreshed reports whether LEVEL has been observed at least once.
// Slat-tilt (LEVEL_2) and group level (LEVEL_GROUP) are auxiliary; LEVEL
// alone is enough to render a meaningful HA cover entity.
func (c *Cover) IsRefreshed() bool {
	if c.Float == nil {
		return false
	}
	_, ok := c.RawValue()
	return ok
}

// IsStatusValid reports whether the LEVEL data point has a valid STATUS
// parameter state (no OVERFLOW / ERROR).
func (c *Cover) IsStatusValid() bool {
	if c.Float == nil {
		return true
	}
	return c.Float.IsStatusValid()
}

// DataPointKey returns the embedded LEVEL DP's wire identifier, or
// the zero key when the cover wraps a channel without a LEVEL data
// point. The override exists because the autogenerated forwarder
// invokes (*DataPoint[float64]).DataPointKey on a nil receiver,
// which panics — see the same defensive check on
// [Cover.SubDataPointKeys] and [Cover.IsRefreshed].
func (c *Cover) DataPointKey() hmtypes.DataPointKey {
	if c == nil || c.Float == nil {
		return hmtypes.DataPointKey{}
	}
	return c.Float.DataPointKey()
}

// Category returns the embedded LEVEL DP's category, or
// [hmenum.DataPointCategoryUndefined] when the cover wraps a channel
// without a LEVEL data point. Same nil-receiver hazard as
// [Cover.DataPointKey] — the autogenerated forwarder dispatches to
// (*DataPoint[float64]).Category which dereferences its receiver.
func (c *Cover) Category() hmenum.DataPointCategory {
	if c == nil || c.Float == nil {
		return hmenum.DataPointCategoryUndefined
	}
	return c.Float.Category()
}

// SubDataPointKeys returns the wire identifier of the LEVEL slot.
func (c *Cover) SubDataPointKeys() []hmtypes.DataPointKey {
	if c.Float == nil {
		return nil
	}
	return []hmtypes.DataPointKey{c.DataPointKey()}
}

// Position returns the current domain-level position (1 = fully open)
// and whether it has been observed. The inverted-control quirk is
// resolved here so callers always see the domain orientation.
//
// When the channel carries a group-level slot AND the cover is
// configured to follow the group channel (`UseGroupChannelForState`),
// the group LEVEL takes precedence over the per-channel LEVEL.
func (c *Cover) Position() (custom.Position, bool) {
	v, ok := c.observedLevel()
	if !ok {
		return custom.Position{}, false
	}
	if c.windowDrive {
		// HM-Sec-Win level → domain remap. Mirrors
		// `CustomDpWindowDrive.current_position`
		// (`model/custom/cover.py:254-262`).
		switch v {
		case wdClosedLevel:
			v = closedLevel // wire -0.005 → fully closed
		case closedLevel:
			v = 0.01 // wire 0.0 → slightly open
		}
	}
	if c.Capabilities.InvertedControl {
		v = 1 - v
	}
	return custom.NewPosition(v), true
}

// observedLevel returns the effective LEVEL: group LEVEL when the
// cover is configured for group-channel state, otherwise the
// per-channel LEVEL. The boolean reports whether the chosen slot
// has actually been observed.
func (c *Cover) observedLevel() (float64, bool) {
	if c.useGroupChannelForState && c.groupLevel != nil {
		if v, ok := c.groupLevel.Value(); ok {
			return v, true
		}
	}
	if c.Float == nil {
		return 0, false
	}
	return c.Value()
}

// SetGroupLevel binds an optional group-channel LEVEL data point.
// Used by the materializer for sub-cover channels whose canonical
// position lives on the group master. Pass nil to clear.
func (c *Cover) SetGroupLevel(dp *generic.Float, useGroupChannelForState bool) {
	c.groupLevel = dp
	c.useGroupChannelForState = useGroupChannelForState
}

// CurrentChannelPosition returns the per-channel LEVEL regardless of the
// group-channel override.
func (c *Cover) CurrentChannelPosition() (custom.Position, bool) {
	if c.Float == nil {
		return custom.Position{}, false
	}
	v, ok := c.Value()
	if !ok {
		return custom.Position{}, false
	}
	if c.Capabilities.InvertedControl {
		v = 1 - v
	}
	return custom.NewPosition(v), true
}

// OnLevel feeds the CCU-reported LEVEL value as-is. Inversion is
// applied on read inside [Position], not on write. The update flows
// into the channel's LEVEL data point through the shared pointer.
func (c *Cover) OnLevel(level float64) {
	if c.Float == nil {
		return
	}
	c.OnEvent(level)
}

// SetPosition commands the cover to a new domain-level position. The
// wire value is inverted when the device's capability profile flags
// InvertedControl. When the cover is a HM-Sec-Win window drive
// ([Config.WindowDrive] == true), the level is remapped.
// (`model/custom/cover.py:264-281`):
//
//	target == 0          → wire -0.005   (fully closed)
//	0 < target ≤ 0.01    → wire 0.0      (slightly open, gasket-safe)
//	otherwise            → wire = target (pass-through)
func (c *Cover) SetPosition(ctx context.Context, target float64, priority hmenum.CommandPriority) error {
	if math.IsNaN(target) || math.IsInf(target, 0) {
		return fmt.Errorf("cover: SET level: position must be a finite number, got %v", target)
	}
	if !c.IsStateChangeArgs(StateChangeArgs{Position: &target}) {
		return nil
	}
	wire := target
	if c.Capabilities.InvertedControl {
		wire = 1 - target
	}
	if c.windowDrive {
		switch {
		case wire == closedLevel:
			wire = wdClosedLevel
		case wire > closedLevel && wire <= 0.01:
			wire = 0.0
		}
	}
	if c.Float == nil {
		return fmt.Errorf("cover: SET level: channel has no LEVEL data point")
	}
	if err := c.Set(custom.EnsureContext(ctx), wire, priority); err != nil {
		return fmt.Errorf("cover: SET level: %w", err)
	}
	return nil
}

// Stop sends the STOP action when the capability profile claims SupportsStop;
// otherwise it is a no-op.
//
// The priority is always overridden to [hmenum.CommandPriorityCritical]
// regardless of the value passed by the caller — STOP must always pre-empt
// any queued motion commands.
func (c *Cover) Stop(ctx context.Context, _ hmenum.CommandPriority) error {
	if !c.Capabilities.SupportsStop {
		return nil
	}
	if c.writer == nil {
		return fmt.Errorf("cover: STOP: no writer configured")
	}
	return c.writer.SetValue(custom.EnsureContext(ctx), c.address, hmenum.ParameterStop, true, hmenum.CommandPriorityCritical)
}

// Open is a convenience for SetPosition(ctx, 1).
func (c *Cover) Open(ctx context.Context, priority hmenum.CommandPriority) error {
	open := true
	if !c.IsStateChangeArgs(StateChangeArgs{Open: &open}) {
		return nil
	}
	return c.SetPosition(ctx, 1.0, priority)
}

// Close is a convenience for SetPosition(ctx, 0).
func (c *Cover) Close(ctx context.Context, priority hmenum.CommandPriority) error {
	closing := true
	if !c.IsStateChangeArgs(StateChangeArgs{Close: &closing}) {
		return nil
	}
	return c.SetPosition(ctx, 0.0, priority)
}

// OnDirection records a CCU-emitted DIRECTION update. Pass [DirectionUnknown]
// to clear the cached state when the CCU stops reporting (e.g. after a
// controller restart).
func (c *Cover) OnDirection(d CoverDirection) {
	c.directionMu.Lock()
	c.direction = d
	c.hasDir = d != DirectionUnknown
	c.directionMu.Unlock()
}

// Direction returns the last observed CCU motion direction and
// whether it has been observed yet.
func (c *Cover) Direction() (CoverDirection, bool) {
	c.directionMu.RLock()
	defer c.directionMu.RUnlock()
	return c.direction, c.hasDir
}

// IsOpening reports whether the cover is currently moving towards
// fully-open.
// Returns false when no DIRECTION has been observed yet.
func (c *Cover) IsOpening() bool {
	c.directionMu.RLock()
	defer c.directionMu.RUnlock()
	if !c.hasDir {
		return false
	}
	if c.Capabilities.InvertedControl {
		return c.direction == DirectionDown
	}
	return c.direction == DirectionUp
}

// IsClosing reports whether the cover is currently moving towards
// fully-closed.
func (c *Cover) IsClosing() bool {
	c.directionMu.RLock()
	defer c.directionMu.RUnlock()
	if !c.hasDir {
		return false
	}
	if c.Capabilities.InvertedControl {
		return c.direction == DirectionUp
	}
	return c.direction == DirectionDown
}

// IsClosed reports whether the cover's last observed position is exactly
// zero. Returns false when no level has been observed yet.
func (c *Cover) IsClosed() bool {
	pos, ok := c.Position()
	if !ok {
		return false
	}
	return pos.Closed()
}

// IsStateChange reports whether the desired position differs from the last
// observed position. Returns true when no position has been observed yet, when
// the state is currently uncertain, or when target differs from the current
// level.
func (c *Cover) IsStateChange(target float64) bool {
	if c.IsOptimistic() {
		return true
	}
	pos, ok := c.Position()
	if !ok {
		return true
	}
	return pos.Level() != target
}

// StateChangeArgs is the kwargs-equivalent for [Cover.IsStateChangeArgs]
// and the slat/garage subclass equivalents. Each pointer-field is a
// presence-aware flag: nil means "the caller did not pass this kwarg",
// non-nil means "the caller passed this kwarg with the dereferenced
// value". Mirrors the Python reference's TypedDict semantics.
type StateChangeArgs struct {
	Open         *bool
	Close        *bool
	Position     *float64
	TiltOpen     *bool
	TiltClose    *bool
	TiltPosition *float64
	Vent         *bool
}

// IsStateChangeArgs reports whether any of the kwarg-equivalents in
// args would amount to a state change. Returns true when at least one
// kwarg is set AND that kwarg's target differs from the corresponding
// observed value. Used by Cover service methods (open/close/set_position)
// to short-circuit out-of-bounds writes.
//
// Mirrors the Python `is_state_change(**kwargs)` overrides on
// `CustomDpCover` (cover.py:181-189). The base Cover only consults the
// position-axis (Open / Close / Position); Blind and Garage extend the
// override with their own axes (TiltOpen / TiltClose / TiltPosition for
// Blind, Vent for Garage).
func (c *Cover) IsStateChangeArgs(args StateChangeArgs) bool {
	// Only consult the position axis when at least one
	// position-axis kwarg was passed. A pure tilt-axis call
	// (TiltPosition / TiltOpen / TiltClose) must not be forced
	// through the wire just because LEVEL has not been observed yet.
	wantsPos := args.Open != nil || args.Close != nil || args.Position != nil || args.Vent != nil
	if !wantsPos {
		return false
	}
	pos, observed := c.Position()
	if !observed {
		return true
	}
	if args.Open != nil && *args.Open && !pos.Open() {
		return true
	}
	if args.Close != nil && *args.Close && !pos.Closed() {
		return true
	}
	if args.Position != nil && *args.Position != pos.Level() {
		return true
	}
	return false
}

// NamePostfix returns the suffix appended to a cover's data-point name
// In The base Cover has no postfix
// subtypes (Blind, Garage) override.
func (c *Cover) NamePostfix() string { return "" }

// Subscribe wires the channel's DIRECTION (and LEVEL_2 when present
// for slat-style covers) parameters into the Cover so push-driven
// CCU updates feed [OnDirection] / [OnLevel] without the ingest
// pipeline having to hand-route them. LEVEL itself is the cover's
// embedded *generic.Float (shared with the channel), so it does not
// need a separate subscription. Each subscription also replays the
// wire DP's currently observed value through the same handler so
// the Cover's hot-path-cached fields (c.direction / c.hasDir) land
// in sync with the CCU state at boot, not only on the next push.
// Implements [device.SubscribingDataPoint].
func (c *Cover) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return nil
	}
	var unsubs []func()
	applyDirection := func(next any) {
		c.OnDirection(toCoverDirection(next))
	}
	applyLevel := func(next any) {
		if v, ok := toFloat(next); ok {
			c.OnLevel(v)
		}
	}
	if dp := ch.Parameter(hmenum.ParameterDirection); dp != nil {
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) {
			applyDirection(next)
		}))
		custom.ReplayCurrentValue(dp, applyDirection)
	}
	// LEVEL_2 is the slat-tilt axis for Blind subtypes; the plain
	// Cover has no tilt concept and writing LEVEL_2 into the LEVEL
	// position cache would silently corrupt the position whenever the
	// CCU pushes a tilt update. Blind tracks LEVEL_2 directly through
	// its level2 *generic.Float field instead, so the base Cover
	// subscribes to LEVEL + DIRECTION only.
	_ = applyLevel
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

func toCoverDirection(v any) CoverDirection {
	switch x := v.(type) {
	case int:
		return CoverDirection(x)
	case int32:
		return CoverDirection(int(x))
	case int64:
		return CoverDirection(int(x))
	case float64:
		return CoverDirection(int(x))
	case string:
		switch x {
		case "UP":
			return DirectionUp
		case "DOWN":
			return DirectionDown
		case "NONE":
			return DirectionNone
		}
	}
	return DirectionUnknown
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}
