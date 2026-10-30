// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/node"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// Reconciler keeps the node's Aggregator in step with the device model:
// every sync describes the fleet of each central as endpoints
// ([Assembler.DescribeDevices]) and applies what changed since the last
// sync to the central's partition of the Aggregator — one batch per
// central, so the node reassembles once per changed central, not once
// per endpoint (node.Partition.Batch).
//
// A central is a partition: the scope the endpoint store files its
// endpoint numbers under (go-fabric #229). An endpoint the walk no longer
// describes is removed from the partition but keeps its stored number, so
// a device that is only unexposed, hidden or not loaded yet — a central
// still waiting on its CCU describes nothing — comes back under the same
// number. A number is freed in three places only: a device the CCU deleted
// is forgotten ([Reconciler.QueueForget]), a central removed from the
// configuration is retired ([Reconciler.Retire]), and a key an earlier
// composition stored that no composition produces any more is forgotten
// once the walk names it ([Described].Superseded).
//
// An endpoint is held as the spec the walk described, wrapped as the
// Aggregator's endpoint; a described spec that differs from the held one
// in what the wire sees (device type, name, source, parts, …) replaces
// it under the same key, which keeps the endpoint number. The name and
// the availability are therefore the model's at every sync, as they were
// when the daemon assembled its own topology; a controller-written
// NodeLabel is the node's persistence and overrides the name there.
type Reconciler struct {
	agg    *node.Aggregator
	asm    *Assembler
	keys   DeviceKeyLister
	logger *slog.Logger

	// mu serialises syncs: two concurrent walks would otherwise race on
	// held and apply their diffs against the same previous state.
	mu   sync.Mutex
	held map[string]map[string]*heldEndpoint // scope → key → held

	// forgetMu guards forget apart from mu, so [Reconciler.QueueForget]
	// never waits for a sync in progress: it is called from the event
	// bus's dispatch.
	forgetMu sync.Mutex
	forget   map[string]map[string]struct{} // scope → device address

	// pruned are the superseded keys a sync already settled in this
	// process, forgotten or found unstored; r.mu guards it. A key is
	// looked up in the store once, not on every sync.
	pruned map[string]struct{}
}

// DeviceKeyLister finds the stored endpoint keys of one device of one
// central, parts included; *matterendpoint.Store is one. It is how a
// deleted device's keys are found once the walk no longer describes it,
// a restart in between included.
type DeviceKeyLister interface {
	DeviceKeys(ctx context.Context, centralName, deviceAddress string) ([]matterendpoint.SourceKey, error)
}

// heldEndpoint is what the Aggregator holds for one key, with the
// identity the next sync compares against.
type heldEndpoint struct {
	ep *specEndpoint
	id endpointIdentity
}

// specEndpoint is a described endpoint as the Aggregator takes it: a
// type with a Spec method (node.Endpoint, go-fabric ADR 0018 decision 4).
// It is not a device the node binds, so the spec it carries is the walk's
// whole statement about the endpoint.
type specEndpoint struct{ spec endpoint.Spec }

// Spec implements node.Endpoint.
func (e *specEndpoint) Spec() endpoint.Spec { return e.spec }

// endpointIdentity is what a sync compares to decide whether a held
// endpoint must be replaced: the fields the wire sees that the walk may
// change between two syncs. The source and the measurement are compared
// by identity — a profile holds its device, so the source is stable while
// the data point lives, and a sensor's measurement is the data point
// itself; a sensor's device is rebuilt by every walk and is not part of
// the identity.
type endpointIdentity struct {
	deviceType     uint16
	name           string
	deviceAddress  string
	channelAddress string
	source         any
	measurement    any
	power          any
	parts          int
}

func identityOf(spec endpoint.Spec) endpointIdentity {
	id := endpointIdentity{
		deviceType:     spec.DeviceType,
		name:           spec.FriendlyName,
		deviceAddress:  spec.DeviceAddress,
		channelAddress: spec.ChannelAddress,
		parts:          len(spec.Parts),
	}
	if spec.Measurement != nil {
		id.measurement = spec.Measurement
	} else {
		id.source = spec.Source
	}
	if spec.PowerSource != nil {
		id.power = spec.PowerSource
	}
	return id
}

// NewReconciler returns a reconciler over agg, describing the model with
// asm and finding a deleted device's stored keys through keys. keys may be
// nil: a forgotten device then frees only the keys the reconciler holds.
// logger may be nil.
func NewReconciler(agg *node.Aggregator, asm *Assembler, keys DeviceKeyLister, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		agg: agg, asm: asm, keys: keys, logger: logger,
		held:   map[string]map[string]*heldEndpoint{},
		forget: map[string]map[string]struct{}{},
		pruned: map[string]struct{}{},
	}
}

// QueueForget marks a device the CCU deleted: the next [Reconciler.Sync]
// frees the endpoint numbers stored for it and its parts before it applies
// the walk, so a device paired again at the same address is numbered anew,
// as any new device is. It never blocks on a sync; the caller triggers one.
func (r *Reconciler) QueueForget(centralName, deviceAddress string) {
	if centralName == "" || deviceAddress == "" {
		return
	}
	r.forgetMu.Lock()
	defer r.forgetMu.Unlock()
	devs := r.forget[centralName]
	if devs == nil {
		devs = map[string]struct{}{}
		r.forget[centralName] = devs
	}
	devs[deviceAddress] = struct{}{}
}

// takeForget hands over and clears the queued devices.
func (r *Reconciler) takeForget() map[string]map[string]struct{} {
	r.forgetMu.Lock()
	defer r.forgetMu.Unlock()
	out := r.forget
	r.forget = map[string]map[string]struct{}{}
	return out
}

// forgetDevices frees the stored numbers of the queued devices of one
// central, in a batch of its own ahead of the walk's: a device described
// again in the same sync is then added under a fresh number rather than
// erased after it was added. A key comes from the store and from what the
// reconciler holds, so a device forgotten before it was ever stored is
// dropped as well. r.mu is held.
func (r *Reconciler) forgetDevices(ctx context.Context, scope string, devs map[string]struct{}) error {
	held := r.held[scope]
	keys := map[string]endpoint.SourceKey{}
	var errs []error
	for addr := range devs {
		if r.keys != nil {
			stored, err := r.keys.DeviceKeys(ctx, scope, addr)
			if err != nil {
				errs = append(errs, fmt.Errorf("stored keys of %s: %w", addr, err))
			}
			for _, k := range stored {
				keys[k.String()] = k
			}
		}
		for key, h := range held {
			if h.ep.spec.DeviceAddress == addr {
				keys[key] = h.ep.spec.StableKey
			}
		}
	}
	if len(keys) == 0 {
		return errors.Join(errs...)
	}
	err := r.agg.Partition(scope).Batch(ctx, func(b *node.Batch) error {
		var errs []error
		for key, k := range keys {
			if err := b.Forget(k); err != nil {
				errs = append(errs, fmt.Errorf("forget %s: %w", key, err))
				continue
			}
			delete(held, key)
		}
		return errors.Join(errs...)
	})
	if err != nil {
		errs = append(errs, err)
	}
	r.logger.Info("matter.reconciler.forget",
		slog.String("central", scope), slog.Int("devices", len(devs)), slog.Int("keys", len(keys)))
	return errors.Join(errs...)
}

// Sync describes snapshots and applies the changes to the Aggregator, one
// batch per central. Every change that applies stays applied whatever
// else fails; the result joins the errors of the walk, the Aggregator and
// the reassemblies. Before the node starts a sync only changes the set;
// after it, each changed central reassembles once.
func (r *Reconciler) Sync(ctx context.Context, snapshots []DeviceSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for scope, devs := range r.takeForget() {
		if err := r.forgetDevices(ctx, scope, devs); err != nil {
			errs = append(errs, fmt.Errorf("matteradapter: central %q: %w", scope, err))
		}
	}
	described, err := r.asm.DescribeDevices(ctx, snapshots)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, d := range described {
		if err := r.syncCentral(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("matteradapter: central %q: %w", d.Scope, err))
		}
		if err := r.forgetSuperseded(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("matteradapter: central %q: %w", d.Scope, err))
		}
	}
	return errors.Join(errs...)
}

// forgetSuperseded frees the stored numbers of the keys the walk names as
// superseded ([supersededKeys]) for one central. Only a key the store
// holds is forgotten, and only once per process: the store is asked per
// device the first time one of its keys is named. Without a
// DeviceKeyLister nothing is known about the store and nothing is freed.
// r.mu is held.
func (r *Reconciler) forgetSuperseded(ctx context.Context, d Described) error {
	if r.keys == nil {
		return nil
	}
	held := r.held[d.Scope]
	byDevice := map[string][]matterendpoint.SourceKey{}
	for _, k := range d.Superseded {
		key := k.String()
		if _, done := r.pruned[key]; done {
			continue
		}
		if _, ok := held[key]; ok {
			// Served under this key in this sync: not superseded after all.
			continue
		}
		byDevice[k.DeviceAddress] = append(byDevice[k.DeviceAddress], k)
	}
	if len(byDevice) == 0 {
		return nil
	}
	var (
		errs    []error
		forget  []matterendpoint.SourceKey
		settled []string
	)
	for addr, keys := range byDevice {
		stored, err := r.keys.DeviceKeys(ctx, d.Scope, addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("stored keys of %s: %w", addr, err))
			continue
		}
		have := make(map[string]struct{}, len(stored))
		for _, k := range stored {
			have[k.String()] = struct{}{}
		}
		for _, k := range keys {
			if _, ok := have[k.String()]; ok {
				forget = append(forget, k)
			}
			settled = append(settled, k.String())
		}
	}
	if len(forget) > 0 {
		err := r.agg.Partition(d.Scope).Batch(ctx, func(b *node.Batch) error {
			var errs []error
			for _, k := range forget {
				if err := b.Forget(k); err != nil {
					errs = append(errs, fmt.Errorf("forget %s: %w", k, err))
				}
			}
			return errors.Join(errs...)
		})
		if err != nil {
			// Settle nothing: the next sync asks the store again.
			return errors.Join(append(errs, err)...)
		}
		r.logger.Info("matter.reconciler.forget_superseded",
			slog.String("central", d.Scope), slog.Int("keys", len(forget)))
	}
	for _, key := range settled {
		r.pruned[key] = struct{}{}
	}
	return errors.Join(errs...)
}

// syncCentral applies one central's described endpoints to its partition.
// r.mu is held.
func (r *Reconciler) syncCentral(ctx context.Context, d Described) error {
	held := r.held[d.Scope]
	if held == nil {
		held = map[string]*heldEndpoint{}
		r.held[d.Scope] = held
	}
	part := r.agg.Partition(d.Scope)
	return part.Batch(ctx, func(b *node.Batch) error {
		var errs []error
		seen := make(map[string]struct{}, len(d.Endpoints))
		for i := range d.Endpoints {
			spec := &d.Endpoints[i]
			if spec.StableKey == nil {
				continue
			}
			key := spec.StableKey.String()
			seen[key] = struct{}{}
			id := identityOf(*spec)
			switch h, ok := held[key]; {
			case !ok:
				ep := &specEndpoint{spec: *spec}
				if err := b.Add(ep); err != nil {
					errs = append(errs, fmt.Errorf("add %s: %w", key, err))
					continue
				}
				held[key] = &heldEndpoint{ep: ep, id: id}
			case h.id != id:
				ep := &specEndpoint{spec: *spec}
				if err := b.Replace(spec.StableKey, ep); err != nil {
					errs = append(errs, fmt.Errorf("replace %s: %w", key, err))
					continue
				}
				held[key] = &heldEndpoint{ep: ep, id: id}
			default:
				// Unchanged in what the wire sees; the held spec stays.
			}
		}
		for key, h := range held {
			if _, ok := seen[key]; ok {
				continue
			}
			if err := b.Remove(h.ep.spec.StableKey); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", key, err))
				continue
			}
			delete(held, key)
		}
		return errors.Join(errs...)
	})
}

// Retire drops every endpoint of the central scope and retires its
// partition: the central left the configuration, so its stored endpoint
// numbers are collected once and the scope is forgotten. It is not for a
// central that is merely stopping — a daemon shutdown stops every central,
// and their numbers must survive it.
func (r *Reconciler) Retire(ctx context.Context, scope string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[scope]
	delete(r.held, scope)
	part := r.agg.Partition(scope)
	var errs []error
	if len(held) > 0 {
		err := part.Batch(ctx, func(b *node.Batch) error {
			var errs []error
			for key, h := range held {
				if err := b.Remove(h.ep.spec.StableKey); err != nil {
					errs = append(errs, fmt.Errorf("remove %s: %w", key, err))
				}
			}
			return errors.Join(errs...)
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	if err := r.agg.RetirePartition(ctx, scope); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("matteradapter: retire central %q: %w", scope, err)
	}
	return nil
}
