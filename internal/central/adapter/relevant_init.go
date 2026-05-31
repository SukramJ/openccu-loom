// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// RelevantInitParameters mirrors
// These three Channel-0 parameters
// drive the daemon's availability tracking; if fetch_all_device_data
// fails to include them, the daemon would default to "reachable" until
// the first push event ever arrives. Loading them explicitly during
// bootstrap closes that gap.
var relevantInitParameters = []hmenum.Parameter{
	hmenum.ParameterConfigPending,
	hmenum.ParameterStickyUnreach,
	hmenum.ParameterUnreach,
}

// seedRelevantInitParameters runs through every device of the given interface
// and triggers a [Device.LoadValue] for each parameter in
// [relevantInitParameters] on Channel 0 — but only when the parameter is not
// yet observed (the rega seed already had a value). This is a best-effort
// pass: failures are logged at debug level so a single CCU misbehaving on one
// device does not abort the bootstrap of the rest.
func seedRelevantInitParameters(ctx context.Context, unit *central.CentralUnit, iface hmenum.Interface, logger *slog.Logger) {
	if unit == nil {
		return
	}
	// Devices are stamped with the wire-form interface_id (`<central>-<iface>`)
	// during pipeline ingest — see [WireInterfaceID]. Match on that
	// composite, not on the bare interface name.
	wireID := WireInterfaceID(unit.Name(), iface)
	loaded, errored := 0, 0
	for _, d := range unit.ModelRegistry.List() {
		if d.InterfaceID != wireID {
			continue
		}
		// Channel 0 is the device-meta channel where UNREACH /
		// CONFIG_PENDING live. Address suffix is always ":0".
		ch0Addr := d.Address + ":0"
		ch := d.Channel(ch0Addr)
		if ch == nil {
			continue
		}
		for _, p := range relevantInitParameters {
			dp := ch.Parameter(p)
			if dp == nil {
				// Channel does not carry this parameter — skip.
				continue
			}
			if _, observed := dp.RawValue(); observed {
				// Push event / fetch_all_device_data already
				// populated it.
				continue
			}
			dpk := hmtypes.DataPointKey{
				InterfaceID:    wireID,
				ChannelAddress: ch0Addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			}
			if _, _, err := d.LoadValue(ctx, dpk, hmenum.CallSourceHMInit, false); err != nil {
				errored++
				if logger != nil {
					logger.Debug("relevant_init.load.failed",
						slog.String("address", d.Address),
						slog.String("parameter", string(p)),
						slog.String("err", err.Error()))
				}
				continue
			}
			loaded++
		}
	}
	if logger != nil {
		logger.Info("relevant_init.seed.ok",
			slog.String("interface", string(iface)),
			slog.Int("loaded", loaded),
			slog.Int("errored", errored))
	}
}

// readableEventCategories enumerates the data-point categories that represent
// transient "readable events" — the CCU emits them as push events on press /
// impulse / device-error and they typically carry no value in
// [fetch_all_device_data] (which only includes DPs with a non-zero
// timestamp).
var readableEventCategories = map[hmenum.DataPointCategory]struct{}{
	hmenum.DataPointCategoryEvent:      {},
	hmenum.DataPointCategoryEventGroup: {},
	hmenum.DataPointCategoryButton:     {},
}

// seedReadableEvents triggers a [Device.LoadValue] for every
// readable event DP that is not yet observed after the regular boot
// pipeline.
//
// Errors are logged at debug level — events are inherently lossy
// (the CCU may legitimately have no last-press timestamp) and a
// failure is not actionable for the operator.
func seedReadableEvents(ctx context.Context, unit *central.CentralUnit, iface hmenum.Interface, logger *slog.Logger) {
	if unit == nil {
		return
	}
	wireID := WireInterfaceID(unit.Name(), iface)
	loaded, errored := 0, 0
	for _, d := range unit.ModelRegistry.List() {
		if d.InterfaceID != wireID {
			continue
		}
		for _, ch := range d.Channels() {
			for _, dp := range ch.DataPoints() {
				if !isReadableEventDP(dp) {
					continue
				}
				if _, observed := dp.RawValue(); observed {
					continue
				}
				dpk := hmtypes.DataPointKey{
					InterfaceID:    wireID,
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(dp.Parameter()),
				}
				if _, _, err := d.LoadValue(ctx, dpk, hmenum.CallSourceHMInit, false); err != nil {
					errored++
					if logger != nil {
						logger.Debug("readable_events.load.failed",
							slog.String("address", ch.Address),
							slog.String("parameter", string(dp.Parameter())),
							slog.String("err", err.Error()))
					}
					continue
				}
				loaded++
			}
		}
	}
	if logger != nil && (loaded > 0 || errored > 0) {
		logger.Info("readable_events.seed.ok",
			slog.String("interface", string(iface)),
			slog.Int("loaded", loaded),
			slog.Int("errored", errored))
	}
}

// isReadableEventDP reports whether dp is an event-style data point
// that should be eagerly loaded during bootstrap. Type-erases via the
// minimal Category + IsReadable interface so the helper does not have
// to import the concrete generic.DataPoint family.
func isReadableEventDP(dp interface{}) bool {
	cat, ok := dp.(interface {
		Category() hmenum.DataPointCategory
	})
	if !ok {
		return false
	}
	if _, hit := readableEventCategories[cat.Category()]; !hit {
		return false
	}
	rd, ok := dp.(interface{ IsReadable() bool })
	if !ok {
		return false
	}
	return rd.IsReadable()
}
