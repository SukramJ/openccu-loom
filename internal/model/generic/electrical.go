// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ElectricalGroup consolidates a channel's electrical measurements into the
// one endpoint the Matter Device Library specifies for them.
//
// A metering plug reports POWER, VOLTAGE, CURRENT, FREQUENCY and
// ENERGY_COUNTER as five separate CCU parameters. Matter models the first four
// as attributes of ONE ElectricalPowerMeasurement cluster (0x0090:
// ActivePower, Voltage, ActiveCurrent, Frequency) and the fifth as
// ElectricalEnergyMeasurement (0x0091), both carried by a single
// ElectricalSensor device type (0x0510) — matter.js
// packages/model/src/standard/elements/electrical-sensor.element.ts. Projecting
// each parameter as its own endpoint would contradict the spec's grouping and
// hand the operator five accessories for one socket.
//
// This mirrors [ButtonGroup], which consolidates a channel's press parameters
// into one GenericSwitch for the same reason.
type ElectricalGroup struct {
	// Each slot holds the source for one Matter attribute, or nil when the
	// device does not report that parameter. A group with every slot nil is
	// never constructed — NewElectricalGroup returns nil instead.
	activePower interfaces.MatterFloatMeasurementSource
	voltage     interfaces.MatterFloatMeasurementSource
	current     interfaces.MatterFloatMeasurementSource
	frequency   interfaces.MatterFloatMeasurementSource
	energy      interfaces.MatterFloatMeasurementSource

	// members keeps every source in registration order so change
	// notifications can be fanned out across all of them.
	members []interfaces.MatterFloatMeasurementSource
}

// Compile-time contracts.
var (
	_ interfaces.MatterMeasurementSource = (*ElectricalGroup)(nil)
	_ interfaces.MatterChangeNotifier    = (*ElectricalGroup)(nil)
)

// electricalParameterSlot names the group slot a CCU parameter feeds, or
// returns false when the parameter is not an electrical measurement.
//
// Kept in lock-step with the MatterMeasurementPower / MatterMeasurementEnergy
// arms of [matterMeasurementForParameter]: a parameter classified electrical
// there but missing here is collected into the group by the assembler and then
// silently dropped by [NewElectricalGroup].
// TestW2GenElectricalGroupCoversEveryElectricalParameter pins the two
// together, reading both member sets out of the source.
func electricalParameterSlot(p hmenum.Parameter) (slot string, ok bool) {
	switch p {
	case hmenum.ParameterPower:
		return "activePower", true
	case hmenum.ParameterVoltage:
		return "voltage", true
	case hmenum.ParameterCurrent:
		return "current", true
	case hmenum.ParameterFrequency:
		return "frequency", true
	case hmenum.ParameterEnergyCounter, hmenum.ParameterEnergyCounterFeedIn:
		return "energy", true
	default:
		return "", false
	}
}

// ElectricalGroupMember is the shape a data point must satisfy to join the
// group: a float reading that names the parameter it came from.
type ElectricalGroupMember interface {
	interfaces.MatterFloatMeasurementSource
	DataPointKey() hmtypes.DataPointKey
}

// NewElectricalGroup builds the consolidated group from a channel's electrical
// data points. Members that are nil, or whose parameter is not electrical, are
// skipped. Returns nil when no member remains, so the caller creates no
// endpoint for a channel that has nothing to report.
//
// ENERGY_COUNTER wins over ENERGY_COUNTER_FEED_IN for the single energy slot:
// both map to the same cluster, and consumption is the reading a controller
// shows by default. A device reporting only feed-in still fills the slot.
func NewElectricalGroup(members ...ElectricalGroupMember) *ElectricalGroup {
	g := &ElectricalGroup{}
	for _, m := range members {
		if m == nil {
			continue
		}
		p := hmenum.Parameter(m.DataPointKey().Parameter)
		slot, ok := electricalParameterSlot(p)
		if !ok {
			continue
		}
		switch slot {
		case "activePower":
			g.activePower = m
		case "voltage":
			g.voltage = m
		case "current":
			g.current = m
		case "frequency":
			g.frequency = m
		case "energy":
			// Consumption takes the slot; feed-in only fills an empty one.
			if p == hmenum.ParameterEnergyCounter || g.energy == nil {
				g.energy = m
			} else {
				continue
			}
		}
		g.members = append(g.members, m)
	}
	if len(g.members) == 0 {
		return nil
	}
	return g
}

// MatterMeasurementClass implements [interfaces.MatterMeasurementSource]: the
// consolidated group projects as an ElectricalSensor endpoint.
func (g *ElectricalGroup) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if g == nil {
		return interfaces.MatterMeasurementNone
	}
	return interfaces.MatterMeasurementElectrical
}

// ActivePower returns the POWER reading in watts.
func (g *ElectricalGroup) ActivePower() (float64, bool) { return readSlot(g, g.slotActivePower) }

// Voltage returns the VOLTAGE reading in volts.
func (g *ElectricalGroup) Voltage() (float64, bool) { return readSlot(g, g.slotVoltage) }

// Current returns the CURRENT reading in milliamperes, the unit the CCU
// reports it in.
func (g *ElectricalGroup) Current() (float64, bool) { return readSlot(g, g.slotCurrent) }

// Frequency returns the FREQUENCY reading in hertz.
func (g *ElectricalGroup) Frequency() (float64, bool) { return readSlot(g, g.slotFrequency) }

// Energy returns the ENERGY_COUNTER reading in watt-hours.
func (g *ElectricalGroup) Energy() (float64, bool) { return readSlot(g, g.slotEnergy) }

// HasEnergy reports whether the channel carries an energy counter at all,
// which is a different question from whether a reading has arrived yet.
//
// The cluster layer must decide the endpoint's ServerList from this rather
// than from Energy()'s observed flag: a Matter endpoint's cluster set is
// quasi-static, and gating it on a value that is absent until the first
// report would have the cluster appear mid-session, after controllers already
// cached the ServerList.
func (g *ElectricalGroup) HasEnergy() bool { return g != nil && g.energy != nil }

// The slot accessors exist so readSlot can stay generic over a nil group.
func (g *ElectricalGroup) slotActivePower() interfaces.MatterFloatMeasurementSource {
	return g.activePower
}
func (g *ElectricalGroup) slotVoltage() interfaces.MatterFloatMeasurementSource { return g.voltage }
func (g *ElectricalGroup) slotCurrent() interfaces.MatterFloatMeasurementSource { return g.current }
func (g *ElectricalGroup) slotFrequency() interfaces.MatterFloatMeasurementSource {
	return g.frequency
}
func (g *ElectricalGroup) slotEnergy() interfaces.MatterFloatMeasurementSource { return g.energy }

// readSlot reads one slot, treating a nil group and an unpopulated slot alike:
// the reading is absent, which the cluster layer renders as a Matter null
// rather than as an unsupported attribute.
func readSlot(g *ElectricalGroup, slot func() interfaces.MatterFloatMeasurementSource) (float64, bool) {
	if g == nil {
		return 0, false
	}
	src := slot()
	if src == nil {
		return 0, false
	}
	return src.MatterFloatValue()
}

// MatterFloatValue implements [interfaces.MatterFloatMeasurementSource] with
// the group's headline reading, active power. Present so the group satisfies
// the same interface as a single-parameter measurement source; the cluster
// layer reads the typed accessors instead.
func (g *ElectricalGroup) MatterFloatValue() (float64, bool) { return g.ActivePower() }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] by
// subscribing to every member. Without the fan-out, a controller subscribed to
// the endpoint would see only whichever parameter happened to own the
// notification, and the other attributes would update on read alone.
func (g *ElectricalGroup) OnMatterValueChanged(cb func()) func() {
	if g == nil || cb == nil {
		return func() {}
	}
	unsubs := make([]func(), 0, len(g.members))
	for _, m := range g.members {
		n, ok := m.(interfaces.MatterChangeNotifier)
		if !ok || n == nil {
			continue
		}
		if unsub := n.OnMatterValueChanged(cb); unsub != nil {
			unsubs = append(unsubs, unsub)
		}
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}
