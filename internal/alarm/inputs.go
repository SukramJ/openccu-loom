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
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
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
	dpIndex := map[string]sensorBinding{}
	devIndex := map[string][]string{}
	for i := range rows {
		row := &rows[i]
		// A malformed config must not drop the sensor from routing: it
		// keeps the historical activation rule and stays armed.
		cfg, err := engine.ParseSensorConfig(row.ConfigJSON)
		if err != nil {
			s.log.Error("alarm sensor config unparsable; falling back to the default activation rule",
				"sensor", row.ID, "error", err)
		}
		dpIndex[dpKey(row.CentralName, row.InterfaceID, row.ChannelAddress, row.Parameter)] = sensorBinding{
			id:             row.ID,
			rule:           activationRule{labels: cfg.ActiveValues},
			centralName:    row.CentralName,
			interfaceID:    row.InterfaceID,
			channelAddress: row.ChannelAddress,
			parameter:      row.Parameter,
		}
		dev := devKey(row.CentralName, deviceAddress(row.ChannelAddress))
		devIndex[dev] = append(devIndex[dev], row.ID)
	}
	s.mu.Lock()
	s.dpIndex = dpIndex
	s.devIndex = devIndex
	s.mu.Unlock()
	// A re-enrolled sensor must not keep a stale value list.
	s.enums.reset()
	s.warnContradictingEnrollments(rows)
	return nil
}

// attachUnit subscribes the service to one central's event bus. Bus
// handlers run detached from any request context by design.
//
//nolint:contextcheck // bus dispatch has no caller ctx; handlers run on the engine lifetime
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
		// Device lifecycle: a device that leaves the model sends no
		// UNREACH — it sends nothing ever again — so nothing else in this
		// domain would ever notice that an enrolled sensor stopped
		// existing.
		events.Subscribe(u.EventBus, func(e hmevent.DeviceRemovedEvent) {
			s.onDeviceLifecycle(name, e.Address)
		}),
		events.Subscribe(u.EventBus, func(e hmevent.DeviceCreatedEvent) {
			s.onDeviceLifecycle(name, e.Address)
		}),
		// The southbound bring-up is readiness-gated, so the device
		// model arrives long after the alarm service starts — on a
		// co-booting CCU, tens of seconds after. The reconcile pass at
		// Start therefore asks an empty registry and finds no sounding
		// siren to adopt or stop (S4). This is the second look, taken
		// when the model is actually there.
		//
		// No seed from Unit.IsSouthboundReady is needed for the
		// already-ready case: both callers of attachUnit reconcile
		// immediately afterwards, which is what covers a central whose
		// event fired before this subscription existed.
		events.Subscribe(u.EventBus, func(_ hmevent.CentralSouthboundReadyEvent) {
			ctx := context.Background()
			s.reconcile(ctx)
			s.engine.ReevaluateSensors(ctx)
			// The model is complete now, so "this channel is not in it"
			// finally means something: a device deleted while the daemon
			// was down produced no removal event anybody heard.
			s.refreshSensorPresence(ctx)
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
	binding, isSensorDP := s.dpIndex[dpKey(centralName, e.Key.InterfaceID, e.Key.ChannelAddress, e.Key.Parameter)]
	devSensors := append([]string(nil), s.devIndex[devKey(centralName, deviceAddress(e.Key.ChannelAddress))]...)
	s.mu.Unlock()

	// Keypad/remote intent routing sees every data point unconditionally:
	// CODE_ID/CODE_STATE and the press edges arrive on channels that are
	// not themselves enrolled sensors.
	s.intents.onEvent(ctx, centralName, e)

	if isSensorDP {
		if active, known := s.active(binding, e.NewValue); known {
			s.engine.HandleSensorEvent(ctx, binding.id, active)
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
	case hmenum.ParameterBlockedTemporary, hmenum.ParameterBlockedPermanent:
		// Keypad lockout after repeated wrong on-device codes. The device
		// self-locks; loom surfaces the onset as a fault so the operator
		// sees the tamper-adjacent signal (notes/concepts/alarm-concept.md §11).
		if blocked, ok := paramValueBool(e.NewValue); ok && blocked {
			s.journalDeviceBlocked(ctx, centralName, e.Key)
		}
	default:
		// Every other parameter is either an enrolled sensor value
		// (handled above) or irrelevant to the alarm engine.
	}
}

// onDeviceLifecycle re-derives the availability of every sensor enrolled
// on a device whose presence in the model just changed.
//
// A device that is unpaired, deleted, or re-paired under a new address
// emits no UNREACH — it emits nothing at all, ever again — so the
// enrolled sensors kept available=true and their last activation value
// for the lifetime of the daemon. The zone then reported ready-to-arm
// and armed with a window that can never fire: the default
// unreachable=block policy defeated by the one case it exists for, with
// no blocker, no journal entry and no health signal anywhere.
func (s *Service) onDeviceLifecycle(centralName, address string) {
	s.mu.Lock()
	sensors := append([]string(nil), s.devIndex[devKey(centralName, address)]...)
	s.mu.Unlock()
	if len(sensors) == 0 {
		return
	}
	available := s.deviceReachable(centralName, address)
	ctx := context.Background()
	for _, id := range sensors {
		s.engine.SetSensorAvailability(ctx, id, available)
	}
}

// refreshSensorPresence re-derives availability for every enrolled
// sensor of every central whose model is complete.
//
// It is the boot-side half of [Service.onDeviceLifecycle]: a device
// removed while the daemon was down publishes no event anyone hears, and
// the restore marks every sensor available. Centrals that are not
// southbound-ready yet are skipped — before the bring-up has loaded the
// devices, "absent from the model" means "not loaded", and reading it as
// "gone" would degrade every sensor of a CCU that is merely still
// booting.
func (s *Service) refreshSensorPresence(ctx context.Context) {
	type target struct {
		central, address string
		sensors          []string
	}
	s.mu.Lock()
	targets := make([]target, 0, len(s.devIndex))
	for key, ids := range s.devIndex {
		centralName, address, ok := splitDevKey(key)
		if !ok {
			continue
		}
		targets = append(targets, target{
			central: centralName, address: address,
			sensors: append([]string(nil), ids...),
		})
	}
	s.mu.Unlock()

	ready := map[string]bool{}
	for _, t := range targets {
		isReady, seen := ready[t.central]
		if !seen {
			u, ok := s.reg.Get(t.central)
			isReady = ok && u.IsSouthboundReady()
			ready[t.central] = isReady
		}
		if !isReady {
			continue
		}
		available := s.deviceReachable(t.central, t.address)
		for _, id := range t.sensors {
			s.engine.SetSensorAvailability(ctx, id, available)
		}
	}
}

// deviceReachable reports the model's view of one device: present in the
// central's model and not reporting UNREACH. That is the same truth the
// UNREACH data-point path carries, read from the model instead of from an
// event, so the two cannot contradict each other — and a device that is
// no longer in the model reads unreachable rather than "unchanged".
func (s *Service) deviceReachable(centralName, address string) bool {
	u, ok := s.reg.Get(centralName)
	if !ok || u.ModelRegistry == nil {
		return false
	}
	dev, ok := u.ModelRegistry.Get(address)
	if !ok || dev == nil {
		return false
	}
	return dev.Availability().IsReachable()
}

// journalDeviceBlocked records a keypad temporary/permanent lockout as a
// fault-class journal entry (fail-visible, S7). The lockout is a
// device-level signal, so the entry carries the device address rather
// than an zone — clearing it stays operator/WebUI-owned per Q4.
func (s *Service) journalDeviceBlocked(ctx context.Context, centralName string, key hmtypes.DataPointKey) {
	if _, err := s.journal.Append(ctx, engine.JournalEntry{
		Class:  hmenum.AlarmJournalClassFault,
		Event:  "keypad_blocked",
		Source: "keypad",
		Details: map[string]any{
			"central":   centralName,
			"device":    deviceAddress(key.ChannelAddress),
			"parameter": key.Parameter,
		},
	}); err != nil {
		s.log.Error("alarm keypad-blocked journal append failed", "device", deviceAddress(key.ChannelAddress), "error", err)
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
//
// ConnectivityChangedEvent.InterfaceID already carries the wire id the
// ingest pipeline stamps onto a data point ("<central>-BidCos-RF"): the
// reconciler publishes what observeProbeLatency stamped, never the bare
// enum. It is the same space every routing entry in this package is keyed
// by, so it is used directly. Re-wrapping it in WireInterfaceID produced a
// doubled id ("<central>-<central>-BidCos-RF") that matched no enrolled
// sensor, which left every contact on a lost radio reporting its last known
// state while armed and never ran the zone's central-loss policy.
//
// An unconfirmed reachability is not an input here. It is the daemon's own
// view of a central it cannot reach at all, so it says nothing about the
// radios behind that CCU — deriving down-events from an unreachable CCU is
// exactly what the reconciler refuses to do for its vanished-interface
// pass. Acting on it turns every CCU reboot into the zone's central-loss
// escalation: a fault in each armed zone's journal on the default policy,
// and a sounding siren plus an incident on `trigger`. The diagnostic planes
// still receive the event and still show the interfaces as down.
func (s *Service) onConnectivity(centralName string, e hmevent.ConnectivityChangedEvent) {
	if e.Unconfirmed {
		return
	}
	ctx := context.Background()
	wireID := e.InterfaceID
	s.mu.Lock()
	m, ok := s.ifaceDown[centralName]
	if !ok {
		m = map[string]bool{}
		s.ifaceDown[centralName] = m
	}
	wasAllDown := s.allEnrolledDownLocked(centralName)
	m[wireID] = !e.Reachable
	nowAllDown := s.allEnrolledDownLocked(centralName)
	// Split the central's sensors into the ones this event speaks for
	// and the ones sitting on an interface that is currently down.
	var affected, onDownInterface []string
	for _, b := range s.dpIndex {
		if b.centralName != centralName {
			continue
		}
		switch {
		case b.interfaceID == wireID:
			affected = append(affected, b.id)
		case m[b.interfaceID]:
			onDownInterface = append(onDownInterface, b.id)
		}
	}
	s.mu.Unlock()

	if nowAllDown != wasAllDown {
		// Whole-central transition: the engine applies the zone
		// central-loss policy (alert or trigger) — never silently.
		s.engine.HandleCentralConnectivity(ctx, centralName, !nowAllDown)
		if !nowAllDown {
			// That restore is central-scoped: it marks every sensor of
			// the central available again, although only the interface
			// this event names came back. Re-assert the per-interface
			// truth, which lives here and not in the engine — otherwise
			// the sensors of a radio that is still gone report
			// available forever, since nothing re-drifts them and the
			// zone would read ready while it is blind on that radio.
			for _, id := range onDownInterface {
				s.engine.SetSensorAvailability(ctx, id, false)
			}
			// Reconnect ends a blind window (§10.1): adopt or stop
			// sounding sirens (S4) and re-evaluate sensor values — a
			// window opened during the gap must surface now, not at
			// the next daemon restart.
			s.reconcile(ctx)
			s.engine.ReevaluateSensors(ctx)
		}
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

// dpKey builds the routing key of a sensor data point. interfaceID is
// the wire id ("<central>-BidCos-RF"), the form an enrolled sensor row
// and an inbound data-point event both carry.
func dpKey(centralName, interfaceID, channelAddress, parameter string) string {
	return dpKeyPrefix(centralName, interfaceID) + channelAddress + "|" + parameter
}

// dpKeyPrefix builds the (central, interface) prefix shared by every
// routing key of one interface.
func dpKeyPrefix(centralName, interfaceID string) string {
	return centralName + "|" + interfaceID + "|"
}

// devKey builds the routing key of a device.
func devKey(centralName, deviceAddr string) string {
	return centralName + "|" + deviceAddr
}

// splitDevKey is the inverse of [devKey].
func splitDevKey(key string) (centralName, deviceAddr string, ok bool) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
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

// warnContradictingEnrollments logs enrollments the security
// classifier disagrees with. It only reports; it never rewrites an
// operator's choice, because a classifier is a heuristic and an
// enrollment is a decision.
//
// The case worth surfacing is the raw smoke-detector status enrolled
// without active values: it works, but its value list contains the
// alarm system's own intrusion-siren command, so the default rule
// treats that command as a smoke detection.
func (s *Service) warnContradictingEnrollments(rows []sqlitestore.AlarmSensorRow) {
	for i := range rows {
		row := &rows[i]
		cfg, err := engine.ParseSensorConfig(row.ConfigJSON)
		if err != nil {
			continue
		}
		param := hmenum.Parameter(row.Parameter)
		if safety.Excluded(param) {
			s.log.Warn("alarm sensor enrolled on an actuator-feedback parameter: the engine writes this value, so the alarm can retrigger itself",
				"sensor", row.ID, "zone", row.ZoneID,
				"channel", row.ChannelAddress, "parameter", row.Parameter)
			continue
		}
		cls, ok := safety.Classify("", s.channelTypeOf(row), param)
		if !ok {
			continue
		}
		if len(cls.ActiveValues) > 0 && len(cfg.ActiveValues) == 0 {
			s.log.Warn("alarm sensor has no active_values on an enumerated parameter: every value but the first counts as an activation",
				"sensor", row.ID, "zone", row.ZoneID,
				"channel", row.ChannelAddress, "parameter", row.Parameter,
				"recommended", cls.ActiveValues)
		}
		if row.SensorType == hmenum.AlarmSensorTypeHazard && !cfg.AlwaysOn {
			s.log.Warn("hazard sensor is not always_on: it only fires while the zone is armed in one of its listed modes",
				"sensor", row.ID, "zone", row.ZoneID, "parameter", row.Parameter)
		}
	}
}

// channelTypeOf resolves the channel type of an enrolled sensor from
// the model, returning "" when the central or channel is unavailable.
func (s *Service) channelTypeOf(row *sqlitestore.AlarmSensorRow) string {
	if s.reg == nil {
		return ""
	}
	u, ok := s.reg.Get(row.CentralName)
	if !ok {
		return ""
	}
	ch := u.GetChannel(row.ChannelAddress)
	if ch == nil {
		return ""
	}
	return ch.Type
}
