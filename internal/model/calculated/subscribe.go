// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// channelSubscribeCapture is the capturing variant of channelSubscribe.
// It returns the unsubscribe closure AND the resolved source DP so
// callers can pass it to [sourceSink.RegisterSource] for StateUncertain
// aggregation. When the parameter is absent the returned dp is nil and
// the unsubscribe is nil.
func channelSubscribeCapture(c *device.Channel, name hmenum.Parameter, fn func(value float64)) (unsub func(), dp SourceDP) {
	if c == nil {
		return nil, nil
	}
	raw := c.Parameter(name)
	if raw == nil {
		return nil, nil
	}
	// device.ParameterDataPoint satisfies SourceDP when the underlying
	// *generic.DataPoint[T] is used — all production DPs do.
	src, ok := raw.(SourceDP)
	if !ok {
		// Unlikely in production (all DPs are *generic.DataPoint[T]);
		// subscribe the callback but skip source registration.
		return raw.OnAnyUpdate(func(_, next any) {
			if v, fok := toFloat64(next); fok {
				fn(v)
			}
		}), nil
	}
	unsub = raw.OnAnyUpdate(func(_, next any) {
		if v, fok := toFloat64(next); fok {
			fn(v)
		}
	})
	return unsub, src
}

func toFloat64(v any) (float64, bool) {
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

// composeUnsubs returns a single closure that fires every non-nil
// member of `unsubs` exactly once.
func composeUnsubs(unsubs ...func()) func() {
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

// subscribeTemperatureHumidityCapture wires the channel's temperature and
// humidity slots to the supplied callbacks, honouring both the
// canonical (ACTUAL_TEMPERATURE / HUMIDITY) and legacy (TEMPERATURE
// / ACTUAL_HUMIDITY) names.
// `fallback_parameters` resolution. It returns the composed unsubscribe
// and a slice of resolved source DPs suitable for
// [sourceSink.RegisterSource] calls.
//
// Each distinct resolved parameter contributes at most one DP to the
// returned slice — the first non-nil wins for each logical slot
// (temperature / humidity). Both aliases are subscribed for callback
// Delivery but only
// one DP per slot is tracked for uncertainty aggregation so that the
// aggregation is not double-counted.
func subscribeTemperatureHumidityCapture(c *device.Channel, onTemp, onHum func(float64)) (func(), []SourceDP) {
	var sources []SourceDP

	// Temperature — prefer ACTUAL_TEMPERATURE, fall back to TEMPERATURE.
	u1, dp1 := channelSubscribeCapture(c, hmenum.ParameterActualTemperature, onTemp)
	u2, dp2 := channelSubscribeCapture(c, hmenum.ParameterTemperature, onTemp)
	if dp1 != nil {
		sources = append(sources, dp1)
	} else if dp2 != nil {
		sources = append(sources, dp2)
	}

	// Humidity — prefer HUMIDITY, fall back to ACTUAL_HUMIDITY.
	u3, dp3 := channelSubscribeCapture(c, hmenum.ParameterHumidity, onHum)
	u4, dp4 := channelSubscribeCapture(c, hmenum.ParameterActualHumidity, onHum)
	if dp3 != nil {
		sources = append(sources, dp3)
	} else if dp4 != nil {
		sources = append(sources, dp4)
	}

	return composeUnsubs(u1, u2, u3, u4), sources
}

// Subscribe wires the DewPointSensor to the channel's temperature
// and humidity parameters (with TEMPERATURE / ACTUAL_HUMIDITY
// aliases). Updates flow through [DewPointSensor.OnTemperature] /
// [OnHumidity] automatically. Resolved source DPs are registered for
// [StateUncertain] aggregation.
func (s *DewPointSensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	return unsub
}

// Subscribe wires the DewPointSpreadSensor to temperature + humidity.
// Resolved source DPs are registered for [StateUncertain] aggregation.
func (s *DewPointSpreadSensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	return unsub
}

// Subscribe wires the FrostPointSensor to temperature + humidity.
// Resolved source DPs are registered for [StateUncertain] aggregation.
func (s *FrostPointSensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	return unsub
}

// Subscribe wires the VaporConcentrationSensor to temperature +
// humidity. Resolved source DPs are registered for [StateUncertain]
// aggregation.
func (s *VaporConcentrationSensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	return unsub
}

// Subscribe wires the EnthalpySensor to temperature + humidity
// (+ AIR_PRESSURE when present). Resolved source DPs are registered
// for [StateUncertain] aggregation.
func (s *EnthalpySensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	uPressure, dpPressure := channelSubscribeCapture(c, hmenum.ParameterAirPressure, s.OnPressure)
	s.RegisterSource(dpPressure) // nil-safe: RegisterSource ignores nil
	return composeUnsubs(unsub, uPressure)
}

// Subscribe wires the ApparentTemperatureSensor to temperature +
// humidity + WIND_SPEED. Resolved source DPs are registered for
// [StateUncertain] aggregation.
func (s *ApparentTemperatureSensor) Subscribe(c *device.Channel) func() {
	unsub, srcs := subscribeTemperatureHumidityCapture(c, s.OnTemperature, s.OnHumidity)
	for _, dp := range srcs {
		s.RegisterSource(dp)
	}
	uWind, dpWind := channelSubscribeCapture(c, hmenum.ParameterWindSpeed, s.OnWindSpeed)
	s.RegisterSource(dpWind) // nil-safe
	return composeUnsubs(unsub, uWind)
}

// Subscribe wires the OperatingVoltageLevelSensor to OPERATING_VOLTAGE
// (VALUES) and feeds an initial reference pair from LOW_BAT_LIMIT
// (MASTER) plus the per-model `voltage_max` derived from the battery
// Table.
// `_post_init` (operating_voltage_level.py:54-148): it reads the
// MASTER LOW_BAT_LIMIT once at materialise time and recomputes
// whenever OPERATING_VOLTAGE changes.
//
// The per-channel battery configuration is resolved through
// [LookupBatteryConfig] on the parent device's model — the same
// Longest-prefix match Callers must therefore
// have stamped the device's Model on the channel's parent before
// attach (the device pipeline does this in [hydrateChannel]).
//
// Returns a no-op closure when the channel cannot supply
// OPERATING_VOLTAGE; the caller's [device.Channel.AttachCalculatedDataPoint]
// will simply hold a sensor that never emits.
func (s *OperatingVoltageLevelSensor) Subscribe(c *device.Channel) func() {
	if c == nil {
		return nil
	}
	// Resolve voltage_max from the per-model battery table. Without it
	// we cannot compute a percentage so the sensor stays inert until
	// SetReferences is called manually.
	if dev := c.Device(); dev != nil {
		if cfg, ok := LookupBatteryConfig(dev.Model); ok {
			s.setBatteryConfig(cfg)
		}
	}
	// Resolve _low_bat_limit_default from the MASTER paramset descriptor. This
	// is the factory default before any user configuration.
	if dp := c.MasterParameter(hmenum.ParameterLowBatLimit); dp != nil {
		if pd := dp.ParameterData(); len(pd.Default) > 0 {
			var raw any
			if err := json.Unmarshal(pd.Default, &raw); err == nil {
				if f, fok := toFloat64(raw); fok {
					s.setLowBatLimitDefault(f)
				}
			}
		}
	}
	// Seed the initial LOW_BAT_LIMIT from MASTER. applyLowBatLimit only stores
	// the pair when the battery table supplied a maximum that exceeds the limit,
	// so it stays inert until both are present.
	if dp := c.MasterParameter(hmenum.ParameterLowBatLimit); dp != nil {
		if v, ok := dp.RawValue(); ok {
			if f, fok := toFloat64(v); fok {
				s.applyLowBatLimit(f)
			}
		}
	}
	// Capture the primary voltage source DP for StateUncertain aggregation.
	// Prefer OPERATING_VOLTAGE; fall back to BATTERY_STATE. Only one is
	// registered — whichever the channel exposes — to avoid double-counting.
	uVoltage, dpVoltage := channelSubscribeCapture(c, hmenum.ParameterOperatingVoltage, s.OnOperatingVoltage)
	uBattery, dpBattery := channelSubscribeCapture(c, hmenum.ParameterBatteryState, s.OnOperatingVoltage)
	if dpVoltage != nil {
		s.RegisterSource(dpVoltage)
	} else if dpBattery != nil {
		s.RegisterSource(dpBattery)
	}
	return composeUnsubs(
		// Live MASTER updates: a re-read of LOW_BAT_LIMIT updates the
		// reference pair so the percentage tracks operator changes.
		masterSubscribe(c, hmenum.ParameterLowBatLimit, s.applyLowBatLimit),
		uVoltage,
		// BATTERY_STATE fallback: BidCos / older HmIP devices expose
		// OPERATING_VOLTAGE through BATTERY_STATE instead. Both subscriptions feed
		// the same recompute so devices that expose only one of the two still drive
		// the sensor.
		uBattery,
	)
}

// Subscribe wires the DerivedBinarySensor to its [SourceParameter] in the
// channel's VALUES paramset. Each upstream wire-level update is fed through
// [DerivedBinarySensor.OnLabel]. When [SourceParameter] is empty the
// subscribe is a no-op so manually-driven test fixtures keep working.
//
// Every source in the registry is a read-only ENUM, which resolves to an
// integer data point: the value pushed here is the 0-based VALUE_LIST index,
// not the label the On/Off value sets are written in. Both shapes are
// resolved through the descriptor — a bare string assertion dropped every
// event these sensors exist to observe.
//
// The resolved source DP is registered for [StateUncertain] aggregation.
func (s *DerivedBinarySensor) Subscribe(c *device.Channel) func() {
	if c == nil || s.SourceParameter == "" {
		return nil
	}
	raw := c.Parameter(s.SourceParameter)
	if raw == nil {
		return nil
	}
	if src, ok := raw.(SourceDP); ok {
		s.RegisterSource(src)
	}
	desc := raw.ParameterData()
	return raw.OnAnyUpdate(func(_, next any) {
		if label, ok := parameter.EnumLabel(desc, next); ok {
			s.OnLabel(label)
		}
	})
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the DewPointSensor. Implements [Sensor.LoadDataPointValue].
func (s *DewPointSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the DewPointSpreadSensor. Implements [Sensor.LoadDataPointValue].
func (s *DewPointSpreadSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the FrostPointSensor. Implements [Sensor.LoadDataPointValue].
func (s *FrostPointSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the VaporConcentrationSensor. Implements [Sensor.LoadDataPointValue].
func (s *VaporConcentrationSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the EnthalpySensor. Implements [Sensor.LoadDataPointValue].
func (s *EnthalpySensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the ApparentTemperatureSensor. Implements [Sensor.LoadDataPointValue].
func (s *ApparentTemperatureSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for every source
// DP wired into the OperatingVoltageLevelSensor. Implements [Sensor.LoadDataPointValue].
func (s *OperatingVoltageLevelSensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// LoadDataPointValue triggers a CCU-side value refresh for the source
// DP wired into the DerivedBinarySensor. Implements [Sensor.LoadDataPointValue].
func (s *DerivedBinarySensor) LoadDataPointValue(loader func(channelAddress, parameter string)) {
	s.sourceSink.LoadDataPointValue(loader)
}

// masterSubscribe wires fn to the MASTER paramset entry `name` if it
// exists, returning the unsubscribe closure. Mirrors
// [channelSubscribe] but reads from [device.Channel.MasterParameter]
// — used for reference-config parameters like LOW_BAT_LIMIT that
// live in MASTER, not VALUES.
func masterSubscribe(c *device.Channel, name hmenum.Parameter, fn func(value float64)) func() {
	if c == nil {
		return nil
	}
	dp := c.MasterParameter(name)
	if dp == nil {
		return nil
	}
	return dp.OnAnyUpdate(func(_, next any) {
		if v, ok := toFloat64(next); ok {
			fn(v)
		}
	})
}
