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

// ErrPreferenceNotFound is returned by Get when no row exists for the
// (subject, key) pair.
var ErrPreferenceNotFound = errors.New("sqlite: preference not found")

// UserPreferencesStore persists per-user UI state as opaque JSON blobs
// addressed by (subject, key). The daemon never interprets the value;
// the SPA owns its schema (e.g. key "favorites" holds a pinned-items
// array). This backs cross-device persistence of preferences such as
// favorites / dashboard layout.
type UserPreferencesStore struct {
	db *sql.DB
}

// NewUserPreferencesStore returns a store backed by db.
func NewUserPreferencesStore(db *sql.DB) *UserPreferencesStore {
	return &UserPreferencesStore{db: db}
}

// Get returns the stored JSON value for (subject, key), or
// ErrPreferenceNotFound when absent.
func (s *UserPreferencesStore) Get(ctx context.Context, subject, key string) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrPreferenceNotFound
	}
	var value string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT value_json FROM user_preferences WHERE subject = ? AND key = ?`,
		subject, key,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPreferenceNotFound
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get preference: %w", err)
	}
	return value, nil
}

// Set upserts the JSON value for (subject, key). valueJSON is stored
// verbatim; the caller is responsible for it being valid JSON.
func (s *UserPreferencesStore) Set(ctx context.Context, subject, key, valueJSON string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite: user-preferences store unavailable")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO user_preferences (subject, key, value_json, updated_unix)
         VALUES (?, ?, ?, ?)
         ON CONFLICT(subject, key) DO UPDATE SET
           value_json = excluded.value_json,
           updated_unix = excluded.updated_unix`,
		subject, key, valueJSON, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("sqlite: set preference: %w", err)
	}
	return nil
}

// Delete removes the (subject, key) row. Deleting a missing row is not
// an error.
func (s *UserPreferencesStore) Delete(ctx context.Context, subject, key string) error {
	if s == nil || s.db == nil {
		return errors.New("sqlite: user-preferences store unavailable")
	}
	_, err := s.db.ExecContext(
		ctx,
		`DELETE FROM user_preferences WHERE subject = ? AND key = ?`,
		subject, key,
	)
	if err != nil {
		return fmt.Errorf("sqlite: delete preference: %w", err)
	}
	return nil
}
