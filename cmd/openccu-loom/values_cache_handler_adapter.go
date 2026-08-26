// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// newValuesCacheHandlerAdapter returns the handler-facing interface, not the
// concrete adapter, so an absent store stays a true nil interface at the
// consumer.
//
// The store is nil on the supported `persistence.values_cache.enabled: false`
// setting. Handed back as a typed nil *valuesCacheHandlerAdapter it would be
// boxed into a NON-nil handlers.ValuesCacheService: the handler's own
// `svc == nil` guard would not fire, and the first call would dereference the
// nil receiver — a panic per request on documented configuration, where the
// operator should see the 503 the endpoint documents.
func newValuesCacheHandlerAdapter(s *sqlite.ValuesCacheStore) handlers.ValuesCacheService {
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

// newDeviceLookupAdapter returns the handler-facing interface for the same
// reason [newValuesCacheHandlerAdapter] does: boxed as a typed nil the absent
// lookup passes the handler's nil check, and the per-device reset then answers
// "device not found" for a device that exists — the lookup is simply unwired.
func newDeviceLookupAdapter(reg *central.Registry) handlers.DeviceLookup {
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
