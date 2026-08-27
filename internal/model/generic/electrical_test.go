// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// electricalSensor builds the read-only float data point a metering channel
// carries for one parameter, which is what the group consolidates.
func electricalSensor(t *testing.T, param hmenum.Parameter) *Sensor[float64] {
	t.Helper()
	return NewFloatSensor(Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "0001PSM01:6",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100000.0"),
		},
	})
}

// TestElectricalGroupRoutesEachParameterToItsOwnSlot pins the mapping the
// cluster layer depends on: each CCU parameter reaches the Matter attribute it
// belongs to, and no other.
//
// A misrouted slot is invisible from the outside — the endpoint still carries
// an ElectricalPowerMeasurement cluster and still answers every attribute, it
// just reports the voltage as a current. Only a per-slot assertion catches it.
func TestElectricalGroupRoutesEachParameterToItsOwnSlot(t *testing.T) {
	t.Parallel()

	power := electricalSensor(t, hmenum.ParameterPower)
	voltage := electricalSensor(t, hmenum.ParameterVoltage)
	current := electricalSensor(t, hmenum.ParameterCurrent)
	frequency := electricalSensor(t, hmenum.ParameterFrequency)
	energy := electricalSensor(t, hmenum.ParameterEnergyCounter)

	power.OnEvent(1400)
	voltage.OnEvent(231.5)
	current.OnEvent(6100)
	frequency.OnEvent(49.98)
	energy.OnEvent(87654)

	g := NewElectricalGroup(power, voltage, current, frequency, energy)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil for five electrical members")
	}

	for _, tc := range []struct {
		name string
		read func() (float64, bool)
		want float64
	}{
		{"ActivePower", g.ActivePower, 1400},
		{"Voltage", g.Voltage, 231.5},
		{"Current", g.Current, 6100},
		{"Frequency", g.Frequency, 49.98},
		{"Energy", g.Energy, 87654},
	} {
		got, ok := tc.read()
		if !ok {
			t.Errorf("%s: no observation, want %v", tc.name, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The headline reading is active power: the group also has to satisfy the
	// single-value measurement-source contract.
	if v, ok := g.MatterFloatValue(); !ok || v != 1400 {
		t.Errorf("MatterFloatValue() = (%v, %v), want (1400, true)", v, ok)
	}
	if g.MatterMeasurementClass() != interfaces.MatterMeasurementElectrical {
		t.Errorf("MatterMeasurementClass() = %v, want the electrical class",
			g.MatterMeasurementClass())
	}
}

// TestElectricalGroupUnpopulatedSlotReportsNoObservation pins what a partial
// device produces. A plug reporting only power must leave the other slots
// empty rather than reporting zero — the cluster layer renders an absent
// reading as a Matter null, and a zero volt reading is a different statement.
func TestElectricalGroupUnpopulatedSlotReportsNoObservation(t *testing.T) {
	t.Parallel()

	power := electricalSensor(t, hmenum.ParameterPower)
	power.OnEvent(950)

	g := NewElectricalGroup(power)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil for a single member")
	}
	if v, ok := g.ActivePower(); !ok || v != 950 {
		t.Errorf("ActivePower() = (%v, %v), want (950, true)", v, ok)
	}
	for _, tc := range []struct {
		name string
		read func() (float64, bool)
	}{
		{"Voltage", g.Voltage},
		{"Current", g.Current},
		{"Frequency", g.Frequency},
		{"Energy", g.Energy},
	} {
		if v, ok := tc.read(); ok {
			t.Errorf("%s reported an observation (%v) for a slot the device does not fill", tc.name, v)
		}
	}
	if g.HasEnergy() {
		t.Error("HasEnergy() is true without an energy member; the endpoint would advertise " +
			"ElectricalEnergyMeasurement with nothing behind it")
	}
}

// TestElectricalGroupHasEnergyIsAboutTheSourceNotTheReading is the guard for
// the defect the consolidation shipped with in draft: deciding the cluster set
// from Energy()'s observed flag rather than from whether the channel carries a
// counter at all. A Matter endpoint's ServerList is quasi-static, so a cluster
// gated on a not-yet-reported value appears mid-session, after controllers
// cached the list.
func TestElectricalGroupHasEnergyIsAboutTheSourceNotTheReading(t *testing.T) {
	t.Parallel()

	energy := electricalSensor(t, hmenum.ParameterEnergyCounter)
	g := NewElectricalGroup(energy)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil for an energy-only channel")
	}

	if _, observed := g.Energy(); observed {
		t.Fatal("the counter reports an observation before any event; the case tests nothing")
	}
	if !g.HasEnergy() {
		t.Error("HasEnergy() is false for a channel that carries ENERGY_COUNTER but has not " +
			"reported yet — the energy cluster would appear only once a value arrives")
	}

	energy.OnEvent(12)
	if !g.HasEnergy() {
		t.Error("HasEnergy() flipped with the reading; it must depend on the source alone")
	}
}

// TestElectricalGroupPrefersConsumptionOverFeedIn pins the tie-break for the
// single energy slot. Both parameters map to the same cluster, and a device
// reporting both would otherwise fill the slot by member order — making the
// reading a controller sees depend on the order the channel hydrated in.
func TestElectricalGroupPrefersConsumptionOverFeedIn(t *testing.T) {
	t.Parallel()

	consumption := electricalSensor(t, hmenum.ParameterEnergyCounter)
	feedIn := electricalSensor(t, hmenum.ParameterEnergyCounterFeedIn)
	consumption.OnEvent(500)
	feedIn.OnEvent(9000)

	// Both orders must yield the same slot occupant.
	for _, tc := range []struct {
		name    string
		members []ElectricalGroupMember
	}{
		{"ConsumptionFirst", []ElectricalGroupMember{consumption, feedIn}},
		{"FeedInFirst", []ElectricalGroupMember{feedIn, consumption}},
	} {
		g := NewElectricalGroup(tc.members...)
		if g == nil {
			t.Fatalf("%s: NewElectricalGroup returned nil", tc.name)
		}
		v, ok := g.Energy()
		if !ok {
			t.Errorf("%s: energy slot unfilled", tc.name)
			continue
		}
		if v != 500 {
			t.Errorf("%s: Energy() = %v, want the consumption counter (500)", tc.name, v)
		}
	}

	// A device that reports only feed-in still fills the slot: dropping it
	// would leave a metering endpoint with no energy cluster at all.
	if g := NewElectricalGroup(feedIn); g == nil {
		t.Error("NewElectricalGroup returned nil for a feed-in-only channel")
	} else if v, ok := g.Energy(); !ok || v != 9000 {
		t.Errorf("feed-in only: Energy() = (%v, %v), want (9000, true)", v, ok)
	}
}

// TestElectricalGroupIgnoresNonElectricalMembers asserts the constructor
// filters rather than trusts its caller. The assembler collects members by
// measurement class; a parameter that classifies electrical but has no slot
// would otherwise be counted as a member and silently contribute nothing.
func TestElectricalGroupIgnoresNonElectricalMembers(t *testing.T) {
	t.Parallel()

	temperature := electricalSensor(t, hmenum.ParameterActualTemperature)
	if g := NewElectricalGroup(temperature); g != nil {
		t.Error("NewElectricalGroup built a group from a temperature parameter alone; an endpoint " +
			"would be created for a channel with no electrical reading")
	}

	power := electricalSensor(t, hmenum.ParameterPower)
	power.OnEvent(42)
	g := NewElectricalGroup(temperature, power)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil despite one electrical member")
	}
	if v, ok := g.ActivePower(); !ok || v != 42 {
		t.Errorf("ActivePower() = (%v, %v), want (42, true)", v, ok)
	}
}

// TestElectricalGroupNilCases covers the shapes the assembler can hand the
// constructor on a channel that carries nothing usable.
func TestElectricalGroupNilCases(t *testing.T) {
	t.Parallel()

	if g := NewElectricalGroup(); g != nil {
		t.Error("NewElectricalGroup() with no members returned non-nil")
	}
	if g := NewElectricalGroup(nil); g != nil {
		t.Error("NewElectricalGroup(nil) returned non-nil")
	}

	// A nil group answers like an empty one rather than panicking: it is
	// reachable through the measurement-source interface before the assembler
	// has decided whether to build an endpoint.
	var g *ElectricalGroup
	if g.MatterMeasurementClass() != interfaces.MatterMeasurementNone {
		t.Errorf("nil group class = %v, want None", g.MatterMeasurementClass())
	}
	if _, ok := g.ActivePower(); ok {
		t.Error("nil group reported an active-power observation")
	}
	if g.HasEnergy() {
		t.Error("nil group reported HasEnergy")
	}
	if unsub := g.OnMatterValueChanged(func() {}); unsub == nil {
		t.Error("nil group returned a nil unsubscribe")
	} else {
		unsub()
	}
}

// TestElectricalGroupFansOutChangeNotifications pins the subscription contract.
// Without the fan-out a controller subscribed to the endpoint sees only
// whichever parameter happens to own the notification, and the other
// attributes update on read alone — which looks like a stuck value.
func TestElectricalGroupFansOutChangeNotifications(t *testing.T) {
	t.Parallel()

	power := electricalSensor(t, hmenum.ParameterPower)
	voltage := electricalSensor(t, hmenum.ParameterVoltage)
	energy := electricalSensor(t, hmenum.ParameterEnergyCounter)

	g := NewElectricalGroup(power, voltage, energy)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil")
	}

	var fired int
	unsub := g.OnMatterValueChanged(func() { fired++ })
	if unsub == nil {
		t.Fatal("OnMatterValueChanged returned a nil unsubscribe")
	}

	power.OnEvent(100)
	voltage.OnEvent(230)
	energy.OnEvent(5)
	if fired < 3 {
		t.Errorf("callback fired %d times for three member updates; a member is not subscribed", fired)
	}

	// Unsubscribing must reach every member, not just the first.
	unsub()
	before := fired
	power.OnEvent(200)
	voltage.OnEvent(231)
	energy.OnEvent(6)
	if fired != before {
		t.Errorf("callback fired %d more times after unsubscribe; a member's subscription leaked",
			fired-before)
	}
}

// TestElectricalGroupNilCallbackIsIgnored covers the defensive branch: a nil
// callback must not reach a member's notifier.
func TestElectricalGroupNilCallbackIsIgnored(t *testing.T) {
	t.Parallel()

	power := electricalSensor(t, hmenum.ParameterPower)
	g := NewElectricalGroup(power)
	if g == nil {
		t.Fatal("NewElectricalGroup returned nil")
	}
	unsub := g.OnMatterValueChanged(nil)
	if unsub == nil {
		t.Fatal("OnMatterValueChanged(nil) returned a nil unsubscribe")
	}
	unsub()
	power.OnEvent(1) // must not panic
}
