// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// matterStatusReaderAdapter implements [handlers.MatterStatusReader]
// against the live bridge + store. The bridge knows endpoint count
// and listen address; the store knows fabric count and exposure
// count.
type matterStatusReaderAdapter struct {
	enabled bool
	bridge  *matterbridge.Bridge
	store   *matterstore.Store
	window  *matterbridge.CommissioningWindow
	cfg     *matterStatusConfig
}

type matterStatusConfig struct {
	advertising bool
}

// MatterStatus implements [handlers.MatterStatusReader].
func (r *matterStatusReaderAdapter) MatterStatus(ctx context.Context) handlers.MatterStatusResponse {
	res := handlers.MatterStatusResponse{Enabled: r.enabled}
	if !r.enabled || r.bridge == nil {
		return res
	}
	res.ListenAddr = r.bridge.LocalAddr()
	res.Listening = res.ListenAddr != ""
	if topo := r.bridge.Topology(); topo != nil {
		// Topology.Endpoints carries the two structural endpoints (root
		// EP 0 + Aggregator EP 1) on top of the bridged devices;
		// Bridged() is the only authoritative source for the
		// caller-facing count, and the SPA renders it as "bridged Matter
		// devices".
		res.EndpointCount = len(topo.Bridged())
	}
	if r.cfg != nil {
		res.Advertising = r.cfg.advertising
	}
	if r.window != nil {
		snap := r.window.CurrentWindow()
		res.WindowOpen = snap.Status != 0
	}
	if r.store != nil {
		fabrics, err := r.store.ListFabrics(ctx)
		if err == nil {
			res.FabricCount = len(fabrics)
		}
		enabled, err := r.store.CountEnabled(ctx, "")
		if err == nil {
			res.EnabledCount = enabled
		}
	}
	return res
}

// matterFabricRevokerAdapter implements [handlers.MatterFabricRevoker].
type matterFabricRevokerAdapter struct {
	store *matterstore.Store
}

// RevokeFabric implements [handlers.MatterFabricRevoker]. Closes the
// fabric in the store; the live bridge picks up the change at the
// next reassemble. CASE sessions tied to the revoked fabric are
// dropped through the existing `subscription.Manager.CloseFabric`
// hook (wired by the daemon at boot).
func (a *matterFabricRevokerAdapter) RevokeFabric(ctx context.Context, fabricIndex uint8) error {
	if a == nil || a.store == nil {
		return matterbridge.ErrCommissioningWindowNotConfigured
	}
	return a.store.RemoveFabric(ctx, fabricIndex)
}

// matterCommissioningCloserAdapter implements
// [handlers.MatterCommissioningCloser].
type matterCommissioningCloserAdapter struct {
	window *matterbridge.CommissioningWindow
}

// CloseCommissioningWindow implements
// [handlers.MatterCommissioningCloser].
func (a *matterCommissioningCloserAdapter) CloseCommissioningWindow(ctx context.Context) error {
	if a == nil || a.window == nil {
		return matterbridge.ErrCommissioningWindowNotConfigured
	}
	return a.window.RevokeWindow(ctx)
}

// matterCandidateProviderAdapter walks the daemon's central registry and
// returns the eligibility candidate list for the allowlist UI. The registry is
// walked on every call so freshly-discovered devices surface immediately, and
// cfg is read live so the operator's expose_secondary_channels choice is always
// current.
type matterCandidateProviderAdapter struct {
	reg *central.Registry
	cfg *config.Config
}

// MatterCandidates implements [handlers.MatterCandidateProvider]. It calls
// [eligibility.CollectCandidates] directly (rather than through a stored
// closure) so the production reachability graph can trace the Matter
// eligibility entry points from this method — the reachability analyzer seeds
// the REST handler that invokes this as an entry point but cannot follow an
// indirect call through a func-typed struct field.
func (a *matterCandidateProviderAdapter) MatterCandidates(_ context.Context) []eligibility.Candidate {
	if a == nil || a.reg == nil || a.cfg == nil {
		return nil
	}
	var out []eligibility.Candidate
	for _, u := range a.reg.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		out = append(out, eligibility.CollectCandidates(u.Name(), u.ModelRegistry.List(), a.cfg.North.Matter.ExposeSecondaryChannels)...)
	}
	return out
}
