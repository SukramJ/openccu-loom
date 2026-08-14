// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SearchWiredDevices triggers a wired-bus scan on the given interface of
// the resolved central (`searchDevices` — hs485d / BidCos-Wired only)
// and returns the count of devices found. The found devices join the
// CCU's inbox (ReadyConfig false); a best-effort inbox refresh surfaces
// them so the operator can accept them. centralName scopes the lookup;
// empty falls back to the sole central (error when ambiguous).
func (a *DeviceAdminDomain) SearchWiredDevices(ctx context.Context, centralName, interfaceID string) (int, error) {
	if a.registry == nil || a.writer == nil {
		return 0, ErrNoDeviceBackend
	}
	iface := hmenum.Interface(interfaceID)
	if !iface.SupportsDeviceSearch() {
		return 0, fmt.Errorf("search devices: interface %s: %w", interfaceID, backends.ErrUnsupported)
	}
	unit, err := a.resolveReplaceCentral(centralName)
	if err != nil {
		return 0, err
	}
	backend, ok := a.writer.Backend(unit.Name(), hmtypes.NewWireInterfaceID(unit.Name(), iface))
	if !ok {
		return 0, fmt.Errorf("%w: %s/%s", hmerr.ErrUnknownCentral, unit.Name(), interfaceID)
	}
	count, err := backend.SearchDevices(ctx)
	if err != nil {
		return 0, err
	}
	if unit.Hub != nil {
		// Best-effort: surface the freshly-found (not-yet-accepted)
		// devices in the inbox without waiting for the periodic sweep.
		_ = unit.Hub.RefreshInbox(ctx)
	}
	return count, nil
}
