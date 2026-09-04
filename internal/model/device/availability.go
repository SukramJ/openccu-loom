// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"sync"
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
	IsReachable bool `json:"IsReachable"`

	// LastUpdated is the most recent OnEvent timestamp across every
	// data point on every channel. Zero when nothing has been
	// observed yet.
	LastUpdated time.Time `json:"LastUpdated"`

	// BatteryLevel is a 0-100 percentage derived from
	// OPERATING_VOLTAGE_LEVEL. It is nil on devices that report only a
	// raw cell voltage — see [Availability.batteryLevel] for why
	// BATTERY_STATE is not a second source.
	BatteryLevel *int `json:"BatteryLevel"`

	// LowBattery is the LOW_BAT parameter. Nil when the device has
	// no battery indicator.
	LowBattery *bool `json:"LowBattery"`

	// SignalStrength is RSSI_DEVICE in dBm (negative values) — the
	// reception strength the device reports for the central.
	SignalStrength *int `json:"SignalStrength"`

	// RSSIPeer is RSSI_PEER in dBm (negative values) — the reception
	// strength the central/partner reports for the device. Nil when the
	// device does not expose it. Excluded from JSON: it is not part of
	// the documented DeviceDetail.availability schema
	// (assets/openapi.yaml) — the field-name-as-key struct had no json
	// tags at all, so this one leaked onto the wire as an undocumented
	// "RSSIPeer" key that no client contract or generated type knows
	// about. RSSI_PEER remains reachable through the generic
	// paramset/data-point endpoints for callers that need it.
	RSSIPeer *int `json:"-"`
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

	// mu guards forced. The override is written from event-bus
	// handlers — the interface client's reconnect/ping-pong goroutine
	// and the system-status handler — while REST, MQTT and Matter
	// readers evaluate IsReachable on their own goroutines. Without
	// the lock those readers race a two-word string-header store and
	// can publish an arbitrary availability verdict for one pass.
	mu     sync.RWMutex
	forced hmenum.ForcedDeviceAvailability
}

// newAvailability constructs a fresh tracker tied to d.
func newAvailability(d *Device) *Availability {
	return &Availability{device: d, forced: hmenum.ForcedDeviceAvailabilityNotSet}
}

// Forced returns the active override.
func (a *Availability) Forced() hmenum.ForcedDeviceAvailability {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.forced
}

// SetForced applies an override. Returns true when the effective
// availability actually flipped — the caller decides whether to emit
// an event.
func (a *Availability) SetForced(v hmenum.ForcedDeviceAvailability) bool {
	_, reachabilityFlipped := a.setForced(v)
	return reachabilityFlipped
}

// setForced applies the override and reports both whether the override
// itself changed and whether the effective reachability flipped. Both
// verdicts are computed in one critical section: the per-interface
// reconnect handler and the system-status handler write the same field
// concurrently, so a compare in one goroutine and a store in the other
// would otherwise interleave and mis-compute the "changed" answer that
// gates the lifecycle event.
func (a *Availability) setForced(v hmenum.ForcedDeviceAvailability) (overrideChanged, reachabilityFlipped bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.forced == v {
		return false, false
	}
	old := a.isReachableWith(a.forced)
	a.forced = v
	return true, old != a.isReachableWith(v)
}

// IsReachable honors the forced override first; otherwise derives
// from UN_REACH (falling back to STICKY_UN_REACH).
func (a *Availability) IsReachable() bool {
	return a.isReachableWith(a.Forced())
}

// isReachableWith is [IsReachable] against an already-resolved override,
// so the reachability verdict can also be computed from inside the
// write-locked section of [setForced] without re-entering the lock.
func (a *Availability) isReachableWith(forced hmenum.ForcedDeviceAvailability) bool {
	if forced == hmenum.ForcedDeviceAvailabilityForceTrue {
		return true
	}
	if forced == hmenum.ForcedDeviceAvailabilityForceFalse {
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
		RSSIPeer:       a.rssiPeer(),
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
	// VALUES paramset first (RSSI_DEVICE, BATTERY_STATE, …).
	if dp := ch.Parameter(p); dp != nil {
		if raw, observed := dp.RawValue(); observed {
			if f, ok := floatFromRaw(raw); ok {
				return f, true
			}
		}
	}
	// Then calculated data points. OPERATING_VOLTAGE_LEVEL — the derived
	// battery-level percentage — lives in the channel's calculated set, not
	// the VALUES paramset, so a plain Parameter() lookup never finds it.
	for _, dp := range ch.CalculatedDataPoints() {
		if dp.DataPointKey().Parameter != string(p) {
			continue
		}
		rv, readable := dp.(interface{ RawValue() (any, bool) })
		if !readable {
			continue
		}
		if raw, observed := rv.RawValue(); observed {
			if f, ok := floatFromRaw(raw); ok {
				return f, true
			}
		}
	}
	return 0, false
}

// floatFromRaw coerces a wire-decoded numeric value into a float64.
func floatFromRaw(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// batteryLevel returns the 0-100 battery percentage, or nil when the device
// reports none.
//
// OPERATING_VOLTAGE_LEVEL is the only source. BATTERY_STATE is deliberately
// NOT a fallback: every device type that declares it declares it as a cell
// voltage — `<logical type="float" min="1.5" max="4.6" unit="V"/>`, docu
// brief "Batteriespannung" — on all three declaring models
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2350-2359,
// rf_cc_rt_dn_bom.xml:2313-2319, rf_tc_it_wm-w-eu.xml:5211-5220, the complete
// set in the shipped rftypes/hs485types corpus). None of them is on channel
// 0 either — HM-CC-RT-DN and its BoM variant put it on channel 4, HM-TC-IT on
// channel 2 — while this reader looks only at `<address>:0`.
//
// This replaces a `BATTERY_STATE > 10` test that read the value as a
// percentage whenever it exceeded 10. No firmware-declared BATTERY_STATE can
// exceed 4.6, so that branch was unreachable, and its premise was the
// opposite of what the descriptor says.
//
// Turning the voltage into a percentage would need a per-model discharge
// curve. The descriptor carries MIN/MAX/UNIT but no such curve, and no other
// source in this repository does either, so that conversion is unverified and
// is not attempted here — LOW_BAT/LOWBAT remains the battery signal for these
// devices.
func (a *Availability) batteryLevel() *int {
	if f, ok := a.channel0Float(hmenum.Parameter(hmenum.CalculatedParameterOperatingVoltageLevel)); ok {
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
		// LOW_BAT (HM/BidCos) and LOWBAT (HmIP) carry the same low-battery
		// flag under different parameter names — check both.
		for _, p := range []hmenum.Parameter{hmenum.ParameterLowBat, hmenum.ParameterLowbat} {
			dp := channel.Parameter(p)
			if dp == nil {
				continue
			}
			if raw, ok := dp.RawValue(); ok {
				if b, ok := raw.(bool); ok {
					return &b
				}
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

func (a *Availability) rssiPeer() *int {
	if f, ok := a.channel0Float(hmenum.ParameterRSSIPeer); ok {
		v := int(f)
		return &v
	}
	return nil
}
