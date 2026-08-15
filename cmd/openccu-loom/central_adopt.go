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
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// errCentralNotLive is returned by [centralOrchestrator.removeCentral] when
// name is live nowhere: neither adopted at runtime nor present in the shared
// registry (never configured, disabled at boot, or already removed). Callers
// that only want to guarantee "not live anymore" — the REST decorator's
// Delete — treat this as a tolerated no-op rather than a hard failure, since
// such a central still needs its persisted row deleted.
var errCentralNotLive = errors.New("central_adopt: central not live-managed")

// centralHandle is what [centralOrchestrator] tracks for one live-adopted
// central: the config it was adopted with (purge needs the interface list),
// the north-bound closers [wireCentralNorthbound] returned, and the Matter
// per-central unwire (nil when the bridge is disabled).
type centralHandle struct {
	cc      config.CentralConfig
	avail   func()
	climate func()
	// unwires holds every per-central domain unwire from
	// [centralOrchestrator.attachCentralHooks], in attach order. They are
	// kept here and not only in the adopt-time rollback list because that
	// list is discarded once the adopt succeeds: an unwire that lives only
	// there lets a removed central keep publishing.
	unwires []func()
	// detachBridge releases the north-bound event bridge. It is separate
	// from unwires because it must run LAST — see [centralHooks].
	detachBridge func()
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
	// lifecycleLocks serializes adopt and remove per central name.
	//
	// Without it removeCentral decided purely on the presence of a handle,
	// which adoptCentral only publishes at its very end: a DELETE arriving
	// while an adopt was still between reg.Register and that publish got
	// errCentralNotLive, which the REST decorator tolerates — so the
	// persisted row was deleted while the central stayed fully live
	// (registry entry, bring-up goroutines, callback route). Every later
	// DELETE then answered 404 and only a daemon restart removed it.
	lifecycleLocks map[string]*sync.Mutex
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
	// securityHook subscribes an adopted central onto the Security &
	// Safety domain. Its unwire half also drops the central from the
	// aggregate, without which a removed CCU leaves ghost sources
	// pinning their hazard class permanently active.
	securityHook func(u *central.Unit) (unwire func())
	// eventSourceHook feeds an adopted central's model event sources from
	// its device triggers. Set via
	// [centralOrchestrator.setEventSourceCentralHook]. Without it the
	// central's channels keep event groups that can never record a
	// trigger — indistinguishable from a fleet whose buttons nobody has
	// pressed, because the feed only walks the registry once at boot.
	eventSourceHook func(u *central.Unit) (unwire func())
	// centralSeed primes a fresh unit's health tracker, gauges,
	// observability recorder and metrics aggregator. Set via
	// [centralOrchestrator.setCentralSeed]. The second argument is the
	// central's `primary_interface` pin, which the adopted row carries and
	// the boot config does not know about.
	//
	// It is a field of its own rather than a registry observer because it is
	// the one wiring that must run BEFORE the unit enters the shared
	// registry: it writes unsynchronised Unit fields the serving handlers
	// read, and the registry is what makes the unit observable.
	centralSeed func(u *central.Unit, primaryInterface string)
	// hubReadyTrigger fires a debounced hub-publisher re-Start once a central's
	// serial resolves. Set via [centralOrchestrator.setHubReadyTrigger] from the
	// southbound wiring result so a runtime-adopted central publishes its
	// serial-gated hub discovery the same way a boot-time central does. Nil when
	// MQTT is not configured.
	hubReadyTrigger func()
}

// centralHooks is what [centralOrchestrator.attachCentralHooks] returns: one
// ordered list holding EVERY per-central unwire, in attach order.
//
// The list is deliberately not a set of named fields. It used to be, and the
// security domain's unwire was the one that reached only the rollback path —
// so a removed central kept its hazard sources pinned active forever, exactly
// the damage the hook's own comment warned about. A list cannot be
// half-remembered: a hook that registers an unwire is torn down, or it is not
// registered at all.
type centralHooks struct {
	unwires []func()
	// detachBridge is the one teardown that is deliberately NOT in the list:
	// the north-bound event bridge carries the per-device retractions that
	// the model eviction publishes during teardown, so detaching it in
	// attach order — first — silently discarded every one of them and left
	// the removed CCU's HA discovery configs and raw-plane topics retained
	// on the broker forever. It runs after the eviction instead; see
	// [centralOrchestrator.removeCentral].
	detachBridge func()
}

// attachCentralHooks subscribes an adopted central onto every per-central
// domain before its southbound bring-up starts, so the first event it reports
// already has a listener. Each hook is nil while its subsystem is disabled.
//
// Everything that merely needs "one call per central" rides
// [central.Registry.OnRegister] instead and is already attached by the time
// this runs — reg.Register happens earlier in adoptCentral. What is left here
// are the hooks whose attach order relative to the bring-up is load-bearing.
//
// Order is load-bearing where noted; the block is extracted from adoptCentral
// so the sequence reads as one thing and the caller stays inside its
// statement budget.
func (o *centralOrchestrator) attachCentralHooks(unit *central.Unit) centralHooks {
	o.mu.Lock()
	matterHook := o.matterHook
	alarmHook := o.alarmHook
	securityHook := o.securityHook
	eventSourceHook := o.eventSourceHook
	hubReadyTrigger := o.hubReadyTrigger
	o.mu.Unlock()

	var h centralHooks
	keep := func(unwire func()) {
		if unwire != nil {
			h.unwires = append(h.unwires, unwire)
		}
	}

	// The north-bound event bridge first: Start snapshots the registry once
	// at boot, so without this subscription the adopted central's bus reaches
	// neither the MQTT fan-out nor the WebSocket live plane, and the CCU
	// stays invisible to every north-bound consumer until a daemon restart.
	if bridge := o.sbDeps.bridge; bridge != nil {
		bridge.AttachCentral(unit)
		name := unit.Name()
		h.detachBridge = func() { bridge.DetachCentral(name) }
	}
	if matterHook != nil {
		keep(matterHook(unit))
	}
	if alarmHook != nil {
		keep(alarmHook(unit))
	}
	// The security domain attaches after the alarm service so its index
	// rebuild sees the enrollment the alarm service has already loaded.
	//
	// Its index build is intentionally NOT relied upon here: this runs
	// before AddCentral starts the bring-up, so the device model is still
	// empty and any index built now would be too. The domain rebuilds itself
	// off the central's southbound-ready event, which is the only point at
	// which the model exists.
	if securityHook != nil {
		keep(securityHook(unit))
	}
	// Feed the central's model event sources from its device triggers, so the
	// first keypress it reports is recorded on the channel's event group.
	if eventSourceHook != nil {
		keep(eventSourceHook(unit))
	}
	// Subscribe onto the hub-discovery ready pipeline so the serial-gated hub
	// discovery (named central device + sysvars) publishes once the
	// readiness-gated bring-up resolves the serial — the same path a boot-time
	// central takes. Subscribing BEFORE AddCentral launches the bring-up makes
	// the ready event a non-race.
	if hubReadyTrigger != nil {
		keep(subscribeHubReadyTrigger(unit.EventBus, hubReadyTrigger))
	}
	return h
}

// setCentralSeed installs the per-central health/metrics seed run on every
// runtime-adopted central before it is registered. Nil-safe on both sides.
func (o *centralOrchestrator) setCentralSeed(seed func(u *central.Unit, primaryInterface string)) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.centralSeed = seed
	o.mu.Unlock()
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

// setSecurityCentralHook installs the per-central Security & Safety
// wiring hook. Nil-safe on both sides, mirroring setAlarmCentralHook.
func (o *centralOrchestrator) setSecurityCentralHook(hook func(u *central.Unit) (unwire func())) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.securityHook = hook
	o.mu.Unlock()
}

// setEventSourceCentralHook installs the per-central event-source feed hook.
// Nil-safe on both sides, mirroring setSecurityCentralHook.
func (o *centralOrchestrator) setEventSourceCentralHook(hook func(u *central.Unit) (unwire func())) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.eventSourceHook = hook
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
	// Hold the per-name lifecycle lock for the whole adopt, so a remove
	// either waits for a complete central or finds nothing at all — never
	// the half-built state between Register and the handle publish.
	lock := o.lifecycleLock(cc.Name)
	lock.Lock()
	defer lock.Unlock()

	logger := o.logger.With(slog.String("central", cc.Name))
	unit, err := central.New(central.Config{
		Name:         cc.Name,
		InstanceName: o.instanceName,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("central_adopt: new unit %s: %w", cc.Name, err)
	}
	// Seed health, gauges, observability and metrics BEFORE the unit becomes
	// visible in the shared registry — those setters write fields the serving
	// handlers read without synchronisation, and boot writes them while
	// nothing is serving yet.
	o.mu.Lock()
	seed := o.centralSeed
	o.mu.Unlock()
	if seed != nil {
		// The pin travels with the adopted config: it comes from the row the
		// operator saved, which never reaches cfg.Centrals.
		seed(unit, cc.PrimaryInterface)
	}
	// The un_ignore patterns travel with the adopted row the same way, and the
	// only other reader of them is the boot walk over cfg.Centrals — which
	// learns about this CCU one restart from now. Seed them BEFORE Register so
	// the visibility observer that Register runs already finds the rows;
	// without them every parameter the operator un-ignored on this CCU stays
	// suppressed for the rest of the run.
	if store := o.sbDeps.visibilityUnIgnoreStore; store != nil && len(cc.Visibility.UnIgnore) > 0 {
		if err := store.SeedIfEmpty(ctx, cc.Name, cc.Visibility.UnIgnore); err != nil {
			logger.Warn("visibility.unignore.seed_failed", slog.String("err", err.Error()))
		}
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
	hooks := o.attachCentralHooks(unit)
	undo = append(undo, hooks.unwires...)
	// A failed adopt has no model to evict, so the ordering constraint that
	// keeps detachBridge out of the unwire list does not apply here — the
	// rollback simply must not leave the bridge attached to a central that
	// never came up.
	if hooks.detachBridge != nil {
		undo = append(undo, hooks.detachBridge)
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
	o.handles[cc.Name] = &centralHandle{
		cc: cc, avail: avail, climate: climate,
		unwires: hooks.unwires, detachBridge: hooks.detachBridge,
	}
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
// It tears a boot-time central down too: liveness is decided by the shared
// registry, not by whether this orchestrator happened to adopt the central.
// Returns [errCentralNotLive] only when the name is live nowhere — the caller
// decides whether that is fatal.
func (o *centralOrchestrator) removeCentral(ctx context.Context, name string) error {
	lock := o.lifecycleLock(name)
	lock.Lock()
	defer lock.Unlock()

	o.mu.Lock()
	h, ok := o.handles[name]
	if ok {
		delete(o.handles, name)
	}
	o.mu.Unlock()
	if !ok {
		// No handle, but the central may still be live: `handles` is written
		// only by adoptCentral, so every central the boot path registered —
		// the normal case once the daemon has been restarted after
		// onboarding — was invisible here. removeCentral returned
		// errCentralNotLive, both REST mutators tolerate that sentinel, and
		// the persisted row was deleted (or flipped to disabled) while the
		// CCU stayed fully live: registry entry, bring-up goroutines,
		// callback routes, MQTT/WS publishing, scheduler jobs. The SPA showed
		// it gone, a second DELETE answered 404, and only a restart made the
		// deletion real.
		//
		// Fall back to the registry, which is the authority on liveness, and
		// recover the interface list from the boot config so purgeCentralState
		// still has something to purge. The per-central subscriptions ride the
		// unit's EventBus and are dropped by Unit.Stop.
		if h = o.bootHandle(name); h == nil {
			return fmt.Errorf("%w: %s", errCentralNotLive, name)
		}
	}

	o.bringUp.RemoveCentral(name)

	// Every per-central domain unwire, in attach order: the Matter
	// subscriptions go first — no readiness, reassemble or hub broadcast
	// must be processed for a central whose teardown has begun — and all of
	// them run before the model teardown below.
	for _, unwire := range h.unwires {
		unwire()
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
	// The event bridge is released only now: it owns the DeviceRemovedEvent
	// subscription that turns the eviction above into MQTT retractions and
	// into the release of the removed devices' week-profile / schedule /
	// firmware callbacks. Detaching it with the other unwires cost both.
	if h.detachBridge != nil {
		h.detachBridge()
	}

	purgeCentralState(ctx, o.valuesCacheStore, o.masterValuesStore, o.historyStore, o.recordingStore, h.cc, o.logger)
	o.logger.Info("central.remove.live", slog.String("central", name))
	return nil
}

// bootHandle synthesises the teardown handle for a central this orchestrator
// never adopted but that IS live in the shared registry — i.e. one the boot
// path registered. It returns nil when the name is not registered, which is
// the only case that genuinely deserves [errCentralNotLive].
//
// The handle carries the boot config's interface list (what purgeCentralState
// needs) and the event-bridge detach, which is keyed by name and therefore
// reachable without an adopt-time closure. Everything else a boot-time central
// subscribed rides its own EventBus and is dropped by Unit.Stop, which clears
// every subscription.
func (o *centralOrchestrator) bootHandle(name string) *centralHandle {
	if _, live := o.reg.Get(name); !live {
		return nil
	}
	h := &centralHandle{cc: config.CentralConfig{Name: name}}
	if o.cfg != nil {
		for i := range o.cfg.Centrals {
			if o.cfg.Centrals[i].Name == name {
				h.cc = o.cfg.Centrals[i]
				break
			}
		}
	}
	if bridge := o.sbDeps.bridge; bridge != nil {
		h.detachBridge = func() { bridge.DetachCentral(name) }
	}
	return h
}

// lifecycleLock returns the per-central adopt/remove serialization mutex,
// creating it on first use. Keyed by name so two different CCUs stay
// independent; the map itself is guarded by o.mu.
func (o *centralOrchestrator) lifecycleLock(name string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lifecycleLocks == nil {
		o.lifecycleLocks = make(map[string]*sync.Mutex)
	}
	m, ok := o.lifecycleLocks[name]
	if !ok {
		m = &sync.Mutex{}
		o.lifecycleLocks[name] = m
	}
	return m
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
		iface := hmtypes.ParseWireInterfaceID(d.InterfaceID)
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
		// The cached rows are keyed by the canonical `<central>-<interface>`
		// wire id the device pipeline stamps onto its devices, while the
		// config names the interface bare — deleting under the bare name is
		// an exact-match DELETE that matches nothing.
		wireIface := cachereset.StoreInterfaceID(cc.Name, ifc.Name)
		if valuesCacheStore != nil {
			if _, err := valuesCacheStore.DeleteForInterface(ctx, cc.Name, wireIface); err != nil {
				logger.Warn("central.remove.purge_values_cache",
					slog.String("central", cc.Name), slog.String("interface", wireIface), slog.String("err", err.Error()))
			}
		}
		if masterValuesStore != nil {
			if _, err := masterValuesStore.DeleteForInterface(ctx, cc.Name, wireIface); err != nil {
				logger.Warn("central.remove.purge_master_values",
					slog.String("central", cc.Name), slog.String("interface", wireIface), slog.String("err", err.Error()))
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
