// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AvailabilityInfo is the unified availability snapshot consumers
// read to answer "is this device reachable and healthy".
//
// Fields are optional where the underlying parameter isn't exposed
// by the device (e.g. battery-less devices have no BatteryLevel).
type AvailabilityInfo struct {
	// IsReachable is the inverse of UN_REACH (falls back to
	// STICKY_UN_REACH, defaults to true when neither has been seen).
	IsReachable bool

	// LastUpdated is the most recent OnEvent timestamp across every
	// data point on every channel. Zero when nothing has been
	// observed yet.
	LastUpdated time.Time

	// BatteryLevel is a 0-100 percentage derived from
	// OPERATING_VOLTAGE_LEVEL (preferred) or BATTERY_STATE when the
	// latter looks like a percentage (> 10).
	BatteryLevel *int

	// LowBattery is the LOW_BAT parameter. Nil when the device has
	// no battery indicator.
	LowBattery *bool

	// SignalStrength is RSSI_DEVICE in dBm (negative values).
	SignalStrength *int
}

// HasBattery reports whether any battery reading is present.
func (a AvailabilityInfo) HasBattery() bool {
	return a.BatteryLevel != nil || a.LowBattery != nil
}

// HasSignalInfo reports whether a signal strength reading is
// present.
func (a AvailabilityInfo) HasSignalInfo() bool {
	return a.SignalStrength != nil
}

// Availability tracks forced-availability overrides and derives
// [AvailabilityInfo] from the host device's observed data points.
type Availability struct {
	device *Device
	forced hmenum.ForcedDeviceAvailability
}

// newAvailability constructs a fresh tracker tied to d.
func newAvailability(d *Device) *Availability {
	return &Availability{device: d, forced: hmenum.ForcedDeviceAvailabilityNotSet}
}

// Forced returns the active override.
func (a *Availability) Forced() hmenum.ForcedDeviceAvailability { return a.forced }

// SetForced applies an override. Returns true when the effective
// availability actually flipped — the caller decides whether to emit
// an event.
func (a *Availability) SetForced(v hmenum.ForcedDeviceAvailability) bool {
	if a.forced == v {
		return false
	}
	old := a.IsReachable()
	a.forced = v
	return old != a.IsReachable()
}

// IsReachable honors the forced override first; otherwise derives
// from UN_REACH (falling back to STICKY_UN_REACH).
func (a *Availability) IsReachable() bool {
	if a.forced == hmenum.ForcedDeviceAvailabilityForceTrue {
		return true
	}
	if a.forced == hmenum.ForcedDeviceAvailabilityForceFalse {
		return false
	}
	if b, ok := a.channel0Bool(hmenum.ParameterUnreach); ok {
		return !b
	}
	if b, ok := a.channel0Bool(hmenum.ParameterStickyUnreach); ok {
		return !b
	}
	return true
}

// IsConfigPending reads the CONFIG_PENDING binary sensor on channel 0.
func (a *Availability) IsConfigPending() bool {
	if b, ok := a.channel0Bool(hmenum.ParameterConfigPending); ok {
		return b
	}
	return false
}

// Info returns a fresh snapshot.
func (a *Availability) Info() AvailabilityInfo {
	return AvailabilityInfo{
		IsReachable:    a.IsReachable(),
		LastUpdated:    a.device.LastUpdated(),
		BatteryLevel:   a.batteryLevel(),
		LowBattery:     a.lowBattery(),
		SignalStrength: a.signalStrength(),
	}
}

func (a *Availability) channel0Bool(p hmenum.Parameter) (value, ok bool) {
	ch := a.device.Channel(a.device.Address + ":0")
	if ch == nil {
		return false, false
	}
	dp := ch.Parameter(p)
	if dp == nil {
		return false, false
	}
	raw, ok := dp.RawValue()
	if !ok {
		return false, false
	}
	b, ok := raw.(bool)
	return b, ok
}

func (a *Availability) channel0Float(p hmenum.Parameter) (value float64, ok bool) {
	ch := a.device.Channel(a.device.Address + ":0")
	if ch == nil {
		return 0, false
	}
	dp := ch.Parameter(p)
	if dp == nil {
		return 0, false
	}
	raw, ok := dp.RawValue()
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func (a *Availability) batteryLevel() *int {
	if f, ok := a.channel0Float(hmenum.Parameter(hmenum.CalculatedParameterOperatingVoltageLevel)); ok {
		v := int(f)
		return &v
	}
	if f, ok := a.channel0Float(hmenum.ParameterBatteryState); ok && f > 10 {
		v := int(f)
		return &v
	}
	return nil
}

func (a *Availability) lowBattery() *bool {
	for _, ch := range []string{":0", ":1", ":2"} {
		channel := a.device.Channel(a.device.Address + ch)
		if channel == nil {
			continue
		}
		dp := channel.Parameter(hmenum.ParameterLowBat)
		if dp == nil {
			continue
		}
		if raw, ok := dp.RawValue(); ok {
			if b, ok := raw.(bool); ok {
				return &b
			}
		}
	}
	return nil
}

func (a *Availability) signalStrength() *int {
	if f, ok := a.channel0Float(hmenum.ParameterRSSIDevice); ok {
		v := int(f)
		return &v
	}
	return nil
}
