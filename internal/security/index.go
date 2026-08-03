// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// RebuildIndex derives the classification index from the device model,
// the alarm enrollment and the operator overrides.
//
// It is built once per model change rather than consulted per event: a
// value-change event carries only the data-point key and the value, not
// the device model, the channel type or the value list the classifier
// needs. Resolving those on every event would mean three registry
// lookups per wire message across the whole fleet.
func (s *Service) RebuildIndex(ctx context.Context) error {
	overrides, err := s.loadOverrides(ctx)
	if err != nil {
		return err
	}
	enrolled, alarmDevices, err := s.loadEnrollment(ctx)
	if err != nil {
		return err
	}

	index := map[string]*indexedSource{}
	for _, u := range s.reg.List() {
		s.indexUnit(u, overrides, enrolled, alarmDevices, index)
	}

	s.mu.Lock()
	// Activation state survives a rebuild for sources that still exist;
	// dropping it would make every active detector look like it cleared
	// the moment an operator saved an unrelated override.
	for key := range s.agg.active {
		if _, ok := index[key]; !ok {
			delete(s.agg.active, key)
		}
	}
	s.agg.sources = index
	s.mu.Unlock()
	return nil
}

// indexUnit classifies every data point of one central.
func (s *Service) indexUnit(u *central.Unit, overrides map[string]sqlitestore.SecuritySource,
	enrolled map[string]sqlitestore.AlarmSensorRow, alarmDevices map[string]bool,
	out map[string]*indexedSource,
) {
	centralName := u.Name()
	for _, d := range u.QueryFacade().ModelDevices() {
		deviceRelevant := alarmDevices[centralName+"|"+d.Address]
		for _, ch := range d.Channels() {
			for _, dp := range ch.DataPoints() {
				param := dp.Parameter()
				key := hmevent.SecurityRefKey(centralName, dp.DataPointKey().InterfaceID, ch.Address, string(param))

				src, ok := s.classify(centralName, d.Model, ch.Type, ch.Address,
					dp.DataPointKey().InterfaceID, param, overrides[key], enrolled[key], deviceRelevant)
				if !ok {
					continue
				}
				src.ref.Name = channelDisplayName(ch, d.Name)
				out[key] = &src
			}
		}
	}
}

// classify decides what one data point means for the domain.
//
// Precedence is: operator override, then alarm enrollment, then the
// classifier table. An override wins outright — the operator has seen
// the device, the classifier has only seen its model string.
func (s *Service) classify(centralName, model, channelType, channelAddress, interfaceID string,
	param hmenum.Parameter, override sqlitestore.SecuritySource,
	enrollment sqlitestore.AlarmSensorRow, deviceRelevant bool,
) (indexedSource, bool) {
	// An excluded parameter is never indexed, whatever anyone
	// configured: it is something the alarm engine writes, and reading
	// it back would let the domain report its own output as a cause.
	if safety.Excluded(param) {
		return indexedSource{}, false
	}
	if override.Parameter != "" && !override.Included {
		return indexedSource{}, false
	}

	ref := hmevent.NewSecuritySourceRef(centralName, interfaceID, channelAddress, string(param))
	src := indexedSource{ref: ref, zoneID: enrollment.ZoneID}

	switch {
	case override.Class != "":
		src.class = hmenum.SecurityClass(override.Class)
		src.relevant = true
	case enrollment.ID != "":
		if cls, ok := hmenum.SecurityClassForSensorType(enrollment.SensorType); ok {
			src.class = cls
		} else if cls, ok := safety.Classify(model, channelType, param); ok {
			src.class = cls.Class
			src.reason = cls.Reason
			src.activeValues = cls.ActiveValues
		} else {
			// An enrolled hazard sensor the classifier cannot place
			// still belongs to the domain; it just has no class yet.
			src.class = hmenum.SecurityClassIntrusion
		}
		src.relevant = true
	default:
		cls, ok := safety.Classify(model, channelType, param)
		if !ok {
			return indexedSource{}, false
		}
		src.class = cls.Class
		src.reason = cls.Reason
		src.activeValues = cls.ActiveValues
		// Hazard classes are always relevant — a smoke detector matters
		// whether or not anyone enrolled it. The diagnostic classes are
		// gated on the device carrying an alarm role, so `problem` stays
		// a real signal instead of standing permanently on across a
		// whole fleet.
		src.relevant = cls.Class.Hazard() || deviceRelevant
	}
	src.ref.Class = src.class
	if !src.class.Valid() {
		return indexedSource{}, false
	}
	return src, src.relevant
}

// loadOverrides indexes the operator decisions by routing key.
func (s *Service) loadOverrides(ctx context.Context) (map[string]sqlitestore.SecuritySource, error) {
	rows, err := s.stores.Sources.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sqlitestore.SecuritySource, len(rows))
	for i := range rows {
		r := rows[i]
		out[hmevent.SecurityRefKey(r.CentralName, r.InterfaceID, r.ChannelAddress, r.Parameter)] = r
	}
	return out, nil
}

// loadEnrollment indexes the alarm sensors by routing key, and the
// devices that carry at least one by device key. The second half is
// what makes the diagnostic classes selective.
func (s *Service) loadEnrollment(ctx context.Context) (byRef map[string]sqlitestore.AlarmSensorRow, alarmDevices map[string]bool, err error) {
	if s.stores.Sensors == nil {
		return map[string]sqlitestore.AlarmSensorRow{}, map[string]bool{}, nil
	}
	rows, err := s.stores.Sensors.GetAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	byRef = make(map[string]sqlitestore.AlarmSensorRow, len(rows))
	alarmDevices = map[string]bool{}
	for i := range rows {
		r := rows[i]
		byRef[hmevent.SecurityRefKey(r.CentralName, r.InterfaceID, r.ChannelAddress, r.Parameter)] = r
		alarmDevices[r.CentralName+"|"+hmevent.SecurityDeviceAddress(r.ChannelAddress)] = true
	}
	return byRef, alarmDevices, nil
}

// channelDisplayName picks the most useful name for a source, falling
// back through channel name, device name, address.
func channelDisplayName(ch *device.Channel, deviceName string) string {
	if n := ch.NameData().ChannelName; n != "" {
		return n
	}
	if deviceName != "" {
		return deviceName
	}
	return ch.Address
}
