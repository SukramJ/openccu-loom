// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errCentralNotLive is returned by [centralOrchestrator.removeCentral] when
// name is not currently live-managed (never adopted at runtime, or already
// removed). Callers that only want to guarantee "not live anymore" — the
// REST decorator's Delete — treat this as a tolerated no-op rather than a
// hard failure, since a disabled / never-adopted central still needs its
// persisted row deleted.
var errCentralNotLive = errors.New("central_adopt: central not live-managed")

// centralHandle is what [centralOrchestrator] tracks for one live-adopted
// central: the config it was adopted with (purge needs the interface list)
// and the north-bound closers [wireCentralNorthbound] returned.
type centralHandle struct {
	cc      config.CentralConfig
	avail   func()
	climate func()
}

// centralOrchestrator is the live-CCU-adopt composition seam: it drives one
// central's southbound + model + scheduler-jobs lifecycle up or down at
// runtime, reusing exactly
// the primitives boot uses (central.New, Registry.Register/Unregister,
// BringUpManager.AddCentral/RemoveCentral, wireCentralNorthbound,
// registerStandardJobsFor) so a live-adopted central is wired identically to
// a boot-time one. REST is the only caller today (the decorator in
// central_adopt_admin.go); it is not itself an HTTP concern.
type centralOrchestrator struct {
	reg          *central.Registry
	bringUp      *adapter.BringUpManager
	sbDeps       southboundWiringDeps
	cfg          *config.Config
	logger       *slog.Logger
	instanceName string

	valuesCacheStore  *sqlite.ValuesCacheStore
	masterValuesStore *sqlite.MasterValuesStore
	historyStore      *sqlite.MeasurementStore

	mu      sync.Mutex
	handles map[string]*centralHandle
}

// newCentralOrchestrator builds the orchestrator from the daemon's
// already-constructed collaborators. Returns nil when bringUp is nil — the
// southbound wiring phase produces a nil [*adapter.BringUpManager] when it
// never came up (mirrors the nil-tolerant pattern [buildCacheResetService]
// uses for the same signal) — so live adopt/remove is simply unavailable
// rather than reaching for southbound machinery that was never built.
func newCentralOrchestrator(
	reg *central.Registry,
	bringUp *adapter.BringUpManager,
	sbDeps southboundWiringDeps,
	cfg *config.Config,
	logger *slog.Logger,
	instanceName string,
	valuesCacheStore *sqlite.ValuesCacheStore,
	masterValuesStore *sqlite.MasterValuesStore,
	historyStore *sqlite.MeasurementStore,
) *centralOrchestrator {
	if bringUp == nil {
		return nil
	}
	return &centralOrchestrator{
		reg:               reg,
		bringUp:           bringUp,
		sbDeps:            sbDeps,
		cfg:               cfg,
		logger:            logger,
		instanceName:      instanceName,
		valuesCacheStore:  valuesCacheStore,
		masterValuesStore: masterValuesStore,
		historyStore:      historyStore,
		handles:           make(map[string]*centralHandle),
	}
}

// isRegistered reports whether name is currently registered in the shared
// central registry — true for both a boot-time central and one this
// orchestrator adopted. The REST decorator uses this to make PUT idempotent
// for an already-live central (update-in-place is out of scope for PR3).
func (o *centralOrchestrator) isRegistered(name string) bool {
	_, ok := o.reg.Get(name)
	return ok
}

// adoptCentral brings up one central's southbound + model + scheduler-jobs
// at runtime, wiring it identically to a boot-time central
// (internal/central/bootstrap.go + cmd/openccu-loom/daemon.go): construct
// the Unit, register it in the shared registry, wire the devices-created
// gate, register the standard scheduler jobs, start the scheduler, bring up
// southbound (callback routes + readiness-gated device pull), then run the
// per-central north-bound hooks. Any failure after Register rolls back
// every step already taken so a failed adopt never leaves an orphaned Unit
// in the registry or a dangling southbound handle.
func (o *centralOrchestrator) adoptCentral(ctx context.Context, cc config.CentralConfig) error {
	if cc.Name == "" {
		return errors.New("central_adopt: central name required")
	}

	logger := o.logger.With(slog.String("central", cc.Name))
	unit, err := central.New(central.Config{
		Name:         cc.Name,
		InstanceName: o.instanceName,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("central_adopt: new unit %s: %w", cc.Name, err)
	}
	if err := o.reg.Register(unit); err != nil {
		return fmt.Errorf("central_adopt: register %s: %w", cc.Name, err)
	}

	// rollback undoes every step already performed once Register succeeded.
	// bringUp.RemoveCentral and Unit.Stop are both safe to call on a handle /
	// unit that never reached that step (RemoveCentral no-ops when the name
	// is not managed; Stop is idempotent).
	rollback := func() { //nolint:contextcheck // Unit.Stop takes no ctx parameter; shutdown always runs to completion regardless of the caller's ctx
		o.bringUp.RemoveCentral(cc.Name)
		unit.Stop()
		o.reg.Unregister(cc.Name)
	}

	// Wire the devices-created gate BEFORE the scheduler starts, mirroring
	// bootstrap + daemon.go's boot-time ordering (daemon.go: WireDevicesCreatedGate
	// runs before registerStandardJobs/StartAll) so the gated hub jobs have a
	// working gate from t=0 for this central too.
	unit.WireDevicesCreatedGate()
	registerStandardJobsFor(unit, o.cfg, o.logger)

	if err := unit.Start(ctx); err != nil {
		rollback()
		return fmt.Errorf("central_adopt: start %s: %w", cc.Name, err)
	}

	// Southbound bring-up (callback routes + readiness-gated device pull) and
	// the north-bound hooks run AFTER Start, matching wireSouthbound's
	// boot-time order (reg.StartAll happens before wireSouthbound/
	// wireCentralNorthbound in daemon.go) — EvaluateCentralState inside
	// wireCentralNorthbound expects the state machine already in RUNNING.
	ccCopy := cc
	//nolint:contextcheck // AddCentral launches the readiness-gated bring-up on the manager's own teardown-bounded parentCtx, not this call's ctx
	if !o.bringUp.AddCentral(&ccCopy, unit) {
		rollback()
		return fmt.Errorf("central_adopt: %s already managed by bring-up", cc.Name)
	}
	avail, climate := wireCentralNorthbound(o.sbDeps, unit)

	o.mu.Lock()
	o.handles[cc.Name] = &centralHandle{cc: cc, avail: avail, climate: climate}
	o.mu.Unlock()

	o.logger.Info("central.adopt.live", slog.String("central", cc.Name))
	return nil
}

// removeCentral tears one live-adopted central down at runtime: deregister
// its callback routes and drain southbound goroutines first (so no further
// callback can arrive while the rest of teardown runs), then run the
// north-bound closers, stop the Unit (whose StopTierExternal hook —
// installed by wireCentralNorthbound — unregisters it from the shared
// registry), evict its in-memory model, and purge its derived SQLite rows.
// Returns [errCentralNotLive] when name was never adopted through this
// orchestrator (or was already removed) — the caller decides whether that is
// fatal.
func (o *centralOrchestrator) removeCentral(ctx context.Context, name string) error {
	o.mu.Lock()
	h, ok := o.handles[name]
	if ok {
		delete(o.handles, name)
	}
	o.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", errCentralNotLive, name)
	}

	o.bringUp.RemoveCentral(name)

	// Climate before availability, mirroring wireSouthbound's teardown order
	// (its LIFO defer runs climate closers before availability closers).
	if h.climate != nil {
		h.climate()
	}
	if h.avail != nil {
		h.avail()
	}

	if unit, live := o.reg.Get(name); live {
		unit.Stop() //nolint:contextcheck // Unit.Stop takes no ctx parameter; teardown always runs to completion regardless of the caller's ctx
		evictModel(unit)
	}

	purgeCentralState(ctx, o.valuesCacheStore, o.masterValuesStore, o.historyStore, h.cc, o.logger)
	o.logger.Info("central.remove.live", slog.String("central", name))
	return nil
}

// evictModel drops every device from unit's in-memory model, mirroring
// centralBringUp.clearModel (internal/central/adapter/central_bringup.go)
// via Unit's exported registries — clearModel itself is unexported to the
// adapter package, so live remove replicates its sequence rather than
// reaching into that package.
func evictModel(u *central.Unit) {
	if u == nil || u.ModelRegistry == nil {
		return
	}
	for _, d := range u.ModelRegistry.List() {
		iface := hmenum.Interface(d.InterfaceID)
		if u.DescRegistry != nil {
			u.DescRegistry.Delete(iface, d.Address)
		}
		if u.ParamsetReg != nil {
			for _, ch := range d.Channels() {
				u.ParamsetReg.DeleteChannel(iface, ch.Address)
			}
		}
		if u.DeviceRegistry != nil {
			u.DeviceRegistry.Remove(iface, d.Address)
		}
		u.RemoveDevice(d.Address)
	}
}

// purgeCentralState deletes every derived SQLite row keyed to a removed
// central: per-interface VALUES/MASTER cache rows and the measurement
// history. The `centrals` row itself is deliberately NOT deleted here — the
// REST decorator's Delete calls the underlying [handlers.CentralAdminService]
// (persistence) right after this returns, so deleting it here would race /
// double-delete against that call.
func purgeCentralState(
	ctx context.Context,
	valuesCacheStore *sqlite.ValuesCacheStore,
	masterValuesStore *sqlite.MasterValuesStore,
	historyStore *sqlite.MeasurementStore,
	cc config.CentralConfig,
	logger *slog.Logger,
) {
	for _, ifc := range cc.Interfaces {
		if valuesCacheStore != nil {
			if _, err := valuesCacheStore.DeleteForInterface(ctx, cc.Name, ifc.Name); err != nil {
				logger.Warn("central.remove.purge_values_cache",
					slog.String("central", cc.Name), slog.String("interface", ifc.Name), slog.String("err", err.Error()))
			}
		}
		if masterValuesStore != nil {
			if _, err := masterValuesStore.DeleteForInterface(ctx, cc.Name, ifc.Name); err != nil {
				logger.Warn("central.remove.purge_master_values",
					slog.String("central", cc.Name), slog.String("interface", ifc.Name), slog.String("err", err.Error()))
			}
		}
	}
	if historyStore != nil {
		if err := historyStore.DeleteForCentral(ctx, cc.Name); err != nil {
			logger.Warn("central.remove.purge_history", slog.String("central", cc.Name), slog.String("err", err.Error()))
		}
	}
}

// liveCentralAdmin wraps a persisted [handlers.CentralAdminService] (the
// *sqlite.CentralsStore the router mounts by default) so POST/PUT/DELETE
// /admin/centrals also drive [centralOrchestrator] — the REST injection
// seam for live CCU adopt. Get/List pass through unchanged.
type liveCentralAdmin struct {
	store  handlers.CentralAdminService
	orch   *centralOrchestrator
	logger *slog.Logger
}

// newLiveCentralAdmin returns the decorated service, or store unchanged when
// persistence or the orchestrator is unavailable (e.g. southbound never came
// up) — the same nil-tolerant pattern the rest of the composition root uses.
func newLiveCentralAdmin(store handlers.CentralAdminService, orch *centralOrchestrator, logger *slog.Logger) handlers.CentralAdminService {
	if store == nil || orch == nil {
		return store
	}
	return &liveCentralAdmin{store: store, orch: orch, logger: logger}
}

func (l *liveCentralAdmin) Get(ctx context.Context, name string) (sqlite.CentralRow, error) {
	return l.store.Get(ctx, name)
}

func (l *liveCentralAdmin) List(ctx context.Context) ([]sqlite.CentralRow, error) {
	return l.store.List(ctx)
}

// Put persists row (create or full-replace update) then adopts it live when
// enabled. An update to an already-registered central (boot-time or already
// live-adopted) skips adopt — live in-place reconfiguration of a running
// central is out of scope for now — so
// PUT stays a safe no-op-adopt for the common "edit an already-live central"
// case instead of surfacing central.ErrAlreadyRegistered to the operator.
func (l *liveCentralAdmin) Put(ctx context.Context, row sqlite.CentralRow) error {
	if err := l.store.Put(ctx, row); err != nil {
		return err
	}
	if !row.Enabled {
		return nil
	}
	if l.orch.isRegistered(row.Name) {
		l.logger.Info("central.adopt.skip_already_live", slog.String("central", row.Name))
		return nil
	}
	cc, _ := configstore.RowToCentralConfig(row, os.Getenv)
	if err := l.orch.adoptCentral(ctx, cc); err != nil {
		l.logger.Error("central.adopt.failed", slog.String("central", row.Name), slog.String("err", err.Error()))
		return err
	}
	return nil
}

// Delete tears the central down live BEFORE removing the persisted row, so a
// failed live teardown leaves the row in place for a retry rather than
// forgetting about a central whose southbound may still be wired. A name
// that was never live-adopted ([errCentralNotLive]) is tolerated — the row
// still needs deleting.
func (l *liveCentralAdmin) Delete(ctx context.Context, name string) error {
	if err := l.orch.removeCentral(ctx, name); err != nil && !errors.Is(err, errCentralNotLive) {
		l.logger.Error("central.remove.failed", slog.String("central", name), slog.String("err", err.Error()))
		return err
	}
	return l.store.Delete(ctx, name)
}
