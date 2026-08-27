// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// centralBringUp owns one central's south-bound bring-up as a restartable
// unit: its own teardown-bounded context, its own ordered closer stack, and
// the goroutine that runs the readiness-gated bring-up. Encapsulating the
// closers per central (rather than pooling them across all centrals at the
// WireCentrals level) is what lets a single central be torn down and brought
// up again mid-life without disturbing its peers — the foundation for the
// "clear caches + re-pull" operation (ADR 0042).
type centralBringUp struct {
	cfg                *config.Config
	cc                 config.CentralConfig
	unit               *central.Unit
	deps               WireDeps
	callbackURL        string
	binRPCCallbackAddr string
	// cbHandlers is the XML-RPC callback handler registered for this
	// central (nil without a callback route). The gated bring-up installs
	// the hot-plug ingestor on it once the pipeline and backends exist.
	cbHandlers *CallbackHandlers
	logger     *slog.Logger

	parentCtx context.Context //nolint:containedctx // teardown-bounded daemon-lifetime ctx; the handle re-derives a child per (re)start

	// reinitMu serializes a full re-init / shutdown cycle (teardown -> clear ->
	// start). Two concurrent clears on the same central would otherwise overlap
	// one start()'s wg.Add with the other's teardown() wg.Wait (a WaitGroup
	// misuse that panics) and leak a bring-up generation by overwriting the
	// live cancel. Distinct from mu, which guards the closer stacks + cancel.
	reinitMu sync.Mutex

	mu sync.Mutex
	// closers are per-generation: installed during a bring-up and run on every
	// teardown (including a re-init), then re-installed by the next bring-up.
	closers []func()
	// permanent closers outlive a re-init — e.g. the local callback routing,
	// which is registered once and must stay routed across re-inits (the
	// gated bring-up re-announces to the CCU but does not re-register the
	// local route). They run only on final shutdown.
	permanent []func()
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	// closed marks the handle as permanently retired by shutdown. Without
	// it a reinit that read the handle just before RemoveCentral deleted it
	// would run teardown (a no-op by then), clearModel and then start —
	// launching a fresh bring-up generation on the still-live parent
	// context, for a central nothing manages any more. That generation
	// re-announces to the CCU and is unreachable from Teardown or a second
	// RemoveCentral: a goroutine and a CCU callback registration that
	// survive until the daemon exits.
	closed atomic.Bool
}

// addPermanentCloser registers a closer that survives re-inits and runs only on
// final shutdown.
func (b *centralBringUp) addPermanentCloser(c func()) {
	if c == nil {
		return
	}
	b.mu.Lock()
	b.permanent = append(b.permanent, c)
	b.mu.Unlock()
}

// addCloser appends a teardown closer for the current bring-up generation.
// Safe for the concurrent gated-bring-up goroutine to call.
func (b *centralBringUp) addCloser(c func()) {
	if c == nil {
		return
	}
	b.mu.Lock()
	b.closers = append(b.closers, c)
	b.mu.Unlock()
}

// start launches the gated bring-up for the current generation. The goroutine
// waits for CCU readiness, then runs the full south-bound bring-up once and
// signals the north-bound adapters. Non-blocking: returns immediately.
func (b *centralBringUp) start() {
	if b.closed.Load() {
		return
	}
	b.mu.Lock()
	ctx, cancel := context.WithCancel(b.parentCtx)
	b.cancel = cancel
	b.mu.Unlock()

	b.wg.Add(1)
	SafeGo("central_bringup."+b.cc.Name, func() {
		defer b.wg.Done()
		ccCopy := b.cc
		gatedCentralBringUp(ctx, b.cfg, &ccCopy, b.unit, b.deps, b.callbackURL, b.binRPCCallbackAddr, b.cbHandlers, b.addCloser, b.logger)
	})
}

// teardown cancels the current generation, waits for its goroutine to drain,
// and runs this central's closers in reverse registration order (the inverse
// of how the bring-up installed them). It leaves the handle ready for a fresh
// start: the closer stack is emptied so the next generation starts clean.
func (b *centralBringUp) teardown() {
	b.mu.Lock()
	cancel := b.cancel
	b.cancel = nil
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	b.wg.Wait()

	b.mu.Lock()
	cs := slices.Clone(b.closers)
	b.closers = nil
	b.mu.Unlock()
	for _, closer := range slices.Backward(cs) {
		closer()
	}
}

// shutdown is the final teardown: it cycles the current generation down and
// then runs the permanent closers (callback routing). The handle is not
// expected to be reused afterwards.
func (b *centralBringUp) shutdown() {
	b.reinitMu.Lock()
	defer b.reinitMu.Unlock()
	// Retire the handle before tearing down: a reinit already blocked on
	// reinitMu must not restart the central afterwards. Whichever of the
	// two wins the lock, the outcome is the same — either reinit completes
	// and shutdown then tears the fresh generation down, or shutdown wins
	// and reinit finds the handle retired.
	b.closed.Store(true)
	b.teardown()
	b.mu.Lock()
	perm := slices.Clone(b.permanent)
	b.permanent = nil
	b.mu.Unlock()
	for _, closer := range slices.Backward(perm) {
		closer()
	}
}

// reinit tears the central's south-bound down, drops every device from the
// model (so a stale device that the CCU no longer reports does not linger and
// north-bound consumers see it removed), then re-runs the readiness-gated
// bring-up which re-pulls device list → descriptions → paramsets → values.
//
// Persisted-row and in-memory descriptor/value-cache clearing is the caller's
// responsibility (the cachereset service) and must happen BEFORE reinit so the
// re-pull starts from a clean slate. reinit only cycles the bring-up itself.
//
// ctx bounds the teardown/clear work; the subsequent re-pull runs on the
// handle's own teardown-bounded context (it may wait indefinitely for a
// co-booting CCU, exactly as at first boot).
func (b *centralBringUp) reinit(ctx context.Context) {
	b.reinitMu.Lock()
	defer b.reinitMu.Unlock()
	if b.closed.Load() {
		b.logger.Info("central.reinit.skipped_removed", slog.String("central", b.cc.Name))
		return
	}
	b.logger.Info("central.reinit.begin", slog.String("central", b.cc.Name))
	b.teardown()
	b.clearModel()
	if ctx.Err() != nil {
		b.logger.Warn("central.reinit.cancelled", slog.String("central", b.cc.Name))
		return
	}
	//nolint:contextcheck // the re-pull runs on the handle's teardown-bounded parent ctx, not the short-lived reinit ctx
	b.start()
	b.logger.Info("central.reinit.repull_started", slog.String("central", b.cc.Name))
}

// clearModel removes every device from the unit's model registry — channel
// teardown (event groups, calculated-DP subscriptions, custom-DP bindings)
// and EventBus subscription sweep — and
// also drops each device's in-memory device-description + paramset entries
// (mirrors the unpair cleanup, DeviceCoordinator.RefreshAfterUnpair).
//
// Clearing the descriptions is what lets the re-pull forget a device the CCU no
// longer reports: the re-pull's ListDevices omits it, but
// CheckAndCreateDevicesFromCache would otherwise re-materialise it from a stale
// description still in the registry — resurrecting a device removed on the CCU.
//
// Removal goes through [central.Unit.RemoveDeviceForTeardown], whose event
// carries ModelTeardown. That flag is what separates the two groups of
// consumers this teardown has: the persistent VALUES/MASTER cache evictors
// stand down (the operator's requested scope was already deleted by
// cachereset.Service.Clear, and evicting again would wipe every other device's
// cache — ADR 0042), while every north-bound plane still reacts.
//
// Suppressing the event altogether was the earlier shape, and it was wrong in
// the one case that matters: a device the CCU has genuinely dropped is not
// re-created by the re-pull, so nothing ever told MQTT to retract its
// discovery config, the WebSocket to report the deletion, or the event bridge
// to release its live subscriptions. The stale entity survived until the next
// daemon boot's orphan sweep.
func (b *centralBringUp) clearModel() {
	if b.unit == nil || b.unit.ModelRegistry == nil {
		return
	}
	for _, d := range b.unit.ModelRegistry.List() {
		iface := hmtypes.ParseWireInterfaceID(d.InterfaceID)
		if b.unit.DescRegistry != nil {
			b.unit.DescRegistry.Delete(iface, d.Address)
		}
		if b.unit.ParamsetReg != nil {
			for _, ch := range d.Channels() {
				b.unit.ParamsetReg.DeleteChannel(iface, ch.Address)
			}
		}
		if b.unit.DeviceRegistry != nil {
			b.unit.DeviceRegistry.Remove(iface, d.Address)
		}
		// The channel teardown (event groups, calculated-DP subscriptions,
		// custom-DP bindings) and the EventBus subscription sweep happen
		// inside the removal itself, in the order it already guarantees.
		b.unit.RemoveDeviceForTeardown(d.Address)
	}
}

// BringUpManager holds every central's restartable bring-up handle and is the
// surface the daemon hands to the cachereset service so a named central can be
// re-initialized mid-life. It also carries the aggregate teardown for daemon
// shutdown, preserving the original WireCentrals teardown semantics.
type BringUpManager struct {
	mu        sync.Mutex
	byCentral map[string]*centralBringUp
	order     []string
	// parentCancel cancels the shared teardown-bounded parent context that
	// every handle derives its per-generation context from. Fired once after
	// all handles have shut down.
	parentCancel context.CancelFunc
	// The following are captured from WireCentrals so a single central can be
	// built + started at runtime (live CCU adopt) exactly as at boot: parentCtx
	// is the teardown-bounded daemon-lifetime context every handle derives from;
	// cfg/deps/logger are the shared wiring inputs. Immutable after WireCentrals.
	parentCtx context.Context //nolint:containedctx // teardown-bounded daemon-lifetime ctx shared by every handle; the source of a runtime-added handle's parentCtx
	cfg       *config.Config
	deps      WireDeps
	logger    *slog.Logger
}

func newBringUpManager() *BringUpManager {
	return &BringUpManager{byCentral: make(map[string]*centralBringUp)}
}

func (m *BringUpManager) add(b *centralBringUp) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byCentral[b.cc.Name] = b
	m.order = append(m.order, b.cc.Name)
}

// buildAndStart constructs a single central's bring-up handle — hydrates the
// descriptor caches, registers the (local, no-I/O) callback routes and
// launches the readiness-gated southbound bring-up — and returns it WITHOUT
// adding it to the manager (the caller adds it, so this never re-enters m.mu).
// Shared by WireCentrals (boot) and AddCentral (runtime); uses the manager's
// captured parentCtx/cfg/deps/logger. The Unit must already be registered in
// the shared registry.
//
// The descriptor wiring lives here rather than at the boot call site because
// both entry points need it: a central adopted at runtime that never attaches
// the persistence sinks writes no device descriptions or paramsets to SQLite,
// so every later daemon start re-inventories it over the radio as if it were
// brand new.
func (m *BringUpManager) buildAndStart(cc *config.CentralConfig, unit *central.Unit) *centralBringUp {
	// Hydrate the descriptor registries from SQLite and attach the
	// persistence sinks BEFORE the gated bring-up starts, so
	// CheckAndCreateDevicesFromCache sees the cached descriptions and the
	// live pull's registry writes are mirrored to disk.
	if m.deps.Descriptors.enabled() {
		devN, psN := WireDescriptorPersistence(m.parentCtx, unit, m.deps.Descriptors, m.logger)
		m.logger.Info("wire.descriptors.hydrated",
			slog.String("central", cc.Name),
			slog.Int("devices", devN),
			slog.Int("paramsets", psN))
	}
	// Restore the deferred-creation queue in the same window and for the
	// same reason: the gated bring-up below is the pull that would
	// otherwise materialise every held-back device before anything could
	// hold it back.
	WirePendingDevices(m.parentCtx, unit, m.deps.PendingDevices,
		cc.Behavior.DelayNewDeviceCreationEnabled(), m.logger)
	callbackURL, binRPCCallbackAddr, cbHandlers, deregister := registerCentralCallbacks(m.deps, cc, unit, m.logger)
	b := &centralBringUp{
		cfg:                m.cfg,
		cc:                 *cc,
		unit:               unit,
		deps:               m.deps,
		callbackURL:        callbackURL,
		binRPCCallbackAddr: binRPCCallbackAddr,
		cbHandlers:         cbHandlers,
		logger:             m.logger,
		parentCtx:          m.parentCtx,
	}
	b.addPermanentCloser(deregister)
	//nolint:contextcheck // start runs the gated bring-up on the handle's teardown-bounded parent ctx, not a short-lived caller ctx
	b.start()
	return b
}

// AddCentral brings up a single central's southbound wiring at runtime,
// mirroring what WireCentrals does per boot central: it registers the callback
// routes and launches the readiness-gated bring-up. The Unit must already be
// registered in the shared registry; the caller (composition root) owns the
// north-bound hooks (wireCentralNorthbound) and per-central scheduler jobs.
// Returns false if a handle for cc.Name already exists.
//
// Add / remove are operator-driven and serialized by the REST handler, so the
// brief check-then-build window is not contended in practice.
func (m *BringUpManager) AddCentral(cc *config.CentralConfig, unit *central.Unit) bool {
	m.mu.Lock()
	_, exists := m.byCentral[cc.Name]
	m.mu.Unlock()
	if exists {
		return false
	}
	m.add(m.buildAndStart(cc, unit))
	return true
}

// LifecycleContext returns the teardown-bounded daemon-lifetime context every
// bring-up handle derives its per-generation context from. [WireCentrals]
// derives it from the daemon's run context; the aggregate teardown cancels it
// once, after all handles have shut down.
//
// It exists for the composition root's runtime-adopt path, which has to start
// machinery that outlives the call that adopted the central. Every caller of
// that path is an HTTP handler — POST/PUT /api/v1/centrals and the first-run
// setup wizard — and net/http cancels the request context the instant the
// response is written, so anything started on it (the central's scheduler
// above all) dies before the operator's browser has rendered the result.
//
// Returns [context.Background] on a manager that never went through
// WireCentrals, which only a test constructs.
func (m *BringUpManager) LifecycleContext() context.Context {
	if m == nil || m.parentCtx == nil {
		return context.Background()
	}
	return m.parentCtx
}

// RemoveCentral tears down a single central's southbound wiring at runtime: it
// drops the handle from the manager, then shuts it down — draining the gated
// bring-up + InterfaceClient goroutines and deregistering the callback routes
// (the handle's permanent closers). Ordering is safe for a live remove: the
// Unit stays alive through the drain (the caller stops it AFTER this returns),
// so a callback arriving in the drain window publishes a harmless event rather
// than hitting a torn-down bus; once shutdown returns the routes are gone, so
// no further callback can arrive before the caller's Unit.Stop. This does NOT
// touch the registry, the Unit, or SQLite — the caller sequences Unit.Stop,
// reg.Unregister, model eviction and row purge afterwards. Returns false if no
// handle is managed for name.
func (m *BringUpManager) RemoveCentral(name string) bool {
	m.mu.Lock()
	b, ok := m.byCentral[name]
	if ok {
		delete(m.byCentral, name)
		m.order = slices.DeleteFunc(m.order, func(s string) bool { return s == name })
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	b.shutdown()
	return true
}

// ReinitCentral re-initializes the named central's south-bound (teardown →
// clear model → readiness-gated re-pull). Returns false if no such central is
// managed. Concurrency across centrals is independent; per-central calls are
// serialized by the handle's own lock.
func (m *BringUpManager) ReinitCentral(ctx context.Context, centralName string) bool {
	m.mu.Lock()
	b, ok := m.byCentral[centralName]
	m.mu.Unlock()
	if !ok {
		return false
	}
	b.reinit(ctx)
	return true
}

// Centrals returns the managed central names in wiring order.
func (m *BringUpManager) Centrals() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.order)
}

// Teardown tears down every managed central (reverse wiring order), draining
// each handle's goroutine and running its closers. This is the daemon-shutdown
// path and replaces the bare teardown func WireCentrals used to return.
func (m *BringUpManager) Teardown() {
	// Snapshot both the order slice and the handle map under the lock. Aliasing
	// m.byCentral and then reading it after the unlock would race a concurrent
	// AddCentral/RemoveCentral mutating the same map (a fatal concurrent map
	// read/write); maps.Clone gives the iteration below its own copy.
	m.mu.Lock()
	order := slices.Clone(m.order)
	handles := maps.Clone(m.byCentral)
	m.mu.Unlock()
	for _, name := range slices.Backward(order) {
		if b := handles[name]; b != nil {
			b.shutdown()
		}
	}
	if m.parentCancel != nil {
		m.parentCancel()
	}
}
