// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
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
		if res.WindowOpen {
			res.WindowDuration = r.window.RequestedDurationSeconds()
		}
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
//
// It carries the runtime teardown because no cluster command runs on this
// path: the OperationalCredentials RemoveFabric hooks that close sessions and
// retire the mDNS instance fire only for a commissioner's wire invocation, so
// the operator surfaces have to run the same fan-out themselves.
type matterFabricRevokerAdapter struct {
	store *matterstore.Store
	// opCreds is the live OperationalCredentials cluster instance. Nil in
	// tests that exercise the store half alone. See [RevokeFabric].
	opCreds *mattercore.OperationalCredentials
	// teardown is the shared fabric-removal fan-out (session + subscription
	// close, resumption purge, fabric-removed emission). Nil only in tests
	// that exercise the store half alone.
	teardown func(ctx context.Context, fabricIndex uint8)
	// withdraw retires the removed identity's operational mDNS instance and
	// republishes the remaining fabric set.
	withdraw func(ctx context.Context, compressedID [8]byte, nodeID uint64)
}

// RevokeFabric implements [handlers.MatterFabricRevoker]: it removes the
// fabric row AND runs every runtime consequence the wire command's
// RemoveFabric would have triggered.
//
// Removing the row alone was the whole implementation once, and it read as a
// success on every surface: the SPA reported "removed", fabric_count dropped
// to zero, and the unpaired controller kept its live CASE session, its
// subscription and the operational `_matter._tcp` record until the daemon
// restarted.
//
// It also runs [mattercore.OperationalCredentials.NotifyFabricRemoved]
// directly — the same call handleRemoveFabric makes inline for the wire
// command — rather than through the shared a.teardown closure: that closure
// is also the wire command's own onFabricRemoved hook, and the wire path
// already runs NotifyFabricRemoved itself, so folding it into the shared
// closure would double it there. Without this call here at all, the
// OperationalCredentials cluster's DataVersion never bumps for a REST
// revoke or a factory reset, so a controller reading behind a cached
// DataVersionFilter keeps seeing the removed fabric in Fabrics /
// CommissionedFabrics.
func (a *matterFabricRevokerAdapter) RevokeFabric(ctx context.Context, fabricIndex uint8) error {
	if a == nil || a.store == nil {
		return matterbridge.ErrCommissioningWindowNotConfigured
	}
	// Read before delete: the operational advertisement is keyed by the
	// fabric's compressed ID + node ID, and the row is the only place that
	// knows them. A failed read is therefore a failed revoke, not a revoke
	// without the mDNS half: deleting the row first and skipping the withdraw
	// on a transient store error (a concurrent write holding the table) leaves
	// the unpaired controller's operational `_matter._tcp` instance advertised
	// until the daemon restarts, while every surface reports the fabric gone.
	// The operator can retry a revoke that failed; they cannot retry one that
	// answered 204.
	rec, err := a.store.GetFabric(ctx, fabricIndex)
	if err != nil {
		return err
	}
	if err := a.store.RemoveFabric(ctx, fabricIndex); err != nil {
		return err
	}
	if a.opCreds != nil {
		a.opCreds.NotifyFabricRemoved(fabricIndex)
	}
	if a.withdraw != nil {
		a.withdraw(ctx, rec.CompressedID, rec.NodeID)
	}
	if a.teardown != nil {
		a.teardown(ctx, fabricIndex)
	}
	return nil
}

// ListFabricIndexes implements [handlers.MatterFabricPurger]. The
// factory reset needs the set to remove, and the store is the only
// place that knows which fabrics survived a restart.
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
