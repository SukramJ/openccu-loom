// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// UnobservedSweep is the [coordinators.UnobservedSweepRunner] implementation
// that walks every device of every central registered in [central.Registry]
// and triggers a [device.Device.LoadValue] for each DP that is still
// unobserved AND on the bootstrap whitelist (RELEVANT_INIT_PARAMETERS on
// Channel 0 + readable events).
//
// Hot-path-safe: the sweep deliberately skips DPs that are already observed,
// so a second call seconds after a successful boot degenerates into a no-op
// map walk. CCU calls only happen for genuine stragglers — channels that the
// CCU did not include in `fetch_all_device_data` and that have not received a
// push event yet.
type UnobservedSweep struct {
	registry *central.Registry
	logger   *slog.Logger
}

// NewUnobservedSweep wires the runner against the given registry. The
// logger is optional; pass nil to silence per-DP failure traces.
func NewUnobservedSweep(reg *central.Registry, logger *slog.Logger) *UnobservedSweep {
	return &UnobservedSweep{registry: reg, logger: logger}
}

// SweepUnobserved implements [coordinators.UnobservedSweepRunner].
// Returns the count of DPs that transitioned from unobserved to
// observed during this run (loaded) and the count that errored
// (errored). Both counts cover every central in the registry.
func (s *UnobservedSweep) SweepUnobserved(ctx context.Context) (loaded, errored int) {
	if s == nil || s.registry == nil {
		return 0, 0
	}
	for _, unit := range s.registry.List() {
		if unit == nil {
			continue
		}
		l, e := s.sweepCentral(ctx, unit)
		loaded += l
		errored += e
	}
	return loaded, errored
}

// sweepCentral walks a single central. Pulled out so callers (tests,
// REST/UI manual triggers) can scope to one Unit.
func (s *UnobservedSweep) sweepCentral(ctx context.Context, unit *central.Unit) (loaded, errored int) {
	for _, d := range unit.ModelRegistry.List() {
		l, e := s.sweepDevice(ctx, d)
		loaded += l
		errored += e
	}
	return loaded, errored
}

// sweepDevice walks one device's unobserved DPs and tries
// [device.Device.LoadValue] for each. The sweep covers:
//
//  1. Channel-0 RELEVANT_INIT_PARAMETERS — UNREACH /
//     STICKY_UN_REACH / CONFIG_PENDING. Bootstrap whitelist; same
//
// Set as `init_base_data_points`.
//  2. Readable events on every channel — buttons / impulse / device-
//
// Error events. Same set as `init_readable_events`.
//  3. **Every visible readable VALUES-paramset DP on every channel.**
//     Battery devices that miss `fetch_all_device_data` at boot
//     (asleep, temporarily unreachable) eventually get loaded by
//     this third pass on a Reconciler tick, so the operator's HA
//     entities transition from `unavailable` to a real value
//
// Without needing a CCU push event.
//
//	`Channel.load_values` which loads every readable DP on demand.
//
// Hot-path-safe: every step skips DPs that are already observed, so
// a second call seconds after a successful boot is a no-op map walk.
// CCU calls only happen for genuine stragglers.
func (s *UnobservedSweep) sweepDevice(ctx context.Context, d *device.Device) (loaded, errored int) {
	if d == nil {
		return 0, 0
	}
	// Channel-0 RELEVANT_INIT_PARAMETERS — same set as the bootstrap
	// pass (UNREACH / STICKY_UN_REACH / CONFIG_PENDING).
	ch0 := d.Channel(d.Address + ":0")
	if ch0 != nil {
		for _, p := range relevantInitParameters {
			dp := ch0.Parameter(p)
			if dp == nil {
				continue
			}
			if _, observed := dp.RawValue(); observed {
				continue
			}
			l, e := s.tryLoad(ctx, d, ch0.Address, p)
			loaded += l
			errored += e
		}
	}
	// Readable events on every channel — buttons / impulse / device-error.
	for _, ch := range d.Channels() {
		for _, dp := range ch.DataPoints() {
			if !isReadableEventDP(dp) {
				continue
			}
			if _, observed := dp.RawValue(); observed {
				continue
			}
			l, e := s.tryLoad(ctx, d, ch.Address, dp.Parameter())
			loaded += l
			errored += e
		}
	}
	// All visible readable VALUES-paramset DPs that have no observed
	// value yet. The two earlier passes are subsets of this superset
	// (channel-0 init + readable events); the visibility-skip and
	// observed-skip below short-circuit the duplicate work in O(1)
	// each.
	for _, ch := range d.Channels() {
		for _, dp := range ch.DataPoints() {
			if _, observed := dp.RawValue(); observed {
				continue
			}
			if !isLoadableValueDP(dp) {
				continue
			}
			l, e := s.tryLoad(ctx, d, ch.Address, dp.Parameter())
			loaded += l
			errored += e
		}
	}
	return loaded, errored
}

// isLoadableValueDP reports whether the bridge should attempt a
// LoadValue on the DP during the broad-coverage sweep.
//
// Gates:
//   - Visible flag — DPs the model has marked NoCreate (ignored
//     parameters, suppression passes) are skipped so the sweep does
//     not wake battery devices for entities that won't be exposed.
//   - Readable operations bit — write-only DPs (PRESS_*, ACTION,
//
// COMMAND triggers) cannot answer a getValue call
//
//	gates the same way via `is_readable`.
//
// Compile-time: the Visible / ParameterData methods are promoted by
// every generic-DP implementation; the type assertion fallback path
// is only hit by test fixtures that bypass the construction wiring.
func isLoadableValueDP(dp interface {
	RawValue() (any, bool)
	Parameter() hmenum.Parameter
},
) bool {
	// Skip DPs marked NoCreate / hidden by the materializer.
	if v, ok := dp.(interface{ Visible() bool }); ok && !v.Visible() {
		return false
	}
	// Skip non-readable DPs — getValue would fail anyway.
	type pdReader interface {
		ParameterData() hmproto.ParameterData
	}
	if r, ok := dp.(pdReader); ok {
		if r.ParameterData().Operations&hmenum.OperationsRead == 0 {
			return false
		}
	}
	return true
}

// tryLoad runs a single LoadValue and counts the outcome.
func (s *UnobservedSweep) tryLoad(ctx context.Context, d *device.Device, channelAddress string, p hmenum.Parameter) (loaded, errored int) {
	dpk := hmtypes.DataPointKey{
		InterfaceID:    d.InterfaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(p),
	}
	_, observed, err := d.LoadValue(ctx, dpk, hmenum.CallSourceManualOrScheduled, false)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("unobserved_sweep.load.failed",
				slog.String("address", channelAddress),
				slog.String("parameter", string(p)),
				slog.String("err", err.Error()))
		}
		return 0, 1
	}
	if !observed {
		// CCU returned a sentinel (no value available) — not a real
		// "loaded" transition. Treat as neither loaded nor errored.
		return 0, 0
	}
	return 1, 0
}
