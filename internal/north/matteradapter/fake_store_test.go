// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// fakeStore is an in-memory, non-thread-safe implementation of the
// [endpoint.Store] interface for use in tests only. It keys rows the way
// the SQLite store does — by this daemon's 5-tuple, recovered from the
// opaque key by type assertion — so a test that loses the key type fails
// here instead of silently reassigning every endpoint id.
type fakeStore struct {
	rows   map[matterendpoint.SourceKey]endpoint.Record
	nextID uint16
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rows:   make(map[matterendpoint.SourceKey]endpoint.Record),
		nextID: 2, // bridged endpoints start at ID 2 (root=0, aggregator=1)
	}
}

// fakeKey recovers the daemon's key, or the zero key for a foreign one.
func fakeKey(key endpoint.SourceKey) matterendpoint.SourceKey {
	k, _ := key.(matterendpoint.SourceKey)
	return k
}

func (s *fakeStore) GetEndpoint(_ context.Context, key endpoint.SourceKey) (endpoint.Record, error) {
	rec, ok := s.rows[fakeKey(key)]
	if !ok {
		return endpoint.Record{}, endpoint.ErrNotFound
	}
	return rec, nil
}

func (s *fakeStore) UpsertEndpointAssigning(_ context.Context, rec endpoint.Record) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	s.rows[fakeKey(rec.Key)] = rec
	return rec.EndpointID, nil
}

func (s *fakeStore) ListEndpoints(_ context.Context, scope string) ([]endpoint.Record, error) {
	var out []endpoint.Record
	for key, rec := range s.rows {
		if scope == "" || key.CentralName == scope {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *fakeStore) RemoveEndpoint(_ context.Context, key endpoint.SourceKey) error {
	delete(s.rows, fakeKey(key))
	return nil
}

// srcKey recovers this daemon's 5-tuple from an assembled endpoint. The
// bridge module carries the key opaquely, so a test that wants to assert
// on a channel number or a dp kind asks for the value back by type
// assertion rather than parsing the rendered form.
func srcKey(tb testing.TB, ep *endpoint.Endpoint) matterendpoint.SourceKey {
	tb.Helper()
	k, ok := ep.SourceKey.(matterendpoint.SourceKey)
	if !ok {
		tb.Fatalf("EP %d: SourceKey is %T, want %T", ep.ID, ep.SourceKey, matterendpoint.SourceKey{})
	}
	return k
}

// errStoreError is a sentinel used in tests that need GetEndpoint to fail.
var errStoreError = errors.New("fake store: injected error")

// failingStore returns errStoreError from GetEndpoint.
type failingStore struct{ *fakeStore }

func (f *failingStore) GetEndpoint(_ context.Context, _ endpoint.SourceKey) (endpoint.Record, error) {
	return endpoint.Record{}, errStoreError
}
