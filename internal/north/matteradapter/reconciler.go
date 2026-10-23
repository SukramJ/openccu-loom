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
)

// Reconciler keeps the node's Aggregator in step with the device model:
// every sync describes the fleet of each central as endpoints
// ([Assembler.DescribeDevices]) and applies what changed since the last
// sync to the central's partition of the Aggregator — one batch per
// central, so the node reassembles once per changed central, not once
// per endpoint (node.Partition.Batch).
//
// A central is a partition: the scope the endpoint store files its
// endpoint numbers under, with its own completeness, so a central whose
// initial device load has finished collects its vanished endpoints while
// a central still loading keeps its stored numbers (go-fabric #229). A
// central removed from the configuration is retired ([Reconciler.Retire]),
// which collects its stored numbers once.
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
	logger *slog.Logger

	// mu serialises syncs: two concurrent walks would otherwise race on
	// held and apply their diffs against the same previous state.
	mu   sync.Mutex
	held map[string]map[string]*heldEndpoint // scope → key → held
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
// asm. logger may be nil.
func NewReconciler(agg *node.Aggregator, asm *Assembler, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{agg: agg, asm: asm, logger: logger, held: map[string]map[string]*heldEndpoint{}}
}

// Sync describes snapshots and applies the changes to the Aggregator, one
// batch per central. Every change that applies stays applied whatever
// else fails; the result joins the errors of the walk, the Aggregator and
// the reassemblies. Before the node starts a sync only changes the set;
// after it, each changed central reassembles once.
func (r *Reconciler) Sync(ctx context.Context, snapshots []DeviceSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	described, err := r.asm.DescribeDevices(ctx, snapshots)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range described {
		if err := r.syncCentral(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("matteradapter: central %q: %w", d.Scope, err))
		}
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
		if d.ModelComplete {
			b.MarkComplete()
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
