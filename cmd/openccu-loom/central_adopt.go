// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
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
// central: the config it was adopted with (purge needs the interface list),
// the north-bound closers [wireCentralNorthbound] returned, and the Matter
// per-central unwire (nil when the bridge is disabled).
type centralHandle struct {
	cc      config.CentralConfig
	avail   func()
	climate func()
	matter  func()
	alarm   func()
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
	recordingStore    *sqlite.RecordingOverrideStore

	mu      sync.Mutex
	handles map[string]*centralHandle
	// matterHook wires an adopted central into the running Matter bridge
	// (readiness latch + reassemble-on-ready + reachable forward). Set via
	// [centralOrchestrator.setMatterCentralHook] after the Matter runtime
	// is stood up (the orchestrator is constructed first); nil while the
	// bridge is disabled or never came up.
	matterHook matterCentralHook
	// alarmHook subscribes an adopted central onto the alarm service's
	// event routing. Set via [centralOrchestrator.setAlarmCentralHook];
	// nil while the alarm engine is disabled.
	alarmHook func(u *central.Unit) (unwire func())
	// hubReadyTrigger fires a debounced hub-publisher re-Start once a central's
	// serial resolves. Set via [centralOrchestrator.setHubReadyTrigger] from the
	// southbound wiring result so a runtime-adopted central publishes its
	// serial-gated hub discovery the same way a boot-time central does. Nil when
	// MQTT is not configured.
	hubReadyTrigger func()
}

// setAlarmCentralHook installs the per-central alarm wiring hook.
// Nil-safe on both sides, mirroring setMatterCentralHook.
func (o *centralOrchestrator) setAlarmCentralHook(hook func(u *central.Unit) (unwire func())) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.alarmHook = hook
	o.mu.Unlock()
}

// setMatterCentralHook installs the per-central Matter wiring hook. Nil-safe
// on both sides: a nil orchestrator (southbound never came up) and a nil hook
// (Matter bridge disabled) are both tolerated no-ops.
func (o *centralOrchestrator) setMatterCentralHook(hook matterCentralHook) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.matterHook = hook
	o.mu.Unlock()
}

// setHubReadyTrigger installs the hub-discovery ready trigger produced by the
// southbound wiring so an adopted central's serial-gated hub discovery is
// (re-)published once its bring-up completes. Nil-safe on both sides.
func (o *centralOrchestrator) setHubReadyTrigger(trigger func()) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.hubReadyTrigger = trigger
	o.mu.Unlock()
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
	recordingStore *sqlite.RecordingOverrideStore,
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
		recordingStore:    recordingStore,
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

	// rollback unwinds only the steps that actually succeeded, in reverse
	// order. Each undo closure is appended after its step completes, so a
	// failed adopt never tears down state it did not create — critically,
	// bringUp.RemoveCentral is registered ONLY after a successful AddCentral,
	// so a rollback triggered by AddCentral returning false (the name is
	// already managed by a pre-existing, e.g. boot-time, bring-up handle)
	// never evicts that foreign handle. The unwind order matches
	// removeCentral's teardown: bringUp.RemoveCentral (drain southbound)
	// before Unit.Stop.
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	undo = append(undo, func() { //nolint:contextcheck // Unit.Stop takes no ctx parameter; shutdown always runs to completion regardless of the caller's ctx
		unit.Stop()
		o.reg.Unregister(cc.Name)
	})

	// Wire the devices-created gate BEFORE the scheduler starts, mirroring
	// bootstrap + daemon.go's boot-time ordering (daemon.go: WireDevicesCreatedGate
	// runs before registerStandardJobs/StartAll) so the gated hub jobs have a
	// working gate from t=0 for this central too.
	unit.WireDevicesCreatedGate()
	registerStandardJobsFor(unit, o.cfg, o.logger)
	registerFirmwareJobsFor(unit, o.sbDeps.valueWriter, o.logger)

	if err := unit.Start(ctx); err != nil {
		rollback()
		return fmt.Errorf("central_adopt: start %s: %w", cc.Name, err)
	}

	// Wire the central into the Matter bridge BEFORE the southbound
	// bring-up starts: the readiness/reassemble subscriptions are then in
	// place before the central's CentralSouthboundReadyEvent can possibly
	// fire, so the Matter snapshotter latches ModelComplete and the
	// topology reassembles exactly as for a boot-time central. (The hook's
	// seed from Unit.IsSouthboundReady would cover a late wiring too, but
	// subscribing first makes the window a non-event.) Nil when the Matter
	// bridge is disabled.
	o.mu.Lock()
	matterHook := o.matterHook
	alarmHook := o.alarmHook
	hubReadyTrigger := o.hubReadyTrigger
	o.mu.Unlock()
	var matterUnwire func()
	if matterHook != nil {
		matterUnwire = matterHook(unit)
		if matterUnwire != nil {
			undo = append(undo, matterUnwire)
		}
	}
	var alarmUnwire func()
	if alarmHook != nil {
		alarmUnwire = alarmHook(unit)
		if alarmUnwire != nil {
			undo = append(undo, alarmUnwire)
		}
	}
	// Subscribe the adopted central onto the hub-discovery ready pipeline so its
	// serial-gated hub discovery (named central device + sysvars) publishes once
	// its readiness-gated bring-up resolves the serial — the same path a
	// boot-time central takes. Subscribing BEFORE AddCentral launches the
	// bring-up makes the ready event a non-race.
	if hubReadyTrigger != nil {
		if unsub := subscribeHubReadyTrigger(unit.EventBus, hubReadyTrigger); unsub != nil {
			undo = append(undo, unsub)
		}
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
	undo = append(undo, func() { o.bringUp.RemoveCentral(cc.Name) })

	avail, climate := wireCentralNorthbound(o.sbDeps, unit)

	o.mu.Lock()
	o.handles[cc.Name] = &centralHandle{cc: cc, avail: avail, climate: climate, matter: matterUnwire, alarm: alarmUnwire}
	o.mu.Unlock()

	o.logger.Info("central.adopt.live", slog.String("central", cc.Name))
	return nil
}

// removeCentral tears one live-adopted central down at runtime: deregister
// its callback routes and drain southbound goroutines first (so no further
// callback can arrive while the rest of teardown runs), then run the
// north-bound closers, evict its in-memory model, stop the Unit (whose
// StopTierExternal hook — installed by wireCentralNorthbound — unregisters it
// from the shared registry), and purge its derived SQLite rows.
//
// The model is evicted BEFORE Unit.Stop: evictModel publishes a
// DeviceRemovedEvent per device, and Unit.Stop clears every EventBus
// subscription. Stopping first would land those retractions on a bus with no
// subscribers, so HA/MQTT discovery configs for the removed central's devices
// would never be retracted (orphaned entities). Mirrors centralBringUp.reinit's
// teardown-then-clearModel ordering (internal/central/adapter/central_bringup.go).
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

	// Drop the Matter per-central subscriptions first — no readiness /
	// reassemble / reachable signal must be processed for a central whose
	// teardown has begun.
	if h.matter != nil {
		h.matter()
	}
	if h.alarm != nil {
		h.alarm()
	}
	// Climate before availability, mirroring wireSouthbound's teardown order
	// (its LIFO defer runs climate closers before availability closers).
	if h.climate != nil {
		h.climate()
	}
	if h.avail != nil {
		h.avail()
	}

	if unit, live := o.reg.Get(name); live {
		evictModel(unit)
		unit.Stop() //nolint:contextcheck // Unit.Stop takes no ctx parameter; teardown always runs to completion regardless of the caller's ctx
	}

	purgeCentralState(ctx, o.valuesCacheStore, o.masterValuesStore, o.historyStore, o.recordingStore, h.cc, o.logger)
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
	recordingStore *sqlite.RecordingOverrideStore,
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
	if recordingStore != nil {
		if err := recordingStore.DeleteForCentral(ctx, cc.Name); err != nil {
			logger.Warn("central.remove.purge_recording_overrides",
				slog.String("central", cc.Name), slog.String("err", err.Error()))
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

// Put persists row (create or full-replace update) then reconciles the live
// state: a newly-enabled row that is not yet registered gets adopted; a
// disabled row that IS currently registered gets torn down (mirroring
// Delete's live-then-persisted order, just with the persist happening
// first here since the row itself is not removed); an edit to a row that
// stays enabled AND already registered cannot be hot-applied — live
// in-place reconfiguration of a running central is out of scope for now —
// so that case is logged instead of silently doing nothing. Every branch
// leaves an audit trail; none of them return a success signal that masks a
// live state the operator did not ask for.
func (l *liveCentralAdmin) Put(ctx context.Context, row sqlite.CentralRow) error {
	// Read the previously persisted row (if any) BEFORE the write so the
	// still-enabled/already-live branch below can tell an actual config
	// change (needs a restart to apply) apart from a no-op re-save.
	prev, prevErr := l.store.Get(ctx, row.Name)
	hadPrev := prevErr == nil

	if err := l.store.Put(ctx, row); err != nil {
		return err
	}

	live := l.orch.isRegistered(row.Name)

	if !row.Enabled {
		if !live {
			return nil
		}
		if err := l.orch.removeCentral(ctx, row.Name); err != nil && !errors.Is(err, errCentralNotLive) {
			l.logger.Error("central.disable.failed", slog.String("central", row.Name), slog.String("err", err.Error()))
			return err
		}
		l.logger.Info("central.disable.live", slog.String("central", row.Name))
		return nil
	}

	if live {
		if hadPrev && centralConfigNeedsRestart(prev, row) {
			l.logger.Warn("central.edit.restart_required", slog.String("central", row.Name))
		} else {
			l.logger.Info("central.adopt.skip_already_live", slog.String("central", row.Name))
		}
		return nil
	}

	cc, _ := configstore.RowToCentralConfig(row, os.Getenv)
	if err := l.orch.adoptCentral(ctx, cc); err != nil {
		l.logger.Error("central.adopt.failed", slog.String("central", row.Name), slog.String("err", err.Error()))
		return err
	}
	return nil
}

// centralConfigNeedsRestart reports whether next's southbound-relevant
// fields differ from prev in a way the running central.Unit cannot pick up
// without a full adopt/remove cycle: host, ports, TLS, credentials, and the
// interface set are all read once at adoptCentral time and never re-read
// afterward. Fields that only affect model/scheduler behavior (Behavior,
// Visibility) are intentionally excluded — those are out of scope for the
// restart signal because whether they are ever hot-reloadable is a separate
// question from this fix.
func centralConfigNeedsRestart(prev, next sqlite.CentralRow) bool {
	return prev.Host != next.Host ||
		prev.Port != next.Port ||
		prev.JSONRPCPort != next.JSONRPCPort ||
		prev.TLS != next.TLS ||
		prev.TLSInsecureSkipVerify != next.TLSInsecureSkipVerify ||
		prev.Username != next.Username ||
		prev.PasswordPlain != next.PasswordPlain ||
		prev.PasswordEnv != next.PasswordEnv ||
		prev.PrimaryInterface != next.PrimaryInterface ||
		!slices.Equal(prev.Interfaces, next.Interfaces)
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
