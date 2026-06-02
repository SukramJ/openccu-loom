// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Default retention limits for IncidentStore helpers.
const (
	// DefaultMaxAgeDays is the default TTL for stored incidents (7 days).
	DefaultMaxAgeDays = 7
	// DefaultMaxPerType is the default per-type incident cap.
	DefaultMaxPerType = 20
)

// Incident is a single row in the incidents table.
type Incident struct {
	ID          int64
	CentralName string
	InterfaceID string
	Type        hmenum.IncidentType
	Severity    hmenum.IncidentSeverity
	Message     string
	Details     string
	// JournalExcerpt holds a short excerpt from the diagnostic journal at the
	// time the incident was recorded.
	JournalExcerpt string
	FirstSeen      time.Time
	LastSeen       time.Time
	Count          int
}

// IncidentStore persists diagnostic incidents.
type IncidentStore struct {
	db *sql.DB
}

// NewIncidentStore returns a store backed by db.
func NewIncidentStore(db *sql.DB) *IncidentStore { return &IncidentStore{db: db} }

// Record inserts a new incident and returns its ID. Deduplication and
// counter-bumping for repeated occurrences is the caller's concern
// (typically a rate-limiter in front of Record).
func (s *IncidentStore) Record(ctx context.Context, inc Incident) (int64, error) {
	const q = `
INSERT INTO incidents (central_name, interface_id, type, severity, message, details, journal_excerpt, first_seen, last_seen, count)
VALUES (?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1)`
	res, err := s.db.ExecContext(
		ctx, q,
		inc.CentralName, inc.InterfaceID, string(inc.Type), string(inc.Severity), inc.Message, inc.Details, inc.JournalExcerpt,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: record incident: %w", err)
	}
	return res.LastInsertId()
}

// BumpIfRecent merges a duplicate incident with an existing row whose
// (central, interface, type, severity, message) tuple matches and
// whose LastSeen is within the supplied window. Returns true when a
// match was found and updated.
func (s *IncidentStore) BumpIfRecent(ctx context.Context, inc Incident, window time.Duration) (bool, error) {
	const q = `
UPDATE incidents
SET last_seen = CURRENT_TIMESTAMP, count = count + 1, details = COALESCE(NULLIF(?, ''), details)
WHERE id = (
    SELECT id FROM incidents
    WHERE central_name = ?
      AND COALESCE(interface_id, '') = ?
      AND type = ?
      AND severity = ?
      AND message = ?
      AND last_seen > datetime('now', ?)
    ORDER BY last_seen DESC
    LIMIT 1
)`
	offset := fmt.Sprintf("-%d seconds", int64(window.Seconds()))
	res, err := s.db.ExecContext(
		ctx, q,
		inc.Details,
		inc.CentralName, inc.InterfaceID, string(inc.Type), string(inc.Severity), inc.Message, offset,
	)
	if err != nil {
		return false, fmt.Errorf("sqlite: bump incident: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// GetAllIncidents returns all incidents for central ordered by last_seen
// descending.
func (s *IncidentStore) GetAllIncidents(ctx context.Context, centralName string) ([]Incident, error) {
	const q = `
SELECT id, COALESCE(interface_id, ''), type, severity, message, COALESCE(details, ''), COALESCE(journal_excerpt, ''), first_seen, last_seen, count
FROM incidents WHERE central_name = ?
ORDER BY last_seen DESC`
	rows, err := s.db.QueryContext(ctx, q, centralName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Incident
	for rows.Next() {
		var inc Incident
		inc.CentralName = centralName
		var typ, sev string
		if err := rows.Scan(&inc.ID, &inc.InterfaceID, &typ, &sev,
			&inc.Message, &inc.Details, &inc.JournalExcerpt, &inc.FirstSeen, &inc.LastSeen, &inc.Count); err != nil {
			return nil, fmt.Errorf("sqlite: scan all incident: %w", err)
		}
		inc.Type = hmenum.IncidentType(typ)
		inc.Severity = hmenum.IncidentSeverity(sev)
		out = append(out, inc)
	}
	return out, rows.Err()
}

// GetDiagnostics returns a summary of incidents for central.
func (s *IncidentStore) GetDiagnostics(ctx context.Context, centralName string, maxPerType, maxAgeDays int) (map[string]any, error) {
	all, err := s.GetAllIncidents(ctx, centralName)
	if err != nil {
		return nil, err
	}
	byType := make(map[string]int)
	bySev := make(map[string]int)
	var recent []map[string]any
	for i := range all {
		byType[string(all[i].Type)]++
		bySev[string(all[i].Severity)]++
	}
	// Build recent slice (last 10).
	start := 0
	if len(all) > 10 {
		start = len(all) - 10
	}
	tail := all[start:]
	for i := range tail {
		recent = append(recent, map[string]any{
			"id":           tail[i].ID,
			"type":         string(tail[i].Type),
			"severity":     string(tail[i].Severity),
			"interface_id": tail[i].InterfaceID,
			"message":      tail[i].Message,
			"last_seen":    tail[i].LastSeen.Format(time.RFC3339),
			"count":        tail[i].Count,
		})
	}
	return map[string]any{
		"total_incidents":       len(all),
		"max_per_type":          maxPerType,
		"max_age_days":          maxAgeDays,
		"incidents_by_type":     byType,
		"incidents_by_severity": bySev,
		"recent_incidents":      recent,
	}, nil
}

// GetIncidentsByType returns all incidents of incidentType for central.
func (s *IncidentStore) GetIncidentsByType(ctx context.Context, centralName string, incidentType hmenum.IncidentType) ([]Incident, error) {
	const q = `
SELECT id, COALESCE(interface_id, ''), severity, message, COALESCE(details, ''), COALESCE(journal_excerpt, ''), first_seen, last_seen, count
FROM incidents WHERE central_name = ? AND type = ?
ORDER BY last_seen DESC`
	rows, err := s.db.QueryContext(ctx, q, centralName, string(incidentType))
	if err != nil {
		return nil, fmt.Errorf("sqlite: get incidents by type: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Incident
	for rows.Next() {
		var inc Incident
		inc.CentralName = centralName
		inc.Type = incidentType
		var sev string
		if err := rows.Scan(&inc.ID, &inc.InterfaceID, &sev,
			&inc.Message, &inc.Details, &inc.JournalExcerpt, &inc.FirstSeen, &inc.LastSeen, &inc.Count); err != nil {
			return nil, fmt.Errorf("sqlite: scan incident by type: %w", err)
		}
		inc.Severity = hmenum.IncidentSeverity(sev)
		out = append(out, inc)
	}
	return out, rows.Err()
}

// PurgeOld removes incidents older than maxAgeDays for central.
func (s *IncidentStore) PurgeOld(ctx context.Context, centralName string, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = DefaultMaxAgeDays
	}
	offset := fmt.Sprintf("-%d days", maxAgeDays)
	res, err := s.db.ExecContext(
		ctx,
		`DELETE FROM incidents WHERE central_name = ? AND last_seen < datetime('now', ?)`,
		centralName, offset,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge old incidents: %w", err)
	}
	return res.RowsAffected()
}

// EnforcePerTypeCap removes the oldest incidents that exceed maxPerType
// for each incident type for central.
func (s *IncidentStore) EnforcePerTypeCap(ctx context.Context, centralName string, maxPerType int) error {
	if maxPerType <= 0 {
		maxPerType = DefaultMaxPerType
	}
	// Find all distinct types for central with more than maxPerType rows.
	const typesQ = `
SELECT DISTINCT type FROM incidents WHERE central_name = ?`
	rows, err := s.db.QueryContext(ctx, typesQ, centralName)
	if err != nil {
		return fmt.Errorf("sqlite: enforce per-type cap (types): %w", err)
	}
	var types []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sqlite: scan type: %w", err)
		}
		types = append(types, t)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: iterate types: %w", err)
	}

	for _, t := range types {
		// Delete oldest rows beyond the cap.
		const delQ = `
DELETE FROM incidents WHERE central_name = ? AND type = ? AND id NOT IN (
    SELECT id FROM incidents WHERE central_name = ? AND type = ?
    ORDER BY last_seen DESC LIMIT ?
)`
		if _, err := s.db.ExecContext(ctx, delQ, centralName, t, centralName, t, maxPerType); err != nil {
			return fmt.Errorf("sqlite: enforce per-type cap (delete): %w", err)
		}
	}
	return nil
}

// GetIncidentsByInterface returns all incidents for the given interface ID.
func (s *IncidentStore) GetIncidentsByInterface(ctx context.Context, centralName, interfaceID string) ([]Incident, error) {
	const q = `
SELECT id, COALESCE(interface_id, ''), type, severity, message, COALESCE(details, ''), COALESCE(journal_excerpt, ''), first_seen, last_seen, count
FROM incidents WHERE central_name = ? AND interface_id = ?
ORDER BY last_seen DESC`
	rows, err := s.db.QueryContext(ctx, q, centralName, interfaceID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get incidents by interface: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Incident
	for rows.Next() {
		var inc Incident
		inc.CentralName = centralName
		var typ, sev string
		if err := rows.Scan(&inc.ID, &inc.InterfaceID, &typ, &sev,
			&inc.Message, &inc.Details, &inc.JournalExcerpt, &inc.FirstSeen, &inc.LastSeen, &inc.Count); err != nil {
			return nil, fmt.Errorf("sqlite: scan incident by interface: %w", err)
		}
		inc.Type = hmenum.IncidentType(typ)
		inc.Severity = hmenum.IncidentSeverity(sev)
		out = append(out, inc)
	}
	return out, rows.Err()
}

// IncidentCount returns the total number of incidents stored for central.
func (s *IncidentStore) IncidentCount(ctx context.Context, centralName string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM incidents WHERE central_name = ?`, centralName).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: incident count: %w", err)
	}
	return n, nil
}

// ClearIncidents removes all incident rows for central from the database.
func (s *IncidentStore) ClearIncidents(ctx context.Context, centralName string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM incidents WHERE central_name = ?`, centralName)
	if err != nil {
		return fmt.Errorf("sqlite: clear incidents: %w", err)
	}
	return nil
}

// RecordWithLimits records an incident and applies TTL purge + per-type
// cap in a single convenient call. Suitable for production use where
// both limits should be enforced automatically.
func (s *IncidentStore) RecordWithLimits(ctx context.Context, inc Incident, maxAgeDays, maxPerType int) (int64, error) {
	id, err := s.Record(ctx, inc)
	if err != nil {
		return 0, err
	}
	if _, err := s.PurgeOld(ctx, inc.CentralName, maxAgeDays); err != nil {
		return id, fmt.Errorf("sqlite: post-record purge: %w", err)
	}
	if err := s.EnforcePerTypeCap(ctx, inc.CentralName, maxPerType); err != nil {
		return id, fmt.Errorf("sqlite: post-record cap: %w", err)
	}
	return id, nil
}

// RecordIncidentCtx is a convenience wrapper around [RecordWithLimits] that
// accepts individual fields rather than a full [Incident] struct. It
// satisfies the [dynamic.IncidentRecorder] interface used by
// [dynamic.PingPongCombinedTracker]. Default retention limits apply.
func (s *IncidentStore) RecordIncidentCtx(ctx context.Context, centralName, interfaceID string,
	incType hmenum.IncidentType, severity hmenum.IncidentSeverity, message string,
) error {
	inc := Incident{
		CentralName: centralName,
		InterfaceID: interfaceID,
		Type:        incType,
		Severity:    severity,
		Message:     message,
	}
	_, err := s.RecordWithLimits(ctx, inc, DefaultMaxAgeDays, DefaultMaxPerType)
	return err
}

// RecordIncident implements [reliability.IncidentRecorder]. It applies
// BumpIfRecent deduplication within a 5-minute window before falling back to
// a fresh insert so repeated occurrences of the same incident (e.g. circuit
// breaker tripping on every retry burst) do not produce unbounded rows.
// Default retention limits (DefaultMaxAgeDays / DefaultMaxPerType) are
// enforced on every insert path via RecordWithLimits.
//
// Errors are returned to the caller but are treated as best-effort by every
// caller in the reliability stack (they log and discard).
func (s *IncidentStore) RecordIncident(ctx context.Context, inc reliability.IncidentRecord) error {
	row := Incident{
		CentralName: inc.CentralName,
		InterfaceID: inc.InterfaceID,
		Type:        inc.Type,
		Severity:    inc.Severity,
		Message:     inc.Message,
		Details:     inc.Details,
	}
	// Try deduplication first — within a 5-minute window the same
	// (central, interface, type, severity, message) tuple is merged into
	// the existing row rather than generating a new one.
	bumped, err := s.BumpIfRecent(ctx, row, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("sqlite: RecordIncident bump: %w", err)
	}
	if bumped {
		return nil
	}
	_, err = s.RecordWithLimits(ctx, row, DefaultMaxAgeDays, DefaultMaxPerType)
	return err
}

// Recent returns the most recent limit incidents (all severities).
func (s *IncidentStore) Recent(ctx context.Context, centralName string, limit int) ([]Incident, error) {
	const q = `
SELECT id, COALESCE(interface_id, ''), type, severity, message, COALESCE(details, ''), COALESCE(journal_excerpt, ''), first_seen, last_seen, count
FROM incidents WHERE central_name = ?
ORDER BY last_seen DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, centralName, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: recent incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Incident
	for rows.Next() {
		var inc Incident
		inc.CentralName = centralName
		var typ, sev string
		if err := rows.Scan(&inc.ID, &inc.InterfaceID, &typ, &sev,
			&inc.Message, &inc.Details, &inc.JournalExcerpt, &inc.FirstSeen, &inc.LastSeen, &inc.Count); err != nil {
			return nil, fmt.Errorf("sqlite: scan incident: %w", err)
		}
		inc.Type = hmenum.IncidentType(typ)
		inc.Severity = hmenum.IncidentSeverity(sev)
		out = append(out, inc)
	}
	return out, rows.Err()
}
