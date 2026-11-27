// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"math"
	"time"

	"thingfabric.dev/go-fabric/node"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/central"
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
	// node is the node that runs now (matterRuntime.Node): an erase
	// replaces it.
	node func() *node.Node
	// exposures counts the operator's enabled allowlist rows. Separate
	// from store because the allowlist is keyed by this daemon's source
	// 5-tuple and therefore lives host-side; see [matterendpoint].
	exposures *matterendpoint.Store
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
	n := r.node()
	if n == nil {
		return res
	}
	res.ListenAddr = n.LocalAddr()
	res.Listening = res.ListenAddr != ""
	// The served topology's bridged devices, without the root and the
	// Aggregator: the count of bridged Matter devices.
	res.EndpointCount = len(n.Topology().Devices)
	if r.cfg != nil {
		res.Advertising = r.cfg.advertising
	}
	// Open rather than the AdministratorCommissioning snapshot: the node's
	// own window — the operator's window, the window an uncommissioned node
	// opens at start — reads WindowNotOpen on the cluster, as matter.js keeps
	// those attributes for administrator windows alone, and the status
	// surface would call a live window closed.
	if window := n.Window(); window.Open {
		res.WindowOpen = true
		res.WindowDuration = uint16(min(window.Timeout/time.Second, math.MaxUint16)) //nolint:gosec // clamped to uint16
	}
	if fabrics, err := n.FabricInfos(ctx); err == nil {
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
// asm is the bridge's assembler: listing a candidate asks its source for
// its device with the request the bridge's walk passes, so the listing
// never makes a source build anew. Its expose_secondary_channels is the
// value the daemon booted with — the field is restart-required in
// [config.RestartRules], so the SPA badges it and the save response says
// so.
type matterCandidateProviderAdapter struct {
	reg *central.Registry
	asm *matteradapter.Assembler
}

// MatterCandidates calls [matteradapter.Assembler.CollectCandidates] directly (rather
// than through a stored closure) so the production reachability graph can
// trace the Matter eligibility entry points from this method — the
// reachability analyzer seeds the handler that invokes this as an entry
// point but cannot follow an indirect call through a func-typed struct
// field.
func (a *matterCandidateProviderAdapter) MatterCandidates(_ context.Context) []matteradapter.Candidate {
	if a == nil || a.reg == nil || a.asm == nil {
		return nil
	}
	var out []matteradapter.Candidate
	for _, u := range a.reg.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		out = append(out, a.asm.CollectCandidates(u.Name(), u.ModelRegistry.List())...)
	}
	return out
}
