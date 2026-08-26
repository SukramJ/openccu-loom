// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DPKind classifies a Matter endpoint's source within the model.
// Persisted as a TEXT column with CHECK constraint — the database
// rejects unknown kinds at insert time.
type DPKind string

// DPKind values. The string forms match the dp_kind CHECK constraint
// in migration 007.
const (
	DPKindCustom      DPKind = "custom"
	DPKindGeneric     DPKind = "generic"
	DPKindCalculated  DPKind = "calculated"
	DPKindCombined    DPKind = "combined"
	DPKindMeasurement DPKind = "measurement"
)

// EndpointKey uniquely identifies the model-side source of a Matter
// endpoint. The 5-tuple is the primary key of matter_endpoints.
type EndpointKey struct {
	CentralName   string
	DeviceAddress string
	ChannelNo     int
	DPKind        DPKind
	DPKey         string
}

// EndpointRecord is the on-disk shape of a (source → endpoint_id)
// mapping. EndpointID is in [1, 65534] (endpoint 0 is the root bridge
// endpoint and is not stored).
type EndpointRecord struct {
	Key        EndpointKey
	EndpointID uint16
	DeviceType uint16
}

// Sentinel errors specific to the endpoint table.
var (
	// ErrEndpointNotFound is returned when a key lookup misses.
	ErrEndpointNotFound = errors.New("matter store: endpoint not found")
	// ErrEndpointIDExhausted is returned when [Store.AssignEndpointID]
	// runs out of free 1..65534 slots.
	ErrEndpointIDExhausted = errors.New("matter store: endpoint id exhausted")
)

// GetEndpoint returns the record for key. Returns
// [ErrEndpointNotFound] when no row exists.
func (s *Store) GetEndpoint(ctx context.Context, key EndpointKey) (EndpointRecord, error) {
	rec := EndpointRecord{Key: key}
	err := s.db.QueryRowContext(
		ctx, `
SELECT endpoint_id, device_type FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	).Scan(&rec.EndpointID, &rec.DeviceType)
	if errors.Is(err, sql.ErrNoRows) {
		return EndpointRecord{}, ErrEndpointNotFound
	}
	if err != nil {
		return EndpointRecord{}, fmt.Errorf("matter store: get endpoint: %w", err)
	}
	return rec, nil
}

// ListEndpoints returns every record ordered by endpoint_id ascending.
// Optionally filtered by central_name (empty string = no filter).
func (s *Store) ListEndpoints(ctx context.Context, centralName string) ([]EndpointRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if centralName == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints ORDER BY endpoint_id ASC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints WHERE central_name = ? ORDER BY endpoint_id ASC`, centralName)
	}
	if err != nil {
		return nil, fmt.Errorf("matter store: list endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EndpointRecord
	for rows.Next() {
		var (
			rec  EndpointRecord
			kind string
		)
		if err := rows.Scan(&rec.Key.CentralName, &rec.Key.DeviceAddress, &rec.Key.ChannelNo,
			&kind, &rec.Key.DPKey, &rec.EndpointID, &rec.DeviceType); err != nil {
			return nil, fmt.Errorf("matter store: list endpoints: scan: %w", err)
		}
		rec.Key.DPKind = DPKind(kind)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list endpoints: rows: %w", err)
	}
	return out, nil
}

// UpsertEndpoint inserts or updates a record. The (5-tuple) primary
// key controls identity; updates rewrite endpoint_id / device_type
// and bump updated_at. Use [Store.AssignEndpointID] to allocate a
// fresh endpoint_id for an unmapped key.
func (s *Store) UpsertEndpoint(ctx context.Context, rec EndpointRecord) error {
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_endpoints
    (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE SET
    endpoint_id = excluded.endpoint_id,
    device_type = excluded.device_type,
    updated_at  = CURRENT_TIMESTAMP`,
		rec.Key.CentralName, rec.Key.DeviceAddress, rec.Key.ChannelNo,
		string(rec.Key.DPKind), rec.Key.DPKey, rec.EndpointID, rec.DeviceType,
	); err != nil {
		return fmt.Errorf("matter store: upsert endpoint: %w", err)
	}
	return nil
}

// RemoveEndpoint deletes the row for key. No error when the row is
// absent (idempotent — the assembler may call this for vanished
// devices without coordinating).
func (s *Store) RemoveEndpoint(ctx context.Context, key EndpointKey) error {
	if _, err := s.db.ExecContext(
		ctx, `
DELETE FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	); err != nil {
		return fmt.Errorf("matter store: remove endpoint: %w", err)
	}
	return nil
}

// AssignEndpointID allocates the next bridged endpoint_id in [2, 65534] and
// advances the persisted counter, so two calls never return the same number
// even when the caller has not inserted the first one yet. Numbers already
// stored in matter_endpoints are skipped; numbers released by
// [Store.RemoveEndpoint] are retired rather than reissued (see
// [allocateEndpointID]).
//
// Typical use is inside a transaction:
//
//	tx, _ := db.Begin()
//	id := AssignEndpointID(ctx, tx)
//	tx.ExecContext(ctx, "INSERT INTO matter_endpoints ... endpoint_id=?", id)
//	tx.Commit()
//
// For convenience, [Store.UpsertEndpointAssigning] does both in one
// transaction.
func (s *Store) AssignEndpointID(ctx context.Context) (uint16, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter store: assign endpoint id: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocateEndpointID(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter store: assign endpoint id: commit: %w", err)
	}
	return id, nil
}

// UpsertEndpointAssigning is the common-case helper: if rec.EndpointID
// is 0, allocate a fresh ID under a transaction and insert; otherwise
// upsert with the caller's ID. Returns the effective ID. The fresh-ID
// path holds an exclusive lock briefly to keep two concurrent
// assemblers from picking the same ID.
func (s *Store) UpsertEndpointAssigning(ctx context.Context, rec EndpointRecord) (uint16, error) {
	// The transaction reads (nextFreeEndpointID) before it writes, so under
	// WAL a concurrent writer — the device-load pipeline during a busy boot
	// with a large fleet — can make the read→write upgrade fail immediately
	// with SQLITE_BUSY, a case the connection's busy_timeout does not retry.
	// Retry the whole transaction a bounded number of times so Matter endpoint
	// assembly survives boot-time write contention instead of failing the
	// bridge bring-up with "database is locked".
	const maxAttempts = 30
	backoff := 5 * time.Millisecond
	for attempt := 1; ; attempt++ {
		id, err := s.upsertEndpointAssigningOnce(ctx, rec)
		if err == nil || attempt >= maxAttempts || !isSQLiteBusy(err) {
			return id, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}

// isSQLiteBusy reports whether err is a SQLite BUSY / locked error — the
// transient class a bounded retry can clear once the current writer commits.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy")
}

func (s *Store) upsertEndpointAssigningOnce(ctx context.Context, rec EndpointRecord) (uint16, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id := rec.EndpointID
	if id == 0 {
		next, err := allocateEndpointID(ctx, tx)
		if err != nil {
			return 0, err
		}
		id = next
	}

	if _, err := tx.ExecContext(
		ctx, `
INSERT INTO matter_endpoints
    (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE SET
    endpoint_id = excluded.endpoint_id,
    device_type = excluded.device_type,
    updated_at  = CURRENT_TIMESTAMP`,
		rec.Key.CentralName, rec.Key.DeviceAddress, rec.Key.ChannelNo,
		string(rec.Key.DPKind), rec.Key.DPKey, id, rec.DeviceType,
	); err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: commit: %w", err)
	}
	return id, nil
}

// Bridged endpoint numbers occupy [2, 65534]: 0 is the RootNode and 1 the
// Aggregator of the bridge topology. Mirrors matter.js's
// `ServerNode.create(...).add(aggregator).add(child)`, which always lands
// bridged endpoints at 2..N.
const (
	firstBridgedEndpointID = 2
	lastBridgedEndpointID  = 65534
	bridgedEndpointIDSlots = lastBridgedEndpointID - firstBridgedEndpointID + 1
)

// allocateEndpointID hands out the next bridged endpoint number and advances
// the persisted high-water mark inside tx, so the allocation and the counter
// commit together. SQLite locks the table at COMMIT, not at SELECT, so callers
// must wrap the SELECT-then-INSERT in the same transaction (see
// [Store.UpsertEndpointAssigning]).
//
// Allocation is monotonic, never hole-filling: a number released by
// [Store.RemoveEndpoint] — an unpaired device, or a channel the operator
// de-exposed — is not handed to a different source afterwards. Controllers key
// their accessory cache on the endpoint number, and the Aggregator's
// Descriptor.PartsList set is unchanged by a remove-then-add pair, so a
// reissued number signals nothing: the controller keeps the removed device's
// cached device type and cluster set and renders the new device under its
// identity until the bridge is removed and re-added by hand.
//
// Mirrors matter.js packages/node/src/storage/server/ServerEndpointStores.ts
// assignNumber, which allocates from the persisted #nextNumber and skips
// numbers held in #allocatedNumbers / #preAllocatedNumbers; eraseStoreForEndpoint
// drops a number from those sets but never rewinds the counter.
func allocateEndpointID(ctx context.Context, tx *sql.Tx) (uint16, error) {
	occupied, highest, err := occupiedEndpointIDs(ctx, tx)
	if err != nil {
		return 0, err
	}
	if len(occupied) >= bridgedEndpointIDSlots {
		return 0, ErrEndpointIDExhausted
	}

	candidate, ok, err := getNextEndpointIDFromMetadata(ctx, tx)
	if err != nil {
		return 0, err
	}
	if !ok {
		candidate = firstBridgedEndpointID
	}
	// Rows can sit above the counter: a database migrated from the former
	// hole-filling allocator, or a row written with an explicit number. Lift
	// the counter past them so those numbers are not reissued either — the
	// equivalent of matter.js pre-allocating every persisted number at load().
	// Skipped once the number space has wrapped, where the occupancy scan
	// below is the only guard left.
	if highest >= candidate && highest < lastBridgedEndpointID {
		candidate = highest + 1
	}

	// Skip numbers still in use. The capacity check above guarantees at least
	// one free slot, so the walk terminates.
	for range bridgedEndpointIDSlots {
		if _, taken := occupied[candidate]; !taken {
			break
		}
		candidate = endpointIDAfter(candidate)
	}
	if _, taken := occupied[candidate]; taken {
		return 0, ErrEndpointIDExhausted
	}

	if err := setNextEndpointID(ctx, tx, endpointIDAfter(candidate)); err != nil {
		return 0, err
	}
	return candidate, nil
}

// endpointIDAfter returns the number following id, wrapping the top of the
// bridged range back to its start. Mirrors matter.js's
// `this.#nextNumber = (this.#nextNumber + 1) % 0xffff` in
// ServerEndpointStores.ts assignNumber.
func endpointIDAfter(id uint16) uint16 {
	if id >= lastBridgedEndpointID {
		return firstBridgedEndpointID
	}
	return id + 1
}

// occupiedEndpointIDs reads every stored endpoint number in tx and returns the
// lookup set plus the highest value seen (0 when the table is empty).
func occupiedEndpointIDs(ctx context.Context, tx *sql.Tx) (occupied map[uint16]struct{}, highest uint16, err error) {
	rows, err := tx.QueryContext(ctx, `
SELECT endpoint_id FROM matter_endpoints ORDER BY endpoint_id ASC`)
	if err != nil {
		return nil, 0, fmt.Errorf("matter store: next endpoint id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	occupied = make(map[uint16]struct{}, 64)
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return nil, 0, fmt.Errorf("matter store: next endpoint id: scan: %w", err)
		}
		if got < firstBridgedEndpointID || got > lastBridgedEndpointID {
			return nil, 0, fmt.Errorf("matter store: next endpoint id: stored value %d out of range", got)
		}
		id := uint16(got) //nolint:gosec // bounded by the range check above; see #20
		occupied[id] = struct{}{}
		if id > highest {
			highest = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("matter store: next endpoint id: rows: %w", err)
	}
	return occupied, highest, nil
}
