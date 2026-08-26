// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Setting keys for persisted writable cluster attributes. Matter
// §11.1.6 marks NodeLabel (0x0005) and Location (0x0006) with the "N"
// (non-volatile) quality — a commissioner write must survive a daemon
// restart. matter.js persists them through the behavior state storage
// (BasicInformationServer state with persistent qualities).
const (
	// SettingNodeLabel holds the commissioner-written
	// BasicInformation.NodeLabel.
	SettingNodeLabel = "basic_information.node_label"
	// SettingLocation holds the commissioner-written
	// BasicInformation.Location (ISO-3166 2-letter code).
	SettingLocation = "basic_information.location"
	// SettingUniqueID holds BasicInformation.UniqueID. The attribute
	// carries quality F (fixed for the lifetime of the device, Matter
	// §11.1.5.13), which a derivation can only approximate: it is stable
	// exactly as long as its inputs are, so a config edit to the bridge
	// label would move the node's identity under every controller cache.
	// Persisting the value on first boot and feeding it back through
	// core.Config.UniqueID makes the attribute genuinely fixed — the same
	// shape matter.js uses (BasicInformationServer's uniqueId, quality
	// "FN": created once, restored from state afterwards).
	SettingUniqueID = "basic_information.unique_id"
	// SettingUniqueIDRotated is "1" when the value currently persisted under
	// [SettingUniqueID] was written by a boot with
	// north.matter.dev_rotate_unique_ids enabled (a per-boot salted value,
	// never meant to be pinned), and unset/"" otherwise. It lets a later
	// boot with the dev flag off recognise a leftover salted value as
	// stale — rather than pinning it as the bridge's permanent identity —
	// and re-derive the deterministic one instead.
	SettingUniqueIDRotated = "basic_information.unique_id_rotated"
)

// MetadataKeyEventNumber is the matter_metadata key holding the
// persisted EventNumber ceiling. The event log seeds its counter from
// this value at boot and re-persists a new ceiling ahead of use, so
// event numbers stay monotonic across restarts (Matter §7.14.2.1;
// chip EventManagement's counter-epoch pattern).
const MetadataKeyEventNumber = "event_number_ceiling"

// GetSetting returns the persisted string for key. ok=false when the
// key has never been written (callers keep their configured default).
func (s *Store) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM matter_settings WHERE key = ?`, key)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("matter store: get setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting upserts the persisted string for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO matter_settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("matter store: set setting %q: %w", key, err)
	}
	return nil
}

// GetMetadataCounter returns the persisted integer counter for key
// from matter_metadata. ok=false when the key has never been written.
func (s *Store) GetMetadataCounter(ctx context.Context, key string) (value uint64, ok bool, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM matter_metadata WHERE key = ?`, key)
	var v int64
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("matter store: get metadata %q: %w", key, err)
	}
	if v < 0 {
		return 0, false, fmt.Errorf("matter store: metadata %q negative: %d", key, v)
	}
	return uint64(v), true, nil
}

// SetMetadataCounter upserts the integer counter for key in
// matter_metadata.
func (s *Store) SetMetadataCounter(ctx context.Context, key string, value uint64) error {
	if value > 1<<62 {
		return fmt.Errorf("matter store: metadata %q overflows int64: %d", key, value)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO matter_metadata (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, int64(value)); err != nil { //nolint:gosec // bounded by the 1<<62 guard above
		return fmt.Errorf("matter store: set metadata %q: %w", key, err)
	}
	return nil
}
