// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// historyHandlerAdapter bridges the measurement store to the handler's
// package-local HistoryService interface, keeping the handler import
// graph free of the sqlite package.
type historyHandlerAdapter struct {
	store *sqlite.MeasurementStore
}

// newHistoryHandlerAdapter returns a HistoryService backed by the store,
// or a genuine nil interface when the store is nil (history disabled) so
// the router omits the /history route entirely. Returning the interface
// type — not the concrete pointer — avoids the typed-nil-in-interface
// trap that would otherwise register the route with a nil store.
func newHistoryHandlerAdapter(s *sqlite.MeasurementStore) handlers.HistoryService {
	if s == nil {
		return nil
	}
	return &historyHandlerAdapter{store: s}
}

func (a *historyHandlerAdapter) Query(
	ctx context.Context, q handlers.HistoryQuery,
) ([]handlers.HistoryBucket, string, error) {
	rows, tier, err := a.store.QueryBuckets(
		ctx, q.Central, q.InterfaceID, q.ChannelAddress, q.Parameter, q.From, q.To, q.Buckets,
	)
	if err != nil {
		return nil, "", err
	}
	out := make([]handlers.HistoryBucket, len(rows))
	for i := range rows {
		out[i] = handlers.HistoryBucket{
			TS:    rows[i].TS,
			Avg:   rows[i].Avg,
			Min:   rows[i].Min,
			Max:   rows[i].Max,
			Count: rows[i].Count,
		}
	}
	return out, string(tier), nil
}
