// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
	logger             *slog.Logger

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
	b.mu.Lock()
	ctx, cancel := context.WithCancel(b.parentCtx)
	b.cancel = cancel
	b.mu.Unlock()

	b.wg.Add(1)
	SafeGo("central_bringup."+b.cc.Name, func() {
		defer b.wg.Done()
		ccCopy := b.cc
		gatedCentralBringUp(ctx, b.cfg, &ccCopy, b.unit, b.deps, b.callbackURL, b.binRPCCallbackAddr, b.addCloser, b.logger)
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

// clearModel removes every device from the unit's model registry via the
// unit's own RemoveDevice path (channel teardown + bus unsubscribe +
// DeviceRemovedEvent fire for each, so north-bound surfaces drop the entities),
// and also drops each device's in-memory device-description + paramset entries
// (mirrors the unpair cleanup, DeviceCoordinator.RefreshAfterUnpair).
//
// Clearing the descriptions is what lets the re-pull forget a device the CCU no
// longer reports: the re-pull's ListDevices omits it, but
// CheckAndCreateDevicesFromCache would otherwise re-materialise it from a stale
// description still in the registry — resurrecting a device removed on the CCU.
func (b *centralBringUp) clearModel() {
	if b.unit == nil || b.unit.ModelRegistry == nil {
		return
	}
	for _, d := range b.unit.ModelRegistry.List() {
		iface := hmenum.Interface(d.InterfaceID)
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
		b.unit.RemoveDevice(d.Address)
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
	m.mu.Lock()
	order := slices.Clone(m.order)
	handles := m.byCentral
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
