// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// Shared test fake for the [endpoint.Store] surface. Lives in
// `package bridge` so both the white-box tests in this directory
// (e.g. receive_test.go) and the black-box tests in `package
// bridge_test` (bridge_test.go) can reuse it via the exported
// constructor. The file name ends in `_test.go` so it never compiles
// into the production binary.

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// FakeStore is an in-memory implementation of [endpoint.Store] for
// bridge tests. It is concurrency-naive (callers must not race) and
// only carries enough state to satisfy the bridge's startup +
// reassembly paths.
type FakeStore struct {
	rows   map[store.EndpointKey]store.EndpointRecord
	nextID uint16
}

// NewFakeStore returns a fresh FakeStore with an empty row map and
// the next-id counter initialised at 2 (Endpoint 0 is the root,
// Endpoint 1 is the aggregator; bridged endpoints start at 2).
func NewFakeStore() *FakeStore {
	return &FakeStore{
		rows:   make(map[store.EndpointKey]store.EndpointRecord),
		nextID: 2,
	}
}

// GetEndpoint implements [endpoint.Store].
func (s *FakeStore) GetEndpoint(_ context.Context, key store.EndpointKey) (store.EndpointRecord, error) {
	rec, ok := s.rows[key]
	if !ok {
		return store.EndpointRecord{}, store.ErrEndpointNotFound
	}
	return rec, nil
}

// UpsertEndpointAssigning implements [endpoint.Store].
func (s *FakeStore) UpsertEndpointAssigning(_ context.Context, rec store.EndpointRecord) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	s.rows[rec.Key] = rec
	return rec.EndpointID, nil
}

// ListEndpoints implements [endpoint.Store]. An empty centralName
// matches every row.
func (s *FakeStore) ListEndpoints(_ context.Context, centralName string) ([]store.EndpointRecord, error) {
	var out []store.EndpointRecord
	for _, rec := range s.rows {
		if centralName == "" || rec.Key.CentralName == centralName {
			out = append(out, rec)
		}
	}
	return out, nil
}

// RemoveEndpoint implements [endpoint.Store].
func (s *FakeStore) RemoveEndpoint(_ context.Context, key store.EndpointKey) error {
	delete(s.rows, key)
	return nil
}
