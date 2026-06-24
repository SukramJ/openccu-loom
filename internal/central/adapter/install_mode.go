// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// installModeNormal is the CCU's "normal" install-mode flavour (mode=1).
// mode=2 means "ready for re-pairing"; only the normal mode is exposed.
const installModeNormal = 1

// installModeWriter resolves the per-interface backend at call time and
// issues the CCU setInstallMode against it. It satisfies
// [hub.InstallModeWriter] (broadcast pairing on a radio) and
// [hub.DeviceInstallModeWriter] (pairing scoped to a single device).
//
// Resolution is lazy — the backend is looked up per call via the
// [clientpkg.ValueWriter], mirroring [DeviceAdminDomain.resolve]. That
// avoids any ordering dependency on interface-client registration at
// wiring time.
type installModeWriter struct {
	unit   *central.Unit
	writer *clientpkg.ValueWriter
}

// SetInstallMode opens (enabled) or closes install mode on interfaceID by
// broadcasting to the interface's backend (no device filter).
func (w *installModeWriter) SetInstallMode(ctx context.Context, interfaceID string, enabled bool, duration time.Duration) error {
	b, ok := w.writer.Backend(w.unit.Name(), interfaceID)
	if !ok {
		return fmt.Errorf("install-mode: no backend for %s/%s", w.unit.Name(), interfaceID)
	}
	return b.SetInstallMode(ctx, enabled, int(duration.Seconds()), installModeNormal, "")
}

// SetInstallModeForDevice opens install mode on interfaceID scoped to a
// single device address (targeted teach-in / re-pairing by serial).
func (w *installModeWriter) SetInstallModeForDevice(ctx context.Context, interfaceID string, duration time.Duration, deviceAddress string) error {
	b, ok := w.writer.Backend(w.unit.Name(), interfaceID)
	if !ok {
		return fmt.Errorf("install-mode: no backend for %s/%s", w.unit.Name(), interfaceID)
	}
	return b.SetInstallMode(ctx, true, int(duration.Seconds()), installModeNormal, deviceAddress)
}

// WireInstallModeDPs registers one install-mode data point per
// pairing-capable radio interface on unit's hub model. install mode on the
// CCU is always per-interface (there is no CCU-wide toggle), so each
// pairing-capable interface gets its own data point; the SPA and REST
// surface them via GET/POST /install-mode/interfaces.
//
// Call this after [WireCentrals] has registered the interface clients, for
// the same late-binding reason as [WireSysvarCreator]: the writer resolves
// each backend lazily, but the interface set is read from the registered
// clients here. Nil arguments are safe (no-op).
func WireInstallModeDPs(unit *central.Unit, writer *clientpkg.ValueWriter) {
	if unit == nil || unit.HubModel == nil || unit.Clients == nil || writer == nil {
		return
	}
	w := &installModeWriter{unit: unit, writer: writer}
	for _, entry := range unit.Clients.List() {
		if entry.Client == nil {
			continue
		}
		iface := entry.Client.Interface()
		if !iface.SupportsInstallMode() {
			continue
		}
		unit.HubModel.PutInstallMode(hub.NewInstallMode(string(iface), w))
	}
}
