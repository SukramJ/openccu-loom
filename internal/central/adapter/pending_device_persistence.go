// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// PendingDeviceStores carries the durable half of the deferred-creation
// queue into the wiring. A nil store leaves the queue in memory only.
//
// loom:reachable:reason="field type of WireDeps.PendingDevices, which daemon_southbound.go fills from the composition root and central_bringup.go reads in buildAndStart; a method-less config struct the analyzer's type heuristic (reachable only via its methods) cannot see used"
type PendingDeviceStores struct {
	Pending *sqlite.PendingDeviceStore
}

// enabled reports whether a store is present.
func (p PendingDeviceStores) enabled() bool { return p.Pending != nil }

// pendingSink adapts the SQLite store to the coordinator's
// [coordinators.PendingDeviceSink] port, binding it to one central.
//
// The port is central-scoped and the table is not, so the binding is what
// keeps one CCU's held-back devices out of another's gate — the same
// scoping dimension every other store in this daemon uses (ADR 0002).
type pendingSink struct {
	store       *sqlite.PendingDeviceStore
	centralName string
}

// Load returns the held devices of this central, keyed by canonical
// wire interface id, each with the phase it stands at.
func (s *pendingSink) Load(ctx context.Context) (map[string][]coordinators.HeldDevice, error) {
	rows, err := s.store.ListByCentral(ctx, s.centralName)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]coordinators.HeldDevice, len(rows))
	for i := range rows {
		phase := rows[i].Phase
		if phase == "" {
			phase = coordinators.PhasePending
		}
		out[rows[i].InterfaceID] = append(out[rows[i].InterfaceID],
			coordinators.HeldDevice{Address: rows[i].Address, Phase: phase})
	}
	return out, nil
}

// Advance moves one address to a later onboarding phase.
func (s *pendingSink) Advance(ctx context.Context, interfaceID, address, phase string) error {
	return s.store.SetPhase(ctx, s.centralName, interfaceID, address, phase)
}

// Add records one address as held back.
func (s *pendingSink) Add(ctx context.Context, interfaceID, address, model string) error {
	return s.store.Put(ctx, sqlite.PendingDevice{
		CentralName: s.centralName,
		InterfaceID: interfaceID,
		Address:     address,
		Model:       model,
	})
}

// Remove drops one address.
func (s *pendingSink) Remove(ctx context.Context, interfaceID, address string) error {
	return s.store.Delete(ctx, s.centralName, interfaceID, address)
}

// Clear drops every held-back address of this central.
func (s *pendingSink) Clear(ctx context.Context) error {
	return s.store.DeleteByCentral(ctx, s.centralName)
}

// Compile-time assertion: the adapter satisfies the coordinator's port.
var _ coordinators.PendingDeviceSink = (*pendingSink)(nil)

// WirePendingDevices attaches the durable deferred-creation queue to a
// central and restores it, then reconciles it against the central's
// current `delay_new_device_creation` setting.
//
// Order matters and is the whole point: this runs BEFORE the gated
// south-bound bring-up, so the boot pull can ask
// [coordinators.DeviceCoordinator.IsParked] and hold back what an earlier
// run parked. Wired after the fact, the pull would have materialised
// every held-back device before anything could stop it.
//
// When the toggle is off the queue is released rather than restored: the
// setting means "ask me about new devices", so turning it off means
// "stop asking". Leaving the rows would strand devices in a state whose
// only explanation is a setting that is no longer on.
func WirePendingDevices(
	ctx context.Context,
	unit *central.Unit,
	stores PendingDeviceStores,
	delayEnabled bool,
	logger *slog.Logger,
) {
	if unit == nil || unit.Devices == nil || !stores.enabled() {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	sink := &pendingSink{store: stores.Pending, centralName: unit.Name()}
	unit.Devices.SetPendingDeviceSink(ctx, sink)

	if delayEnabled {
		return
	}
	if freed := unit.Devices.ReleaseAllParked(ctx); freed > 0 {
		logger.Info("wire.pending_devices.released",
			slog.String("central", unit.Name()),
			slog.Int("devices", freed),
			slog.String("detail", "delay_new_device_creation is off; held-back devices are materialised by this bring-up"))
	}
}
