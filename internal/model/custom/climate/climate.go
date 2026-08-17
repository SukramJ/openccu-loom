// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package climate implements the thermostat custom data point.
//
// A single Climate type covers the three physical thermostat flavours
// (SimpleRF, RF, IP) by reading the [Kind] field at command time.
// Wire-backed values (ACTUAL_TEMPERATURE, setpoint, humidity) are
// references to the channel's existing generic data points — Climate
// holds typed pointers, not duplicate instances. The Mode / Profile
// states are synthesised from several wire parameters depending on
// Kind and stay on the Climate struct itself.
package climate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Writer is an alias for [generic.Writer].
type Writer = generic.Writer

// HeatingValveType enumerates the two heating valve wiring modes that HmIP
// thermostats report on the HEATING_VALVE_TYPE parameter.
type HeatingValveType string

// HeatingValveType values.
const (
	// HeatingValveTypeNormallyClose is the default wiring: valve is closed
	// when power is off (active cooling when drive signal is absent).
	HeatingValveTypeNormallyClose HeatingValveType = "NORMALLY_CLOSE"
	// HeatingValveTypeNormallyOpen: valve is open when power is off
	// (active heating when drive signal is absent).
	HeatingValveTypeNormallyOpen HeatingValveType = "NORMALLY_OPEN"
)

// Mode enumerates the high-level climate modes every backend supports. It is a
// type alias of [hmenum.ClimateMode] — the canonical, codegen-exported home of
// the vocabulary — so the closed mode set lives in one place and the schema
// exporter (which scans pkg/hmenum) picks it up automatically.
type Mode = hmenum.ClimateMode

// Mode values (re-exported from hmenum for the climate package's call sites).
const (
	ModeAuto = hmenum.ClimateModeAuto
	ModeHeat = hmenum.ClimateModeHeat
	ModeCool = hmenum.ClimateModeCool
	ModeOff  = hmenum.ClimateModeOff
)

// Profile enumerates the thermostat profile slots. Type alias of
// [hmenum.ClimateProfile]; see [Mode].
type Profile = hmenum.ClimateProfile

// Profile values (re-exported from hmenum for the climate package's call sites).
const (
	ProfileNone         = hmenum.ClimateProfileNone
	ProfileAway         = hmenum.ClimateProfileAway
	ProfileBoost        = hmenum.ClimateProfileBoost
	ProfileComfort      = hmenum.ClimateProfileComfort
	ProfileEco          = hmenum.ClimateProfileEco
	ProfileWeekProgram1 = hmenum.ClimateProfileWeekProgram1
	ProfileWeekProgram2 = hmenum.ClimateProfileWeekProgram2
	ProfileWeekProgram3 = hmenum.ClimateProfileWeekProgram3
	ProfileWeekProgram4 = hmenum.ClimateProfileWeekProgram4
	ProfileWeekProgram5 = hmenum.ClimateProfileWeekProgram5
	ProfileWeekProgram6 = hmenum.ClimateProfileWeekProgram6
)

// ProfilePrefix is the string prefix shared by all week-program profile keys
// (e.g. "week_program_1").
const ProfilePrefix = "week_program_"

// HMWeekProfilePointersToNames maps the RF WEEK_PROGRAM_POINTER integer index
// (0-based) to the CCU internal week-program name string. Used when writing
// ACTIVE_PROFILE / WEEK_PROGRAM_POINTER to the CCU.
var HMWeekProfilePointersToNames = map[int]string{
	0: "WEEK PROGRAM 1",
	1: "WEEK PROGRAM 2",
	2: "WEEK PROGRAM 3",
	3: "WEEK PROGRAM 4",
	4: "WEEK PROGRAM 5",
	5: "WEEK PROGRAM 6",
}

// HMWeekProfilePointersToIdx is the reverse of
// [HMWeekProfilePointersToNames]: it maps a CCU week-program name back to its
// 0-based pointer index.
var HMWeekProfilePointersToIdx = func() map[string]int {
	m := make(map[string]int, len(HMWeekProfilePointersToNames))
	for k, v := range HMWeekProfilePointersToNames {
		m[v] = k
	}
	return m
}()

// ErrModeNotSupported is returned when Set* is called for a mode not
// listed in the capability profile.
var ErrModeNotSupported = errors.New("climate: mode not supported by device")

// Kind discriminates between the three thermostat flavours.
type Kind int

// Kind values.
const (
	KindSimpleRF Kind = iota
	KindRF
	KindIP
)

// Config is the constructor record. Channel carries the per-channel
// generic data points Climate references for ACTUAL_TEMPERATURE, the
// setpoint parameter (kind-dependent) and HUMIDITY. Writer is used for
// the synthesis-only parameters that are not held as typed fields
// (mode, profile, boost).
type Config struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.ClimateCapabilities
	Kind         Kind

	// ActivityStateChannels lists the absolute channel numbers whose
	// STATE parameter acts as the heating-activity source per the
	// device profile's channel-field map (e.g. the HmIP-BWTH relay on
	// channel 9, the heating-group switch channel). Resolved by the
	// IP profile constructors from the rebased channel-group schema;
	// empty for devices whose profile maps no STATE field.
	ActivityStateChannels []int
}

// Climate is a thermostat custom data point.
type Climate struct {
	// baseDP carries the observability timestamps and in-flight write counter.
	// Named rather than embedded to avoid ambiguity with the struct's own mu field.
	baseDP custom.BaseDP

	Address      string
	Capabilities custom.ClimateCapabilities
	Kind         Kind

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful MatterWrite / MatterInvoke
	// so DataVersionFilter evaluation correctly detects cluster changes.
	dataVersion hmtypes.DataVersionTracker

	// key is the composite data-point key used by [DataPointKey] to
	// satisfy [device.AttachableDataPoint]. It is keyed on the setpoint
	// parameter so the materializer can attach this DP to the channel.
	key hmtypes.DataPointKey

	// Wire-backed values held as typed references to the channel's
	// generic data points. Each pointer is the same instance the
	// channel exposes via Channel.Parameter — there is exactly one
	// instance per (channel, parameter), and an event arriving on the
	// channel's DP is immediately visible here.
	setpoint          *generic.Float
	actualTemperature *generic.Sensor[float64]
	humidity          *generic.Sensor[float64]
	// humidityInt covers HmIP thermostats, whose HUMIDITY parameter is
	// INTEGER-typed on the wire (BidCos wall thermostats ship FLOAT).
	humidityInt        *generic.Sensor[int32]
	temperatureMinimum *generic.Float // TEMPERATURE_MINIMUM operator override
	temperatureMaximum *generic.Float // TEMPERATURE_MAXIMUM operator override
	// activityStateChannels carries [Config.ActivityStateChannels];
	// consulted by [Climate.activityStateDPs] to resolve the heating-
	// relay STATE data points on sibling channels at call time.
	activityStateChannels []int
	// channelRef is the climate channel handed to the constructor.
	// Held only so [numWeekPrograms] can walk to the device root at
	// call-time — the WEEK_PROGRAM_POINTER DP for several RF
	// thermostats lives on the device-root MASTER paramset (e.g.
	// HM-CC-VG-1 → INT0000001), which the device pipeline only
	// populates after Climate construction.
	channelRef *device.Channel

	writer Writer

	// Mode and Profile are synthesised from several wire parameters
	// and stay bespoke.
	mu                sync.RWMutex
	mode              Mode
	hasMode           bool
	profile           Profile
	hasProfile        bool
	activity          Activity
	hasActivity       bool
	temperatureOffset string
	hasTempOffset     bool
	awayUntil         time.Time
	hasAwayUntil      bool

	// activeProfileIdx is the last observed ACTIVE_PROFILE (IP) or
	// WEEK_PROGRAM_POINTER (RF) value — the operator-selected week-program slot
	// that drives `profile = ProfileWeekProgram<n>` when the thermostat is in
	// AUTO mode. Stored independently of `profile` so a SET_POINT_MODE → AUTO
	// transition can recover the week-program selection from the cache instead
	// of waiting for the next ACTIVE_PROFILE push. Guarded by mu.
	activeProfileIdx    int
	hasActiveProfileIdx bool

	muHC           sync.RWMutex
	heatingCooling string
	hasHC          bool

	// optimumStartStop holds the last observed OPTIMUM_START_STOP
	// parameter value (IP thermostats only). Guarded by mu.
	optimumStartStop    bool
	hasOptimumStartStop bool

	// minMaxNotRelevantForManu holds the last observed
	// MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE parameter value. Guarded by mu.
	minMaxNotRelevantForManu bool

	// oldManuSetpoint caches the last setpoint observed while the thermostat
	// was in MANU (HEAT) mode. When the operator later switches back to HEAT
	// from AUTO or OFF, temperatureForHeatMode() returns this cached value
	// instead of the current setpoint (which may be the OFF sentinel 4.5 °C
	// or a week-program value). Guarded by mu.
	oldManuSetpoint    float64
	hasOldManuSetpoint bool
}

// Activity enumerates the high-level heating/cooling activity state
// derived from VALVE_STATE / LEVEL / STATE depending on the family.
type Activity string

// Activity values.
const (
	ActivityIdle    Activity = "idle"
	ActivityHeating Activity = "heating"
	ActivityCooling Activity = "cooling"
	// ActivityOff signals the thermostat is in OFF mode (heating disabled, no
	// setpoint tracking). HA's `hvac_action` field reads this directly.
	ActivityOff Activity = "off"
)

// New constructs a Climate. The channel must already carry the
// per-kind setpoint, ACTUAL_TEMPERATURE and (optional) HUMIDITY data
// points; missing fields leave the corresponding accessor returning
// `(zero, false)` — Climate gracefully degrades for partial
// thermostats (e.g. RF without humidity).
func New(cfg Config) *Climate {
	address := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		address = cfg.Channel.Address
		// Build the DataPointKey from the setpoint DP when available;
		// fall back to a minimal key (ChannelAddress + parameter name)
		// so the materializer can attach us even on channels without a
		// wired setpoint. The key satisfies [device.AttachableDataPoint].
		setpointParam := paramForSetpoint(cfg.Kind)
		if sp := custom.FloatField(cfg.Channel, setpointParam); sp != nil {
			key = sp.DataPointKey()
		} else {
			key = hmtypes.DataPointKey{
				ChannelAddress: address,
				Parameter:      string(setpointParam),
			}
		}
	}
	c := &Climate{
		Address:            address,
		Capabilities:       cfg.Capabilities,
		Kind:               cfg.Kind,
		key:                key,
		writer:             cfg.Writer,
		setpoint:           custom.FloatField(cfg.Channel, paramForSetpoint(cfg.Kind)),
		actualTemperature:  custom.FloatSensorField(cfg.Channel, hmenum.ParameterActualTemperature),
		humidity:           custom.FloatSensorField(cfg.Channel, hmenum.ParameterHumidity),
		humidityInt:        custom.IntegerSensorField(cfg.Channel, hmenum.ParameterHumidity),
		temperatureMinimum: custom.FloatField(cfg.Channel, hmenum.ParameterTemperatureMinimum),
		temperatureMaximum: custom.FloatField(cfg.Channel, hmenum.ParameterTemperatureMaximum),
		// channelRef carries the climate channel so the lazy week-
		// program-pointer lookup in `numWeekPrograms` can walk to the
		// device root at call time. Construction-time resolution
		// missed RF thermostats whose root MASTER paramset is
		// populated only after Climate is registered.
		channelRef:            cfg.Channel,
		activityStateChannels: cfg.ActivityStateChannels,
	}
	c.registerServices()
	// Matter §10.6.5: DataVersion advances on every CCU-confirmed attribute change.
	if c.setpoint != nil {
		_ = c.setpoint.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	if c.actualTemperature != nil {
		_ = c.actualTemperature.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	if c.humidity != nil {
		_ = c.humidity.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	if c.humidityInt != nil {
		_ = c.humidityInt.OnConfirmedUpdate(func(_, _ int32) { c.dataVersion.Bump() })
	}
	if c.temperatureMinimum != nil {
		_ = c.temperatureMinimum.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	if c.temperatureMaximum != nil {
		_ = c.temperatureMaximum.OnConfirmedUpdate(func(_, _ float64) { c.dataVersion.Bump() })
	}
	return c
}

// DataPointKey returns the composite identifier used by the materializer
// to attach this custom DP to its primary channel. Satisfies
// [device.AttachableDataPoint].
func (c *Climate) DataPointKey() hmtypes.DataPointKey { return c.key }

// Category reports the HA data-point category — clients spawn the
// entity off this value (climate platform).
func (c *Climate) Category() hmenum.DataPointCategory { return hmenum.DataPointCategoryClimate }

// aggregate returns the AggregateView over Climate's wire-backed slots
// (setpoint + actual_temperature + humidity). The synthetic fields (mode /
// profile / activity) are derived state so they don't participate in the
// aggregate refresh check — they're meaningful only once at least one wire
// slot has been observed.
func (c *Climate) aggregate() custom.AggregateView {
	slots := make([]custom.AggregateSlot, 0, 3)
	if c.setpoint != nil {
		slots = append(slots, c.setpoint)
	}
	if c.actualTemperature != nil {
		slots = append(slots, c.actualTemperature)
	}
	if c.humidity != nil {
		slots = append(slots, c.humidity)
	}
	if c.humidityInt != nil {
		slots = append(slots, c.humidityInt)
	}
	return custom.AggregateStatus(slots...)
}

// IsRefreshed reports whether the climate aggregate has observed at
// least one wire-level reading. North-bound adapters use it to gate
// "available" badges so they don't render an HA entity as
// "unbekannt" before any data lands.
func (c *Climate) IsRefreshed() bool { return c.aggregate().IsRefreshed() }

// StateUncertain reports whether any wire-level slot is currently
// awaiting CCU confirmation.
func (c *Climate) StateUncertain() bool { return c.aggregate().StateUncertain() }

// IsStatusValid reports whether all wire-level slots have a valid STATUS
// parameter state (no OVERFLOW / ERROR). REST and MQTT adapters may use
// this to gate "status_ok" badges.
func (c *Climate) IsStatusValid() bool { return c.aggregate().IsStatusValid() }

// SubDataPointKeys returns the wire identifiers of every wire-level
// slot. Used by REST diagnostic endpoints.
func (c *Climate) SubDataPointKeys() []hmtypes.DataPointKey {
	return c.aggregate().SubDataPointKeys()
}

// MarkModified records the wall time of the most recent outbound command.
// Thread-safe.
func (c *Climate) MarkModified() { c.baseDP.MarkModified() }

// MarkRefreshed records the wall time of the most recent inbound CCU event.
// Thread-safe.
func (c *Climate) MarkRefreshed() { c.baseDP.MarkRefreshed() }

// ModifiedAt returns the time of the last outbound command and whether it
// has ever been set.
func (c *Climate) ModifiedAt() (time.Time, bool) { return c.baseDP.ModifiedAt() }

// RefreshedAt returns the time of the last CCU confirmation and whether
// it has ever been set.
func (c *Climate) RefreshedAt() (time.Time, bool) { return c.baseDP.RefreshedAt() }

// UnconfirmedLastValuesSend returns the number of in-flight writes.
func (c *Climate) UnconfirmedLastValuesSend() int { return c.baseDP.UnconfirmedLastValuesSend() }

// --- accessors ---

// CurrentTemperature returns the last observed ACTUAL_TEMPERATURE.
func (c *Climate) CurrentTemperature() (float64, bool) {
	if c.actualTemperature == nil {
		return 0, false
	}
	return c.actualTemperature.Value()
}

// Setpoint returns the last observed target temperature.
func (c *Climate) Setpoint() (float64, bool) {
	if c.setpoint == nil {
		return 0, false
	}
	return c.setpoint.Value()
}

// Mode returns the last observed mode.
func (c *Climate) Mode() (Mode, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode, c.hasMode
}

// Profile returns the last observed profile.
func (c *Climate) Profile() (Profile, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profile, c.hasProfile
}

// Humidity returns the last observed HUMIDITY. HmIP thermostats type
// the parameter INTEGER on the wire, BidCos wall thermostats FLOAT —
// whichever slot resolved is read.
func (c *Climate) Humidity() (float64, bool) {
	if c.humidity != nil {
		return c.humidity.Value()
	}
	if c.humidityInt != nil {
		if v, ok := c.humidityInt.Value(); ok {
			return float64(v), true
		}
	}
	return 0, false
}

// HasHumidity reports whether the channel carries a HUMIDITY parameter
// at all, in either of the two wire types [Climate.Humidity] reads.
// Surfaces that decide *whether to advertise* a humidity plane — the HA
// discovery payload, the Matter RelativeHumidityMeasurement server —
// must ask this rather than probing one slot, otherwise every HmIP wall
// thermostat (INTEGER-typed HUMIDITY) silently loses the plane.
func (c *Climate) HasHumidity() bool {
	if c == nil {
		return false
	}
	return c.humidity != nil || c.humidityInt != nil
}

// Modes returns the list of modes this climate device supports,
// Derived from the capability flags.
// state-property (climate.py:511, :755) which the UI layer reads to
// Build the mode dropdown. Order matches
// canonical order: AUTO, HEAT, COOL, OFF.
func (c *Climate) Modes() []Mode {
	modes := make([]Mode, 0, 4)
	if c.Capabilities.SupportsAuto {
		modes = append(modes, ModeAuto)
	}
	if c.Capabilities.SupportsHeat {
		modes = append(modes, ModeHeat)
	}
	if c.Capabilities.SupportsCool {
		modes = append(modes, ModeCool)
	}
	if c.Capabilities.SupportsOff {
		modes = append(modes, ModeOff)
	}
	return modes
}

// Profiles returns the list of profiles this climate device exposes.
// The default set is the six week-program slots
// surfaces (profile_names property in climate.py:471, :679); BOOST and
// AWAY are appended when the capability flags advertise them.
// `_profiles` calculation.
//
// **Mode-aware (ADR 0011):** week-program slots are only
// surfaced when the thermostat is currently in AUTO mode —.
//
//	control_modes = [ClimateProfile.BOOST, ClimateProfile.NONE]
//	if self.mode == ClimateMode.AUTO:
//	 control_modes.extend(self._profile_names)
//
// HA's MQTT Climate platform takes preset_modes statically in the
// discovery config; the bridge therefore re-renders + re-publishes
// the discovery whenever this list changes (driven by the
// [DiscoveryTriggers] CONTROL_MODE / HEATING_COOLING / ACTIVE_PROFILE).
// Until Mode() returns AUTO the list shrinks to just BOOST + capability
// flags so HA's preset selector doesn't show invalid options.
func (c *Climate) Profiles() []Profile {
	if !c.Capabilities.SupportsProfile {
		return nil
	}
	out := make([]Profile, 0, 9)
	out = append(out, ProfileNone)
	// BOOST is in every preset list
	// IP and RF thermostats.
	if c.Capabilities.SupportsBoost {
		out = append(out, ProfileBoost)
	}
	// COMFORT + ECO are RF-only
	// `CustomDpRfThermostat.profiles` always includes them.
	// IP-thermostats omit them.
	if c.Capabilities.SupportsComfort {
		out = append(out, ProfileComfort)
	}
	if c.Capabilities.SupportsEco {
		out = append(out, ProfileEco)
	}
	// Week-programs are mode-aware
	// `mode == ClimateMode.AUTO` (climate.py:530-535 / :776-781). HA's
	// MQTT-Climate platform refreshes preset_modes from discovery, so
	// openccu-loom re-publishes the discovery payload via
	// [DiscoveryTriggers] when CONTROL_MODE / HEATING_COOLING
	// ACTIVE_PROFILE flips.
	//
	// Count is kind-dependent (mirrors
	// `_dp_week_program_pointer` descriptor range — IP devices ship
	// six pointer slots, RF devices three).
	//
	// Discovery-time bootstrap (no event observed yet)
	// `mode` getter reads the wire DP whose descriptor `DEFAULT`
	// resolves to AUTO for IP/RF thermostats — at first discovery the
	// device IS treated as AUTO. Match that by treating "no observed
	// mode" as AUTO when the device supports the auto mode at all.
	mode, ok := c.Mode()
	// Discovery-time bootstrap
	// the wire DP whose descriptor DEFAULT resolves to AUTO for both
	// IP- and RF-thermostat families when the CCU has not been
	// reconfigured. SimpleRF lacks the dedicated CONTROL_MODE DP, so
	// it is not bootstrapped.
	bootstrapAuto := !ok && c.Capabilities.SupportsAuto &&
		(c.Kind == KindIP || c.Kind == KindRF)
	if (ok && mode == ModeAuto) || bootstrapAuto {
		count := c.numWeekPrograms()
		programs := []Profile{
			ProfileWeekProgram1,
			ProfileWeekProgram2,
			ProfileWeekProgram3,
			ProfileWeekProgram4,
			ProfileWeekProgram5,
			ProfileWeekProgram6,
		}
		if count > len(programs) {
			count = len(programs)
		}
		out = append(out, programs[:count]...)
	}
	return out
}

// MinTemp returns the effective minimum setpoint temperature applying
// The three-step resolution chain.
// `BaseCustomDpClimate.min_temp` (model/custom/climate.py:110-119):
//
// 1. Operator-configured TEMPERATURE_MINIMUM observed value (when present).
// 2. SET_POINT_TEMPERATURE descriptor MIN (when set by CCU).
// 3. [custom.ClimateCapabilities].MinTemperature fallback.
//
// When the resolved value equals `_OFF_TEMPERATURE` (4.5 °C — the
// "off" sentinel used by HmIP/HM thermostats)
// `_DEFAULT_TEMPERATURE_STEP` (0.5) to keep HA's slider from
// presenting the off-state as a normal setpoint. Mirror that
// behaviour 1:1; otherwise openccu-loom emitted 4.5 where
// Shows 5.0 for ~13 thermostat models.
func (c *Climate) MinTemp() float64 {
	const offTemperature = 4.5
	const defaultTemperatureStep = 0.5

	resolve := func() float64 {
		// Step 1: operator-configured TEMPERATURE_MINIMUM.
		if dp := c.crossChannelTemperatureBound(hmenum.ParameterTemperatureMinimum); dp != nil {
			if v, ok := dp.Value(); ok {
				return v
			}
		}
		// Step 2: setpoint descriptor MIN.
		if c.setpoint != nil {
			if lo, ok := c.setpoint.DescriptorMin(); ok {
				return lo
			}
		}
		return c.Capabilities.MinTemperature
	}

	v := resolve()
	if v == offTemperature {
		return v + defaultTemperatureStep
	}
	return v
}

// MaxTemp returns the effective maximum setpoint temperature applying
// The three-step resolution chain.
// `CustomDpIpThermostat.max_temp`:
//
// 1. Operator-configured TEMPERATURE_MAXIMUM observed value (when present).
// 2. SET_POINT_TEMPERATURE descriptor MAX (when set by CCU).
// 3. [custom.ClimateCapabilities].MaxTemperature fallback.
func (c *Climate) MaxTemp() float64 {
	// Step 1: operator-configured TEMPERATURE_MAXIMUM. Same
	// whitelist-gating as [MinTemp] — only models in
	// `RELEVANT_MASTER_PARAMSETS_BY_DEVICE` actually surface the DP
	// In; off-list models get a DpDummy.
	if dp := c.crossChannelTemperatureBound(hmenum.ParameterTemperatureMaximum); dp != nil {
		if v, ok := dp.Value(); ok {
			return v
		}
	}

	// Step 2: setpoint descriptor MAX. Use the partial-accept reader
	// so a setpoint that ships MAX-only (no MIN) still contributes
	// its MAX.
	if c.setpoint != nil {
		if hi, ok := c.setpoint.DescriptorMax(); ok {
			return hi
		}
	}

	return c.Capabilities.MaxTemperature
}

// TemperatureStep returns the smallest setpoint adjustment, defaulting to 0.5
// °C when not configured.
func (c *Climate) TemperatureStep() float64 {
	if c.Capabilities.TemperatureStep > 0 {
		return c.Capabilities.TemperatureStep
	}
	return 0.5
}

// offTemperatureConst is the setpoint sentinel that means "thermostat is off"
// on HmIP/HM thermostats.
const offTemperatureConst = 4.5

// temperatureForHeatMode returns a safe setpoint to use when transitioning to
// HEAT (or COOL) mode.
//
// 1. Read the last observed setpoint. 2. If none observed, or the value is ≤
// _OFF_TEMPERATURE (4.5), or the value is below min_temp: return min_temp
// when min_temp > 4.5, otherwise return 4.5 + 0.5 = 5.0. 3. If the value
// exceeds max_temp: clamp to max_temp. 4. Otherwise return the observed value
// as-is.
func (c *Climate) temperatureForHeatMode() float64 {
	minTemp := c.MinTemp()
	maxTemp := c.MaxTemp()

	// Prefer the cached manual setpoint (last setpoint observed while in HEAT
	// mode) so a HEAT→AUTO→HEAT round-trip restores the operator's chosen
	// temperature rather than falling back to min or the week-program value.
	temp, hasTemp := func() (float64, bool) {
		c.mu.RLock()
		old, hasOld := c.oldManuSetpoint, c.hasOldManuSetpoint
		c.mu.RUnlock()
		if hasOld {
			return old, true
		}
		if c.setpoint == nil {
			return 0, false
		}
		return c.setpoint.Value()
	}()

	// Step 2: None / OFF sentinel / below min.
	if !hasTemp || temp <= offTemperatureConst || temp < minTemp {
		if minTemp > offTemperatureConst {
			return minTemp
		}
		return offTemperatureConst + 0.5
	}
	// Step 3: clamp to max.
	if temp > maxTemp {
		return maxTemp
	}
	return temp
}

// TemperatureUnit returns the temperature unit, defaulting to "°C".
func (c *Climate) TemperatureUnit() string {
	if c.Capabilities.TemperatureUnit != "" {
		return c.Capabilities.TemperatureUnit
	}
	return "°C"
}

// TemperatureOffset returns the last observed temperature offset as a string
// label (RF devices, e.g. "0.0 K", "-3.5 K") or numeric string (IP devices).
// Returns ("", false) when the parameter has not been observed yet or the
// channel does not expose TEMPERATURE_OFFSET.
func (c *Climate) TemperatureOffset() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasTempOffset {
		return "", false
	}
	return c.temperatureOffset, true
}

// SetTemperatureOffset writes a new TEMPERATURE_OFFSET value.
// The value must be a string label for RF devices (e.g. "0.0 K", "-3.5 K")
// or a numeric string for IP devices. The CCU accepts the string form for
// DpSelect parameters and numeric for DpFloat. Returns [ErrModeNotSupported]
// when the writer or address is missing.
func (c *Climate) SetTemperatureOffset(ctx context.Context, v string, priority hmenum.CommandPriority) error {
	if c.writer == nil {
		return ErrModeNotSupported
	}
	// TEMPERATURE_OFFSET lives on a MASTER paramset — on the climate channel
	// for HmIP thermostats and on the device-root channel for the classic RF
	// family. A plain setValue only ever reaches VALUES, so the offset never
	// arrives at the thermostat; resolve the owning channel the way the
	// TEMPERATURE_MINIMUM / MAXIMUM bounds do and write it through the MASTER
	// put_paramset path.
	address, paramset := c.masterConfigWriteTarget(resolveConfigParam(c.channelRef, hmenum.ParameterTemperatureOffset))
	if err := custom.PutParamsetForce(custom.EnsureContext(ctx), c.writer, address, paramset, map[hmenum.Parameter]any{
		hmenum.ParameterTemperatureOffset: v,
	}, priority); err != nil {
		return fmt.Errorf("climate: set temperature offset: %w", err)
	}
	c.mu.Lock()
	c.temperatureOffset = v
	c.hasTempOffset = true
	c.mu.Unlock()
	return nil
}

// OnTemperatureOffset records a CCU-emitted TEMPERATURE_OFFSET update.
// Accepts both string labels (RF DpSelect) and float64 values (IP DpFloat),
// storing both as a string representation.
func (c *Climate) OnTemperatureOffset(v any) {
	var s string
	switch tv := v.(type) {
	case string:
		s = tv
	case float64:
		s = fmt.Sprintf("%.1f", tv)
	case float32:
		s = fmt.Sprintf("%.1f", float64(tv))
	default:
		s = fmt.Sprintf("%v", v)
	}
	c.mu.Lock()
	c.temperatureOffset = s
	c.hasTempOffset = true
	c.mu.Unlock()
}

// Activity reports the current heating-vs-idle status of the thermostat.
// Computed from LEVEL / STATE (IP) or VALVE_STATE (classic RF).
// Returns observed=false when the underlying source has not reported yet.
func (c *Climate) Activity() (Activity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasActivity {
		return ActivityIdle, false
	}
	return c.activity, true
}

// HasActivitySource reports whether the thermostat carries any wire
// source the activity (HA `hvac_action`) can be derived from:
//
//   - KindIP: LEVEL or STATE on the climate channel, or a STATE DP on
//     one of the profile-mapped activity-state channels (HmIP-BWTH
//     relay channel, heating-group switch channel).
//   - KindRF: VALVE_STATE on the climate channel.
//   - KindSimpleRF: none.
//
// A previously fed activity value (e.g. from linked peer channels via
// [Climate.RefreshLinkPeerActivitySources]) also counts as a source.
//
// Display-only thermostats without any source (HmIP-STHD) report
// false; the reference stack returns `activity = None` for them, so
// the aggregate state and the HA discovery omit the action surface
// entirely instead of stamping a misleading "idle".
func (c *Climate) HasActivitySource() bool {
	c.mu.RLock()
	fed := c.hasActivity
	c.mu.RUnlock()
	if fed {
		return true
	}
	ch := c.channelRef
	if ch == nil {
		return false
	}
	switch c.Kind {
	case KindIP:
		if ch.Parameter(hmenum.ParameterLevel) != nil || ch.Parameter(hmenum.ParameterState) != nil {
			return true
		}
		return len(c.activityStateDPs()) > 0
	case KindRF:
		return ch.Parameter(hmenum.ParameterValveState) != nil
	case KindSimpleRF:
		return false
	default:
		return false
	}
}

// activityStateDPs resolves the STATE data points of the profile-
// mapped activity-state channels ([Config.ActivityStateChannels])
// against the device at call time, so pipeline ordering (channels
// hydrated after Climate construction) does not matter. Channels that
// do not exist on the device — the profile schema lists offsets for
// the whole family, e.g. the HmIP-WGTC config channel — are skipped.
func (c *Climate) activityStateDPs() []device.ParameterDataPoint {
	ch := c.channelRef
	if ch == nil || len(c.activityStateChannels) == 0 {
		return nil
	}
	dev := ch.Device()
	if dev == nil {
		return nil
	}
	var out []device.ParameterDataPoint
	for _, no := range c.activityStateChannels {
		for _, sibling := range dev.Channels() {
			if sibling == nil || sibling.Number != no {
				continue
			}
			if dp := sibling.Parameter(hmenum.ParameterState); dp != nil {
				out = append(out, dp)
			}
			break
		}
	}
	return out
}

// OnActivity records an inferred activity state. Coordinator-side
// subscriptions (VALVE_STATE on RF, LEVEL/STATE on IP) feed this.
func (c *Climate) OnActivity(a Activity) {
	c.mu.Lock()
	c.activity = a
	c.hasActivity = true
	c.mu.Unlock()
}

// --- ingestion ---
//
// Wire-backed values flow through the channel's generic data points
// directly — no per-Climate ingestion shim. Tests that need to drive
// a value can call OnEvent on the channel-side pointer they captured
// at setup time.

// OnMode records a CCU-emitted mode.
func (c *Climate) OnMode(m Mode) {
	c.mu.Lock()
	c.mode = m
	c.hasMode = true
	c.mu.Unlock()
}

// OnProfile records a CCU-emitted profile.
func (c *Climate) OnProfile(p Profile) {
	c.mu.Lock()
	c.profile = p
	c.hasProfile = true
	c.mu.Unlock()
}

// OnActiveProfile records a CCU-emitted week-program-pointer value (the wire
// `ACTIVE_PROFILE` for IP thermostats, `WEEK_PROGRAM_POINTER` for classic-HM
// RF thermostats). The integer is the operator-selected week-program slot —
// typically 1..6 for IP, 0..5 for RF.
//
// The method:
//
// 1. Caches the value on the Climate so a later SET_POINT_MODE→AUTO
// transition can recover the week-program selection without waiting for the
// next push. 2. Updates `c.profile` to ProfileWeekProgram<idx> when the
// thermostat is currently in AUTO mode and not in boost.
//
// if boost: return BOOST if set_point_mode == AWAY: return AWAY if mode ==
// AUTO: return week_program_<active_profile> else: return NONE
//
// `idxBase0` is the wire-side base — true when the wire delivers 0-based
// indices (RF WEEK_PROGRAM_POINTER), false when it delivers 1-based indices
// (IP ACTIVE_PROFILE). Both are normalised to 1-based week-program labels
// (`week_program_1` …) on the way through.
func (c *Climate) OnActiveProfile(idx int, idxBase0 bool) {
	wpIdx := idx
	if idxBase0 {
		wpIdx = idx + 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activeProfileIdx = wpIdx
	c.hasActiveProfileIdx = true
	// Only project to the live profile when the thermostat is
	// currently in AUTO and no overriding profile (boost / away)
	// is active. The other branches own their slot — boost is
	// stamped by OnBoostMode, away by OnSetPointMode AUTO=2 or
	// OnControlMode PARTY-MODE.
	if c.hasMode && c.mode == ModeAuto && c.profile != ProfileBoost && c.profile != ProfileAway {
		if p, ok := profileForWeekIndex(wpIdx); ok {
			c.profile = p
			c.hasProfile = true
		}
	}
}

// profileForWeekIndex maps a 1-based week-program index to the
// matching ProfileWeekProgram<n> constant. Returns ok=false for
// out-of-range indices so the caller can decide whether to keep the
// previous profile or fall back to NONE.
func profileForWeekIndex(idx int) (Profile, bool) {
	switch idx {
	case 1:
		return ProfileWeekProgram1, true
	case 2:
		return ProfileWeekProgram2, true
	case 3:
		return ProfileWeekProgram3, true
	case 4:
		return ProfileWeekProgram4, true
	case 5:
		return ProfileWeekProgram5, true
	case 6:
		return ProfileWeekProgram6, true
	}
	return "", false
}

// --- commands ---

// ErrTemperatureOutOfRange is returned by SetTemperature when the requested
// value falls outside [MinTemp, MaxTemp] and the device is not in a mode that
// bypasses the min/max constraint.
var ErrTemperatureOutOfRange = errors.New("climate: temperature out of allowed range")

// SetTemperature writes a new setpoint. When the value is outside [MinTemp,
// MaxTemp] and min/max validation is active, SetTemperature returns
// [ErrTemperatureOutOfRange] rather than silently clamping.
//
// Validation is skipped when MinMaxValueNotRelevantForManuMode() is true AND
// the thermostat is currently in HEAT mode — matches the manual-mode bypass
// for devices where the valid range is deliberately wider in manual operation.
//
// After range validation the value is still clamped to the capability bounds
// so a bypassed write never exceeds the hardware-safe envelope.
func (c *Climate) SetTemperature(ctx context.Context, v float64, priority hmenum.CommandPriority) error {
	minT := c.MinTemp()
	maxT := c.MaxTemp()
	doValidate := true
	if c.MinMaxValueNotRelevantForManuMode() {
		if m, ok := c.Mode(); ok && m == ModeHeat {
			doValidate = false
		}
	}
	if doValidate && (v < minT || v > maxT) {
		return fmt.Errorf("%w: %.1f not in [%.1f, %.1f]", ErrTemperatureOutOfRange, v, minT, maxT)
	}
	v = clamp(v, c.Capabilities.MinTemperature, c.Capabilities.MaxTemperature)
	if c.setpoint != nil {
		if err := c.setpoint.Set(custom.EnsureContext(ctx), v, priority); err != nil {
			return fmt.Errorf("climate: set temperature: %w", err)
		}
		return nil
	}
	if c.writer == nil {
		return errors.New("climate: set temperature: no setpoint data point and no writer")
	}
	if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, paramForSetpoint(c.Kind), v, priority); err != nil {
		return fmt.Errorf("climate: set temperature: %w", err)
	}
	return nil
}

// SetTemperatureRaw writes a setpoint value without range validation. The
// value is still clamped to the hardware-safe capability bounds
// [Capabilities.MinTemperature .. Capabilities.MaxTemperature] so no
// out-of-spec value reaches the wire. Use SetTemperature when the caller
// wants the additional configured min/max guard.
func (c *Climate) SetTemperatureRaw(ctx context.Context, v float64, priority hmenum.CommandPriority) error {
	v = clamp(v, c.Capabilities.MinTemperature, c.Capabilities.MaxTemperature)
	if c.setpoint != nil {
		if err := c.setpoint.Set(custom.EnsureContext(ctx), v, priority); err != nil {
			return fmt.Errorf("climate: set temperature raw: %w", err)
		}
		return nil
	}
	if c.writer == nil {
		return errors.New("climate: set temperature raw: no setpoint data point and no writer")
	}
	if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, paramForSetpoint(c.Kind), v, priority); err != nil {
		return fmt.Errorf("climate: set temperature raw: %w", err)
	}
	return nil
}

// SetMode writes a new mode.
func (c *Climate) SetMode(ctx context.Context, m Mode, priority hmenum.CommandPriority) error {
	if !c.IsStateChange(nil, &m, nil) {
		return nil
	}
	ctx = custom.EnsureContext(ctx)
	switch c.Kind {
	case KindIP:
		return c.setIPMode(ctx, m, priority)
	case KindRF:
		return c.setRFMode(ctx, m, priority)
	case KindSimpleRF:
		return c.setSimpleRFMode(ctx, m, priority)
	}
	return ErrModeNotSupported
}

// SetProfile activates one of the profiles [Climate.Profiles]
// advertises: a week-program slot, or one of the mode profiles (boost,
// comfort, eco) that carry no pointer index and are dispatched to their
// own wire parameter by [Climate.setModeProfile].
//
// For a week program the wire parameter differs by Kind
// `set_profile` overrides (model/custom/climate.py):
//
// - **KindIP** (HmIP-BWTH / -eTRV / -STH / -WTH / …): write
// `ACTIVE_PROFILE` (TYPE=INTEGER, range 1..6) — the IP
// thermostat exposes the schedule selector as an integer index.
// `_dp_active_profile.send_value(profile_idx)` at climate.py:849.
//
// - **KindRF** (HM-CC-RT-DN / HM-TC-IT-WM-W-EU / …): write
// `WEEK_PROGRAM_POINTER` (TYPE=ENUM) using the case-sensitive
// string label `"WEEK PROGRAM N"` (1-based) — the RF thermostat
// exposes the schedule pointer as an ENUM with explicit value
// list. `_dp_week_program_pointer.send_value(POINTERS_TO_NAMES[idx])`
// at climate.py:606.
//
// Sending the wrong shape (e.g. WEEK_PROGRAM_POINTER on an IP
// thermostat that only carries ACTIVE_PROFILE) triggers XML-RPC
// fault `-5 "Invalid parameter or value"` from the CCU.
func (c *Climate) SetProfile(ctx context.Context, p Profile, priority hmenum.CommandPriority) error {
	if !c.Capabilities.SupportsProfile {
		return ErrModeNotSupported
	}
	if !c.IsStateChange(nil, nil, &p) {
		return nil
	}
	// The mode profiles have no week-program index, so they are dispatched
	// before one is resolved — requiring a pointer first made every
	// advertised boost / comfort / eco preset fail on the guard below.
	handled, err := c.setModeProfile(ctx, p, priority)
	if err != nil {
		return err
	}
	if handled {
		c.recordProfile(p)
		return nil
	}
	idx, ok := profileWeekIndex(p)
	if !ok {
		return fmt.Errorf("climate: profile %q has no pointer mapping", p)
	}
	switch c.Kind {
	case KindIP:
		// ACTIVE_PROFILE is 1-based INTEGER on HmIP — matches the
		// `device_active_profile_index` we already surface in
		// StatePayload.
		if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, hmenum.ParameterActiveProfile, idx+1, priority); err != nil {
			return fmt.Errorf("climate: set profile: %w", err)
		}
	case KindRF:
		// A week-program profile requires the thermostat to be in AUTO mode
		// with BOOST cleared before the pointer is written. AUTO_MODE and
		// BOOST_MODE are VALUES parameters on the climate channel, so they go
		// out together in one VALUES put_paramset (clearing BOOST while setting
		// AUTO in the same envelope avoids an intermediate inconsistent state).
		// WEEK_PROGRAM_POINTER is a MASTER parameter that lives on the
		// device-root channel for the classic RF family — it cannot ride in the
		// VALUES envelope, so it is written to its own MASTER paramset
		// separately.
		modeParams := map[hmenum.Parameter]any{
			hmenum.ParameterAutoMode:  true,
			hmenum.ParameterBoostMode: false,
		}
		if err := custom.PutOrSet(custom.EnsureContext(ctx), c.writer, c.Address, hmenum.ParamsetKeyValues, modeParams, priority); err != nil {
			return fmt.Errorf("climate: set profile: %w", err)
		}
		value := profilePointerEnumLabel(idx)
		address, paramset := c.masterConfigWriteTarget(resolveWeekProgramPointer(c.channelRef, c.Kind))
		if err := custom.PutParamsetForce(custom.EnsureContext(ctx), c.writer, address, paramset, map[hmenum.Parameter]any{
			hmenum.ParameterWeekProgramPointer: value,
		}, priority); err != nil {
			return fmt.Errorf("climate: set profile: %w", err)
		}
	default:
		// KindSimpleRF: WEEK_PROGRAM_POINTER ENUM label.
		value := profilePointerEnumLabel(idx)
		if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, hmenum.ParameterWeekProgramPointer, value, priority); err != nil {
			return fmt.Errorf("climate: set profile: %w", err)
		}
	}
	c.recordProfile(p)
	return nil
}

// setModeProfile writes the wire parameter behind a mode profile — the
// presets that switch a mode instead of selecting a week program.
//
// handled is false for every other profile, which leaves the caller on the
// week-program pointer path; keeping the classification and the write in
// one switch is what stops the two from drifting apart again.
//
// Each preset is gated on the capability [Climate.Profiles] offers it
// from, so one the device never advertised is refused rather than written
// to a parameter it does not carry — COMFORT_MODE and LOWERING_MODE exist
// on RF thermostats only.
func (c *Climate) setModeProfile(ctx context.Context, p Profile, priority hmenum.CommandPriority) (handled bool, err error) {
	var (
		param     hmenum.Parameter
		supported bool
	)
	switch p {
	case ProfileBoost:
		param, supported = hmenum.ParameterBoostMode, c.Capabilities.SupportsBoost
	case ProfileComfort:
		param, supported = hmenum.ParameterComfortMode, c.Capabilities.SupportsComfort
	case ProfileEco:
		param, supported = hmenum.ParameterLoweringMode, c.Capabilities.SupportsEco
	default:
		return false, nil
	}
	if !supported {
		return false, ErrModeNotSupported
	}
	if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, param, true, priority); err != nil {
		return false, fmt.Errorf("climate: set profile %s: %w", p, err)
	}
	return true, nil
}

// recordProfile stores the profile locally so the UI reflects the change
// before the CCU echoes it back.
func (c *Climate) recordProfile(p Profile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profile = p
	c.hasProfile = true
}

// profilePointerEnumLabel maps a 0-based week-program index to the CCU's
// expected ENUM label for `WEEK_PROGRAM_POINTER`.
func profilePointerEnumLabel(idx int32) string {
	return fmt.Sprintf("WEEK PROGRAM %d", idx+1)
}

// EnableBoost activates BOOST_MODE.
func (c *Climate) EnableBoost(ctx context.Context, priority hmenum.CommandPriority) error {
	return c.SetBoost(ctx, true, priority)
}

// DisableBoost deactivates BOOST_MODE.
func (c *Climate) DisableBoost(ctx context.Context, priority hmenum.CommandPriority) error {
	return c.SetBoost(ctx, false, priority)
}

// SetAway activates the device's away/party mode through the wire parameters
// appropriate for the Kind.
//
// - KindIP: writes `SET_POINT_MODE = 2` (AWAY), `SET_POINT_TEMPERATURE`, and
// the `PARTY_TIME_START/END` window. The CCU returns to the previous mode at
// `until`. - KindRF: writes `PARTY_MODE_SUBMIT` with the encoded code. -
// KindSimpleRF: returns [ErrModeNotSupported].
//
// `away` is the temperature held while away; pass 0 for the device default.
func (c *Climate) SetAway(
	ctx context.Context, until time.Time, away float64, priority hmenum.CommandPriority,
) (err error) {
	if !c.Capabilities.SupportsAway {
		return ErrModeNotSupported
	}
	ctx = custom.EnsureContext(ctx)
	// Attach a collector so any future Channel.Set routing batches the
	// multi-parameter PARTY_* writes into one put_paramset.
	if c.writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(c.writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		// Anything staged on the collector only reaches the wire in the
		// flush, so its error is part of this command's result.
		defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	}
	switch c.Kind {
	case KindIP:
		if c.writer == nil {
			return ErrModeNotSupported
		}
		// The away window applies atomically through one PutParamset,
		// mirroring the reference model/custom/climate.py
		// enable_away_mode_by_calendar: SET_POINT_MODE=AWAY (2),
		// SET_POINT_TEMPERATURE (the held temperature), and the
		// PARTY_TIME_START/END window.
		params := map[hmenum.Parameter]any{
			hmenum.ParameterPartyTimeStart: encodePartyTime(time.Now()),
			hmenum.ParameterPartyTimeEnd:   encodePartyTime(until),
			hmenum.ParameterSetPointMode:   int32(2),
		}
		if away > 0 {
			params[hmenum.ParameterSetPointTemperature] = away
		}
		if err := custom.PutOrSet(ctx, c.writer, c.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
			return fmt.Errorf("climate: SetAway IP: %w", err)
		}
	case KindRF:
		if c.writer == nil {
			return ErrModeNotSupported
		}
		if err := c.writer.SetValue(ctx, c.Address, hmenum.ParameterPartyModeSubmit, partyModeCode(time.Now(), until, away), priority); err != nil {
			return fmt.Errorf("climate: PARTY_MODE_SUBMIT: %w", err)
		}
	default:
		return ErrModeNotSupported
	}
	c.mu.Lock()
	c.profile = ProfileAway
	c.hasProfile = true
	c.awayUntil = until
	c.hasAwayUntil = true
	c.mu.Unlock()
	return nil
}

// SetAwayForDuration is a convenience wrapper that activates away mode
// for `d` from now.
func (c *Climate) SetAwayForDuration(ctx context.Context, d time.Duration, away float64, priority hmenum.CommandPriority) error {
	return c.SetAway(ctx, time.Now().Add(d), away, priority)
}

// DisableAway leaves away/party mode and returns the device to its previous
// schedule.
func (c *Climate) DisableAway(ctx context.Context, priority hmenum.CommandPriority) (err error) {
	if !c.Capabilities.SupportsAway {
		return ErrModeNotSupported
	}
	ctx = custom.EnsureContext(ctx)
	// Attach a collector for forward-compatible batching of the
	// multi-parameter IP path.
	if c.writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(c.writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		// Anything staged on the collector only reaches the wire in the
		// flush, so its error is part of this command's result.
		defer func() { err = generic.FlushCollector(ctx, coll, err) }()
	}
	switch c.Kind {
	case KindIP:
		if c.writer == nil {
			return ErrModeNotSupported
		}
		// SET_POINT_MODE = 0 (AUTO) and zero out PARTY_TIME_END so the CCU
		// re-evaluates immediately.
		params := map[hmenum.Parameter]any{
			hmenum.ParameterPartyTimeEnd: encodePartyTime(time.Now()),
			hmenum.ParameterSetPointMode: int32(0),
		}
		if err := custom.PutOrSet(ctx, c.writer, c.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
			return fmt.Errorf("climate: DisableAway IP: %w", err)
		}
	case KindRF:
		if c.writer == nil {
			return ErrModeNotSupported
		}
		if err := c.writer.SetValue(ctx, c.Address, hmenum.ParameterPartyModeSubmit, "", priority); err != nil {
			return fmt.Errorf("climate: PARTY_MODE_SUBMIT clear: %w", err)
		}
	default:
		return ErrModeNotSupported
	}
	c.mu.Lock()
	c.profile = ProfileNone
	c.hasProfile = true
	c.hasAwayUntil = false
	c.mu.Unlock()
	return nil
}

// AwayUntil returns the timestamp at which the away period ends, when
// one is active. Returns observed=false when no away period is in
// effect.
func (c *Climate) AwayUntil() (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasAwayUntil {
		return time.Time{}, false
	}
	return c.awayUntil, true
}

// encodePartyTime renders a timestamp in the CCU PARTY_TIME format
// `yyyy_mm_dd hh:mm`, mirroring the reference model/custom/climate.py
// _PARTY_DATE_FORMAT = "%Y_%m_%d %H:%M" used for PARTY_TIME_START/END.
//
// PARTY_TIME_START/END carry no zone, so the CCU reads them as its own
// local wall clock — which is the daemon's zone, the one `time.Now()`
// already renders the window's start in. The conversion is what keeps
// both ends of one window in the same zone: an `until` parsed from an
// RFC-3339 timestamp keeps the literal offset of the string it came
// from, so a client sending the canonical trailing `Z` used to encode an
// end that was one UTC offset earlier than requested — and, past the
// offset, earlier than the start, which the CCU rejects as an inverted
// window without reporting anything back.
func encodePartyTime(t time.Time) string {
	return t.Local().Format("2006_01_02 15:04")
}

// partyModeCode renders the PARTY_MODE_SUBMIT payload the HM-CC-RT firmware
// accepts. The format is a comma-separated string:
//
//	temp,start_minutes_of_day,start_dd,start_mm,start_yy,end_minutes_of_day,end_dd,end_mm,end_yy
//
// Example: "21.5,1200,20,10,16,1380,20,10,16"
//
// Both ends are converted to the daemon's zone for the same reason
// [encodePartyTime] does: the payload is a bare wall clock the CCU reads
// in its own local time, and the caller's `end` carries whatever offset
// its RFC-3339 source spelled out.
func partyModeCode(start, end time.Time, setpoint float64) string {
	start, end = start.Local(), end.Local()
	startMOD := start.Hour()*60 + start.Minute()
	endMOD := end.Hour()*60 + end.Minute()
	return fmt.Sprintf(
		"%.1f,%d,%s,%d,%s",
		setpoint,
		startMOD,
		start.Format("02,01,06"),
		endMOD,
		end.Format("02,01,06"),
	)
}

// SetBoost is a single-call boost toggle. Returns [ErrModeNotSupported] when
// the device profile does not advertise boost capability.
//
// On success the local Profile state is set to ProfileBoost (when activating)
// so the UI reflects the change before the CCU echoes back.
func (c *Climate) SetBoost(ctx context.Context, on bool, priority hmenum.CommandPriority) error {
	if !c.Capabilities.SupportsBoost {
		return ErrModeNotSupported
	}
	if err := c.writer.SetValue(custom.EnsureContext(ctx), c.Address, hmenum.ParameterBoostMode, on, priority); err != nil {
		return fmt.Errorf("climate: set boost: %w", err)
	}
	if on {
		c.mu.Lock()
		c.profile = ProfileBoost
		c.hasProfile = true
		c.mu.Unlock()
	}
	return nil
}

// --- helpers ---

func (c *Climate) setIPMode(ctx context.Context, m Mode, priority hmenum.CommandPriority) error {
	// HmIP thermostats (HmIP-BWTH / -eTRV / -STH / -WTH / …) use the
	// **write-only** `CONTROL_MODE` ACTION parameter to change mode, not the
	// read-only `SET_POINT_MODE` (which the firmware *reports* the active mode
	// through). Writing SET_POINT_MODE has no effect on most CCU firmwares — the
	// value is read-only on the wire and the device snaps right back to the mode
	// it derived from the control flow (CONTROL_MODE / BOOST_MODE /
	// WEEK_PROGRAM_POINTER).
	//
	// Every wire path is a put_paramset envelope, never a bare setValue —
	// some CCU firmware stages reject a raw setValue for CONTROL_MODE
	// while accepting the same write inside put_paramset.
	//
	// When BOOST is currently active, the AUTO / HEAT / OFF write bundles
	// BOOST_MODE=False so the mode switch implies "boost off". Without
	// that bundling the CCU keeps boost active and rejects the new
	// mode with a 502.
	//
	// The profile is read through the accessor rather than the field: the
	// CCU event goroutine writes it under c.mu whenever BOOST_MODE or
	// SET_POINT_MODE arrives, and setIPMode runs on whichever north-bound
	// goroutine issued the command.
	params := map[hmenum.Parameter]any{}
	if p, ok := c.Profile(); ok && p == ProfileBoost {
		params[hmenum.ParameterBoostMode] = false
	}
	switch m {
	case ModeAuto:
		params[hmenum.ParameterControlMode] = int32(0)
	case ModeHeat, ModeCool:
		params[hmenum.ParameterControlMode] = int32(1)
		params[hmenum.ParameterSetPointTemperature] = c.temperatureForHeatMode()
	case ModeOff:
		params[hmenum.ParameterControlMode] = int32(1)
		params[hmenum.ParameterSetPointTemperature] = 4.5
	default:
		return ErrModeNotSupported
	}
	if err := custom.PutParamsetForce(ctx, c.writer, c.Address, hmenum.ParamsetKeyValues, params, priority); err != nil {
		return fmt.Errorf("climate: IP mode %s: %w", m, err)
	}
	c.mu.Lock()
	c.mode = m
	c.hasMode = true
	c.mu.Unlock()
	return nil
}

func (c *Climate) setRFMode(ctx context.Context, m Mode, priority hmenum.CommandPriority) error {
	switch m {
	case ModeAuto:
		return c.writer.SetValue(ctx, c.Address, hmenum.ParameterAutoMode, true, priority)
	case ModeHeat:
		// MANU_MODE write restoring the last known manual setpoint
		// (clipped to [min_temp, max_temp]) so the thermostat does not jump
		// to max on every HEAT selection.
		return c.writer.SetValue(ctx, c.Address, hmenum.ParameterManuMode, c.temperatureForHeatMode(), priority)
	case ModeOff:
		// MANU_MODE + SET_TEMPERATURE atomic — mirrors
		// `set_mode(OFF)` (`put_paramset({"MANU_MODE": MIN,
		// "SET_TEMPERATURE": MIN})`).
		return custom.PutOrSet(ctx, c.writer, c.Address, hmenum.ParamsetKeyValues, map[hmenum.Parameter]any{
			hmenum.ParameterManuMode:       c.Capabilities.MinTemperature,
			hmenum.ParameterSetTemperature: c.Capabilities.MinTemperature,
		}, priority)
	default:
		return ErrModeNotSupported
	}
}

func (c *Climate) setSimpleRFMode(ctx context.Context, m Mode, priority hmenum.CommandPriority) error {
	switch m {
	case ModeHeat:
		return c.SetTemperature(ctx, c.Capabilities.MaxTemperature, priority)
	case ModeOff:
		return c.SetTemperature(ctx, c.Capabilities.MinTemperature, priority)
	case ModeAuto, ModeCool:
		return ErrModeNotSupported
	}
	return ErrModeNotSupported
}

// OnControlMode maps a wire CONTROL_MODE string value (RF thermostats
// only) to the domain Mode and Profile.
//
// CONTROL_MODE is an ENUM parameter whose value_list entries on the CCU
// are "AUTO-MODE" (0), "MANU-MODE" (1), "PARTY-MODE" (2), "BOOST-MODE" (3).
// The CCU sends either the string label or its numeric index; both are
// supported here.
//
// Mapping mirrors
// properties in CustomDpRfThermostat (climate.py:502-527):
//
//	CONTROL_MODE Mode Profile
//	"AUTO-MODE" AUTO (kept — may be week-program from WEEK_PROGRAM_POINTER)
//	"MANU-MODE" HEAT NONE
//	"PARTY-MODE" AUTO AWAY
//	"BOOST-MODE" AUTO BOOST
//
// The OFF mode check (setpoint ≤ 4.5 °C) is intentionally NOT applied
// here — that guard lives in StatePayload / Mode() and requires the
// setpoint value, which is a separate generic DP updated independently.
func (c *Climate) OnControlMode(wireValue any) {
	// Accept both the numeric index (int / int32 from the wire) and the
	// string label (sent as ENUM label by some CCU firmware revisions).
	var label string
	switch v := wireValue.(type) {
	case string:
		label = v
	case int:
		label = rfControlModeLabel(v)
	case int32:
		label = rfControlModeLabel(int(v))
	case int64:
		label = rfControlModeLabel(int(v))
	case float64:
		label = rfControlModeLabel(int(v))
	default:
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	switch label {
	case "AUTO-MODE":
		c.mode = ModeAuto
		c.hasMode = true
		// Recover the profile from the cached WEEK_PROGRAM_POINTER value when the
		// thermostat is in AUTO and no boost/away override is active.
		if c.profile != ProfileBoost && c.profile != ProfileAway && c.hasActiveProfileIdx {
			if p, ok := profileForWeekIndex(c.activeProfileIdx); ok {
				c.profile = p
				c.hasProfile = true
			}
		}
	case "MANU-MODE":
		c.mode = ModeHeat
		c.hasMode = true
		c.profile = ProfileNone
		c.hasProfile = true
	case "PARTY-MODE":
		c.mode = ModeAuto
		c.hasMode = true
		c.profile = ProfileAway
		c.hasProfile = true
	case "BOOST-MODE":
		c.mode = ModeAuto
		c.hasMode = true
		c.profile = ProfileBoost
		c.hasProfile = true
	}
}

// rfControlModeLabel returns the string label for the integer index
// of the RF CONTROL_MODE enum (values 0–3).
func rfControlModeLabel(idx int) string {
	switch idx {
	case 0:
		return "AUTO-MODE"
	case 1:
		return "MANU-MODE"
	case 2:
		return "PARTY-MODE"
	case 3:
		return "BOOST-MODE"
	}
	return ""
}

// OnSetPointMode maps a wire SET_POINT_MODE integer value (IP thermostats
// only) to the domain Mode and Profile.
//
// SET_POINT_MODE values mirror _ModeHmIP IntEnum.
// climate.py:76-82:
//
//	0 = AUTO → ModeAuto, Profile unchanged
//	1 = MANU → ModeHeat (or ModeCool when heating_cooling == "COOLING")
//	2 = AWAY → ModeAuto, ProfileAway
//
// The OFF mode check (setpoint ≤ 4.5 °C) is intentionally NOT applied
// here — that guard lives in StatePayload / Mode() and requires the
// setpoint value. The BOOST_MODE path is handled separately via the
// BOOST_MODE parameter subscription.
func (c *Climate) OnSetPointMode(wireValue any) {
	idx, ok := toInt(wireValue)
	if !ok {
		return
	}
	// Read heating-cooling mode before acquiring mu to avoid nested locks
	// (heatingMode acquires muHC; we then acquire mu). Separate reads are
	// safe because heatingMode changes are infrequent and guarded by muHC.
	hc := c.heatingMode()
	c.mu.Lock()
	defer c.mu.Unlock()
	switch idx {
	case 0: // AUTO
		c.mode = ModeAuto
		c.hasMode = true
		// Recover the profile from the cached ACTIVE_PROFILE value when the
		// thermostat is in AUTO and no boost/away override is active. Without this,
		// an HmIP-BWTH that boots in AUTO with a stable week-program would surface
		// preset_mode="none" until the next ACTIVE_PROFILE push.
		if c.profile != ProfileBoost && c.profile != ProfileAway && c.hasActiveProfileIdx {
			if p, ok := profileForWeekIndex(c.activeProfileIdx); ok {
				c.profile = p
				c.hasProfile = true
			}
		}
	case 1: // MANU
		if hc == "COOLING" {
			c.mode = ModeCool
		} else {
			c.mode = ModeHeat
		}
		c.hasMode = true
		c.profile = ProfileNone
		c.hasProfile = true
	case 2: // AWAY
		c.mode = ModeAuto
		c.hasMode = true
		c.profile = ProfileAway
		c.hasProfile = true
	}
}

// toInt converts common integer wire types to int. Returns ok=false when
// the value cannot be interpreted as an integer.
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	}
	return 0, false
}

// paramFromAnyParamset is the climate-package alias for the shared
// [custom.ParamFromAnyParamset] helper. Kept as a local indirection
// so existing call sites stay terse while the implementation lives
// in one place across cover / light / climate.
func paramFromAnyParamset(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	return custom.ParamFromAnyParamset(ch, p)
}

// replayCurrentValue is the climate-package alias for the shared
// [custom.ReplayCurrentValue] helper. See that function's documentation
// for why "register OnAnyUpdate + replay current value" is the only
// pattern that lands the custom-DP cache in sync with the CCU at boot.
func replayCurrentValue(dp interface {
	RawValue() (any, bool)
}, apply func(value any),
) {
	custom.ReplayCurrentValue(custom.RawValuer(dp), apply)
}

// Subscribe wires the channel's activity-source parameters into [OnActivity]
// so callers do not need to forward VALVE_STATE / LEVEL / STATE updates
// themselves. Implements [device.SubscribingDataPoint].
//
// - KindIP: subscribes to LEVEL (valve openness) and STATE (heating/idle
// bool, own channel plus the profile-mapped activity-state channels);
// LEVEL > 0 ⇒ heating, otherwise idle. Also subscribes to
// SET_POINT_MODE to update Mode / Profile. - KindRF: subscribes to
// VALVE_STATE (0–100 %); >0 ⇒ heating, =0 ⇒ idle, plus CONTROL_MODE to
// update Mode / Profile. - KindSimpleRF: no activity source.
//
// Returns a closure that detaches every subscription. Each subscription also
// replays the wire DP's currently observed value through the same handler so
// the Climate cache lands in sync with the CCU state at boot, not only on the
// next push.
func (c *Climate) Subscribe(ch *device.Channel) func() { //nolint:gocognit,gocyclo,funlen // single-purpose climate subscription wiring with many data-point branches
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	// runningActivity picks HEAT vs. COOL when the channel is currently active
	// (LEVEL > 0 / VALVE_STATE > 0 / STATE = true). HmIP thermostats honour
	// `_is_heating_mode` derived from HEATING_COOLING; RF / SimpleRF thermostats
	// are heat-only.
	runningActivity := func() Activity {
		if c.Kind == KindIP && c.heatingMode() == "COOLING" {
			return ActivityCooling
		}
		return ActivityHeating
	}
	classify := func(level float64) Activity {
		if level > 0 {
			return runningActivity()
		}
		return ActivityIdle
	}
	// activity producers — VALVE_STATE / LEVEL (numeric) and STATE (bool).
	applyActivityFloat := func(next any) {
		if v, ok := toFloat(next); ok {
			c.OnActivity(classify(v))
		}
	}
	applyActivityBool := func(next any) {
		if b, ok := next.(bool); ok {
			if b {
				c.OnActivity(runningActivity())
			} else {
				c.OnActivity(ActivityIdle)
			}
		}
	}
	// Activity wiring is kind-gated to mirror the reference stack:
	//
	// - KindIP derives activity from LEVEL (valve openness) and STATE
	//   (heating relay — possibly on a profile-mapped sibling channel,
	//   e.g. the HmIP-BWTH relay on channel 9). HmIP VALVE_STATE is an
	//   adaption-state ENUM (4 == ADAPTION_DONE), NOT a valve-openness
	//   percentage; treating it as one reported "heating" on idle
	//   eTRVs whose LEVEL was 0.
	// - KindRF derives activity from VALVE_STATE (0–100 %).
	// - KindSimpleRF has no activity source at all — hvac_action stays
	//   absent for these thermostats.
	switch c.Kind {
	case KindIP:
		if dp := ch.Parameter(hmenum.ParameterLevel); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyActivityFloat(next) }))
			replayCurrentValue(dp, applyActivityFloat)
		}
		if dp := ch.Parameter(hmenum.ParameterState); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyActivityBool(next) }))
			replayCurrentValue(dp, applyActivityBool)
		}
		for _, dp := range c.activityStateDPs() {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyActivityBool(next) }))
			replayCurrentValue(dp, applyActivityBool)
		}
	case KindRF:
		if dp := ch.Parameter(hmenum.ParameterValveState); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyActivityFloat(next) }))
			replayCurrentValue(dp, applyActivityFloat)
		}
	case KindSimpleRF:
		// No activity source.
	}
	applyTemperatureOffset := func(next any) {
		if next != nil {
			c.OnTemperatureOffset(next)
		}
	}
	// TEMPERATURE_OFFSET is a MASTER-paramset DP on HmIP/RF thermostats —
	// channel-configuration item, not runtime state. It sits on the climate
	// channel's MASTER for HmIP thermostats but on the device-root MASTER for
	// the classic RF family, so the resolution has to walk both — a
	// climate-channel-only lookup left the offset unobserved and the state
	// payload dropped it. The OPTIMUM_START_STOP and
	// MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE bindings below stay on the
	// climate channel, where the HmIP family that carries them advertises them.
	if dp := resolveConfigParam(ch, hmenum.ParameterTemperatureOffset); dp != nil {
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyTemperatureOffset(next) }))
		replayCurrentValue(dp, applyTemperatureOffset)
	}
	if dp := ch.Parameter(hmenum.ParameterHeatingCooling); dp != nil {
		// HEATING_COOLING is a read+write ENUM, so the value pushed here
		// is the 0-based VALUE_LIST index; a bare string assertion left
		// heatingMode() on its "HEATING" default while the installation
		// cooled. The label form is still accepted for the firmwares that
		// spell it out.
		applyHeatingCooling := func(next any) {
			if label, ok := custom.EnumWireLabel(dp, next); ok {
				c.OnHeatingCooling(label)
			}
		}
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyHeatingCooling(next) }))
		replayCurrentValue(dp, applyHeatingCooling)
	}
	applyOptimumStartStop := func(next any) {
		if b, ok := next.(bool); ok {
			c.OnOptimumStartStop(b)
		}
	}
	if dp := paramFromAnyParamset(ch, hmenum.ParameterOptimumStartStop); dp != nil {
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyOptimumStartStop(next) }))
		replayCurrentValue(dp, applyOptimumStartStop)
	}
	applyMinMaxNotRelevant := func(next any) {
		if b, ok := next.(bool); ok {
			c.OnMinMaxValueNotRelevantForManuMode(b)
		}
	}
	if dp := paramFromAnyParamset(ch, hmenum.ParameterMinMaxNotRelevantForManuMode); dp != nil {
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyMinMaxNotRelevant(next) }))
		replayCurrentValue(dp, applyMinMaxNotRelevant)
	}
	// Mode / Profile producers — wire CONTROL_MODE (RF) and SET_POINT_MODE (IP).
	//
	// **Replay ordering note**: ACTIVE_PROFILE / WEEK_PROGRAM_POINTER is
	// replayed *first* so OnSetPointMode / OnControlMode see the cached
	// week-program index when they recover the profile from the AUTO branch. The
	// forward case (Subscribe runs after SET_POINT_MODE has been observed) is
	// also covered: OnActiveProfile reads c.mode and applies the profile when in
	// AUTO.
	switch c.Kind { //nolint:exhaustive // KindSimpleRF has no week-program wiring; only RF and IP need subscribe logic
	case KindRF:
		applyControlMode := func(next any) { c.OnControlMode(next) }
		// Replay WEEK_PROGRAM_POINTER first so OnControlMode AUTO can
		// recover the profile from the cached idx.
		applyWeekProgramPointer := func(next any) {
			if v, ok := toInt(next); ok {
				c.OnActiveProfile(v, true)
			}
		}
		// WEEK_PROGRAM_POINTER drives `preset_mode = week_program_<n>` when the
		// thermostat is in AUTO. RF firmware delivers a 0-based index;
		// OnActiveProfile normalises to 1-based week-program labels. The pointer
		// sits on the device-root MASTER paramset for the classic RF family
		// (HM-TC-IT-WM-W-EU, HM-CC-VG-1), so it is resolved through the same
		// MASTER+root walk numWeekPrograms uses — a climate-channel VALUES-only
		// lookup never observed it and the active week program stayed unknown.
		if dp := resolveWeekProgramPointer(ch, c.Kind); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyWeekProgramPointer(next) }))
			replayCurrentValue(dp, applyWeekProgramPointer)
		}
		if dp := ch.Parameter(hmenum.ParameterControlMode); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyControlMode(next) }))
			replayCurrentValue(dp, applyControlMode)
		}
	case KindIP:
		applySetPointMode := func(next any) { c.OnSetPointMode(next) }
		// Replay ACTIVE_PROFILE first so OnSetPointMode AUTO can
		// recover the profile from the cached idx.
		applyActiveProfile := func(next any) {
			if v, ok := toInt(next); ok {
				c.OnActiveProfile(v, false)
			}
		}
		// ACTIVE_PROFILE drives `preset_mode = week_program_<n>` for IP thermostats
		// — same role WEEK_PROGRAM_POINTER plays for RF. IP firmware delivers a
		// 1-based index.
		if dp := ch.Parameter(hmenum.ParameterActiveProfile); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyActiveProfile(next) }))
			replayCurrentValue(dp, applyActiveProfile)
		}
		if dp := ch.Parameter(hmenum.ParameterSetPointMode); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applySetPointMode(next) }))
			replayCurrentValue(dp, applySetPointMode)
		}
		// BOOST_MODE is an independent bool on IP thermostats — when it flips to
		// true the profile must become ProfileBoost regardless of SET_POINT_MODE.
		applyBoostMode := func(next any) {
			b, ok := next.(bool)
			if !ok {
				return
			}
			c.mu.Lock()
			if b {
				c.profile = ProfileBoost
				c.hasProfile = true
			} else if c.profile == ProfileBoost {
				// Boost deactivated — restore the cached
				// week-program profile when in AUTO; otherwise
				// fall back to None.
				if c.hasMode && c.mode == ModeAuto && c.hasActiveProfileIdx {
					if p, ok := profileForWeekIndex(c.activeProfileIdx); ok {
						c.profile = p
					} else {
						c.profile = ProfileNone
					}
				} else {
					c.profile = ProfileNone
				}
			}
			c.mu.Unlock()
		}
		if dp := ch.Parameter(hmenum.ParameterBoostMode); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) { applyBoostMode(next) }))
			replayCurrentValue(dp, applyBoostMode)
		}

	}
	// Track the last manually-set temperature while the thermostat is in MANU
	// (HEAT) mode — applies to all thermostat kinds (IP, RF, SimpleRF).
	// When the operator later restores HEAT mode via set_mode,
	// temperatureForHeatMode() returns this cached value instead of the
	// current setpoint (which could be the OFF sentinel 4.5 °C or a
	// week-program value). Mirrors _manu_temp_changed / _old_manu_setpoint
	// (climate.py:855-858).
	setpointParam := paramForSetpoint(c.Kind)
	if dp := ch.Parameter(setpointParam); dp != nil {
		unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) {
			v, ok := toFloat(next)
			if !ok {
				return
			}
			c.mu.Lock()
			if c.hasMode && c.mode == ModeHeat {
				c.oldManuSetpoint = v
				c.hasOldManuSetpoint = true
			}
			c.mu.Unlock()
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

func (c *Climate) heatingMode() string {
	c.muHC.RLock()
	defer c.muHC.RUnlock()
	if c.hasHC {
		return c.heatingCooling
	}
	return "HEATING"
}

// OnHeatingCooling records a CCU-emitted HEATING_COOLING mode update.
// Called from a wire-side subscription on KindIP hybrid thermostats.
func (c *Climate) OnHeatingCooling(mode string) {
	c.muHC.Lock()
	c.heatingCooling = mode
	c.hasHC = true
	c.muHC.Unlock()
}

// IsHeating reports whether the device is in heating mode (rather than
// cooling).
// Defaults to true when the wire parameter has not been observed.
func (c *Climate) IsHeating() bool {
	return c.heatingMode() == "HEATING"
}

// ScheduleProfileNos returns the number of schedule (week-program) slots the
// device supports. Both derive the count from the number of entries in their
// `_profiles` mapping, which in turn is driven by the WEEK_PROGRAM_POINTER
// (RF) or ACTIVE_PROFILE (IP) descriptor min/max range.
//
// The Go implementation is a static capability-flag count: every profile with
// a week-program slot contributes one.
func (c *Climate) ScheduleProfileNos() int {
	if !c.Capabilities.SupportsProfile {
		return 0
	}
	// Count the week-program slots that would appear in Profiles().
	// The canonical slots are week_program_1 … week_program_6.
	count := 0
	for _, p := range []Profile{
		ProfileWeekProgram1, ProfileWeekProgram2, ProfileWeekProgram3,
		ProfileWeekProgram4, ProfileWeekProgram5, ProfileWeekProgram6,
	} {
		if _, ok := profileWeekIndex(p); ok {
			count++
		}
	}
	return count
}

// OptimumStartStop returns the current state of the OPTIMUM_START_STOP
// parameter and whether it has been observed. This feature is exclusive to IP
// thermostats (CustomDpIpThermostat) and controls whether the device starts
// heating slightly earlier to reach the target temperature at the scheduled
// time.
//
// Returns (false, false) on RF/SimpleRF kinds that do not expose this
// parameter.
func (c *Climate) OptimumStartStop() (value, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.optimumStartStop, c.hasOptimumStartStop
}

// OnOptimumStartStop records a CCU-emitted OPTIMUM_START_STOP update.
// Called from a wire-side subscription when the channel exposes the
// OPTIMUM_START_STOP parameter (IP thermostats only).
func (c *Climate) OnOptimumStartStop(v bool) {
	c.mu.Lock()
	c.optimumStartStop = v
	c.hasOptimumStartStop = true
	c.mu.Unlock()
}

// MinMaxValueNotRelevantForManuMode returns whether min/max temperature
// validation should be skipped when the thermostat is in manual (MANU) mode.
// When true, set_temperature bypasses the min/max clamping guard — the device
// accepts any setpoint in manual mode.
//
// The value is populated by [OnMinMaxValueNotRelevantForManuMode] when the
// channel exposes the MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE parameter.
func (c *Climate) MinMaxValueNotRelevantForManuMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.minMaxNotRelevantForManu
}

// OnMinMaxValueNotRelevantForManuMode records a CCU-emitted
// MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE update.
func (c *Climate) OnMinMaxValueNotRelevantForManuMode(v bool) {
	c.mu.Lock()
	c.minMaxNotRelevantForManu = v
	c.mu.Unlock()
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

func paramForSetpoint(k Kind) hmenum.Parameter {
	if k == KindIP {
		return hmenum.ParameterSetPointTemperature
	}
	return hmenum.ParameterSetTemperature
}

// crossChannelTemperatureBound resolves a TEMPERATURE_MINIMUM
// TEMPERATURE_MAXIMUM operator-config DP. The lookup walks both the
// climate channel and the device-root channel because some classic
// HM thermostats (HM-CC-RT-DN-BoM, HM-CC-VG-1, HM-TC-IT-WM-W-EU)
// expose the bounds on the device-root MASTER paramset rather than
// The climate channel itself
// pins these to `channel_fields={None: ...}`.
//
// Result is gated by the `RELEVANT_MASTER_PARAMSETS_BY_DEVICE`
// Whitelist
// that whitelist (HmIPW-WTH / HmIPW-SCTHD are deliberately off-list,
// so their `_dp_temperature_*` bindings are DpDummy with value=None
// → max_temp falls back to setpoint.descriptor.max). openccu-loom
// emulates the same behaviour.
//
// The test-rig path (no [device.Device.Model] set) bypasses the
// whitelist gate so unit tests that pre-attach a temperature DP keep
// asserting against it. Production callers always have Model set via
// the device-pipeline ingestion.
func (c *Climate) crossChannelTemperatureBound(p hmenum.Parameter) *generic.Float {
	directDP := func() *generic.Float {
		switch p { //nolint:exhaustive // climate-relevant subset of hmenum.Parameter; only temperature-bound parameters apply
		case hmenum.ParameterTemperatureMinimum:
			return c.temperatureMinimum
		case hmenum.ParameterTemperatureMaximum:
			return c.temperatureMaximum
		}
		return nil
	}
	if c.channelRef == nil {
		return directDP()
	}
	dev := c.channelRef.Device()
	model := ""
	if dev != nil {
		model = dev.Model
	}
	if model == "" {
		// Test-rig path — keep the rig-attached DP authoritative.
		return directDP()
	}
	// Whitelist gate — mirrors
	// `RELEVANT_MASTER_PARAMSETS_BY_DEVICE` semantics. We accept the
	// model on the climate channel **or** on the synthetic device-root
	// pseudo-channel (channel-no `ChannelNumberDevice`).
	channelNo := c.channelRef.Number
	if !visibility.IsRelevantMasterParameter(model, channelNo, p) &&
		!visibility.IsRelevantMasterParameter(model, int(device.ChannelNumberDevice), p) {
		return nil
	}
	// Look up on the climate channel first.
	if dp := custom.FloatField(c.channelRef, p); dp != nil {
		return dp
	}
	// Fall back to the synthetic device-root channel.
	if dev != nil {
		if root := dev.RootChannel(); root != nil {
			if dp := custom.FloatField(root, p); dp != nil {
				return dp
			}
		}
	}
	return nil
}

// resolveConfigParam locates an operator-configuration parameter that lives on
// a MASTER paramset. The HmIP thermostat family carries it on the climate
// channel's own MASTER paramset (TEMPERATURE_OFFSET on <device>:N/MASTER),
// while the classic RF family carries it on the device-root MASTER paramset
// (HM-CC-RT-DN, HM-TC-IT-WM-W-EU, HM-CC-VG-1 on <device>/MASTER). The lookup
// walks the climate channel first and the device-root channel second — the
// same climate-then-root resolution [Climate.crossChannelTemperatureBound]
// uses for the TEMPERATURE_MINIMUM / MAXIMUM bounds. Returns nil when neither
// channel advertises p.
func resolveConfigParam(ch *device.Channel, p hmenum.Parameter) device.ParameterDataPoint {
	if ch == nil {
		return nil
	}
	if dp := paramFromAnyParamset(ch, p); dp != nil {
		return dp
	}
	if dev := ch.Device(); dev != nil {
		if root := dev.RootChannel(); root != nil {
			if dp := paramFromAnyParamset(root, p); dp != nil {
				return dp
			}
		}
	}
	return nil
}

// masterConfigWriteTarget resolves the (channel address, paramset key) a
// MASTER-resident configuration parameter must be written to, taken from its
// resolved owning data point so the write lands on the exact paramset and
// channel the CCU advertises the parameter on. A nil dp — the parameter has
// not been materialised on the model yet — falls back to the climate channel's
// MASTER paramset, the paramset every thermostat in the fleet carries these
// parameters on.
func (c *Climate) masterConfigWriteTarget(dp device.ParameterDataPoint) (address string, paramset hmenum.ParamsetKey) {
	if dp != nil {
		key := dp.DataPointKey()
		return key.ChannelAddress, key.ParamsetKey
	}
	return c.Address, hmenum.ParamsetKeyMaster
}

// resolveWeekProgramPointer locates the week-program-pointer DP at
// call time. KindIP uses ACTIVE_PROFILE (an INTEGER); KindRF uses
// WEEK_PROGRAM_POINTER (an ENUM on classic devices, INTEGER on a few
// HmIPW models). KindSimpleRF returns nil. RF families ship the DP
// on either the climate channel itself or the device-root MASTER
// paramset (HM-CC-VG-1, HM-TC-IT-WM-W-EU); the lookup walks both.
//
// The return type is the broad [device.ParameterDataPoint] interface
// because both Integer- and Select-DPs satisfy it — only the wire
// descriptor MIN/MAX matter for [numWeekPrograms].
func resolveWeekProgramPointer(ch *device.Channel, k Kind) device.ParameterDataPoint {
	if ch == nil {
		return nil
	}
	switch k { //nolint:exhaustive // KindSimpleRF has no week-program pointer; only IP and RF have one
	case KindIP:
		if dp := ch.Parameter(hmenum.ParameterActiveProfile); dp != nil {
			return dp
		}
		if dp := ch.MasterParameter(hmenum.ParameterActiveProfile); dp != nil {
			return dp
		}
		return nil
	case KindRF:
		if dp := ch.Parameter(hmenum.ParameterWeekProgramPointer); dp != nil {
			return dp
		}
		if dp := ch.MasterParameter(hmenum.ParameterWeekProgramPointer); dp != nil {
			return dp
		}
		if dev := ch.Device(); dev != nil {
			if root := dev.RootChannel(); root != nil {
				if dp := root.MasterParameter(hmenum.ParameterWeekProgramPointer); dp != nil {
					return dp
				}
				if dp := root.Parameter(hmenum.ParameterWeekProgramPointer); dp != nil {
					return dp
				}
			}
			// Final fallback — scan every channel.
			for _, sibling := range dev.Channels() {
				if sibling == nil {
					continue
				}
				if dp := sibling.Parameter(hmenum.ParameterWeekProgramPointer); dp != nil {
					return dp
				}
				if dp := sibling.MasterParameter(hmenum.ParameterWeekProgramPointer); dp != nil {
					return dp
				}
			}
		}
	}
	return nil
}

// numWeekPrograms returns the count of week-program slots advertised
// by the wire descriptor on the week-program-pointer DP.
// count = max-min+1.
//
// When the device does not carry a week-program-pointer DP at all
// (typical for HM-CC-RT-DN — no WEEK_PROGRAM_POINTER in the paramset
// description), the count is 0 and `Profiles()` emits no week
// program slots. The IP family ships six pointer slots on every
// channel; the RF family ranges from three (HM-CC-VG-1) to zero
// (HM-CC-RT-DN). Static "family default" guesses produce drift
// Against, so the exact wire MIN/MAX is the
// only authoritative source.
func (c *Climate) numWeekPrograms() int {
	dp := resolveWeekProgramPointer(c.channelRef, c.Kind)
	if dp == nil {
		// In production this means the device has no week-program-
		// pointer DP at all (e.g. HM-CC-RT-DN) — preset list collapses
		// To the static base. Test rigs that
		// don't wire a pointer DP fall back to a kind-default so unit
		// tests don't have to construct a synthetic descriptor.
		if c.channelRef == nil {
			switch c.Kind { //nolint:exhaustive // KindSimpleRF has no week programs; only IP and RF have kind-defaults
			case KindIP:
				return 6
			case KindRF:
				return 3
			}
		}
		return 0
	}
	desc := dp.ParameterData()
	lo, loOK := parseFloat(desc.Min)
	hi, hiOK := parseFloat(desc.Max)
	if !loOK || !hiOK {
		return 0
	}
	count := min(max(int(hi-lo)+1, 0), 6)
	return count
}

// parseFloat extracts a float64 from a wire descriptor's RawMessage.
// Local copy of internal/model/generic.parseFloat — kept here because
// the climate package can't import generic's unexported helper.
func parseFloat(rm json.RawMessage) (float64, bool) {
	if len(rm) == 0 {
		return 0, false
	}
	var v any
	if err := json.Unmarshal(rm, &v); err != nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if x == "" {
			return 0, false
		}
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func profileWeekIndex(p Profile) (int32, bool) {
	switch p {
	case ProfileWeekProgram1:
		return 0, true
	case ProfileWeekProgram2:
		return 1, true
	case ProfileWeekProgram3:
		return 2, true
	case ProfileWeekProgram4:
		return 3, true
	case ProfileWeekProgram5:
		return 4, true
	case ProfileWeekProgram6:
		return 5, true
	case ProfileNone, ProfileAway, ProfileBoost, ProfileComfort, ProfileEco:
		return 0, false
	}
	return 0, false
}

func clamp(v, lo, hi float64) float64 {
	if lo != 0 && v < lo {
		return lo
	}
	if hi != 0 && v > hi {
		return hi
	}
	return v
}

// IsStateChange reports whether the proposed climate change would alter the
// device state. Returns true when any of the following conditions hold:
//
// - temperature differs from the current setpoint - mode differs from the
// current mode (when mode has been observed) - profile differs from the
// current profile (when profile has been observed)
//
// Returns true when no current value has been observed (first command always
// goes through), or when the state is uncertain. Thread-safe.
func (c *Climate) IsStateChange(temperature *float64, mode *Mode, profile *Profile) bool {
	if c.StateUncertain() {
		return true
	}
	if temperature != nil {
		sp, ok := c.Setpoint()
		if !ok || sp != *temperature {
			return true
		}
	}
	if mode != nil {
		m, ok := c.Mode()
		if !ok || m != *mode {
			return true
		}
	}
	if profile != nil {
		p, ok := c.Profile()
		if !ok || p != *profile {
			return true
		}
	}
	return false
}

// RefreshLinkPeerActivitySources re-subscribes the Climate to any linked
// valve/switch peer channels so that heating activity can be inferred from
// them.
//
// In the Go model, the materializer calls this after a device's LINK paramset
// changes — it passes the set of peer channels; the Climate unsubscribes from
// previous peers, then subscribes to LEVEL / STATE on the new set. Each valid
// peer channel that exposes LEVEL (valve-style) or STATE (switch-style)
// contributes an activity inference subscription.
//
// Returns a combined cancel function that detaches all peer subscriptions.
// The caller is responsible for storing and invoking it when peer topology
// changes again or the Climate is torn down.
func (c *Climate) RefreshLinkPeerActivitySources(peerChannels []*device.Channel) func() {
	var unsubs []func()
	// Peer-sourced activity must honour `_is_heating_mode` so that HmIP
	// thermostats linked to a valve actuator (e.g. HmIP-WTH-1 + HmIP-FALMOT-C12)
	// report COOL while HEATING_COOLING == "COOLING".
	runningActivity := func() Activity {
		if c.Kind == KindIP && c.heatingMode() == "COOLING" {
			return ActivityCooling
		}
		return ActivityHeating
	}
	classify := func(level float64) Activity {
		if level > 0 {
			return runningActivity()
		}
		return ActivityIdle
	}
	for _, ch := range peerChannels {
		if ch == nil {
			continue
		}
		// Prefer LEVEL (valve openness percentage) over STATE (binary on/off).
		if dp := ch.Parameter(hmenum.ParameterLevel); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) {
				if v, ok := toFloat(next); ok {
					c.OnActivity(classify(v))
				}
			}))
			continue
		}
		if dp := ch.Parameter(hmenum.ParameterState); dp != nil {
			unsubs = append(unsubs, dp.OnAnyUpdate(func(_, next any) {
				if b, ok := next.(bool); ok {
					if b {
						c.OnActivity(runningActivity())
					} else {
						c.OnActivity(ActivityIdle)
					}
				}
			}))
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
