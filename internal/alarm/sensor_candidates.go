// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SensorCandidate is one data point a zone can enrol as an alarm
// sensor, with everything a picker needs to make the choice and to
// pre-fill the enrollment correctly.
//
// This closes a real gap: the alarm surface has always offered output
// and remote-key candidate lists, but never a sensor list — sensor
// enrollment was unvalidated free text over (central, interface,
// channel address, parameter). A typo produced a sensor that silently
// never fired.
type SensorCandidate struct {
	Central        string
	InterfaceID    string
	DeviceAddress  string
	DeviceName     string
	Model          string
	ChannelAddress string
	ChannelNo      int
	ChannelName    string
	ChannelType    string
	Parameter      string
	// Rooms / Functions carry the channel's CCU assignments so a picker
	// can group without a second lookup.
	Rooms     []string
	Functions []string
	// SensorType is the alarm role this candidate suggests.
	SensorType hmenum.AlarmSensorType
	// SecurityClass is the hazard/fault class, empty when the data
	// point carries no security classification.
	SecurityClass hmenum.SecurityClass
	// ValueList is the parameter's enumeration vocabulary, empty for a
	// boolean parameter. A picker offers these as the active-value
	// choices.
	ValueList []string
	// ActiveValues is the recommended active-value selection. It is set
	// only where the default rule would be wrong — most importantly for
	// the smoke-detector status, whose value list contains the alarm
	// system's own intrusion-siren command.
	ActiveValues []string
	// Recommended marks the data point a zone should enrol when a
	// device offers several for the same purpose.
	Recommended bool
	// Deprioritised marks a data point that works but has a better
	// sibling — the raw enumeration status where a derived boolean
	// exists. Reason names why.
	Deprioritised bool
	Reason        string
	// Enrolled reports whether this data point is already enrolled, and
	// ZoneID names the zone that holds it.
	Enrolled bool
	ZoneID   string
}

// sensorTypeByChannelType suggests the alarm role for a channel type.
// The mapping is deliberately small: it covers the channel types whose
// role is unambiguous, and leaves everything else to the operator.
//
// Every name below is a channel TYPE the CCU declares, and each one's
// detection-state parameter is the one the intrusion branch accepts
// below (STATE / MOTION / PRESENCE_DETECTION_STATE), read off the
// devices' own device descriptions and paramset descriptions.
//
// Two boundaries a reader should know. The three hazard rows are
// behaviour-neutral: sensorCandidateFor consults this map only inside
// the safety.Classify branch, whose fallback is already
// AlarmSensorTypeHazard, and the intrusion branch below skips
// role==Hazard. They are kept because they state the role of those
// channel types, not because removing them would change an outcome.
// And the table is narrower than the firmware: DOOR_STATE_TRANSCEIVER
// (STATE={CLOSED,OPEN} on HmIP-DLP), ACCELERATION_TRANSCEIVER (MOTION on
// HmIP-SAM and HmIP-STV) and the BidCos SMOKE_DETECTOR_TEAM channels all
// declare a detection state and are absent here, so they never become
// candidates. Whether they should is a product decision, not a firmware
// one — widening the table widens what the picker offers.
var sensorTypeByChannelType = map[string]hmenum.AlarmSensorType{
	"SHUTTER_CONTACT":                    hmenum.AlarmSensorTypeWindow,
	"TILT_SENSOR":                        hmenum.AlarmSensorTypeWindow,
	"ROTARY_HANDLE_SENSOR":               hmenum.AlarmSensorTypeWindow,
	"ROTARY_HANDLE_TRANSCEIVER":          hmenum.AlarmSensorTypeWindow,
	"MOTIONDETECTOR_TRANSCEIVER":         hmenum.AlarmSensorTypeMotion,
	"MOTIONDETECTOR_VIRTUAL_TRANSCEIVER": hmenum.AlarmSensorTypeMotion,
	"MOTION_DETECTOR":                    hmenum.AlarmSensorTypeMotion,
	"PRESENCEDETECTOR_TRANSCEIVER":       hmenum.AlarmSensorTypeMotion,
	"SMOKE_DETECTOR":                     hmenum.AlarmSensorTypeHazard,
	"WATER_DETECTION_TRANSMITTER":        hmenum.AlarmSensorTypeHazard,
	"WATERDETECTIONSENSOR":               hmenum.AlarmSensorTypeHazard,
}

// SensorCandidates enumerates the enrollable alarm-sensor data points
// across every central.
//
// A candidate qualifies when the security classifier recognises it as a
// hazard source, or when its channel type maps onto an intrusion role.
// Everything else is excluded — an alarm sensor list that offers all
// 3600 parameters of a fleet is not a picker, it is a haystack.
func (s *Service) SensorCandidates(ctx context.Context) []SensorCandidate {
	enrolled := s.enrolledByRef(ctx)
	var out []SensorCandidate
	for _, u := range s.reg.List() {
		centralName := u.Name()
		for _, d := range u.QueryFacade().ModelDevices() {
			for _, ch := range d.Channels() {
				for _, dp := range ch.DataPoints() {
					cand, ok := sensorCandidateFor(d.Model, ch.Type, dp.Parameter())
					if !ok {
						continue
					}
					cand.Central = centralName
					cand.InterfaceID = dp.DataPointKey().InterfaceID
					cand.DeviceAddress = d.Address
					cand.DeviceName = d.Name()
					cand.Model = d.Model
					cand.ChannelAddress = ch.Address
					cand.ChannelNo = ch.Number
					cand.ChannelName = ch.NameData().ChannelName
					cand.ChannelType = ch.Type
					cand.Rooms = ch.Rooms()
					cand.Functions = ch.Functions()
					cand.ValueList = append([]string(nil), dp.ParameterData().ValueList...)
					if zoneID, ok := enrolled[dpKey(centralName, cand.InterfaceID, ch.Address, cand.Parameter)]; ok {
						cand.Enrolled = true
						cand.ZoneID = zoneID
					}
					out = append(out, cand)
				}
			}
		}
	}
	sortSensorCandidates(out)
	return out
}

// sensorCandidateFor decides whether one data point is enrollable and
// how it should be pre-filled.
func sensorCandidateFor(model, channelType string, parameter hmenum.Parameter) (SensorCandidate, bool) {
	cand := SensorCandidate{Parameter: string(parameter)}

	// A parameter the alarm engine itself writes must never be
	// offered: enrolling it would let the system read its own output
	// back as a detection.
	if safety.Excluded(parameter) {
		return SensorCandidate{}, false
	}

	if cls, ok := safety.Classify(model, channelType, parameter); ok && cls.Class.Hazard() {
		cand.SecurityClass = cls.Class
		cand.ActiveValues = append([]string(nil), cls.ActiveValues...)
		cand.Recommended = cls.Preferred
		if role, ok := sensorTypeByChannelType[channelType]; ok {
			cand.SensorType = role
		} else {
			cand.SensorType = hmenum.AlarmSensorTypeHazard
		}
		if len(cls.ActiveValues) > 0 && !cls.Preferred {
			cand.Deprioritised = true
			cand.Reason = "raw status enumeration; prefer the derived boolean of the same device"
		}
		return cand, true
	}

	// Intrusion sensors are recognised by channel type: their detection
	// state carries no security classification of its own, so nothing
	// upstream of this branch marks them.
	//
	// It is NOT a plain boolean. The HmIP firmware builds these states as
	// enumerations — HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory
	// #createStateWindowOpenClosed over {CLOSED, OPEN} and
	// #createStateWindowOpenTiltedClosed over {CLOSED, TILTED, OPEN} — and
	// the descriptors agree: SHUTTER_CONTACT STATE is an ENUM on the five
	// HmIP contacts and a BOOL only on the eight classic ones, while
	// ROTARY_HANDLE_SENSOR and ROTARY_HANDLE_TRANSCEIVER STATE is
	// {CLOSED, TILTED, OPEN} everywhere. That is why the candidate carries
	// the declared ValueList out to the picker: without it an operator
	// cannot narrow a window handle to OPEN alone, and every non-CLOSED
	// index — TILTED included — counts as an activation by default
	// (safety.ActiveFromRaw).
	if role, ok := sensorTypeByChannelType[channelType]; ok && role != hmenum.AlarmSensorTypeHazard {
		if parameter != hmenum.ParameterState && parameter != hmenum.ParameterMotion &&
			parameter != hmenum.ParameterPresenceDetectionState {
			return SensorCandidate{}, false
		}
		cand.SensorType = role
		cand.SecurityClass = hmenum.SecurityClassIntrusion
		cand.Recommended = true
		return cand, true
	}
	return SensorCandidate{}, false
}

// enrolledByRef maps the routing key of every enrolled sensor onto its
// zone, so a picker can show what is already taken.
//
// It reads the store rather than the in-memory routing index: the index
// holds no zone, and reaching into the engine while holding the service
// lock would invert the established lock order.
func (s *Service) enrolledByRef(ctx context.Context) map[string]string {
	rows, err := s.stores.Sensors.GetAll(ctx)
	if err != nil {
		s.log.Error("alarm sensor candidates: load enrolled sensors", "error", err)
		return nil
	}
	out := make(map[string]string, len(rows))
	for i := range rows {
		row := &rows[i]
		out[dpKey(row.CentralName, row.InterfaceID, row.ChannelAddress, row.Parameter)] = row.ZoneID
	}
	return out
}

// sortSensorCandidates orders by central, device, channel, parameter —
// stable and human-scannable.
func sortSensorCandidates(out []SensorCandidate) {
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].Central != out[j].Central:
			return out[i].Central < out[j].Central
		case out[i].DeviceAddress != out[j].DeviceAddress:
			return out[i].DeviceAddress < out[j].DeviceAddress
		case out[i].ChannelNo != out[j].ChannelNo:
			return out[i].ChannelNo < out[j].ChannelNo
		default:
			return out[i].Parameter < out[j].Parameter
		}
	})
}
