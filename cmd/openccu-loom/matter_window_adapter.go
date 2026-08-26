// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"

	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// matterCommissioningOpenerAdapter bridges the type gap between the
// bridge-side [matterbridge.CommissioningWindowOpener] (which returns
// a [matterbridge.OpenCommissioningWindowResult]) and the REST
// handler's [handlers.MatterCommissioningOpener] interface (which
// expects [handlers.MatterCommissioningWindowResult]).
//
// The two result types are structurally identical; this adapter
// keeps the import directions clean — `internal/north/matter/bridge`
// must not import `internal/north/rest/handlers` (that would be a
// cycle, and conceptually the bridge has no business knowing about
// REST).
//
// The adapter additionally drives the `_matterc._udp` mDNS publish
// lifecycle: a successful open emits the commissionable record
// (`_L<long>` / `_S<short>` / `_V<vendor>` subtypes); the close hook
// withdraws it.
type matterCommissioningOpenerAdapter struct {
	inner  *matterbridge.CommissioningWindowOpener
	bridge *matterbridge.Bridge
	advert matterbridge.CommissioningAdvertisement
	// allowEmptyTopology disables the bridged-endpoint readiness check.
	// Production keeps it false so OpenCommissioningWindow refuses to
	// open a window before the CCU initial-load has populated the
	// topology (see [matterCommissioningOpenerAdapter.OpenCommissioningWindow]).
	// Tests that exercise the adapter without a real CCU pipeline set
	// it to true.
	allowEmptyTopology bool
}

// OpenCommissioningWindow translates the bridge result into the
// handler-side struct + maps the bridge sentinel errors to the
// handler sentinels so the HTTP layer surfaces the right status
// codes (409 Conflict on already-open, 503 on not-configured).
//
// Pre-flight: refuse to open the window when the bridge topology has
// no bridged endpoints yet. Without this guard a commissioner can
// pair against the bridge before the CCU initial load completes —
// Apple's MTREndpointInfo (CHIPFramework
// `src/darwin/Framework/CHIP/MTREndpointInfo.mm:209-265`) reads
// Descriptor.PartsList once from the initial Subscribe report and
// never re-reads it; an empty PartsList collapses the HAP-Mapper to
// RootNode-only and surfaces as `endpointDeviceTypes={0=(22)}` even
// after a later Reassemble adds the bridged endpoints.
func (a *matterCommissioningOpenerAdapter) OpenCommissioningWindow(ctx context.Context, durationSeconds uint16) (handlers.MatterCommissioningWindowResult, error) {
	if a == nil || a.inner == nil {
		return handlers.MatterCommissioningWindowResult{}, handlers.ErrCommissioningInProgress
	}
	if a.bridge != nil && !a.allowEmptyTopology {
		if topo := a.bridge.Topology(); topo != nil {
			bridged := 0
			for _, ep := range topo.Endpoints {
				if ep == nil || ep.IsRoot() || ep.IsAggregator() {
					continue
				}
				bridged++
			}
			if bridged == 0 {
				slog.Default().Warn("matter.window_adapter.refused_open",
					slog.String("reason", "topology_no_bridged_endpoints"),
					slog.Int("topology_endpoints", len(topo.Endpoints)))
				return handlers.MatterCommissioningWindowResult{}, handlers.ErrBridgeTopologyNotReady
			}
		}
	}
	res, err := a.inner.OpenCommissioningWindow(ctx, durationSeconds)
	if err != nil {
		if errors.Is(err, matterbridge.ErrCommissioningWindowAlreadyOpen) {
			return handlers.MatterCommissioningWindowResult{}, handlers.ErrCommissioningInProgress
		}
		return handlers.MatterCommissioningWindowResult{}, err
	}
	// Publish the commissionable record. The discriminator may have
	// rotated under ephemeral mode — stamp the result's value, not the
	// configured one. InstanceID stays stable across windows for the
	// daemon's lifetime: rolling it per open confused Apple Home,
	// which silently aborts pairing when a service it has just begun
	// resolving disappears mid-handshake. The boot-time ID set in
	// daemon.go is enough to keep different daemon runs distinguishable.
	// Best-effort: a publish failure does not abort the open;
	// commissioners that already have the IP can still pair via
	// direct PASE.
	if a.bridge != nil {
		params := a.advert
		params.Discriminator = res.Discriminator
		if err := a.bridge.AnnounceCommissioning(ctx, params); err != nil {
			slog.Default().Warn("matter.window_adapter.announce_err",
				slog.String("err", err.Error()),
				slog.Int("discriminator", int(params.Discriminator)))
		}
	}
	return handlers.MatterCommissioningWindowResult{
		Discriminator:   res.Discriminator,
		Passcode:        res.Passcode,
		DurationSeconds: res.DurationSeconds,
		QRCode:          res.QRCode,
		ManualCode:      res.ManualCode,
	}, nil
}
