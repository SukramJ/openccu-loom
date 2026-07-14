// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// rebuildIndexes derives the event-routing indexes from the enrolled
// sensor rows: data-point identity → sensor ID, and device address →
// sensor IDs (device-level health parameters affect every sensor of
// the device).
func (s *Service) rebuildIndexes(ctx context.Context) error {
	rows, err := s.stores.Sensors.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("load sensors: %w", err)
	}
	dpIndex := map[string]string{}
	devIndex := map[string][]string{}
	for _, row := range rows {
		dpIndex[dpKey(row.CentralName, row.InterfaceID, row.ChannelAddress, row.Parameter)] = row.ID
		dev := devKey(row.CentralName, deviceAddress(row.ChannelAddress))
		devIndex[dev] = append(devIndex[dev], row.ID)
	}
	s.mu.Lock()
	s.dpIndex = dpIndex
	s.devIndex = devIndex
	s.mu.Unlock()
	return nil
}

// attachUnit subscribes the service to one central's event bus.
func (s *Service) attachUnit(u *central.Unit) {
	if u == nil || u.EventBus == nil {
		return
	}
	name := u.Name()
	unsubs := []func(){
		events.Subscribe(u.EventBus, func(e hmevent.DataPointValueChangedEvent) {
			s.onDataPoint(name, e)
		}),
		events.Subscribe(u.EventBus, func(e hmevent.ConnectivityChangedEvent) {
			s.onConnectivity(name, e)
		}),
		events.Subscribe(u.EventBus, func(e hmevent.SysvarChangedEvent) {
			s.sysvarMirror.onInbound(name, e)
		}),
	}
	s.mu.Lock()
	s.unsubs[name] = append(s.unsubs[name], unsubs...)
	s.mu.Unlock()
}

// onDataPoint routes a wire value change: enrolled sensor data points
// feed the state machine; device-level health parameters feed
// availability and health flags of every sensor on the device.
func (s *Service) onDataPoint(centralName string, e hmevent.DataPointValueChangedEvent) {
	ctx := context.Background()
	s.mu.Lock()
	sensorID, isSensorDP := s.dpIndex[dpKey(centralName, e.Key.InterfaceID, e.Key.ChannelAddress, e.Key.Parameter)]
	devSensors := append([]string(nil), s.devIndex[devKey(centralName, deviceAddress(e.Key.ChannelAddress))]...)
	s.mu.Unlock()

	if isSensorDP {
		if active, known := paramValueActive(e.NewValue); known {
			s.engine.HandleSensorEvent(ctx, sensorID, active)
		}
	}
	if len(devSensors) == 0 {
		return
	}
	switch hmenum.Parameter(e.Key.Parameter) {
	case hmenum.ParameterUnreach, hmenum.ParameterStickyUnreach:
		if unreach, ok := paramValueBool(e.NewValue); ok {
			for _, id := range devSensors {
				s.engine.SetSensorAvailability(ctx, id, !unreach)
			}
		}
	case hmenum.ParameterSabotage:
		if sab, ok := paramValueBool(e.NewValue); ok {
			s.updateDeviceHealth(ctx, centralName, e.Key, devSensors, func(h *engine.SensorHealth) { h.Sabotage = sab })
		}
	case hmenum.ParameterLowBat, hmenum.ParameterLowbat:
		if low, ok := paramValueBool(e.NewValue); ok {
			s.updateDeviceHealth(ctx, centralName, e.Key, devSensors, func(h *engine.SensorHealth) { h.LowBattery = low })
		}
	}
}

// updateDeviceHealth merges one health flag into the cached per-device
// state and pushes the full flag set to every sensor of the device.
func (s *Service) updateDeviceHealth(ctx context.Context, centralName string, key hmtypes.DataPointKey, sensorIDs []string, apply func(*engine.SensorHealth)) {
	dev := devKey(centralName, deviceAddress(key.ChannelAddress))
	s.mu.Lock()
	h := s.devHealth[dev]
	apply(&h)
	s.devHealth[dev] = h
	s.mu.Unlock()
	for _, id := range sensorIDs {
		s.engine.SetSensorHealth(ctx, id, h)
	}
}

// onConnectivity degrades and restores sensors per interface, and
// escalates to the central-loss policy when every enrolled interface
// of a central is gone.
func (s *Service) onConnectivity(centralName string, e hmevent.ConnectivityChangedEvent) {
	ctx := context.Background()
	s.mu.Lock()
	m, ok := s.ifaceDown[centralName]
	if !ok {
		m = map[string]bool{}
		s.ifaceDown[centralName] = m
	}
	wasAllDown := s.allEnrolledDownLocked(centralName)
	m[e.InterfaceID] = !e.Reachable
	nowAllDown := s.allEnrolledDownLocked(centralName)
	// Collect the sensors of the affected interface.
	var affected []string
	for key, id := range s.dpIndex {
		if strings.HasPrefix(key, centralName+"|"+e.InterfaceID+"|") {
			affected = append(affected, id)
		}
	}
	s.mu.Unlock()

	if nowAllDown != wasAllDown {
		// Whole-central transition: the engine applies the area
		// central-loss policy (alert or trigger) — never silently.
		s.engine.HandleCentralConnectivity(ctx, centralName, !nowAllDown)
		return
	}
	for _, id := range affected {
		s.engine.SetSensorAvailability(ctx, id, e.Reachable)
	}
}

// allEnrolledDownLocked reports whether every interface that carries
// enrolled sensors of the central is currently unreachable. The
// caller holds the lock.
func (s *Service) allEnrolledDownLocked(centralName string) bool {
	enrolled := map[string]bool{}
	for key := range s.dpIndex {
		parts := strings.SplitN(key, "|", 3)
		if len(parts) >= 2 && parts[0] == centralName {
			enrolled[parts[1]] = true
		}
	}
	if len(enrolled) == 0 {
		return false
	}
	down := s.ifaceDown[centralName]
	for iface := range enrolled {
		if !down[iface] {
			return false
		}
	}
	return true
}

// dpKey builds the routing key of a sensor data point.
func dpKey(centralName, interfaceID, channelAddress, parameter string) string {
	return centralName + "|" + interfaceID + "|" + channelAddress + "|" + parameter
}

// devKey builds the routing key of a device.
func devKey(centralName, deviceAddr string) string {
	return centralName + "|" + deviceAddr
}

// deviceAddress strips the channel suffix of a channel address.
func deviceAddress(channelAddress string) string {
	if i := strings.IndexByte(channelAddress, ':'); i >= 0 {
		return channelAddress[:i]
	}
	return channelAddress
}

// paramValueActive normalizes a wire value onto binary activation
// semantics (bool direct; integer enums like rotary-handle positions
// activate on non-zero).
func paramValueActive(v hmtypes.ParamValue) (active, known bool) {
	switch v.Kind {
	case hmtypes.ValueKindBool:
		return v.Bool, true
	case hmtypes.ValueKindInt:
		return v.Int != 0, true
	case hmtypes.ValueKindFloat:
		return v.Float != 0, true
	default:
		return false, false
	}
}

// paramValueBool extracts a strict boolean.
func paramValueBool(v hmtypes.ParamValue) (val, ok bool) {
	if v.Kind == hmtypes.ValueKindBool {
		return v.Bool, true
	}
	return false, false
}
