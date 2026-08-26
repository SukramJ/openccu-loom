// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package journal is the domain facade over the alarm-journal store:
// it stamps and serializes engine journal entries, persists them, and
// publishes the journal-appended event. The journal is append-only
// from the engine's perspective; deletion happens only through the
// privileged retention path.
package journal

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/clock"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Store is the persistence surface the facade needs. Satisfied by
// *sqlitestore.AlarmJournalStore.
type Store interface {
	Append(ctx context.Context, e sqlitestore.AlarmJournalEntry) (int64, error)
	PurgeBefore(ctx context.Context, cutoffMS int64) (int64, error)
}

// Journal persists alarm-journal entries and publishes their events.
// It implements the engine's Journal port.
type Journal struct {
	store   Store
	clk     clock.Clock
	publish func(hmevent.Event)
	log     *slog.Logger
}

// New constructs the facade. publish may be nil (no event fan-out);
// clk nil selects the wall clock.
func New(store Store, clk clock.Clock, publish func(hmevent.Event), logger *slog.Logger) *Journal {
	if clk == nil {
		clk = clock.New()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Journal{store: store, clk: clk, publish: publish, log: logger}
}

// Append implements engine.Journal: it stamps the entry, serializes
// the details, persists, and publishes. Hidden entries (duress
// privacy) are persisted but never published to the event fan-out.
func (j *Journal) Append(ctx context.Context, e engine.JournalEntry) (int64, error) {
	details := "{}"
	if len(e.Details) > 0 {
		if b, err := json.Marshal(e.Details); err == nil {
			details = string(b)
		} else {
			j.log.Error("alarm journal details not serializable", "event", e.Event, "error", err)
		}
	}
	now := j.clk.Now()
	id, err := j.store.Append(ctx, sqlitestore.AlarmJournalEntry{
		TsMS:        now.UnixMilli(),
		ZoneID:      e.ZoneID,
		Class:       e.Class,
		Event:       e.Event,
		Actor:       e.Actor,
		Source:      e.Source,
		IncidentID:  e.IncidentID,
		Hidden:      e.Hidden,
		DetailsJSON: details,
	})
	if err != nil {
		return 0, err
	}
	if j.publish != nil && !e.Hidden {
		j.publish(hmevent.AlarmJournalAppendedEvent{
			Base:    hmevent.NewBaseAt(now),
			EntryID: id,
			ZoneID:  e.ZoneID,
			Class:   e.Class,
			Event:   e.Event,
			Actor:   e.Actor,

			IncidentID: e.IncidentID,
		})
	}
	return id, nil
}

// Purge deletes entries older than maxAge and returns the number of
// deleted rows. This is the privileged retention path.
func (j *Journal) Purge(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := j.clk.Now().Add(-maxAge).UnixMilli()
	return j.store.PurgeBefore(ctx, cutoff)
}
