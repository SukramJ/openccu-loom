// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// energyHandlerAdapter bridges the measurement store to the handler's
// package-local EnergyService interface, keeping the handler import
// graph free of the sqlite and central packages. The adapter itself
// stays thin (read rows, resolve names): the correctness-critical
// power/energy folding + counter-reset rule lives in
// [handlers.FoldEnergyRows] so it is unit-testable without a live store
// or central registry.
type energyHandlerAdapter struct {
	store *sqlite.MeasurementStore
	reg   *central.Registry
}

// newEnergyHandlerAdapter returns an EnergyService backed by the store,
// or a genuine nil interface when the store is nil (history disabled)
// so the router omits the /energy route entirely — mirrors
// [newHistoryHandlerAdapter]'s nil handling.
func newEnergyHandlerAdapter(s *sqlite.MeasurementStore, reg *central.Registry) handlers.EnergyService {
	if s == nil {
		return nil
	}
	return &energyHandlerAdapter{store: s, reg: reg}
}

// Energy reads the matching rollup tier via [sqlite.MeasurementStore.QueryEnergy]
// and folds it into the per-device response via [handlers.FoldEnergyRows],
// resolving device display names from the live model registry of the
// query's central.
func (a *energyHandlerAdapter) Energy(ctx context.Context, q handlers.EnergyQuery) (handlers.EnergyResponse, error) {
	rows, err := a.store.QueryEnergy(ctx, q.Central, q.Device, q.From, q.To, q.Group)
	if err != nil {
		return handlers.EnergyResponse{}, err
	}
	raw := make([]handlers.EnergyRawRow, len(rows))
	for i := range rows {
		raw[i] = handlers.EnergyRawRow{
			ChannelAddress: rows[i].ChannelAddress,
			Parameter:      rows[i].Parameter,
			BucketTS:       rows[i].BucketTS,
			Sum:            rows[i].Sum,
			Min:            rows[i].Min,
			Max:            rows[i].Max,
			First:          rows[i].First,
			Last:           rows[i].Last,
			Count:          rows[i].Count,
		}
	}
	return handlers.FoldEnergyRows(q, raw, a.deviceNamer(q.Central)), nil
}

// deviceNamer returns a [handlers.DeviceNamer] resolving addresses
// against centralName's live model registry. Returns a namer that
// always misses when the central or registry is unknown, so the
// caller falls back to the bare address as the display name.
func (a *energyHandlerAdapter) deviceNamer(centralName string) handlers.DeviceNamer {
	return func(address string) (string, bool) {
		if a == nil || a.reg == nil {
			return "", false
		}
		unit, ok := a.reg.Get(centralName)
		if !ok || unit == nil || unit.ModelRegistry == nil {
			return "", false
		}
		dev, found := unit.ModelRegistry.Get(address)
		if !found || dev == nil {
			return "", false
		}
		return dev.Name, true
	}
}
