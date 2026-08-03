// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"context"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// applyFault raises or clears the fault of one diagnostic source.
//
// Faults are persisted rather than held in memory because `since` is
// the interesting part: "unreachable for three days" is a different
// fact from "unreachable", and a daemon restart must not reset that
// clock.
//
// Reporting runs on the raise edge only. A fault clearing is a state
// change worth publishing, but not worth waking anyone at night for.
func (s *Service) applyFault(src *indexedSource, active bool) {
	ctx := context.Background()
	reason := src.reason
	if reason == "" {
		return
	}
	now := s.clk.Now()
	if !active {
		cleared, err := s.stores.Faults.Clear(ctx, src.ref.Ref, string(reason), nowMS(now))
		if err != nil {
			s.log.Error("security: clear fault", "ref", src.ref.Ref, "error", err)
			return
		}
		if !cleared {
			return
		}
		s.mu.Lock()
		for id, f := range s.agg.faults {
			if f.Source.Ref == src.ref.Ref && f.Reason == reason {
				delete(s.agg.faults, id)
			}
		}
		open := len(s.agg.faults)
		snap := s.agg.snapshot()
		s.mu.Unlock()
		events.Publish(s.bus, hmevent.SecurityFaultChangedEvent{
			Base: hmevent.NewBaseAt(now), Class: src.class, Reason: reason,
			Source: src.ref, Open: false, OpenCount: open,
		})
		s.publishState(snap)
		// A resolved fault is reported too. It folds to severity OK, so
		// it cannot wake anyone, but "the smoke chamber is clean again"
		// is the answer to the report that woke them earlier.
		s.notify(reportInput{
			Class:      src.class,
			Verb:       hmenum.SecurityVerbCleared,
			Reason:     reason,
			Sources:    []hmevent.SecuritySourceRef{src.ref},
			At:         now,
			Fault:      true,
			Retainable: true,
		}, true)
		return
	}

	severity := hmenum.SeverityForClass(src.class)
	row := sqlitestore.SecurityFault{
		ID:             faultID(src.ref.Ref, reason, nowMS(now)),
		Ref:            src.ref.Ref,
		Class:          string(src.class),
		Reason:         string(reason),
		Severity:       string(severity),
		CentralName:    src.ref.Central,
		InterfaceID:    src.ref.InterfaceID,
		DeviceAddress:  src.ref.DeviceAddress,
		ChannelAddress: src.ref.ChannelAddress,
		Parameter:      src.ref.Parameter,
		Name:           src.ref.Name,
		SinceMS:        nowMS(now),
	}
	effective, opened, err := s.stores.Faults.Raise(ctx, row)
	if err != nil {
		s.log.Error("security: raise fault", "ref", src.ref.Ref, "error", err)
		return
	}
	if !opened {
		// Already standing: keep the original `since` and stay quiet.
		return
	}
	f := faultFromRow(effective)
	s.mu.Lock()
	s.agg.faults[f.ID] = &f
	open := len(s.agg.faults)
	snap := s.agg.snapshot()
	s.mu.Unlock()

	events.Publish(s.bus, hmevent.SecurityFaultChangedEvent{
		Base: hmevent.NewBaseAt(now), FaultID: f.ID, Class: f.Class, Reason: f.Reason,
		Severity: f.Severity, Source: f.Source, Open: true,
		SinceMS: f.SinceMS, OpenCount: open,
	})
	s.publishState(snap)
	s.notify(reportInput{
		Class:      f.Class,
		Verb:       hmenum.SecurityVerbRaised,
		Reason:     f.Reason,
		Sources:    []hmevent.SecuritySourceRef{f.Source},
		At:         now,
		Fault:      true,
		Retainable: true,
	}, true)
}

// AcknowledgeFault marks a standing fault as seen. It never clears the
// fault: the condition is still there.
func (s *Service) AcknowledgeFault(ctx context.Context, id, by string) (bool, error) {
	ok, err := s.stores.Faults.Acknowledge(ctx, id, nowMS(s.clk.Now()), by)
	if err != nil || !ok {
		return false, err
	}
	s.mu.Lock()
	if f, found := s.agg.faults[id]; found {
		f.AcknowledgedAtMS = nowMS(s.clk.Now())
		f.AcknowledgedBy = by
		s.mu.Unlock()
		events.Publish(s.bus, hmevent.SecurityFaultChangedEvent{
			Base: hmevent.NewBaseAt(s.clk.Now()), FaultID: id, Class: f.Class,
			Reason: f.Reason, Severity: f.Severity, Source: f.Source,
			Open: true, Acknowledged: true, SinceMS: f.SinceMS,
		})
		return true, nil
	}
	s.mu.Unlock()
	return true, nil
}

// notify renders a report and publishes it, recording it as the
// retained last-alarm or last-fault when the visibility policy allows.
//
// The retained half is deliberately separate from the delivery half.
// A covert-trigger report under the notify-only policy is delivered —
// it must reach a phone — but never retained, because retained state
// stays readable long after the moment has passed, and a wall tablet
// showing it defeats the point of triggering covertly.
func (s *Service) notify(in reportInput, isFault bool) {
	n := s.render.render(in)
	if in.Retainable {
		s.mu.Lock()
		if isFault {
			s.agg.lastFault = &n
		} else {
			s.agg.lastAlarm = &n
		}
		s.mu.Unlock()
	}
	events.Publish(s.bus, hmevent.SecurityNotificationEvent{
		Base:       hmevent.NewBaseAt(s.clk.Now()),
		Class:      n.Class,
		Severity:   n.Severity,
		Verb:       n.Verb,
		Subject:    n.Subject,
		Message:    n.Message,
		I18nKey:    n.I18nKey,
		Args:       n.Args,
		Sources:    n.Sources,
		ZoneID:     n.ZoneID,
		ZoneSlug:   n.ZoneSlug,
		ZoneName:   n.ZoneName,
		Mode:       n.Mode,
		IncidentID: n.IncidentID,
		Link:       n.Link,
		Fault:      isFault,
		Retainable: in.Retainable,
	})
}

// faultID derives the row id from the source, the reason and the time
// the fault opened.
//
// The timestamp is load-bearing, not decoration. The open-fault index is
// deliberately partial (`WHERE cleared_at_ms = 0`) so a cleared fault
// stays in history while a fresh one opens for the same source — but the
// id is an unconditional primary key. Without the timestamp a flapping
// condition would collide with its own cleared row on the second raise,
// and since applyFault only logs a failed raise, the fault would
// silently stop reopening until retention aged the old row out.
func faultID(ref string, reason hmenum.SecurityFaultReason, sinceMS int64) string {
	return ref + "|" + string(reason) + "|" + strconv.FormatInt(sinceMS, 10)
}

// Faults returns the standing faults.
func (s *Service) Faults() []security.Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agg.snapshot().Faults
}
