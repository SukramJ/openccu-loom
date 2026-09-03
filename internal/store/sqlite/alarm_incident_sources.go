// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// AlarmIncidentSource is one data point that contributed to an
// incident. The incident's own cause document records only the source
// that opened it; this ledger records every one, so "which detectors
// fired?" survives the incident.
type AlarmIncidentSource struct {
	ID             int64
	IncidentID     int64
	ZoneID         string
	Ref            string
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	DeviceAddress  string
	Parameter      string
	SensorID       string
	Name           string
	SensorType     string
	Class          string
	Cause          string
	AtMS           int64
}

// AlarmIncidentSourceStore persists the per-incident source ledger.
type AlarmIncidentSourceStore struct {
	db *sql.DB
}

// NewAlarmIncidentSourceStore returns a store backed by db.
func NewAlarmIncidentSourceStore(db *sql.DB) *AlarmIncidentSourceStore {
	return &AlarmIncidentSourceStore{db: db}
}

// Append records a source contribution. It is idempotent per
// (incident, ref): a sensor that re-activates within the same incident
// keeps its first observation time rather than inflating the list or
// drifting the timestamp forward. That matters for attribution — the
// interesting fact is when a detector *first* fired, not when it last
// re-reported.
func (s *AlarmIncidentSourceStore) Append(ctx context.Context, row AlarmIncidentSource) error {
	const q = `
INSERT INTO alarm_incident_sources (incident_id, zone_id, ref, central_name, interface_id,
    channel_address, device_address, parameter, sensor_id, name, sensor_type, class, cause, at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(incident_id, ref) DO NOTHING`
	_, err := s.db.ExecContext(ctx, q,
		row.IncidentID, row.ZoneID, row.Ref, row.CentralName, row.InterfaceID,
		row.ChannelAddress, row.DeviceAddress, row.Parameter, row.SensorID, row.Name,
		row.SensorType, row.Class, row.Cause, row.AtMS)
	if err != nil {
		return fmt.Errorf("sqlite: append alarm incident source: %w", err)
	}
	return nil
}

// ListByIncident returns the sources of one incident, oldest first —
// the order in which the alarm actually unfolded.
func (s *AlarmIncidentSourceStore) ListByIncident(ctx context.Context, incidentID int64) ([]AlarmIncidentSource, error) {
	const q = alarmIncidentSourceSelect + ` WHERE incident_id = ? ORDER BY at_ms ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, q, incidentID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alarm incident sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmIncidentSource
	for rows.Next() {
		row, err := scanAlarmIncidentSource(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm incident source: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListByIncidents returns the sources of several incidents in one
// round trip, keyed by incident ID. The list view needs the sources of
// every listed incident; querying per incident would turn one page
// into N+1 statements.
func (s *AlarmIncidentSourceStore) ListByIncidents(ctx context.Context, incidentIDs []int64) (map[int64][]AlarmIncidentSource, error) {
	out := map[int64][]AlarmIncidentSource{}
	if len(incidentIDs) == 0 {
		return out, nil
	}
	// The only concatenated fragment comes from repeatPlaceholders, which
	// emits nothing but ", ?" — every incident id travels as a bound
	// argument, so no caller-controlled text reaches the statement.
	//nolint:gosec // G202: placeholders only; ids are bound parameters
	q := alarmIncidentSourceSelect + ` WHERE incident_id IN (?` +
		repeatPlaceholders(len(incidentIDs)-1) + `) ORDER BY at_ms ASC, id ASC`
	args := make([]any, 0, len(incidentIDs))
	for _, id := range incidentIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alarm incident sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		row, err := scanAlarmIncidentSource(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm incident source: %w", err)
		}
		out[row.IncidentID] = append(out[row.IncidentID], row)
	}
	return out, rows.Err()
}

// PurgeOrphans deletes source rows whose incident no longer exists.
// The incident retention purge is a plain DELETE without a cascade, so
// without this the ledger would outlive every incident it describes.
func (s *AlarmIncidentSourceStore) PurgeOrphans(ctx context.Context) (int64, error) {
	const q = `
DELETE FROM alarm_incident_sources
WHERE incident_id NOT IN (SELECT id FROM alarm_incidents)`
	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge orphaned alarm incident sources: %w", err)
	}
	return res.RowsAffected()
}

// repeatPlaceholders returns n repetitions of ", ?" for an IN clause
// that already carries its first placeholder.
func repeatPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*3)
	for range n {
		buf = append(buf, ',', ' ', '?')
	}
	return string(buf)
}

func scanAlarmIncidentSource(sc scannable) (AlarmIncidentSource, error) {
	var r AlarmIncidentSource
	err := sc.Scan(&r.ID, &r.IncidentID, &r.ZoneID, &r.Ref, &r.CentralName, &r.InterfaceID,
		&r.ChannelAddress, &r.DeviceAddress, &r.Parameter, &r.SensorID, &r.Name,
		&r.SensorType, &r.Class, &r.Cause, &r.AtMS)
	return r, err
}

const alarmIncidentSourceSelect = `
SELECT id, incident_id, zone_id, ref, central_name, interface_id, channel_address,
       device_address, parameter, sensor_id, name, sensor_type, class, cause, at_ms
FROM alarm_incident_sources`
