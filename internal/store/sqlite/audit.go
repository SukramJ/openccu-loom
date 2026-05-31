// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// AuditStore persists user-initiated configuration changes for the SPA's
// change-history view.
//
// Writes are append-only; the SPA filters / paginates the read side.
type AuditStore struct {
	db *sql.DB
}

// NewAuditStore returns a store backed by db.
func NewAuditStore(db *sql.DB) *AuditStore { return &AuditStore{db: db} }

// Append inserts one audit entry. The caller is expected to have
// stamped Entry.Timestamp; if zero, the SQL default (CURRENT_TIMESTAMP)
// fills in.
func (s *AuditStore) Append(ctx context.Context, entry audit.Entry) error {
	if s == nil || s.db == nil {
		return nil
	}
	var changes string
	if len(entry.Changes) > 0 {
		raw, err := json.Marshal(entry.Changes)
		if err != nil {
			return fmt.Errorf("sqlite: encode audit changes: %w", err)
		}
		changes = string(raw)
	}
	const q = `
INSERT INTO audit_log (timestamp, user, action, device_address, channel_no, paramset, peer, parameter, note, changes_json)
VALUES (
    COALESCE(?, CURRENT_TIMESTAMP),
    NULLIF(?, ''),
    ?,
    NULLIF(?, ''),
    NULLIF(?, 0),
    NULLIF(?, ''),
    NULLIF(?, ''),
    NULLIF(?, ''),
    NULLIF(?, ''),
    NULLIF(?, '')
)`
	var ts any
	if !entry.Timestamp.IsZero() {
		ts = entry.Timestamp.UTC().Format("2006-01-02 15:04:05")
	}
	_, err := s.db.ExecContext(
		ctx, q,
		ts,
		entry.User,
		string(entry.Action),
		entry.DeviceAddress,
		entry.ChannelNo,
		entry.Paramset,
		entry.Peer,
		entry.Parameter,
		entry.Note,
		changes,
	)
	if err != nil {
		return fmt.Errorf("sqlite: append audit entry: %w", err)
	}
	return nil
}

// List returns the most-recent audit entries. limit <= 0 means "all".
// Filter is intentionally narrow (only deviceAddress); the SPA does the
// rest in memory after fetching.
func (s *AuditStore) List(ctx context.Context, deviceAddress string, limit int) ([]audit.Entry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	args := []any{}
	q := `SELECT timestamp, COALESCE(user, ''), action, COALESCE(device_address, ''),
            COALESCE(channel_no, 0), COALESCE(paramset, ''), COALESCE(peer, ''),
            COALESCE(parameter, ''), COALESCE(note, ''), COALESCE(changes_json, '')
          FROM audit_log`
	if deviceAddress != "" {
		q += ` WHERE device_address = ?`
		args = append(args, deviceAddress)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []audit.Entry
	for rows.Next() {
		var (
			ts      time.Time
			entry   audit.Entry
			action  string
			changes string
		)
		if err := rows.Scan(
			&ts, &entry.User, &action, &entry.DeviceAddress,
			&entry.ChannelNo, &entry.Paramset, &entry.Peer,
			&entry.Parameter, &entry.Note, &changes,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan audit row: %w", err)
		}
		entry.Timestamp = ts.UTC()
		entry.Action = audit.Action(action)
		if changes != "" {
			if err := json.Unmarshal([]byte(changes), &entry.Changes); err != nil {
				return nil, fmt.Errorf("sqlite: decode audit changes: %w", err)
			}
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
