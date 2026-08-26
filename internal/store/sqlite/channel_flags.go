// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ChannelFlag is one operator-set per-channel override (G12). 'Hidden'
// removes the channel from operation lists / MQTT discovery / Matter
// exposure (guest views) without touching the CCU; 'Locked' blocks control
// writes (the VALUES paramset) to the channel while leaving reads intact.
// Keyed on (central, channel address) — one physical channel instance.
type ChannelFlag struct {
	CentralName    string
	ChannelAddress string
	Hidden         bool
	Locked         bool
	UpdatedBy      string
	UpdatedAt      string
}

// ChannelFlagsStore persists per-channel operator overrides in the main
// application database.
type ChannelFlagsStore struct {
	db *sql.DB
}

// NewChannelFlagsStore returns a store backed by the main app database.
func NewChannelFlagsStore(db *sql.DB) *ChannelFlagsStore {
	return &ChannelFlagsStore{db: db}
}

// List returns every channel flag across all centrals. Used once at wire
// time to populate the in-memory overlay.
func (s *ChannelFlagsStore) List(ctx context.Context) ([]ChannelFlag, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT central_name, channel_address, hidden, locked, updated_by, updated_at
          FROM channel_flags
    `)
	if err != nil {
		return nil, fmt.Errorf("channel_flags.List: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChannelFlag
	for rows.Next() {
		var f ChannelFlag
		var hidden, locked int
		if err := rows.Scan(&f.CentralName, &f.ChannelAddress, &hidden, &locked,
			&f.UpdatedBy, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("channel_flags.List scan: %w", err)
		}
		f.Hidden = hidden != 0
		f.Locked = locked != 0
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel_flags.List rows: %w", err)
	}
	return out, nil
}

// Set upserts the flags for one channel. When both flags are false the row
// is deleted, so a channel with no override carries no row.
func (s *ChannelFlagsStore) Set(
	ctx context.Context,
	centralName, channelAddress string,
	hidden, locked bool, updatedBy string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	if !hidden && !locked {
		return s.clear(ctx, centralName, channelAddress)
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO channel_flags
            (central_name, channel_address, hidden, locked, updated_by, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(central_name, channel_address)
        DO UPDATE SET hidden = excluded.hidden,
                      locked = excluded.locked,
                      updated_by = excluded.updated_by,
                      updated_at = excluded.updated_at
    `, centralName, channelAddress, boolToInt(hidden), boolToInt(locked),
		updatedBy, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("channel_flags.Set: %w", err)
	}
	return nil
}

// clear removes the flag row for one channel (both flags off).
func (s *ChannelFlagsStore) clear(ctx context.Context, centralName, channelAddress string) error {
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM channel_flags WHERE central_name = ? AND channel_address = ?
    `, centralName, channelAddress)
	if err != nil {
		return fmt.Errorf("channel_flags.clear: %w", err)
	}
	return nil
}

// DeleteDevice removes every channel flag for every channel of the given
// device. Called on device-remove / unpair. Prefix-safe ("DEVICE" never
// matches "DEVICE2:0").
func (s *ChannelFlagsStore) DeleteDevice(ctx context.Context, centralName, deviceAddress string) error {
	if s == nil || s.db == nil {
		return nil
	}
	prefix := deviceAddress + ":"
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM channel_flags
         WHERE central_name = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, deviceAddress, prefix)
	if err != nil {
		return fmt.Errorf("channel_flags.DeleteDevice: %w", err)
	}
	return nil
}

// DeleteForCentral removes every channel flag for a central. Called on live
// central removal.
func (s *ChannelFlagsStore) DeleteForCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_flags WHERE central_name = ?`, centralName); err != nil {
		return fmt.Errorf("channel_flags.DeleteForCentral: %w", err)
	}
	return nil
}
