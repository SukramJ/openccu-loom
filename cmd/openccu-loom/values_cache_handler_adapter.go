// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// valuesCacheHandlerAdapter bridges the sqlite-flavour store API to
// the handler's package-local interface. Keeps the handler import
// graph free of the sqlite package while letting the daemon wire
// the real store.
type valuesCacheHandlerAdapter struct {
	store *sqlite.ValuesCacheStore
}

func newValuesCacheHandlerAdapter(s *sqlite.ValuesCacheStore) *valuesCacheHandlerAdapter {
	if s == nil {
		return nil
	}
	return &valuesCacheHandlerAdapter{store: s}
}

func (a *valuesCacheHandlerAdapter) DeleteAll(ctx context.Context) error {
	return a.store.DeleteAll(ctx)
}

func (a *valuesCacheHandlerAdapter) DeleteDevice(
	ctx context.Context, centralName, interfaceID, deviceAddress string,
) error {
	return a.store.DeleteDevice(ctx, centralName, interfaceID, deviceAddress)
}

func (a *valuesCacheHandlerAdapter) Stats(ctx context.Context) (handlers.ValuesCacheStats, error) {
	s, err := a.store.Stats(ctx)
	if err != nil {
		return handlers.ValuesCacheStats{}, err
	}
	return handlers.ValuesCacheStats{Rows: s.Rows, ValueJSONSize: s.ValueJSONSize}, nil
}

func (a *valuesCacheHandlerAdapter) Metrics() handlers.ValuesCacheMetrics {
	m := a.store.MetricsSnapshot()
	return handlers.ValuesCacheMetrics{
		RestoredRows:   m.RestoredRows,
		CastFailures:   m.CastFailures,
		GCRowsDeleted:  m.GCRowsDeleted,
		FlushBatches:   m.FlushBatches,
		FlushedEntries: m.FlushedEntries,
	}
}

// deviceLookupAdapter resolves a bare device address to its
// (central, interface) tuple by walking every registered central's
// ModelRegistry. Used by the per-device values-cache reset endpoint.
type deviceLookupAdapter struct {
	reg *central.Registry
}

func newDeviceLookupAdapter(reg *central.Registry) *deviceLookupAdapter {
	if reg == nil {
		return nil
	}
	return &deviceLookupAdapter{reg: reg}
}

func (l *deviceLookupAdapter) LocateDevice(addr string) (centralName, interfaceID string, ok bool) {
	if l == nil || l.reg == nil {
		return "", "", false
	}
	for _, unit := range l.reg.List() {
		if unit == nil || unit.ModelRegistry == nil {
			continue
		}
		if dev, found := unit.ModelRegistry.Get(addr); found && dev != nil {
			return unit.Name(), dev.InterfaceID, true
		}
	}
	return "", "", false
}
