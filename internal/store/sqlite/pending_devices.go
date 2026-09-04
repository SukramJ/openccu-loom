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
//
// loom:reachable:reason="argument and result type of PendingDeviceStore's Put/ListByCentral, which adapter.pendingSink calls on every park, accept and restore; a method-less row struct the analyzer's type heuristic cannot see used"
type PendingDevice struct {
	CentralName string
	InterfaceID string
	Address     string
	Model       string
	// FirstSeen is the RFC3339 instant the device was first announced.
	// It is what lets an operator tell a decision they postponed
	// yesterday from one that arrived a minute ago.
	FirstSeen string
	// Phase is where the device stands in onboarding: "pending" (held
	// out of the model) or "unreleased" (materialised and configurable,
	// withheld from the ecosystems). An absent row means fully
	// onboarded. The store persists the string it is given and never
	// interprets it.
	Phase string
}

// PhasePending is the phase a row gets when [PendingDeviceStore.Put] is
// handed none — the device is held out of the model entirely: no ise_id,
// no channels, nothing to configure. It matches the column default that
// migration 042_pending_devices_phase.sql declares.
//
// The vocabulary itself is owned by internal/central/coordinators
// (PhasePending / PhaseUnreleased); that is what the onboarding loop
// writes and reads, and the adapter passes it through untranslated. A
// second "unreleased" constant lived here with no reader or writer, so
// editing it changed nothing while looking like it changed the persisted
// vocabulary; it is gone.
const PhasePending = "pending"

// PendingDeviceStore persists the deferred-creation queue in the main
// application database.
//
// loom:reachable:reason="constructed in cmd/openccu-loom/daemon.go and reached through adapter.pendingSink, which the coordinator holds as a coordinators.PendingDeviceSink; the analyzer loses the trail at the interface boundary"
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
        SELECT central_name, interface_id, address, model, first_seen, phase
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
		if err := rows.Scan(&p.CentralName, &p.InterfaceID, &p.Address, &p.Model, &p.FirstSeen, &p.Phase); err != nil {
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
	if p.Phase == "" {
		p.Phase = PhasePending
	}
	// The phase is NOT refreshed on conflict: a re-announcement of a
	// device that has already been accepted must not drag it back to
	// pending and un-materialise it on the next boot. Phase moves only
	// through SetPhase, which is the wizard advancing.
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO pending_devices (central_name, interface_id, address, model, first_seen, phase)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(central_name, interface_id, address) DO UPDATE SET
            model = excluded.model
    `, p.CentralName, p.InterfaceID, p.Address, p.Model, p.FirstSeen, p.Phase)
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

// SetPhase advances a held device to the next onboarding phase, keeping
// its FirstSeen so the age an operator sees stays the age of the
// original decision. A no-op when the device is not held.
func (s *PendingDeviceStore) SetPhase(ctx context.Context, centralName, interfaceID, address, phase string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        UPDATE pending_devices SET phase = ?
         WHERE central_name = ? AND interface_id = ? AND address = ?
    `, phase, centralName, interfaceID, address)
	if err != nil {
		return fmt.Errorf("pending_devices.SetPhase: %w", err)
	}
	return nil
}
