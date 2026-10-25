// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/go-fabric/node"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
)

// matterStatusReaderAdapter implements [mcp.MatterStatusReader] against
// the node facade and the exposure store, for the assistant's
// `get_matter_status` and the daemon's health probe. The node knows the
// listen address, the served topology and the fabrics; the exposure store
// knows the operator's allowlist. The operator's REST status is
// go-fabric-ui's (GET /api/v1/node/status) over the same node.
type matterStatusReaderAdapter struct {
	enabled bool
	node    *node.Node
	// exposures counts the operator's enabled allowlist rows. Separate
	// from store because the allowlist is keyed by this daemon's source
	// 5-tuple and therefore lives host-side; see [matterendpoint].
	exposures *matterendpoint.Store
	window    *node.CommissioningWindow
	cfg       *matterStatusConfig
}

type matterStatusConfig struct {
	advertising bool
}

// MatterStatus implements [mcp.MatterStatusReader].
func (r *matterStatusReaderAdapter) MatterStatus(ctx context.Context) mcp.MatterStatus {
	res := mcp.MatterStatus{Enabled: r.enabled}
	if !r.enabled || r.node == nil {
		return res
	}
	res.ListenAddr = r.node.LocalAddr()
	res.Listening = res.ListenAddr != ""
	// The served topology's bridged devices, without the root and the
	// Aggregator: the count of bridged Matter devices.
	res.EndpointCount = len(r.node.Topology().Devices)
	if r.cfg != nil {
		res.Advertising = r.cfg.advertising
	}
	if r.window != nil {
		// IsOpen rather than the AdministratorCommissioning snapshot: the
		// node's own window — the operator's window, the window an
		// uncommissioned node opens at start — reads WindowNotOpen on the
		// cluster, as matter.js keeps those attributes for administrator
		// windows alone, and the status surface would call a live window
		// closed.
		res.WindowOpen = r.window.IsOpen()
		if res.WindowOpen {
			res.WindowDuration = r.window.RequestedDurationSeconds()
		}
	}
	if fabrics, err := r.node.FabricInfos(ctx); err == nil {
		res.FabricCount = len(fabrics)
	}
	if r.exposures != nil {
		enabled, err := r.exposures.CountEnabled(ctx, "")
		if err == nil {
			res.EnabledCount = enabled
		}
	}
	return res
}

// matterCandidateProviderAdapter walks the daemon's central registry and
// returns the eligibility candidate list for the exposure provider
// (matteradapter.ExposureProviderConfig.Candidates). The registry is walked
// on every call, so freshly-discovered devices surface immediately.
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

// MatterCandidates calls [matteradapter.CollectCandidates] directly (rather
// than through a stored closure) so the production reachability graph can
// trace the Matter eligibility entry points from this method — the
// reachability analyzer seeds the handler that invokes this as an entry
// point but cannot follow an indirect call through a func-typed struct
// field.
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
