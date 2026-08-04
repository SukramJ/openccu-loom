// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// subscribeAlarm wires the alarm domain's events.
//
// The subscription set is an explicit allow-list rather than a
// catch-all. That is deliberate: a covert-trigger event must never
// reach a surface by accident, and a pattern subscription would make
// "what does this domain observe" a question you can only answer by
// running it.
func (s *Service) subscribeAlarm() {
	if s.alarmBus == nil {
		// No alarm engine: the domain runs without the intrusion and
		// zone halves. The hazard classes and the fault plane, which
		// are the parts that work without an alarm system, are
		// unaffected.
		return
	}
	unsubs := []func(){
		events.Subscribe(s.alarmBus, s.onAlarmTriggered),
		events.Subscribe(s.alarmBus, s.onAlarmStateChanged),
		events.Subscribe(s.alarmBus, s.onAlarmHealthChanged),
		events.Subscribe(s.alarmBus, s.onAlarmDuress),
	}
	s.mu.Lock()
	s.unsubs = append(s.unsubs, unsubs...)
	s.mu.Unlock()
}

// attachUnit subscribes one central's value stream. Handlers run
// detached from any caller context by design.
//
//nolint:contextcheck // bus dispatch has no caller ctx; handlers run on the service lifetime
func (s *Service) attachUnit(u *central.Unit) {
	if u == nil || u.EventBus == nil {
		return
	}
	name := u.Name()
	unsubs := []func(){
		events.Subscribe(u.EventBus, func(e hmevent.DataPointValueChangedEvent) {
			s.onDataPoint(name, e)
		}),
	}
	s.mu.Lock()
	s.centralUnsubs[name] = append(s.centralUnsubs[name], unsubs...)
	s.mu.Unlock()
}

// onDataPoint folds a wire value change into the aggregate.
func (s *Service) onDataPoint(centralName string, e hmevent.DataPointValueChangedEvent) {
	key := hmevent.SecurityRefKey(centralName, e.Key.InterfaceID, e.Key.ChannelAddress, e.Key.Parameter)

	s.mu.Lock()
	src, known := s.agg.sources[key]
	if !known {
		s.mu.Unlock()
		return
	}
	active, ok := sourceActive(src, e.NewValue)
	if !ok {
		s.mu.Unlock()
		return
	}
	now := nowMS(s.clk.Now())
	changed := s.agg.setActive(key, active, now)
	if !changed {
		s.mu.Unlock()
		return
	}
	if active && s.agg.classSince[src.class] == 0 {
		s.agg.classSince[src.class] = now
	}
	state := s.agg.classState(src.class)
	if !state.Active {
		delete(s.agg.classSince, src.class)
	}
	snap := s.agg.snapshot()
	s.mu.Unlock()

	events.Publish(s.bus, hmevent.SecurityClassChangedEvent{
		Base:     hmevent.NewBaseAt(s.clk.Now()),
		Class:    src.class,
		Active:   state.Active,
		Sources:  state.Sources,
		Centrals: state.Centrals,
		SinceMS:  state.SinceMS,
	})
	s.publishState(snap)

	// A hazard class turning on is worth telling a person about; a
	// diagnostic class is a fault, which the fault plane reports with
	// its own debounce rather than as an alarm.
	if src.class.Hazard() {
		verb := hmenum.SecurityVerbTriggered
		if !active {
			verb = hmenum.SecurityVerbCleared
		}
		s.notify(reportInput{
			Class:      src.class,
			Verb:       verb,
			Sources:    state.Sources,
			At:         s.clk.Now(),
			Retainable: true,
		}, false)
	} else {
		s.applyFault(src, active)
	}
}

// sourceActive maps a wire value onto the domain's activation
// semantics, honouring the classifier's value narrowing.
func sourceActive(src *indexedSource, v hmtypes.ParamValue) (active, known bool) {
	return activeFromRaw(src.activeValues, v.Unwrap(), nil)
}

// activeFromRaw is the single activation rule of the domain, shared by
// the event path and the index seeding.
//
// valueList is optional: with it, an enumeration arriving as an index is
// narrowed properly; without it the rule falls back to the default. The
// fallback keeps a source reporting rather than silently going dark —
// for a hazard detector, over-reporting costs a false alarm and
// under-reporting costs the alarm entirely.
func activeFromRaw(activeValues []string, raw any, valueList []string) (active, known bool) {
	if len(activeValues) == 0 {
		return normalizeActive(raw)
	}
	if label, ok := raw.(string); ok {
		return containsString(activeValues, label), true
	}
	if idx, ok := rawIndex(raw); ok {
		for i, label := range valueList {
			if i == idx {
				return containsString(activeValues, label), true
			}
		}
	}
	return normalizeActive(raw)
}

// rawIndex narrows the integer wire kinds onto an enumeration index.
func rawIndex(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

// normalizeActive is the domain's default activation rule: booleans map
// directly, numbers activate on non-zero.
func normalizeActive(raw any) (active, known bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case int:
		return v != 0, true
	case int32:
		return v != 0, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	default:
		return false, false
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// onAlarmTriggered folds an intrusion incident into the zone view.
func (s *Service) onAlarmTriggered(e hmevent.AlarmTriggeredEvent) {
	s.mu.Lock()
	z := s.agg.zones[e.ZoneID]
	z.ID = e.ZoneID
	z.Name = e.ZoneName
	if z.Slug == "" {
		z.Slug = e.ZoneID
	}
	z.State = hmenum.AlarmZoneStateTriggered
	z.Mode = e.Mode
	z.IncidentID = e.IncidentID
	z.Sources = e.Sources
	z.ByClass = groupByClass(e.Sources)
	z.SinceMS = nowMS(s.clk.Now())
	s.agg.zones[e.ZoneID] = z
	if s.agg.classSince[hmenum.SecurityClassIntrusion] == 0 {
		s.agg.classSince[hmenum.SecurityClassIntrusion] = z.SinceMS
	}
	snap := s.agg.snapshot()
	s.mu.Unlock()

	s.publishZone(z)
	s.publishState(snap)
	s.notify(reportInput{
		Class:      hmenum.SecurityClassIntrusion,
		Verb:       hmenum.SecurityVerbTriggered,
		Sources:    e.Sources,
		ZoneID:     e.ZoneID,
		ZoneSlug:   z.Slug,
		ZoneName:   e.ZoneName,
		Mode:       e.Mode,
		IncidentID: e.IncidentID,
		At:         s.clk.Now(),
		Retainable: true,
	}, false)
}

// onAlarmStateChanged tracks the zone state and reports a cleared
// incident.
func (s *Service) onAlarmStateChanged(e hmevent.AlarmStateChangedEvent) {
	s.mu.Lock()
	z := s.agg.zones[e.ZoneID]
	z.ID = e.ZoneID
	z.Name = e.ZoneName
	if z.Slug == "" {
		z.Slug = e.ZoneID
	}
	wasTriggered := z.State == hmenum.AlarmZoneStateTriggered
	z.State = e.To
	z.Mode = e.Mode
	if e.To != hmenum.AlarmZoneStateTriggered {
		z.Sources = nil
		z.ByClass = nil
		z.IncidentID = 0
	}
	s.agg.zones[e.ZoneID] = z
	if e.To != hmenum.AlarmZoneStateTriggered {
		delete(s.agg.classSince, hmenum.SecurityClassIntrusion)
	}
	snap := s.agg.snapshot()
	s.mu.Unlock()

	s.publishZone(z)
	s.publishState(snap)
	if wasTriggered && e.To != hmenum.AlarmZoneStateTriggered {
		s.notify(reportInput{
			Class:      hmenum.SecurityClassIntrusion,
			Verb:       hmenum.SecurityVerbCleared,
			ZoneID:     e.ZoneID,
			ZoneSlug:   z.Slug,
			ZoneName:   e.ZoneName,
			Mode:       e.Mode,
			At:         s.clk.Now(),
			Retainable: true,
		}, false)
	}
}

// onAlarmHealthChanged mirrors the engine's own health verdict. It is a
// severity contribution rather than an availability flag so a consumer
// can tell an unhealthy alarm system from a broker outage.
func (s *Service) onAlarmHealthChanged(e hmevent.AlarmHealthChangedEvent) {
	s.mu.Lock()
	s.agg.engineHealthy = e.Healthy
	snap := s.agg.snapshot()
	s.mu.Unlock()
	s.publishState(snap)
}

// onAlarmDuress reports a duress-code use or a silent panic trigger,
// bounded by the configured visibility.
//
// The policy is applied here, at the single point where the report is
// created, rather than in each plane: a decision replicated across MQTT,
// the webhook and the WebSocket is a decision that will eventually be
// implemented three different ways, and the one that gets it wrong
// exposes the person the feature protects.
//
//   - hidden: the security plane stays silent entirely. The alarm
//     domain's own webhook path still carries it, which is the
//     historical behaviour.
//   - notify_only: the report is delivered but marked non-retainable, so
//     it reaches a phone and never lands in retained state or on a local
//     screen.
//   - full: an ordinary report.
func (s *Service) onAlarmDuress(e hmevent.AlarmDuressEvent) {
	vis := s.settings.DuressVisibility
	if !vis.AllowsNotification() {
		return
	}
	s.mu.Lock()
	z := s.agg.zones[e.ZoneID]
	s.mu.Unlock()
	s.notify(reportInput{
		Class:      hmenum.SecurityClassPanic,
		Verb:       hmenum.SecurityVerbTriggered,
		ZoneID:     e.ZoneID,
		ZoneSlug:   z.Slug,
		ZoneName:   e.ZoneName,
		IncidentID: e.IncidentID,
		At:         s.clk.Now(),
		Retainable: vis.AllowsRetained(),
	}, false)
}

// publishZone announces a zone view change.
func (s *Service) publishZone(z security.ZoneState) {
	events.Publish(s.bus, hmevent.SecurityZoneChangedEvent{
		Base:       hmevent.NewBaseAt(s.clk.Now()),
		ZoneID:     z.ID,
		ZoneSlug:   z.Slug,
		ZoneName:   z.Name,
		State:      z.State,
		Mode:       z.Mode,
		Sources:    z.Sources,
		ByClass:    z.ByClass,
		IncidentID: z.IncidentID,
	})
}

// groupByClass buckets source names by hazard class.
func groupByClass(refs []hmevent.SecuritySourceRef) map[hmenum.SecurityClass][]string {
	if len(refs) == 0 {
		return nil
	}
	out := map[hmenum.SecurityClass][]string{}
	for i := range refs {
		c := refs[i].Class
		if c == "" {
			c = hmenum.SecurityClassIntrusion
		}
		name := refs[i].Name
		if name == "" {
			name = refs[i].ChannelAddress
		}
		out[c] = append(out[c], name)
	}
	return out
}
