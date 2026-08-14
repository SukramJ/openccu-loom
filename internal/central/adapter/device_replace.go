// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ReplaceCandidates lists the already-paired devices the new (inbox)
// device at newAddress may replace. It queries every replace-capable
// interface of the resolved central via `listReplaceableDevices`,
// tolerating per-interface faults (a wrong-interface serial faults on
// the interfaces that do not own it), keeps only device rows the daemon
// already models, and flags whether each candidate's model matches the
// new device's (an exact swap vs. a compatible cross-type one).
func (a *DeviceAdminDomain) ReplaceCandidates(ctx context.Context, centralName, newAddress string) ([]hmapi.ReplaceCandidate, error) {
	unit, err := a.resolveReplaceCentral(centralName)
	if err != nil {
		return nil, err
	}
	newModel := a.inboxModelOf(unit, newAddress)

	var out []hmapi.ReplaceCandidate
	for _, entry := range a.replaceInterfaces(unit) {
		// The ValueWriter registry is keyed by the canonical wire id
		// (`<central>-<iface>`); the bare interface only ever reaches the
		// operator-facing DTO. Resolving with the bare form misses every
		// entry and silently yields an empty candidate list.
		backend, ok := a.writer.Backend(unit.Name(), entry.InterfaceID)
		if !ok {
			slog.Default().Debug("device_replace.no_backend",
				slog.String("central", unit.Name()),
				slog.String("interface", entry.InterfaceID))
			continue
		}
		descs, listErr := backend.ListReplaceableDevices(ctx, newAddress)
		if listErr != nil {
			// A serial belonging to another interface faults here; the
			// other interfaces still contribute their candidates.
			continue
		}
		for i := range descs {
			d := &descs[i]
			if !d.IsDevice() {
				continue
			}
			dev, known := unit.ModelRegistry.Get(d.Address)
			if !known {
				// Only surface devices the daemon already models
				// (accepted), matching the CCU WebUI's accepted-only
				// filter.
				continue
			}
			out = append(out, hmapi.ReplaceCandidate{
				Address:      d.Address,
				Name:         dev.Name,
				Model:        d.Type,
				Interface:    string(entry.Client.Interface()),
				Central:      unit.Name(),
				ModelMatches: newModel != "" && d.Type == newModel,
			})
		}
	}
	return out, nil
}

// ReplaceDevice swaps the paired oldAddress for the new device at
// newAddress. The interface daemon migrates direct links, teams and
// link paramsets; ReGa re-binds the existing object in place (same
// ise-ID, so programs / names / rooms survive). The old device's model
// caches are then evicted and the replacement re-ingested eagerly so
// the SPA updates without waiting for the CCU's replaceDevice callback
// (which dedups when it arrives).
func (a *DeviceAdminDomain) ReplaceDevice(ctx context.Context, centralName, oldAddress, newAddress string) error {
	unit, err := a.resolveReplaceCentral(centralName)
	if err != nil {
		return err
	}
	dev, ok := unit.ModelRegistry.Get(oldAddress)
	if !ok {
		return fmt.Errorf("%w: old device %s", ErrNoDeviceBackend, oldAddress)
	}
	if !dev.Interface.SupportsReplace() {
		return fmt.Errorf("replace device: interface %s: %w", dev.Interface, backends.ErrUnsupported)
	}
	backend, ok := a.writer.Backend(unit.Name(), dev.InterfaceID)
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoDeviceBackend, unit.Name(), dev.InterfaceID)
	}
	if err := backend.ReplaceDevice(ctx, oldAddress, newAddress); err != nil {
		return err
	}
	if unit.Devices != nil {
		fetcher := &callbackDescFetcher{ops: backend}
		if rerr := unit.Devices.ReplaceDevice(ctx, fetcher, dev.Interface, oldAddress, newAddress); rerr != nil {
			// The CCU swap already happened and is irreversible; the eager
			// model refresh is best-effort. The CCU's own replaceDevice
			// callback reconciles the model authoritatively (and treats the
			// same error as non-fatal — see CallbackHandlers.ReplaceDevice),
			// and it can even win the race and evict the old device before
			// this runs, so a "old device not found" or a transient
			// post-swap ListDevices error here must NOT surface the
			// already-committed swap as a failure. Log and report success.
			slog.Default().Warn("device_replace.eager_refresh_failed",
				slog.String("central", unit.Name()),
				slog.String("interface", string(dev.Interface)),
				slog.String("old", oldAddress),
				slog.String("new", newAddress),
				slog.String("err", rerr.Error()))
		}
	}
	return nil
}

// resolveReplaceCentral resolves the owning central: an explicit name,
// else the sole central, else an ambiguity error.
func (a *DeviceAdminDomain) resolveReplaceCentral(centralName string) (*central.Unit, error) {
	if a.registry == nil || a.writer == nil {
		return nil, ErrNoDeviceBackend
	}
	if centralName != "" {
		u, ok := a.registry.Get(centralName)
		if !ok {
			return nil, fmt.Errorf("%w: %s", hmerr.ErrUnknownCentral, centralName)
		}
		return u, nil
	}
	units := a.registry.List()
	if len(units) == 1 {
		return units[0], nil
	}
	return nil, fmt.Errorf("%w: central name required (%d configured)", hmerr.ErrUnknownCentral, len(units))
}

// replaceInterfaces returns the client entries of the replace-capable
// interfaces the unit has a client for (rfd / hs485d). The whole entry is
// returned rather than the bare interface so the caller never has to
// reconstruct the wire id: [coordinators.ClientEntry] carries both
// identifier spaces (`InterfaceID` = wire, `Interface` = bare), and
// conflating them is what silently empties the candidate list.
func (a *DeviceAdminDomain) replaceInterfaces(unit *central.Unit) []*coordinators.ClientEntry {
	if unit.Clients == nil {
		return nil
	}
	var out []*coordinators.ClientEntry
	for _, entry := range unit.Clients.List() {
		if entry.Client == nil {
			continue
		}
		if entry.Client.Interface().SupportsReplace() {
			out = append(out, entry)
		}
	}
	return out
}

// inboxModelOf returns the model string of the new device as seen in the
// hub inbox, or "" when it is not (yet) in the inbox. Used only to flag
// exact-model candidates.
func (a *DeviceAdminDomain) inboxModelOf(unit *central.Unit, newAddress string) string {
	if unit.HubModel == nil || unit.HubModel.Inbox == nil {
		return ""
	}
	for _, d := range unit.HubModel.Inbox.List() {
		if d.Address == newAddress {
			return d.Model
		}
	}
	return ""
}
