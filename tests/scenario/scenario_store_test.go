// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scenario

import (
	"context"
	"sync"

	"github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// scenarioStore is an in-memory [endpoint.Store] for the scenario
// harness. Each harness gets its own instance so parallel scenarios
// never share an endpoint-id counter — two scenarios assigning the
// same device to different endpoint ids would otherwise make their
// wire expectations depend on execution order.
//
// The mutex is not decoration: the assembler runs on the bridge's
// reassembly path while the harness's own setup still holds the
// constructor goroutine, so both can reach the row map.
type scenarioStore struct {
	mu     sync.Mutex
	rows   map[matterendpoint.SourceKey]endpoint.Record
	nextID uint16
}

// newScenarioStore returns a fresh store with the next-id counter at
// 2: endpoint 0 is the root and endpoint 1 the aggregator, so bridged
// endpoints start at 2 — which is the id every scenario's `endpoint`
// field is written against.
func newScenarioStore() *scenarioStore {
	return &scenarioStore{
		rows:   make(map[matterendpoint.SourceKey]endpoint.Record),
		nextID: 2,
	}
}

// scenarioKey recovers the daemon's own key from the opaque one the
// bridge module carries, so the fake indexes rows the way the SQLite
// store does.
func scenarioKey(key endpoint.SourceKey) matterendpoint.SourceKey {
	k, _ := key.(matterendpoint.SourceKey)
	return k
}

func (s *scenarioStore) GetEndpoint(_ context.Context, key endpoint.SourceKey) (endpoint.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.rows[scenarioKey(key)]
	if !ok {
		return endpoint.Record{}, endpoint.ErrNotFound
	}
	return rec, nil
}

func (s *scenarioStore) UpsertEndpointAssigning(_ context.Context, rec endpoint.Record) (uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scenarioKey(rec.Key)
	if rec.EndpointID == 0 {
		if existing, ok := s.rows[key]; ok && existing.EndpointID != 0 {
			rec.EndpointID = existing.EndpointID
		} else {
			rec.EndpointID = s.nextID
			s.nextID++
		}
	}
	s.rows[key] = rec
	return rec.EndpointID, nil
}

// ListEndpoints returns every row when scope is empty.
func (s *scenarioStore) ListEndpoints(_ context.Context, scope string) ([]endpoint.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]endpoint.Record, 0, len(s.rows))
	for key, rec := range s.rows {
		if scope == "" || key.CentralName == scope {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *scenarioStore) RemoveEndpoint(_ context.Context, key endpoint.SourceKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, scenarioKey(key))
	return nil
}
