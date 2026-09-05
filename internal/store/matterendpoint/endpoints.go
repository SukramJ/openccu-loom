// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
)

// ErrEndpointIDExhausted is returned when [Store.AssignEndpointID] runs
// out of free 2..65534 slots.
// loom:reachable:reason="returned by AssignEndpointID in this file when the 1..65534 range is full; a sentinel is part of the contract even while no caller distinguishes it yet"
var ErrEndpointIDExhausted = errors.New("matter endpoint store: endpoint id exhausted")

// GetEndpoint implements [fabricendpoint.Store]. It returns
// [fabricendpoint.ErrNotFound] when no row exists, which is what makes
// the assembler allocate a fresh id.
func (s *Store) GetEndpoint(ctx context.Context, key fabricendpoint.SourceKey) (fabricendpoint.Record, error) {
	k, err := keyOf(key)
	if err != nil {
		return fabricendpoint.Record{}, err
	}
	rec := fabricendpoint.Record{Key: k, Scope: k.CentralName}
	err = s.db.QueryRowContext(
		ctx, `
SELECT endpoint_id, device_type FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		k.CentralName, k.DeviceAddress, k.ChannelNo, string(k.DPKind), k.DPKey,
	).Scan(&rec.EndpointID, &rec.DeviceType)
	if errors.Is(err, sql.ErrNoRows) {
		return fabricendpoint.Record{}, fabricendpoint.ErrNotFound
	}
	if err != nil {
		return fabricendpoint.Record{}, fmt.Errorf("matter endpoint store: get endpoint: %w", err)
	}
	return rec, nil
}

// ListEndpoints implements [fabricendpoint.Store]. Records come back
// ordered by endpoint_id ascending, filtered to one central (empty scope
// = no filter).
func (s *Store) ListEndpoints(ctx context.Context, scope string) ([]fabricendpoint.Record, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if scope == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints ORDER BY endpoint_id ASC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints WHERE central_name = ? ORDER BY endpoint_id ASC`, scope)
	}
	if err != nil {
		return nil, fmt.Errorf("matter endpoint store: list endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []fabricendpoint.Record
	for rows.Next() {
		var (
			key  SourceKey
			kind string
			rec  fabricendpoint.Record
		)
		if err := rows.Scan(&key.CentralName, &key.DeviceAddress, &key.ChannelNo,
			&kind, &key.DPKey, &rec.EndpointID, &rec.DeviceType); err != nil {
			return nil, fmt.Errorf("matter endpoint store: list endpoints: scan: %w", err)
		}
		key.DPKind = DPKind(kind)
		rec.Key = key
		rec.Scope = key.CentralName
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter endpoint store: list endpoints: rows: %w", err)
	}
	return out, nil
}

// UpsertEndpoint inserts or updates a record. The 5-tuple primary key
// controls identity; updates rewrite endpoint_id / device_type and bump
// updated_at. Use [Store.UpsertEndpointAssigning] to allocate a fresh
// endpoint_id for an unmapped key.
func (s *Store) UpsertEndpoint(ctx context.Context, rec fabricendpoint.Record) error {
	k, err := recordKey(rec)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_endpoints
    (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE SET
    endpoint_id = excluded.endpoint_id,
    device_type = excluded.device_type,
    updated_at  = CURRENT_TIMESTAMP`,
		k.CentralName, k.DeviceAddress, k.ChannelNo,
		string(k.DPKind), k.DPKey, rec.EndpointID, rec.DeviceType,
	); err != nil {
		return fmt.Errorf("matter endpoint store: upsert endpoint: %w", err)
	}
	return nil
}

// RemoveEndpoint implements [fabricendpoint.Store]. No error when the
// row is absent (idempotent — the assembler may call this for vanished
// devices without coordinating).
func (s *Store) RemoveEndpoint(ctx context.Context, key fabricendpoint.SourceKey) error {
	k, err := keyOf(key)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(
		ctx, `
DELETE FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		k.CentralName, k.DeviceAddress, k.ChannelNo, string(k.DPKind), k.DPKey,
	); err != nil {
		return fmt.Errorf("matter endpoint store: remove endpoint: %w", err)
	}
	return nil
}

// recordKey recovers the concrete key from a record and rejects a Scope
// that contradicts it. The central is part of the primary key, so the
// two must not be able to disagree: a record written under one central's
// scope but keyed to another would land in the wrong partition and be
// garbage-collected by whichever central is listed next.
func recordKey(rec fabricendpoint.Record) (SourceKey, error) {
	k, err := keyOf(rec.Key)
	if err != nil {
		return SourceKey{}, err
	}
	if rec.Scope != "" && rec.Scope != k.CentralName {
		return SourceKey{}, fmt.Errorf(
			"matter endpoint store: record scope %q contradicts its key's central %q",
			rec.Scope, k.CentralName,
		)
	}
	return k, nil
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
		return 0, fmt.Errorf("matter endpoint store: assign endpoint id: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocateEndpointID(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter endpoint store: assign endpoint id: commit: %w", err)
	}
	return id, nil
}

// UpsertEndpointAssigning implements [fabricendpoint.Store]: if
// rec.EndpointID is 0, allocate a fresh ID under a transaction and
// insert; otherwise upsert with the caller's ID. Returns the effective
// ID. The fresh-ID path holds an exclusive lock briefly to keep two
// concurrent assemblers from picking the same ID.
func (s *Store) UpsertEndpointAssigning(ctx context.Context, rec fabricendpoint.Record) (uint16, error) {
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

func (s *Store) upsertEndpointAssigningOnce(ctx context.Context, rec fabricendpoint.Record) (uint16, error) {
	k, err := recordKey(rec)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter endpoint store: upsert endpoint assigning: begin tx: %w", err)
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
		k.CentralName, k.DeviceAddress, k.ChannelNo,
		string(k.DPKind), k.DPKey, id, rec.DeviceType,
	); err != nil {
		return 0, fmt.Errorf("matter endpoint store: upsert endpoint assigning: insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter endpoint store: upsert endpoint assigning: commit: %w", err)
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
		return nil, 0, fmt.Errorf("matter endpoint store: next endpoint id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	occupied = make(map[uint16]struct{}, 64)
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return nil, 0, fmt.Errorf("matter endpoint store: next endpoint id: scan: %w", err)
		}
		if got < firstBridgedEndpointID || got > lastBridgedEndpointID {
			return nil, 0, fmt.Errorf("matter endpoint store: next endpoint id: stored value %d out of range", got)
		}
		id := uint16(got) //nolint:gosec // bounded by the range check above
		occupied[id] = struct{}{}
		if id > highest {
			highest = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("matter endpoint store: next endpoint id: rows: %w", err)
	}
	return occupied, highest, nil
}
