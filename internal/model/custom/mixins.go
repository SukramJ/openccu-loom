// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Writer is re-exported for ergonomic use by `custom/*` sub-packages
// — they would otherwise have to import generic just for this alias.
type Writer = generic.Writer

// readWriteEvent is the operation triplet most VALUES paramset entries
// expose. Pulled out so the constructor helpers don't repeat the
// bitfield literal four times.
const readWriteEvent = hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent

// ParamsetWriter is re-exported for ergonomic use by `custom/*` sub-packages.
type ParamsetWriter = generic.ParamsetWriter

// PutOrSet writes `values` to `address` using the writer's PutParamset
// When the implementation supports it (mirrors
// `bind_collector` choosing put_paramset for multi-param sets), and
// falls back to per-parameter SetValue calls otherwise. Order of
// SetValue calls is the iteration order of `values` — callers that
// require a specific dispatch order should set parameters one at a
// Time.
// strategy at the per-method level.
//
// Returns the first error encountered; on PutParamset failure the
// CCU's state is authoritative and no rollback is performed (same
// behaviour as the inner CallParameterCollector).
func PutOrSet(
	ctx context.Context,
	w Writer,
	address string,
	paramset hmenum.ParamsetKey,
	values map[hmenum.Parameter]any,
	priority hmenum.CommandPriority,
) error {
	if len(values) == 0 {
		return nil
	}
	// Single parameter → SetValue (matches CallParameterCollector
	// `len(paramset) == 1` short-circuit).
	if len(values) == 1 {
		for p, v := range values {
			return w.SetValue(EnsureContext(ctx), address, p, v, priority)
		}
	}
	// Multiple parameters → prefer PutParamset when the writer
	// supports it.
	if pw, ok := w.(ParamsetWriter); ok {
		raw := make(map[string]any, len(values))
		for p, v := range values {
			raw[string(p)] = v
		}
		return pw.PutParamset(EnsureContext(ctx), address, paramset, raw, priority)
	}
	// Fallback: sequential SetValue per parameter, sorted by parameter
	// name so the order is deterministic across runs (Go's map
	// iteration is intentionally randomised).
	keys := make([]string, 0, len(values))
	byName := make(map[string]hmenum.Parameter, len(values))
	for p := range values {
		s := string(p)
		keys = append(keys, s)
		byName[s] = p
	}
	sort.Strings(keys)
	for _, s := range keys {
		p := byName[s]
		if err := w.SetValue(EnsureContext(ctx), address, p, values[p], priority); err != nil {
			return err
		}
	}
	return nil
}

// PutParamsetForce writes `values` through PutParamset whenever the
// writer supports it — even when `len(values) == 1`. The single-slot
// case still resolves to a put_paramset envelope so collector-scoped
// callers see uniform wire shape. Some CCU firmware variants reject a
// raw setValue for parameters they accept inside put_paramset —
// HmIP CONTROL_MODE=0 (AUTO) is the canonical case.
//
// Falls back to PutOrSet (per-parameter SetValue) when the writer
// does not implement ParamsetWriter.
func PutParamsetForce(
	ctx context.Context,
	w Writer,
	address string,
	paramset hmenum.ParamsetKey,
	values map[hmenum.Parameter]any,
	priority hmenum.CommandPriority,
) error {
	if len(values) == 0 {
		return nil
	}
	if pw, ok := w.(ParamsetWriter); ok {
		raw := make(map[string]any, len(values))
		for p, v := range values {
			raw[string(p)] = v
		}
		return pw.PutParamset(EnsureContext(ctx), address, paramset, raw, priority)
	}
	return PutOrSet(ctx, w, address, paramset, values, priority)
}

// timeUnitThreshold is the maximum value in a given unit before the encoder
// switches to the next coarser unit. The threshold is deliberately not 60 or
// 3600: 61 seconds stays in the seconds bucket (value=61, unit=S) because 61
// < 16343; only when the seconds value exceeds 16343 does the encoder promote
// to minutes.
const timeUnitThreshold = 16343

// TimerNotUsed is the magic sentinel value
// used". When this exact float64 is encoded, the result is (111600, H) so
// the device can distinguish "not set" from any ordinary duration.
const timerNotUsed = 111600.0

// EncodeTimerDuration maps a time.Duration onto a (value, unit) pair matching
// the CCU's combined-timer enum convention (0 = seconds, 1 = minutes, 2 =
// hours). Used by Light.SetOnTime / Light.SetRampTime, Siren.TurnOn duration,
// Switch.TurnOn(on_time) etc.
//
// Threshold semantics : the encoder uses _TIME_UNIT_THRESHOLD = 16343 as the
// bucket boundary, not 60 or 3600. So 61 s → (61, S), not (1, M). The
// promotion chain is: if seconds > 16343 → convert to minutes; then if
// minutes > 16343 → convert to hours. Exact sentinel 111600 s → (111600, H).
func EncodeTimerDuration(d time.Duration) (value, unit int32) {
	secs := d.Seconds()
	const maxInt32 = float64(int64(^uint32(0) >> 1))
	clamp := func(v float64) int32 {
		if v > maxInt32 {
			v = maxInt32
		}
		if v < 0 {
			v = 0
		}
		return int32(v) //nolint:gosec // bounded above; see #20
	}
	if secs <= 0 {
		return 0, 0
	}
	// Special sentinel: timerNotUsed → (111600, H) so the device
	// distinguishes "timer disabled" from any real duration.
	if secs == timerNotUsed {
		return clamp(secs), 2
	}
	t := secs
	u := int32(0) // seconds
	if t > timeUnitThreshold {
		t /= 60
		u = 1 // minutes
	}
	if t > timeUnitThreshold {
		t /= 60
		u = 2 // hours
	}
	return clamp(t), u
}

// NewLevelFloat returns the standard `*generic.Float` backing a
// position / brightness data point: VALUES paramset, parameter LEVEL,
// read+write+event. Cover, ModulatingValve, Light and every Color*
// light variant share this shape.
//
// centralName must be the owning Unit's name (from
// [device.Channel.CentralName]) so that the data point's
// [generic.Spec.CentralName] is correctly set and
// [datapoint.BaseDataPointFields.UniqueID] does not collide across
// multi-CCU deployments. Pass an empty string only in test fixtures
// that have no real CCU context.
func NewLevelFloat(address, centralName string, w Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: readWriteEvent,
		},
		CentralName: centralName,
		Writer:      w,
	})
}

// NewStateSwitch returns the standard `*generic.Switch` backing an
// on/off data point: VALUES paramset, parameter STATE,
// read+write+event. Switch and IrrigationValve share this shape.
//
// centralName must be the owning Unit's name (from
// [device.Channel.CentralName]) so that the data point's
// [generic.Spec.CentralName] is correctly set and
// [datapoint.BaseDataPointFields.UniqueID] does not collide across
// multi-CCU deployments. Pass an empty string only in test fixtures
// that have no real CCU context.
func NewStateSwitch(address, centralName string, w Writer) *generic.Switch {
	return generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: readWriteEvent,
		},
		CentralName: centralName,
		Writer:      w,
	})
}

// StateChangeTimer is a light-weight debouncer used by custom data
// points that emit a burst of transient state changes (e.g. dimmer
// while ramping). Consumers call Schedule to register a pending
// transition; the previously scheduled transition is cancelled if it
// has not yet fired.
type StateChangeTimer struct {
	mu    sync.Mutex
	timer *time.Timer
	delay time.Duration
}

// NewStateChangeTimer returns a timer with the given debounce delay.
func NewStateChangeTimer(delay time.Duration) *StateChangeTimer {
	return &StateChangeTimer{delay: delay}
}

// Schedule (re-)arms the timer. fn will run exactly once after delay
// elapses, unless Schedule is called again first, in which case fn
// replaces the pending callback.
func (t *StateChangeTimer) Schedule(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.delay, fn)
}

// Cancel stops the pending callback if any.
func (t *StateChangeTimer) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// IsTimerStateChange reports whether the timer is currently scheduled
// (armed). Returns true when a pending debounce callback has been
// registered via [Schedule] and not yet fired or cancelled. This is
// The Go equivalent of is_timer_state_change
// (model/custom/mixins.py:is_timer_state_change):
//
//	return self.timer_on_time_running is True or self.timer_on_time is not None
//
// used by light/dimmer set-paths to decide whether to emit an
// event even when the brightness value has not changed, because the
// on-time timer means the device will change state shortly.
func (t *StateChangeTimer) IsTimerStateChange() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.timer != nil
}

// IsStateChangeForOnOff reports whether a turn-on / turn-off command should
// be considered a state change, taking the debounce timer into account.
// Returns true when: - the timer is currently scheduled
// ([IsTimerStateChange]), or - turnOn is true and the current value is not
// already true, or - turnOff is true and the current value is not already
// false.
//
// currentValue is a pointer so callers can pass nil when the current value
// has never been observed; a nil pointer is treated as "neither on nor off"
// so both turn-on and turn-off are considered state changes.
func (t *StateChangeTimer) IsStateChangeForOnOff(turnOn, turnOff bool, currentValue *bool) bool {
	if t.IsTimerStateChange() {
		return true
	}
	if turnOn && (currentValue == nil || !*currentValue) {
		return true
	}
	if turnOff && (currentValue == nil || *currentValue) {
		return true
	}
	return false
}

// ---------- GroupState ----------

// GroupState aggregates per-member boolean states into a group verdict
// ("all on", "any on", "none on"). Used by group-aware custom types
// (light groups, siren groups).
type GroupState struct {
	mu      sync.RWMutex
	members map[string]bool
}

// ---------- GroupState — GroupValue ----------------------------------------

// GroupValue returns true when every member in the group is on (AllOn),
// and false otherwise (including the empty-group case). This is the
// unified single-bool view that mirrors
// GroupStateMixin.group_value (model/custom/mixins.py:group_value):
//
//	return self._dp_group_state.value
//
// In the Go model, GroupState carries the member map; GroupValue collapses
// it to AllOn semantics: the group is "on" only when all members are on.
// Callers that need the AnyOn or AllOn views can use those methods
// directly.
func (g *GroupState) GroupValue() bool {
	return g.AllOn()
}

// ---------- Position ----------

// Position wraps a 0.0–1.0 level value with a CCU-level OpenFraction
// helper. Common to covers and valves.
type Position struct{ level float64 }

// NewPosition clamps level into [0, 1] and returns a Position.
func NewPosition(level float64) Position {
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	return Position{level: level}
}

// Level returns the underlying 0–1 value.
func (p Position) Level() float64 { return p.level }

// OpenFraction returns the position as a 0–100 percentage.
func (p Position) OpenFraction() int { return int(p.level*100 + 0.5) }

// Closed reports whether the position is exactly 0.
func (p Position) Closed() bool { return p.level == 0 }

// Open reports whether the position is fully open (level == 1).
func (p Position) Open() bool { return p.level == 1 }

// Vent reports whether the position matches the garage door's
// intermediate ventilation step (level == 0.5). Used by Garage's
// IsStateChangeArgs / Vent service path; covers without an
// intermediate position never see this true.
func (p Position) Vent() bool { return p.level == 0.5 }

// ---------- Brightness ----------

// Brightness wraps a 0.0–1.0 light level with an 8-bit "byte"
// projection useful for MQTT and REST consumers.
type Brightness struct{ level float64 }

// NewBrightness clamps level into [0, 1].
func NewBrightness(level float64) Brightness {
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	return Brightness{level: level}
}

// Level returns the 0–1 value.
func (b Brightness) Level() float64 { return b.level }

// Byte returns the value as 0–255.
func (b Brightness) Byte() uint8 { return uint8(b.level * 255) }

// Pct returns the brightness as a 0–100 integer percentage.
//
// return int(level * 100)
//
// Note: unlike [Byte] (which uses 255 as the ceiling), Pct uses 100 so that
// the HA brightness_pct entity field receives an integer in the [0, 100]
// range without any float-to-byte rounding.
func (b Brightness) Pct() int { return int(b.level * 100) }

// IsOn reports whether brightness is above zero.
func (b Brightness) IsOn() bool { return b.level > 0 }

// NewGroupState returns an empty group.
func NewGroupState() *GroupState {
	return &GroupState{members: make(map[string]bool)}
}

// Set stores the member's state.
func (g *GroupState) Set(name string, on bool) {
	g.mu.Lock()
	g.members[name] = on
	g.mu.Unlock()
}

// Remove drops a member.
func (g *GroupState) Remove(name string) {
	g.mu.Lock()
	delete(g.members, name)
	g.mu.Unlock()
}

// AllOn reports whether every member is on. Empty groups return false.
func (g *GroupState) AllOn() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.members) == 0 {
		return false
	}
	for _, v := range g.members {
		if !v {
			return false
		}
	}
	return true
}

// AnyOn reports whether at least one member is on.
func (g *GroupState) AnyOn() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, v := range g.members {
		if v {
			return true
		}
	}
	return false
}

// ---------- Capability markers ----------

// ClimateCapabilities, CoverCapabilities, LightCapabilities,
// LockCapabilities, and SirenCapabilities are marker structs that
// concrete custom types embed to expose a uniform capability surface
// to north-bound adapters. Each capability names what the device is
// guaranteed to support — the adapters don't need to introspect the
// struct itself.
type (
	// ClimateCapabilities signals the device exposes a climate
	// interface with set-temperature, modes and profiles.
	ClimateCapabilities struct {
		SupportsBoost   bool
		SupportsProfile bool
		SupportsAuto    bool
		SupportsHeat    bool
		SupportsCool    bool
		SupportsOff     bool
		SupportsAway    bool
		// SupportsComfort advertises the COMFORT preset profile (typical RF
		// thermostats: HM-CC-RT-DN, HmIP-eTRV).
		SupportsComfort bool
		// SupportsEco advertises the ECO preset profile (typical RF
		// thermostats — co-listed with COMFORT).
		SupportsEco    bool
		MinTemperature float64
		MaxTemperature float64
		// TemperatureStep is the smallest setpoint adjustment the device accepts
		// (typically 0.5 °C).
		TemperatureStep float64
		// TemperatureUnit is the wire/UI unit, defaulting to "°C" when
		// empty.
		TemperatureUnit string
	}

	// CoverCapabilities signals a position-based cover.
	CoverCapabilities struct {
		SupportsTilt bool
		SupportsStop bool
		// SupportsPosition advertises that the device accepts a position write
		// (LEVEL parameter). HA discovery uses this to decide whether to emit a
		// `position_topic`/`set_position_topic` pair.
		SupportsPosition bool
		// SupportsVent advertises the garage "vent" intermediate position the
		// IP-Garage drive supports.
		SupportsVent    bool
		InvertedControl bool
	}

	// LightCapabilities signals a light.
	LightCapabilities struct {
		Dimmable          bool
		SupportsColor     bool
		SupportsColorTemp bool
		SupportsEffects   bool
		// Transition indicates the device accepts RAMP_TIME, enabling HA's
		// transition field in JSON-Schema MQTT light mode.
		Transition bool
	}

	// LockCapabilities signals a lock.
	LockCapabilities struct {
		SupportsOpen      bool
		SupportsChildSafe bool
	}

	// SirenCapabilities signals an acoustic/optical siren.
	SirenCapabilities struct {
		SupportsAcoustic bool
		SupportsOptical  bool
		SupportsDuration bool
		// SupportsSoundfiles advertises a SOUND_PLAYER preset that allows selecting
		// one of the device's recorded sound files rather than just an alarm tone.
		// HA discovery uses this to decide whether to emit a `available_tones`
		// array in the siren entity payload.
		SupportsSoundfiles bool
		// SupportsVolumeSet reports whether the siren can adjust its volume. HA
		// discovery uses this for the `support_volume_set` field in the siren
		// entity payload.
		SupportsVolumeSet bool
	}
)

// ---------- Context helper ----------

// EnsureContext returns ctx or context.Background() when ctx is nil.
// Tiny helper used by custom data-points that accept an optional
// context in set/send paths.
func EnsureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// ---------- BaseDP — observability fields ----------

// BaseDP carries the three observability timestamps / counters that
//
// - modified_at — wall time of the last local write (command sent to CCU).
// - refreshed_at — wall time of the last confirmed value received from CCU.
// - unconfirmed_last_values_send — number of in-flight writes that have
// not yet been confirmed by a CCU event. A non-zero value means the
// device may still be in transition.
//
// (model/custom/data_point.py:modified_at, refreshed_at,
// unconfirmed_last_values_send). Embed BaseDP into concrete custom types
// that need these observability signals.
type BaseDP struct {
	mu                        sync.Mutex
	modifiedAt                time.Time
	refreshedAt               time.Time
	unconfirmedLastValuesSend int
}

// MarkModified records the wall time of the most recent outbound command.
// Call this immediately before issuing a SetValue / PutOrSet so that
// observers can detect in-flight transitions. Thread-safe.
func (b *BaseDP) MarkModified() {
	b.mu.Lock()
	b.modifiedAt = time.Now()
	b.unconfirmedLastValuesSend++
	b.mu.Unlock()
}

// MarkRefreshed records the wall time of the most recent inbound CCU event
// and decrements the in-flight counter (clamped at zero). Call this when
// a CCU callback delivers a new value for this data point. Thread-safe.
func (b *BaseDP) MarkRefreshed() {
	b.mu.Lock()
	b.refreshedAt = time.Now()
	if b.unconfirmedLastValuesSend > 0 {
		b.unconfirmedLastValuesSend--
	}
	b.mu.Unlock()
}

// ModifiedAt returns the time of the last outbound command and whether it
// has ever been set.
func (b *BaseDP) ModifiedAt() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modifiedAt, !b.modifiedAt.IsZero()
}

// RefreshedAt returns the time of the last CCU confirmation and whether
// it has ever been set.
func (b *BaseDP) RefreshedAt() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refreshedAt, !b.refreshedAt.IsZero()
}

// UnconfirmedLastValuesSend returns the number of in-flight writes that have
// not yet been confirmed by a CCU event.
func (b *BaseDP) UnconfirmedLastValuesSend() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.unconfirmedLastValuesSend
}
