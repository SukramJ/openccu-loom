// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint_test

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// fakeStore is an in-memory, non-thread-safe implementation of the
// [endpoint.Store] interface for use in tests only.
type fakeStore struct {
	rows   map[store.EndpointKey]store.EndpointRecord
	nextID uint16
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rows:   make(map[store.EndpointKey]store.EndpointRecord),
		nextID: 2, // bridged endpoints start at ID 2 (root=0, aggregator=1)
	}
}

func (s *fakeStore) GetEndpoint(_ context.Context, key store.EndpointKey) (store.EndpointRecord, error) {
	rec, ok := s.rows[key]
	if !ok {
		return store.EndpointRecord{}, store.ErrEndpointNotFound
	}
	return rec, nil
}

func (s *fakeStore) UpsertEndpointAssigning(_ context.Context, rec store.EndpointRecord) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	s.rows[rec.Key] = rec
	return rec.EndpointID, nil
}

func (s *fakeStore) ListEndpoints(_ context.Context, centralName string) ([]store.EndpointRecord, error) {
	var out []store.EndpointRecord
	for _, rec := range s.rows {
		if centralName == "" || rec.Key.CentralName == centralName {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *fakeStore) RemoveEndpoint(_ context.Context, key store.EndpointKey) error {
	delete(s.rows, key)
	return nil
}

// errStoreError is a sentinel used in tests that need GetEndpoint to fail.
var errStoreError = errors.New("fake store: injected error")

// failingStore returns errStoreError from GetEndpoint.
type failingStore struct{ *fakeStore }

func (f *failingStore) GetEndpoint(_ context.Context, _ store.EndpointKey) (store.EndpointRecord, error) {
	return store.EndpointRecord{}, errStoreError
}
