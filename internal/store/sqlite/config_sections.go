// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ConfigSectionStore persists typed JSON snapshots of runtime
// config sections (north.mqtt, north.matter, callback, etc.). Each
// section is one row; the SPA's section-aware editor maps 1:1.
type ConfigSectionStore struct {
	db *sql.DB
}

// NewConfigSectionStore returns a store backed by db.
func NewConfigSectionStore(db *sql.DB) *ConfigSectionStore {
	return &ConfigSectionStore{db: db}
}

// SectionRow is one section snapshot.
type SectionRow struct {
	Section   string
	ValueJSON []byte
	Version   int
	UpdatedAt time.Time
	UpdatedBy string
}

// ErrSectionNotFound is returned for missing sections.
var ErrSectionNotFound = errors.New("sqlite: config section not found")

// Get fetches one section. Returns [ErrSectionNotFound] when the
// section has never been written.
func (s *ConfigSectionStore) Get(ctx context.Context, section string) (SectionRow, error) {
	var r SectionRow
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT section, value_json, version, updated_at, updated_by
		 FROM config_sections WHERE section = ?`, section).
		Scan(&r.Section, &raw, &r.Version, &r.UpdatedAt, &r.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return SectionRow{}, ErrSectionNotFound
	}
	if err != nil {
		return SectionRow{}, fmt.Errorf("sqlite: config_sections get: %w", err)
	}
	r.ValueJSON = []byte(raw)
	return r, nil
}

// Put stores or replaces a section. Bumps version on every write.
func (s *ConfigSectionStore) Put(ctx context.Context, section string, valueJSON []byte, updatedBy string) (SectionRow, error) {
	if section == "" {
		return SectionRow{}, errors.New("sqlite: config section name required")
	}
	if len(valueJSON) == 0 {
		return SectionRow{}, errors.New("sqlite: config section value required")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SectionRow{}, fmt.Errorf("sqlite: config_sections put: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM config_sections WHERE section = ?`, section).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		version = 1
	} else if err != nil {
		return SectionRow{}, fmt.Errorf("sqlite: config_sections put: version select: %w", err)
	} else {
		version++
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO config_sections (section, value_json, version, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(section) DO UPDATE SET value_json=excluded.value_json,
		     version=excluded.version, updated_at=excluded.updated_at,
		     updated_by=excluded.updated_by`,
		section, string(valueJSON), version, now, updatedBy)
	if err != nil {
		return SectionRow{}, fmt.Errorf("sqlite: config_sections put: exec: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SectionRow{}, fmt.Errorf("sqlite: config_sections put: commit: %w", err)
	}
	return SectionRow{
		Section:   section,
		ValueJSON: valueJSON,
		Version:   version,
		UpdatedAt: now,
		UpdatedBy: updatedBy,
	}, nil
}

// Delete removes a section, reverting to defaults on next load.
func (s *ConfigSectionStore) Delete(ctx context.Context, section string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM config_sections WHERE section = ?`, section)
	if err != nil {
		return fmt.Errorf("sqlite: config_sections delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSectionNotFound
	}
	return nil
}

// List returns every section row sorted by name.
func (s *ConfigSectionStore) List(ctx context.Context) ([]SectionRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT section, value_json, version, updated_at, updated_by
		 FROM config_sections ORDER BY section`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: config_sections list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SectionRow
	for rows.Next() {
		var r SectionRow
		var raw string
		if err := rows.Scan(&r.Section, &raw, &r.Version, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, fmt.Errorf("sqlite: config_sections list scan: %w", err)
		}
		r.ValueJSON = []byte(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}
