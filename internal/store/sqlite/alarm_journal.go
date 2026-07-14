// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AlarmJournalEntry is one row of the persistent, filterable alarm-journal
// log (docs/alarm-concept.md §14). The journal is append-only from the
// engine's perspective; deletion happens only through the privileged
// retention path (PurgeBefore). Hidden entries exist for duress events —
// they stay out of user-visible surfaces until resolved but are fully
// retained, so Query excludes them unless the caller explicitly opts in.
type AlarmJournalEntry struct {
	ID          int64
	TsMS        int64
	AreaID      string
	Class       hmenum.AlarmJournalClass
	Event       string
	Actor       string
	Source      string
	IncidentID  int64
	Hidden      bool
	DetailsJSON string
}

// AlarmJournalFilter bounds an AlarmJournalStore.Query call. The zero value
// of every field disables that criterion: AreaID "" matches every area,
// Class "" matches every class, FromMS/ToMS 0 leaves that time bound open,
// IncidentID 0 matches every entry regardless of incident, and Limit <= 0
// returns every matching row.
type AlarmJournalFilter struct {
	AreaID        string
	Class         hmenum.AlarmJournalClass
	FromMS        int64
	ToMS          int64
	IncidentID    int64
	IncludeHidden bool
	Limit         int
}

// AlarmJournalStore persists the alarm-engine event journal.
type AlarmJournalStore struct {
	db *sql.DB
}

// NewAlarmJournalStore returns a store backed by db.
func NewAlarmJournalStore(db *sql.DB) *AlarmJournalStore { return &AlarmJournalStore{db: db} }

// Append inserts e and returns the assigned row id. e.ID is ignored on
// input — the row id is always assigned by the database.
func (s *AlarmJournalStore) Append(ctx context.Context, e AlarmJournalEntry) (int64, error) {
	const q = `
INSERT INTO alarm_journal (ts_ms, area_id, class, event, actor, source, incident_id, hidden, details_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(
		ctx, q,
		e.TsMS, e.AreaID, string(e.Class), e.Event, e.Actor, e.Source, e.IncidentID,
		boolToInt(e.Hidden), e.DetailsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: append alarm journal entry: %w", err)
	}
	return res.LastInsertId()
}

// Query returns journal entries matching f, newest first (highest id
// first). Hidden entries are excluded unless f.IncludeHidden is set — this
// is the duress-privacy filter: a duress event must not surface on a
// journal view that an intruder standing next to the operator could see.
func (s *AlarmJournalStore) Query(ctx context.Context, f AlarmJournalFilter) ([]AlarmJournalEntry, error) {
	var (
		where []string
		args  []any
	)
	if f.AreaID != "" {
		where = append(where, "area_id = ?")
		args = append(args, f.AreaID)
	}
	if f.Class != "" {
		where = append(where, "class = ?")
		args = append(args, string(f.Class))
	}
	if f.FromMS != 0 {
		where = append(where, "ts_ms >= ?")
		args = append(args, f.FromMS)
	}
	if f.ToMS != 0 {
		where = append(where, "ts_ms <= ?")
		args = append(args, f.ToMS)
	}
	if f.IncidentID != 0 {
		where = append(where, "incident_id = ?")
		args = append(args, f.IncidentID)
	}
	if !f.IncludeHidden {
		where = append(where, "hidden = 0")
	}

	q := `
SELECT id, ts_ms, area_id, class, event, actor, source, incident_id, hidden, details_json
FROM alarm_journal`
	if len(where) > 0 {
		// The joined fragments are fixed literals; every value travels as a
		// bound parameter in args, never via concatenation.
		q += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // G202: static fragments; values are parameterized
	}
	q += " ORDER BY id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query alarm journal: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmJournalEntry
	for rows.Next() {
		e, err := scanAlarmJournalEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm journal entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PurgeBefore deletes journal entries whose ts_ms is before cutoffMS,
// including hidden ones, and returns the number of rows deleted. This is
// the privileged retention path — the only way alarm_journal rows are ever
// removed.
func (s *AlarmJournalStore) PurgeBefore(ctx context.Context, cutoffMS int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alarm_journal WHERE ts_ms < ?`, cutoffMS)
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge alarm journal: %w", err)
	}
	return res.RowsAffected()
}

func scanAlarmJournalEntry(sc scannable) (AlarmJournalEntry, error) {
	var e AlarmJournalEntry
	var class string
	var hidden int
	if err := sc.Scan(&e.ID, &e.TsMS, &e.AreaID, &class, &e.Event, &e.Actor, &e.Source,
		&e.IncidentID, &hidden, &e.DetailsJSON); err != nil {
		return AlarmJournalEntry{}, err
	}
	e.Class = hmenum.AlarmJournalClass(class)
	e.Hidden = hidden != 0
	return e, nil
}
