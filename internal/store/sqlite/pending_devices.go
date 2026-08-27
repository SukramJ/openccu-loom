// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PendingDevice is one device held back from the model because
// `delay_new_device_creation` is enabled and no operator has accepted it
// yet.
//
// It carries no descriptions. The boot pull delivers a fresh set on every
// start, so persisting them here would keep a second copy that can go
// stale — and would let a device unpaired while the daemon was down
// reappear on the inbox surface with nothing behind it. Model is kept
// only so the inbox can name the device before the pull has run.
type PendingDevice struct {
	CentralName string
	InterfaceID string
	Address     string
	Model       string
	// FirstSeen is the RFC3339 instant the device was first announced.
	// It is what lets an operator tell a decision they postponed
	// yesterday from one that arrived a minute ago.
	FirstSeen string
}

// PendingDeviceStore persists the deferred-creation queue in the main
// application database.
type PendingDeviceStore struct {
	db *sql.DB
}

// NewPendingDeviceStore returns a store backed by the main app database.
func NewPendingDeviceStore(db *sql.DB) *PendingDeviceStore {
	return &PendingDeviceStore{db: db}
}

// ListByCentral returns every held-back device of one central.
func (s *PendingDeviceStore) ListByCentral(ctx context.Context, centralName string) ([]PendingDevice, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT central_name, interface_id, address, model, first_seen
          FROM pending_devices
         WHERE central_name = ?
         ORDER BY interface_id, address
    `, centralName)
	if err != nil {
		return nil, fmt.Errorf("pending_devices.ListByCentral: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PendingDevice
	for rows.Next() {
		var p PendingDevice
		if err := rows.Scan(&p.CentralName, &p.InterfaceID, &p.Address, &p.Model, &p.FirstSeen); err != nil {
			return nil, fmt.Errorf("pending_devices.ListByCentral scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending_devices.ListByCentral rows: %w", err)
	}
	return out, nil
}

// Put records a device as held back. Idempotent per
// (central, interface, address): a re-announcement of an already-parked
// device keeps the original FirstSeen, so the age an operator sees is the
// age of the decision, not of the last CCU reconnect.
func (s *PendingDeviceStore) Put(ctx context.Context, p PendingDevice) error {
	if s == nil || s.db == nil {
		return nil
	}
	if p.FirstSeen == "" {
		p.FirstSeen = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO pending_devices (central_name, interface_id, address, model, first_seen)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(central_name, interface_id, address) DO UPDATE SET
            model = excluded.model
    `, p.CentralName, p.InterfaceID, p.Address, p.Model, p.FirstSeen)
	if err != nil {
		return fmt.Errorf("pending_devices.Put: %w", err)
	}
	return nil
}

// Delete drops one held-back device — the operator accepted it, or the
// CCU no longer reports it.
func (s *PendingDeviceStore) Delete(ctx context.Context, centralName, interfaceID, address string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM pending_devices
         WHERE central_name = ? AND interface_id = ? AND address = ?
    `, centralName, interfaceID, address)
	if err != nil {
		return fmt.Errorf("pending_devices.Delete: %w", err)
	}
	return nil
}

// DeleteByCentral drops every held-back device of one central. This is
// the `delay_new_device_creation` off-switch: the toggle means "ask me
// about new devices", so turning it off means "stop asking" — the queue
// is released rather than left as state an operator could only clear
// through the database.
func (s *PendingDeviceStore) DeleteByCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_devices WHERE central_name = ?`, centralName)
	if err != nil {
		return fmt.Errorf("pending_devices.DeleteByCentral: %w", err)
	}
	return nil
}
