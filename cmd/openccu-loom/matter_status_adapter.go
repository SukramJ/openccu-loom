// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

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
		// Topology.Endpoints includes the root; subtract 1 for the
		// caller-facing "bridged endpoint" count.
		if n := len(topo.Endpoints); n > 0 {
			res.EndpointCount = n - 1
		}
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

// matterCandidateProviderAdapter walks the daemon's central registry
// and returns the eligibility candidate list for the allowlist UI.
// The closure is rebuilt on every call so freshly-discovered devices
// surface immediately.
type matterCandidateProviderAdapter struct {
	walk func() []eligibility.Candidate
}

// MatterCandidates implements [handlers.MatterCandidateProvider].
func (a *matterCandidateProviderAdapter) MatterCandidates(_ context.Context) []eligibility.Candidate {
	if a == nil || a.walk == nil {
		return nil
	}
	return a.walk()
}
