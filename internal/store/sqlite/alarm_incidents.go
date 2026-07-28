// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AlarmIncident is one trigger episode of an alarm zone. It carries
// the safety-critical persisted counters: the silenced flag (silence
// is incident-scoped and survives restarts), the re-trigger cycle
// counter, the cumulative acoustic-milliseconds ledger, and the
// restore-driven re-fire counter feeding the restart-loop breaker.
// ClosedAtMS == 0 marks the open incident of an zone.
type AlarmIncident struct {
	ID                int64
	ZoneID            string
	Mode              hmenum.AlarmMode
	CauseJSON         string
	StartedAtMS       int64
	TriggerDeadlineMS int64
	Silenced          bool
	SilencedAtMS      int64
	SilencedBy        string
	RetriggerCycles   int
	AcousticMS        int64
	RestoreRefires    int
	ClosedAtMS        int64
	CloseReason       string
}

// AlarmIncidentStore persists alarm incidents. Counter updates are
// atomic in-database increments so a crash between an increment and
// the activation it accounts for can only over-count, never
// under-count — the bounded-activation invariant depends on that
// direction.
type AlarmIncidentStore struct {
	db *sql.DB
}

// NewAlarmIncidentStore returns a store backed by db.
func NewAlarmIncidentStore(db *sql.DB) *AlarmIncidentStore { return &AlarmIncidentStore{db: db} }

// Create inserts a new incident and returns its ID.
func (s *AlarmIncidentStore) Create(ctx context.Context, inc AlarmIncident) (int64, error) {
	const q = `
INSERT INTO alarm_incidents (zone_id, mode, cause_json, started_at_ms, trigger_deadline_ms,
    silenced, silenced_at_ms, silenced_by, retrigger_cycles, acoustic_ms, restore_refires,
    closed_at_ms, close_reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(
		ctx, q,
		inc.ZoneID, string(inc.Mode), inc.CauseJSON, inc.StartedAtMS, inc.TriggerDeadlineMS,
		boolToInt(inc.Silenced), inc.SilencedAtMS, inc.SilencedBy, inc.RetriggerCycles,
		inc.AcousticMS, inc.RestoreRefires, inc.ClosedAtMS, inc.CloseReason,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: create alarm incident: %w", err)
	}
	return res.LastInsertId()
}

// Get returns the incident with id. The boolean reports whether it
// exists.
func (s *AlarmIncidentStore) Get(ctx context.Context, id int64) (AlarmIncident, bool, error) {
	inc, err := scanAlarmIncident(s.db.QueryRowContext(ctx, alarmIncidentSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmIncident{}, false, nil
	}
	if err != nil {
		return AlarmIncident{}, false, fmt.Errorf("sqlite: get alarm incident: %w", err)
	}
	return inc, true, nil
}

// GetOpenByZone returns the open (not yet closed) incident of zoneID.
// The boolean reports whether one exists. The engine keeps at most one
// incident open per zone; if historical corruption ever leaves more,
// the newest wins.
func (s *AlarmIncidentStore) GetOpenByZone(ctx context.Context, zoneID string) (AlarmIncident, bool, error) {
	q := alarmIncidentSelect + ` WHERE zone_id = ? AND closed_at_ms = 0 ORDER BY id DESC LIMIT 1`
	inc, err := scanAlarmIncident(s.db.QueryRowContext(ctx, q, zoneID))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmIncident{}, false, nil
	}
	if err != nil {
		return AlarmIncident{}, false, fmt.Errorf("sqlite: get open alarm incident: %w", err)
	}
	return inc, true, nil
}

// ListByZone returns incidents of zoneID, newest first. limit <= 0
// returns every row.
func (s *AlarmIncidentStore) ListByZone(ctx context.Context, zoneID string, limit int) ([]AlarmIncident, error) {
	q := alarmIncidentSelect + ` WHERE zone_id = ? ORDER BY id DESC`
	args := []any{zoneID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alarm incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmIncident
	for rows.Next() {
		inc, err := scanAlarmIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// MarkSilenced sets the persisted silenced flag. It is deliberately
// one-way: there is no store API to un-silence an incident, so a
// silence can never be lost to a later write (a genuinely new alarm
// episode gets a new incident instead).
func (s *AlarmIncidentStore) MarkSilenced(ctx context.Context, id, atMS int64, by string) error {
	const q = `
UPDATE alarm_incidents
SET silenced = 1,
    silenced_at_ms = CASE WHEN silenced = 1 THEN silenced_at_ms ELSE ? END,
    silenced_by = CASE WHEN silenced = 1 THEN silenced_by ELSE ? END
WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, atMS, by, id); err != nil {
		return fmt.Errorf("sqlite: silence alarm incident: %w", err)
	}
	return nil
}

// AddAcousticMS atomically adds deltaMS to the incident's cumulative
// acoustic ledger. Callers account an activation before sending it.
func (s *AlarmIncidentStore) AddAcousticMS(ctx context.Context, id, deltaMS int64) error {
	const q = `UPDATE alarm_incidents SET acoustic_ms = acoustic_ms + ? WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, deltaMS, id); err != nil {
		return fmt.Errorf("sqlite: add alarm acoustic ledger: %w", err)
	}
	return nil
}

// IncrementRetriggerCycles atomically bumps the re-trigger cycle
// counter. Callers account a cycle before starting it.
func (s *AlarmIncidentStore) IncrementRetriggerCycles(ctx context.Context, id int64) error {
	const q = `UPDATE alarm_incidents SET retrigger_cycles = retrigger_cycles + 1 WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("sqlite: increment alarm retrigger cycles: %w", err)
	}
	return nil
}

// IncrementRestoreRefires atomically bumps the restore-driven re-fire
// counter (restart-loop breaker input). Callers account a re-fire
// before performing it.
func (s *AlarmIncidentStore) IncrementRestoreRefires(ctx context.Context, id int64) error {
	const q = `UPDATE alarm_incidents SET restore_refires = restore_refires + 1 WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("sqlite: increment alarm restore refires: %w", err)
	}
	return nil
}

// SetTriggerDeadline updates the trigger-time deadline (a new
// re-trigger cycle extends it).
func (s *AlarmIncidentStore) SetTriggerDeadline(ctx context.Context, id, deadlineMS int64) error {
	const q = `UPDATE alarm_incidents SET trigger_deadline_ms = ? WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, deadlineMS, id); err != nil {
		return fmt.Errorf("sqlite: set alarm trigger deadline: %w", err)
	}
	return nil
}

// Close marks the incident closed with a stable machine-readable
// reason. Closing is idempotent; the first close wins.
func (s *AlarmIncidentStore) Close(ctx context.Context, id, atMS int64, reason string) error {
	const q = `
UPDATE alarm_incidents SET closed_at_ms = ?, close_reason = ?
WHERE id = ? AND closed_at_ms = 0`
	if _, err := s.db.ExecContext(ctx, q, atMS, reason, id); err != nil {
		return fmt.Errorf("sqlite: close alarm incident: %w", err)
	}
	return nil
}

// PurgeClosedBefore deletes closed incidents whose close time is
// before cutoffMS. Open incidents are never purged. Returns the
// number of deleted rows.
func (s *AlarmIncidentStore) PurgeClosedBefore(ctx context.Context, cutoffMS int64) (int64, error) {
	const q = `DELETE FROM alarm_incidents WHERE closed_at_ms > 0 AND closed_at_ms < ?`
	res, err := s.db.ExecContext(ctx, q, cutoffMS)
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge alarm incidents: %w", err)
	}
	return res.RowsAffected()
}

// boolToInt maps a Go bool onto the 0/1 INTEGER convention used by
// the alarm tables.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const alarmIncidentSelect = `
SELECT id, zone_id, mode, cause_json, started_at_ms, trigger_deadline_ms,
    silenced, silenced_at_ms, silenced_by, retrigger_cycles, acoustic_ms,
    restore_refires, closed_at_ms, close_reason
FROM alarm_incidents`

func scanAlarmIncident(sc scannable) (AlarmIncident, error) {
	var inc AlarmIncident
	var mode string
	var silenced int
	if err := sc.Scan(&inc.ID, &inc.ZoneID, &mode, &inc.CauseJSON, &inc.StartedAtMS,
		&inc.TriggerDeadlineMS, &silenced, &inc.SilencedAtMS, &inc.SilencedBy,
		&inc.RetriggerCycles, &inc.AcousticMS, &inc.RestoreRefires,
		&inc.ClosedAtMS, &inc.CloseReason); err != nil {
		return AlarmIncident{}, err
	}
	inc.Mode = hmenum.AlarmMode(mode)
	inc.Silenced = silenced != 0
	return inc, nil
}
