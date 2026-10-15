// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/node"
	matterstore "github.com/SukramJ/go-fabric/store"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
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
	// exposures counts the operator's enabled allowlist rows. Separate
	// from store because the allowlist is keyed by this daemon's source
	// 5-tuple and therefore lives host-side; see [matterendpoint].
	exposures *matterendpoint.Store
	window    *matterbridge.CommissioningWindow
	cfg       *matterStatusConfig
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
		// IsOpen rather than the AdministratorCommissioning snapshot: the
		// node's own window — the operator's REST window, the window an
		// uncommissioned node opens at start — reads WindowNotOpen on the
		// cluster, as matter.js keeps those attributes for administrator
		// windows alone, and the status surface would call a live window
		// closed.
		res.WindowOpen = r.window.IsOpen()
		if res.WindowOpen {
			res.WindowDuration = r.window.RequestedDurationSeconds()
		}
	}
	if r.store != nil {
		fabrics, err := r.store.ListFabrics(ctx)
		if err == nil {
			res.FabricCount = len(fabrics)
		}
	}
	if r.exposures != nil {
		enabled, err := r.exposures.CountEnabled(ctx, "")
		if err == nil {
			res.EnabledCount = enabled
		}
	}
	return res
}

// matterFabricRevokerAdapter removes fabrics for the REST revoke and the
// factory reset through the node facade: node.RemoveFabric is the
// RemoveFabric command's own path — the fabric's rows go and the same
// fan-out runs (the Leave event, the CASE identity and the operational
// record withdrawn, the sessions and subscriptions closed, the group
// state, the node's own window reopened once the last fabric is gone),
// followed by the daemon's fabric-removed observer. Deleting the row
// alone left the removed controller's CASE session and subscription
// alive, and the next pair retry collided on the same session id with
// `aesccm: authentication failed`.
type matterFabricRevokerAdapter struct {
	node  *node.Node
	store *matterstore.Store
}

// RevokeFabric implements [handlers.MatterFabricRevoker].
func (a *matterFabricRevokerAdapter) RevokeFabric(ctx context.Context, fabricIndex uint8) error {
	if a == nil || a.node == nil {
		return matterbridge.ErrCommissioningWindowNotConfigured
	}
	return a.node.RemoveFabric(ctx, fabricIndex)
}

func (a *matterFabricRevokerAdapter) ListFabricIndexes(ctx context.Context) ([]uint8, error) {
	if a == nil || a.store == nil {
		return nil, matterbridge.ErrCommissioningWindowNotConfigured
	}
	records, err := a.store.ListFabrics(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]uint8, 0, len(records))
	for _, r := range records {
		out = append(out, r.FabricIndex)
	}
	return out, nil
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
// walked on every call, so freshly-discovered devices surface immediately.
//
// cfg is the config the daemon booted with, not a live view: nothing mutates
// North.Matter after boot, so a saved expose_secondary_channels change is
// reflected here only on the next start. That is the same restriction the
// Matter bridge itself has — the field is restart-required in
// [config.RestartRules], so the SPA badges it and the save response says so.
type matterCandidateProviderAdapter struct {
	reg *central.Registry
	cfg *config.Config
}

// MatterCandidates implements [handlers.MatterCandidateProvider]. It calls
// [matteradapter.CollectCandidates] directly (rather than through a stored
// closure) so the production reachability graph can trace the Matter
// eligibility entry points from this method — the reachability analyzer seeds
// the REST handler that invokes this as an entry point but cannot follow an
// indirect call through a func-typed struct field.
func (a *matterCandidateProviderAdapter) MatterCandidates(_ context.Context) []matteradapter.Candidate {
	if a == nil || a.reg == nil || a.cfg == nil {
		return nil
	}
	var out []matteradapter.Candidate
	for _, u := range a.reg.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		out = append(out, matteradapter.CollectCandidates(u.Name(), u.ModelRegistry.List(), a.cfg.North.Matter.ExposeSecondaryChannels)...)
	}
	return out
}
