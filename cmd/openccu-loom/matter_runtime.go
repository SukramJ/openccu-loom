// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"thingfabric.dev/go-fabric-ui/fabricui"
	"thingfabric.dev/go-fabric/bridge"
	mattercore "thingfabric.dev/go-fabric/cluster/core"
	"thingfabric.dev/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// matterGeneration is one node and the reconciler that keeps its
// Aggregator in step with the device model.
type matterGeneration struct {
	node       *node.Node
	reconciler *matteradapter.Reconciler
}

// matterNodeAttachment is something the daemon hangs on the running node —
// an observer, a listener, a hook. It runs on every node the runtime
// starts and returns what takes it off that node again (nil when nothing
// has to).
type matterNodeAttachment func(*node.Node) (detach func())

// matterRuntime is the Matter bridge as the rest of the daemon reaches it:
// the node that runs now and the reconciler on its Aggregator. go-fabric's
// node is single-use — Node.FactoryReset removes the fabrics, stops the node
// and erases its store, and the host brings a new node up on the erased
// store (go-fabric node/factory_reset.go:42-49) — so Erase builds that new node, and
// everything that reads the node reads it here: the reconciler's callers,
// the status and health readers, the stop, and the attachments, which move
// to the new node with it. The node pages follow it through a
// fabricui.NodeSwitch, Set once the new node runs.
//
// The calls the device model drives (Sync, Retire, QueueForget,
// SetReachable) are skipped while an erase runs: the old node is going
// down, and the erase syncs the new one from the model once it runs, which
// covers every change they carried. Skipping them keeps the event bus that
// drives them from waiting for an erase.
type matterRuntime struct {
	// mu guards gen, erasing and the attachments' detach functions. It is
	// held for one call or one swap, never across an erase's reset or
	// build.
	mu      sync.RWMutex
	gen     matterGeneration
	erasing bool
	// detach takes the attachments off gen.node.
	detach      []func()
	attachments []matterNodeAttachment
	// eraseMu serialises Erase with Erase and Stop.
	eraseMu sync.Mutex

	// newNode builds a node on the bridge's stores from the configuration
	// the first node was built from; only the boot reason differs.
	newNode func(ctx context.Context, bootReason uint8) (*node.Node, error)
	// newReconciler builds the reconciler on a node's Aggregator.
	newReconciler func(*node.Node) *matteradapter.Reconciler
	// walk describes the device model as the reconciler syncs it.
	walk    func(context.Context) []matteradapter.DeviceSnapshot
	labeler *matterNodeLabeler
	// nodes is the node pages' source; nil until the pages are wired.
	nodes  *fabricui.NodeSwitch
	db     *gosql.DB
	logger *slog.Logger

	// sealScrubOwed reports that the start sealed the credential store but
	// could not scrub the clear bytes out of the file; go-fabric retries the
	// scrub on every open (ADR 0092).
	sealScrubOwed bool

	// scrubMu guards scrubErr and scrubSince: the scrub the last erase
	// could not finish, which the boot runs (runPendingScrub).
	scrubMu    sync.Mutex
	scrubErr   string
	scrubSince time.Time
}

// Node is the node that runs now — after an erase whose new node did not
// start, the stopped one.
func (rt *matterRuntime) Node() *node.Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.gen.node
}

// Sync describes the device model and applies the changes to the node's
// Aggregator (matteradapter.Reconciler.Sync).
func (rt *matterRuntime) Sync(ctx context.Context) error {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.erasing {
		return nil
	}
	return rt.gen.reconciler.Sync(ctx, rt.walk(ctx))
}

// Retire collects a removed central's endpoint numbers
// (matteradapter.Reconciler.Retire).
func (rt *matterRuntime) Retire(ctx context.Context, centralName string) error {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.erasing {
		return nil
	}
	return rt.gen.reconciler.Retire(ctx, centralName)
}

// QueueForget queues a device the CCU deleted, so the next sync frees its
// numbers (matteradapter.Reconciler.QueueForget).
func (rt *matterRuntime) QueueForget(centralName, address string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.erasing {
		return
	}
	rt.gen.reconciler.QueueForget(centralName, address)
}

// SetReachable states a physical device's reachability on the bridged
// device the reconciler holds for it (matteradapter.Reconciler.SetReachable).
func (rt *matterRuntime) SetReachable(ctx context.Context, centralName, address string, reachable bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.erasing {
		return
	}
	rt.gen.reconciler.SetReachable(ctx, centralName, address, reachable)
}

// attach hangs a on the running node and on every node an erase brings
// up. The composition root attaches before the node pages are served, so
// no erase runs yet. An attachment runs under the runtime's lock and gets
// the node it hangs on as its argument; it must not call back into the
// runtime (Node and the rest take the same lock).
func (rt *matterRuntime) attach(a matterNodeAttachment) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.attachments = append(rt.attachments, a)
	if d := a(rt.gen.node); d != nil {
		rt.detach = append(rt.detach, d)
	}
}

// Erase factory-resets the node and brings a new one up on the erased
// store: go-fabric-ui's Options.Erase, served as POST
// /node/maintenance/erase. The new node starts uncommissioned with a fresh
// UniqueID and states BootReason SoftwareReset — an operator-commanded
// restart of the node, as a requested daemon restart states it
// (matterBootReason). What the daemon keeps of its own survives: the
// exposure selection, the root serial and the node label it applied.
//
// A reset go-fabric does not complete is returned and changes nothing
// here: the old node stays the pages' source, and the next erase finishes
// the reset (go-fabric node/factory_reset.go:51-55). A reset whose only failure is
// the scrub of the database file removed every row; the new node starts,
// the page warns, and the next boot runs the scrub (runPendingScrub). A
// new node that does not start is returned too; the pages keep the stopped
// node, as go-fabric-ui's NodeSwitch contract has it, and the next erase
// tries again.
func (rt *matterRuntime) Erase(ctx context.Context) error {
	rt.eraseMu.Lock()
	defer rt.eraseMu.Unlock()

	rt.mu.Lock()
	old := rt.gen.node
	rt.erasing = true
	rt.mu.Unlock()
	swapped := false
	defer func() {
		if !swapped {
			rt.mu.Lock()
			rt.erasing = false
			rt.mu.Unlock()
		}
	}()

	if err := old.FactoryReset(ctx); err != nil {
		if !errors.Is(err, node.ErrScrubIncomplete) {
			return fmt.Errorf("matter erase: %w", err)
		}
		// The scrub follows a committed erase (go-fabric
		// node/factory_reset.go:127-139): the rows are gone, only their
		// bytes may remain.
		rt.scrubPending(ctx, err)
	}

	n, err := rt.newNode(ctx, mattercore.BootReasonSoftwareReset)
	if err != nil {
		return fmt.Errorf("matter erase: build the new node: %w", err)
	}
	rec := rt.newReconciler(n)
	// The erase took the label the node stored; the new node serves the
	// one the operator last applied.
	if err := rt.labeler.rebind(ctx, n); err != nil {
		rt.logger.Warn("matter.bridge.node_label_apply_failed", slog.String("err", err.Error()))
	}
	if err := n.Start(ctx); err != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = n.Stop(stopCtx)
		cancel()
		return fmt.Errorf("matter erase: start the new node: %w", err)
	}

	rt.mu.Lock()
	for _, d := range rt.detach {
		d()
	}
	rt.detach = rt.detach[:0]
	rt.gen = matterGeneration{node: n, reconciler: rec}
	for _, a := range rt.attachments {
		if d := a(n); d != nil {
			rt.detach = append(rt.detach, d)
		}
	}
	rt.erasing = false
	swapped = true
	rt.mu.Unlock()
	if rt.nodes != nil {
		rt.nodes.Set(matterNodeSource{n})
	}

	// The new node's Aggregator starts empty; the selection comes back
	// under fresh numbers (the reset erased the endpoint records).
	if err := rt.Sync(ctx); err != nil {
		rt.logger.Warn("matter.bridge.sync.erase", slog.String("err", err.Error()))
	}
	rt.logger.Info("matter.bridge.erased",
		slog.String("addr", n.LocalAddr()),
		slog.Int("boot_reason", int(n.BootReason())))
	return nil
}

// scrubPending records a scrub the erase could not finish, for the boot
// and for the node pages.
func (rt *matterRuntime) scrubPending(ctx context.Context, err error) {
	rt.logger.Warn("matter.bridge.erase.scrub_incomplete",
		slog.String("err", err.Error()),
		slog.String("hint", "the erased rows are gone; their bytes may remain in the database file until the next start scrubs it"))
	if mErr := sqlite.MarkScrubPending(context.WithoutCancel(ctx), rt.db); mErr != nil {
		rt.logger.Warn("boot_record.scrub_mark_failed", slog.String("err", mErr.Error()))
	}
	rt.scrubMu.Lock()
	rt.scrubErr = err.Error()
	rt.scrubSince = time.Now()
	rt.scrubMu.Unlock()
}

// warnings are the scrub the sealing of the store still owes and the scrub
// an erase left pending, as go-fabric-ui's host warnings, in the daemon's locale; without catalogues the code stands in
// for the message, as matterWarnings does.
func (rt *matterRuntime) warnings(catalogs *i18n.Catalogs, locale string) []fabricui.Warning {
	var out []fabricui.Warning
	if rt.sealScrubOwed {
		const code = "seal_scrub_owed"
		msg := code
		if catalogs != nil {
			msg = catalogs.T(locale, "matter.warning."+code)
		}
		out = append(out, fabricui.Warning{Code: code, Message: msg})
	}
	rt.scrubMu.Lock()
	defer rt.scrubMu.Unlock()
	if rt.scrubSince.IsZero() {
		return out
	}
	const code = "scrub_pending"
	msg := code
	if catalogs != nil {
		msg = catalogs.TF(locale, "matter.warning."+code, map[string]string{"error": rt.scrubErr})
	}
	return append(out, fabricui.Warning{Code: code, Message: msg, Since: rt.scrubSince})
}

// release takes the attachments off the node that runs now; the daemon's
// teardown runs it.
func (rt *matterRuntime) release() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, d := range rt.detach {
		d()
	}
	rt.detach = nil
}

// matterControllerRTT is the controller round-trip statistics of the bridge
// that runs now; zero while the node has no bridge.
func matterControllerRTT(rt *matterRuntime) bridge.MRPRTTStats {
	mb := rt.Node().Bridge()
	if mb == nil {
		return bridge.MRPRTTStats{}
	}
	return mb.ControllerRTT()
}

// Stop stops the node that runs now. It waits for an erase in progress, so
// the stop reaches the node that erase brings up.
func (rt *matterRuntime) Stop(ctx context.Context) error {
	rt.eraseMu.Lock()
	defer rt.eraseMu.Unlock()
	return rt.Node().Stop(ctx)
}
