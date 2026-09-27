// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteMetaRetry is the pause after a failed metadata read before the
// mirror tries again. A missing scope waits for the scope refresh instead.
const liteMetaRetry = 30 * time.Second

// liteMetadata mirrors an openccu-lite box's metadata store — names, the
// enum trees and every object's node paths — into the central's
// DeviceDetails cache, which the device pipeline stamps at ingest and
// [restampDeviceDetails] re-applies to the running model.
//
// The mirror is a copy of the store's document: a snapshot, then the
// change stream applied on top. An object event carries the whole object
// and is applied as it is. A tree event is not: moving or deleting a node
// rewrites the paths of its members, and the stream does not promise an
// event per member, so the mirror re-reads the snapshot instead. So does a
// revision gap (the box drops events silently for a slow subscriber), an
// import and a resync.
//
// Lifecycle: [liteMetadata.start] runs one goroutine following the change
// stream until [liteMetadata.stop], which waits for it.
type liteMetadata struct {
	client *occulited.Client
	unit   *central.Unit
	name   string
	ifaces map[string]hmenum.Interface
	logger *slog.Logger

	// mu guards the document and the revisions; commits run under it, so
	// the DeviceDetails cache sees one generation at a time.
	mu      sync.Mutex
	objects map[string]occulited.Object
	enums   map[string]occulited.Enum
	// snapRev is the revision of the last snapshot: events at or below it
	// are already in the document. lastRev is the newest revision applied.
	snapRev int
	lastRev int
	loaded  bool

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newLiteMetadata(p *liteProfile, unit *central.Unit, logger *slog.Logger) *liteMetadata {
	ifaces := make(map[string]hmenum.Interface)
	for _, name := range configuredInterfaceNames(&p.cc) {
		ifaces[name] = hmenum.Interface(name)
	}
	return &liteMetadata{client: p.client, unit: unit, name: p.cc.Name, ifaces: ifaces, logger: logger}
}

// load reads the snapshot once, for the bring-up. A token without
// meta:read is not an error — the central runs without names and the
// feature set says why — but any other failure is: the bring-up re-gates,
// as a CCU's failed name load does.
func (m *liteMetadata) load(ctx context.Context) error {
	err := m.resnapshot(ctx)
	if errors.Is(err, hmerr.ErrScopeMissing) {
		m.logger.Info("lite.meta.no_scope", slog.String("central", m.name), slog.String("err", err.Error()))
		return nil
	}
	return err
}

// start follows the change stream in the background.
func (m *liteMetadata) start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(1)
	SafeGo("lite_meta."+m.name, func() {
		defer m.wg.Done()
		m.run(ctx)
	})
}

// stop ends the stream and waits for the goroutine.
func (m *liteMetadata) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// run keeps the mirror loaded and follows the stream. Without a loaded
// document there is nothing to apply events to, so it first retries the
// snapshot — a scope granted later is picked up this way.
func (m *liteMetadata) run(ctx context.Context) {
	for ctx.Err() == nil {
		if !m.isLoaded() {
			if err := m.resnapshot(ctx); err != nil {
				wait := liteMetaRetry
				if errors.Is(err, hmerr.ErrScopeMissing) {
					wait = liteScopeRefreshInterval
				}
				m.logger.Debug("lite.meta.snapshot_failed", slog.String("central", m.name), slog.String("err", err.Error()))
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				continue
			}
		}
		m.follow(ctx)
	}
}

// follow consumes the change stream until ctx ends or the mirror loses
// its document (a failed re-read). Messages are applied one by one and
// committed once the stream has nothing more queued, so a burst (a bulk
// write, an import) restamps the model once.
func (m *liteMetadata) follow(ctx context.Context) {
	m.mu.Lock()
	since := m.lastRev
	m.mu.Unlock()
	sctx, cancel := context.WithCancel(ctx)
	stream := m.client.MetaEvents(sctx, occulited.MetaStreamOptions{Since: &since})
	defer func() {
		cancel()
		<-stream.Done()
	}()
	dirty := false
	for msg := range stream.Messages() {
		if m.apply(ctx, msg) {
			dirty = true
		}
		if !m.isLoaded() {
			return
		}
		if dirty && len(stream.Messages()) == 0 {
			m.commit()
			dirty = false
		}
	}
	if dirty {
		m.commit()
	}
}

// apply folds one stream message into the document and reports whether
// it changed anything to commit. A re-read snapshot commits itself.
func (m *liteMetadata) apply(ctx context.Context, msg occulited.MetaMessage) bool {
	switch msg.Kind {
	case occulited.MetaResync:
		m.refresh(ctx, "resync")
		return false
	case occulited.MetaChange:
	default:
		return false
	}
	ev := msg.Event
	if ev == nil {
		return false
	}
	m.mu.Lock()
	if ev.Revision <= m.snapRev {
		m.mu.Unlock()
		return false
	}
	if ev.Revision > m.lastRev+1 {
		m.mu.Unlock()
		m.refresh(ctx, "gap")
		return false
	}
	m.lastRev = ev.Revision
	switch ev.Kind {
	case "object.updated":
		var obj occulited.Object
		if err := json.Unmarshal(ev.Value, &obj); err != nil || ev.Ref == "" {
			m.mu.Unlock()
			m.refresh(ctx, "undecodable object event")
			return false
		}
		m.objects[ev.Ref] = obj
		m.mu.Unlock()
		return true
	case "object.deleted":
		delete(m.objects, ev.Ref)
		m.mu.Unlock()
		return true
	default:
		// enum.*, node.* and import rewrite more than the event names.
		m.mu.Unlock()
		m.refresh(ctx, ev.Kind)
		return false
	}
}

// refresh re-reads the snapshot after an event the document cannot absorb.
// A failure leaves the mirror unloaded, and [liteMetadata.run] retries.
func (m *liteMetadata) refresh(ctx context.Context, reason string) {
	m.logger.Debug("lite.meta.resnapshot", slog.String("central", m.name), slog.String("reason", reason))
	if err := m.resnapshot(ctx); err != nil {
		m.logger.Warn("lite.meta.resnapshot_failed", slog.String("central", m.name), slog.String("err", err.Error()))
		m.mu.Lock()
		m.loaded = false
		m.mu.Unlock()
	}
}

// resnapshot replaces the document with the store's snapshot and commits.
func (m *liteMetadata) resnapshot(ctx context.Context) error {
	snap, err := m.client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("openccu-lite metadata snapshot: %w", err)
	}
	m.mu.Lock()
	m.objects = snap.Objects
	if m.objects == nil {
		m.objects = map[string]occulited.Object{}
	}
	m.enums = snap.Enums
	m.snapRev, m.lastRev, m.loaded = snap.Revision, snap.Revision, true
	m.mu.Unlock()
	m.commit()
	return nil
}

func (m *liteMetadata) isLoaded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded
}

// commit rebuilds the DeviceDetails cache from the document and restamps
// the running model; only what actually changed is published.
func (m *liteMetadata) commit() {
	if m.unit.DeviceDetails == nil {
		return
	}
	m.mu.Lock()
	staging := m.buildCache()
	m.mu.Unlock()
	m.unit.DeviceDetails.ReplaceWith(staging, time.Now())
	restampDeviceDetails(m.unit, m.logger)
}

// buildCache maps the document onto a fresh cache. Only objects of the
// configured interfaces are taken; an orphaned object is kept, since the
// device registry — not the metadata store — decides what exists. A room
// or function assignment is recorded by the display name of the node
// assigned directly, never of its ancestors: a device in
// room/eg/wohnzimmer is in "Wohnzimmer", not also in "Erdgeschoss".
// Callers hold m.mu.
func (m *liteMetadata) buildCache() *devicedetails.Cache {
	c := devicedetails.New()
	tax := liteTaxonomy(m.enums, m.snapRev)
	c.SetTaxonomy(tax)
	for ref, obj := range m.objects {
		ifaceName, address, ok := strings.Cut(ref, ".")
		if !ok || address == "" {
			continue
		}
		iface, configured := m.ifaces[ifaceName]
		if !configured {
			continue
		}
		c.AddInterface(address, iface)
		if obj.Name != "" {
			c.AddName(address, obj.Name)
		}
		for _, p := range obj.Enums {
			r, err := taxonomy.ParseRef(p)
			if err != nil {
				continue
			}
			c.AddRef(address, r)
			node, ok := tax.Node(r)
			if !ok {
				continue
			}
			switch r.Enum {
			case taxonomy.EnumRoom:
				c.AddChannelRoom(address, node.Name)
			case taxonomy.EnumFunction:
				c.AddFunction(address, node.Name)
			}
		}
	}
	return c
}

// liteTaxonomy converts the store's enum trees.
func liteTaxonomy(enums map[string]occulited.Enum, revision int) *taxonomy.Taxonomy {
	t := &taxonomy.Taxonomy{Enums: make(map[taxonomy.EnumID]*taxonomy.Enum, len(enums))}
	if revision > 0 {
		t.Revision = uint64(revision)
	}
	for id, e := range enums {
		t.Enums[taxonomy.EnumID(id)] = &taxonomy.Enum{
			ID:    taxonomy.EnumID(id),
			Names: maps.Clone(e.Name),
			Roots: liteNodes(e.Tree),
		}
	}
	return t
}

func liteNodes(in []occulited.Node) []*taxonomy.Node {
	if len(in) == 0 {
		return nil
	}
	out := make([]*taxonomy.Node, 0, len(in))
	for _, n := range in {
		out = append(out, &taxonomy.Node{ID: n.ID, Name: n.Name, Icon: n.Icon, Children: liteNodes(n.Children)})
	}
	return out
}
