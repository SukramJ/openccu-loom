// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Diagram-config sentinels surfaced to the REST layer for status mapping.
var (
	// ErrDiagramNotFound — no diagram with that id exists (→ 404).
	ErrDiagramNotFound = errors.New("sqlite: diagram not found")
	// ErrDiagramForbidden — the diagram is private and the caller is
	// neither its owner nor an admin (→ 403/404).
	ErrDiagramForbidden = errors.New("sqlite: diagram forbidden")
	// ErrDiagramInvalid — the write payload failed validation (→ 400/413).
	ErrDiagramInvalid = errors.New("sqlite: diagram invalid")
)

const (
	maxDiagramSeries   = 8
	maxDiagramBlobSize = 64 * 1024
)

// DiagramConfig is one named multi-series diagram definition (SV03).
type DiagramConfig struct {
	ID           string
	OwnerSubject string
	Name         string
	Visibility   string
	ConfigJSON   string
	CreatedAtMs  int64
	UpdatedAtMs  int64
}

// DiagramConfigStore persists named diagram definitions in the main app
// DB (not the history DB) so they survive/edit even when the opt-in
// history feature is off.
type DiagramConfigStore struct {
	db *sql.DB
}

// NewDiagramConfigStore returns a store backed by db.
func NewDiagramConfigStore(db *sql.DB) *DiagramConfigStore {
	return &DiagramConfigStore{db: db}
}

// validateDiagram checks the SPA-owned config blob: bounded size, a
// series list capped at [maxDiagramSeries], and every series carrying a
// non-empty central (multi-CCU routing safety). The rest of the blob is
// opaque to the daemon.
//
// Two properties of this validator are worth knowing before relying on it.
//
// Neither cap is published. The REST schema documents `config` as an opaque
// object, the SPA's diagram editor enforces no series limit and measures no
// blob size, and no i18n key exists for either message — so a diagram with
// more series than the cap is constructible in the editor, passes every check
// the operator can see, and comes back rejected with the English sentence
// below rendered verbatim in a toast. Raising or lowering the cap here does
// not change what the editor lets an operator build.
//
// The blob cap is additionally restated in the REST handler, over the whole
// request body rather than over the blob alone, so on the REST path the
// handler's is the binding one and this cap is defence-in-depth for a
// non-REST caller. The two literals are pinned to one value by
// TestW2StoDiagramBlobCapHasOneValue in tests/contract — the series cap has
// no such counterpart anywhere, which is the asymmetry above.
//
// And the check keys on the document's shape by name: json.Unmarshal ignores
// unknown keys, so a blob whose series list is not called `series`, or whose
// entries do not carry `central`, unmarshals into an empty slice, skips both
// loops and validates clean. The central check is a routing-safety check, so
// its silent form is the dangerous one — a renamed key on the SPA side turns
// it off with a 200, not with an error. TestDiagram_ValidationKeysOnSeriesKey
// measures that, so the coupling is visible rather than assumed.
func validateDiagram(name, visibility, configJSON string) error {
	if name == "" {
		return fmt.Errorf("%w: name required", ErrDiagramInvalid)
	}
	if visibility != "private" && visibility != "shared" {
		return fmt.Errorf("%w: visibility must be private or shared", ErrDiagramInvalid)
	}
	if len(configJSON) > maxDiagramBlobSize {
		return fmt.Errorf("%w: config too large", ErrDiagramInvalid)
	}
	if configJSON == "" {
		return nil
	}
	var cfg struct {
		Series []struct {
			Central string `json:"central"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("%w: config not valid JSON", ErrDiagramInvalid)
	}
	if len(cfg.Series) > maxDiagramSeries {
		return fmt.Errorf("%w: at most %d series", ErrDiagramInvalid, maxDiagramSeries)
	}
	for i, s := range cfg.Series {
		if s.Central == "" {
			return fmt.Errorf("%w: series[%d] missing central", ErrDiagramInvalid, i)
		}
	}
	return nil
}

func newDiagramID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sqlite: diagram id rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// List returns every diagram the subject may see: their own plus every
// shared diagram, ordered by name.
func (s *DiagramConfigStore) List(ctx context.Context, subject string) ([]DiagramConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, owner_subject, name, visibility, config_json, created_at_ms, updated_at_ms
          FROM diagram_configs
         WHERE owner_subject = ? OR visibility = 'shared'
         ORDER BY name COLLATE NOCASE
    `, subject)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list diagrams: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []DiagramConfig
	for rows.Next() {
		var d DiagramConfig
		if err := rows.Scan(&d.ID, &d.OwnerSubject, &d.Name, &d.Visibility,
			&d.ConfigJSON, &d.CreatedAtMs, &d.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("sqlite: scan diagram: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DiagramConfigStore) get(ctx context.Context, id string) (DiagramConfig, error) {
	var d DiagramConfig
	err := s.db.QueryRowContext(ctx, `
        SELECT id, owner_subject, name, visibility, config_json, created_at_ms, updated_at_ms
          FROM diagram_configs WHERE id = ?
    `, id).Scan(&d.ID, &d.OwnerSubject, &d.Name, &d.Visibility,
		&d.ConfigJSON, &d.CreatedAtMs, &d.UpdatedAtMs)
	if errors.Is(err, sql.ErrNoRows) {
		return DiagramConfig{}, ErrDiagramNotFound
	}
	if err != nil {
		return DiagramConfig{}, fmt.Errorf("sqlite: get diagram: %w", err)
	}
	return d, nil
}

// Get returns a diagram by id. A private diagram is visible only to its
// owner or an admin.
func (s *DiagramConfigStore) Get(ctx context.Context, id, subject string, isAdmin bool) (DiagramConfig, error) {
	if s == nil || s.db == nil {
		return DiagramConfig{}, ErrDiagramNotFound
	}
	d, err := s.get(ctx, id)
	if err != nil {
		return DiagramConfig{}, err
	}
	if d.Visibility == "private" && d.OwnerSubject != subject && !isAdmin {
		return DiagramConfig{}, ErrDiagramForbidden
	}
	return d, nil
}

// Create inserts a new diagram owned by subject.
func (s *DiagramConfigStore) Create(ctx context.Context, subject, name, visibility, configJSON string) (DiagramConfig, error) {
	if s == nil || s.db == nil {
		return DiagramConfig{}, errors.New("sqlite: diagram store unavailable")
	}
	if err := validateDiagram(name, visibility, configJSON); err != nil {
		return DiagramConfig{}, err
	}
	id, err := newDiagramID()
	if err != nil {
		return DiagramConfig{}, err
	}
	if configJSON == "" {
		configJSON = "{}"
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
        INSERT INTO diagram_configs (id, owner_subject, name, visibility, config_json, created_at_ms, updated_at_ms)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, id, subject, name, visibility, configJSON, now, now); err != nil {
		return DiagramConfig{}, fmt.Errorf("sqlite: create diagram: %w", err)
	}
	return DiagramConfig{
		ID: id, OwnerSubject: subject, Name: name, Visibility: visibility,
		ConfigJSON: configJSON, CreatedAtMs: now, UpdatedAtMs: now,
	}, nil
}

// Update replaces a diagram's name / visibility / config. Only the owner
// or an admin may update.
func (s *DiagramConfigStore) Update(ctx context.Context, id, subject string, isAdmin bool, name, visibility, configJSON string) (DiagramConfig, error) {
	if s == nil || s.db == nil {
		return DiagramConfig{}, errors.New("sqlite: diagram store unavailable")
	}
	if err := validateDiagram(name, visibility, configJSON); err != nil {
		return DiagramConfig{}, err
	}
	existing, err := s.get(ctx, id)
	if err != nil {
		return DiagramConfig{}, err
	}
	if existing.OwnerSubject != subject && !isAdmin {
		return DiagramConfig{}, ErrDiagramForbidden
	}
	if configJSON == "" {
		configJSON = "{}"
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
        UPDATE diagram_configs SET name = ?, visibility = ?, config_json = ?, updated_at_ms = ?
         WHERE id = ?
    `, name, visibility, configJSON, now, id); err != nil {
		return DiagramConfig{}, fmt.Errorf("sqlite: update diagram: %w", err)
	}
	existing.Name, existing.Visibility, existing.ConfigJSON, existing.UpdatedAtMs = name, visibility, configJSON, now
	return existing, nil
}

// Delete removes a diagram. Only the owner or an admin may delete.
func (s *DiagramConfigStore) Delete(ctx context.Context, id, subject string, isAdmin bool) error {
	if s == nil || s.db == nil {
		return ErrDiagramNotFound
	}
	existing, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if existing.OwnerSubject != subject && !isAdmin {
		return ErrDiagramForbidden
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM diagram_configs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete diagram: %w", err)
	}
	return nil
}
