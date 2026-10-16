// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"context"
	"errors"
	"fmt"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
)

// ErrKeyNotDecodable is returned when a source key's rendering does not
// split back into the same key. go-fabric's sqlitestore persists only the
// rendering and lists rows back through [DecodeSourceKey]; a key that does
// not survive that round trip would come back as a foreign key, look
// vanished to the assembler's garbage collection, and lose its endpoint
// number on the next model-complete assembly. Refusing the write makes that
// one source fail loudly instead of silently re-identifying it under a
// paired controller.
var ErrKeyNotDecodable = errors.New("matter endpoint store: source key does not decode back to itself")

// GetEndpoint implements [fabricendpoint.Store]. It returns
// [fabricendpoint.ErrNotFound] when no row exists, which is what makes
// the assembler allocate a fresh id.
func (s *Store) GetEndpoint(ctx context.Context, key fabricendpoint.SourceKey) (fabricendpoint.Record, error) {
	k, err := keyOf(key)
	if err != nil {
		return fabricendpoint.Record{}, err
	}
	return s.endpoints.GetEndpoint(ctx, k)
}

// ListEndpoints implements [fabricendpoint.Store]. Records come back
// ordered by endpoint_id ascending, filtered to one central (empty scope
// = no filter), their keys rebuilt by [DecodeSourceKey].
func (s *Store) ListEndpoints(ctx context.Context, scope string) ([]fabricendpoint.Record, error) {
	return s.endpoints.ListEndpoints(ctx, scope)
}

// UpsertEndpointAssigning implements [fabricendpoint.Store]: it writes rec,
// allocating a fresh endpoint id when rec.EndpointID is 0, and returns the
// effective id. Allocation and its write-lock discipline are go-fabric's
// (sqlitestore opens the transaction with BEGIN IMMEDIATE, so the
// connection's busy_timeout covers a concurrent writer).
func (s *Store) UpsertEndpointAssigning(ctx context.Context, rec fabricendpoint.Record) (uint16, error) {
	k, err := recordKey(rec)
	if err != nil {
		return 0, err
	}
	if back, ok := parseSourceKey(k.String()); !ok || back != k {
		return 0, fmt.Errorf("%w: %q", ErrKeyNotDecodable, k.String())
	}
	rec.Key = k
	rec.Scope = k.CentralName
	return s.endpoints.UpsertEndpointAssigning(ctx, rec)
}

// RemoveEndpoint implements [fabricendpoint.Store]. Idempotent.
func (s *Store) RemoveEndpoint(ctx context.Context, key fabricendpoint.SourceKey) error {
	k, err := keyOf(key)
	if err != nil {
		return err
	}
	return s.endpoints.RemoveEndpoint(ctx, k)
}

// recordKey recovers rec's key and rejects a record whose scope
// contradicts it. The central is the scope, so the two must not be able
// to disagree: a record written under one central's scope but keyed to
// another would land in the wrong partition and be garbage-collected by
// whichever central is listed next.
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
