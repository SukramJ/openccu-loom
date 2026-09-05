// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// Config tunes the assembler. The zero value is *not* valid — at
// minimum, a non-zero VendorID + ProductID + non-empty NodeLabel
// must be supplied (the root BasicInformation cluster mandates them).
type Config struct {
	// VendorID is the bridge's IANA-assigned vendor identifier.
	VendorID uint16
	// ProductID is the bridge's vendor-assigned product identifier.
	ProductID uint16
	// NodeLabel is the user-visible bridge label.
	NodeLabel string
}

// Validate returns nil when the config is internally consistent.
func (c Config) Validate() error {
	if c.VendorID == 0 {
		return errors.New("endpoint: Config.VendorID must be non-zero")
	}
	if c.ProductID == 0 {
		return errors.New("endpoint: Config.ProductID must be non-zero")
	}
	if strings.TrimSpace(c.NodeLabel) == "" {
		return errors.New("endpoint: Config.NodeLabel must be non-empty")
	}
	return nil
}

// Store is the subset of [store.Store] the assembler depends on.
// Defined as an interface so tests substitute an in-memory fake
// without standing up a SQLite database for every assembly run.
type Store interface {
	GetEndpoint(ctx context.Context, key store.EndpointKey) (store.EndpointRecord, error)
	UpsertEndpointAssigning(ctx context.Context, rec store.EndpointRecord) (uint16, error)
	ListEndpoints(ctx context.Context, centralName string) ([]store.EndpointRecord, error)
	RemoveEndpoint(ctx context.Context, key store.EndpointKey) error
}

// Assembler turns snapshots of [Spec] values into a [Topology].
// Multi-call-safe; concurrent calls serialise through the underlying
// store transactions.
type Assembler struct {
	store  Store
	cfg    Config
	logger *slog.Logger
	// states owns the per-endpoint state that must outlive a single
	// dispatch (DataVersion trackers, the Identify cluster server),
	// keyed by the stable [store.EndpointKey]. It lives across every
	// [Assembler.Assemble] so a bridged endpoint's DataVersion and
	// running Identify survive reassembly — see
	// [endpointStateRegistry] and [Endpoint.state].
	states *endpointStateRegistry
}

// New returns an assembler. logger may be nil; the assembler then
// uses [slog.Default]. Deciding *which* sources deserve a [Spec] — the
// operator's allowlist above all — happens before this point, in
// whatever walks the owner's model.
func New(s Store, cfg Config, logger *slog.Logger) (*Assembler, error) {
	if s == nil {
		return nil, errors.New("endpoint: store is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	a := &Assembler{
		store:  s,
		cfg:    cfg,
		logger: logger,
		states: newEndpointStateRegistry(),
	}
	return a, nil
}

// Assemble produces the topology from the given snapshots of
// [Spec] values. Endpoint IDs are looked up in the store; new
// sources receive a fresh ID allocated under a transaction. Vanished
// sources (rows in the store with no matching snapshot entry) are
// removed.
//
// Snapshots must have unique CentralName values; the assembler does
// not deduplicate. The caller is expected to pass exactly one
// snapshot per central.
func (a *Assembler) Assemble(ctx context.Context, snapshots []Snapshot) (*Topology, error) {
	// Apple-compatible three-tier topology (mirrors matter.js's
	// `BridgedDevicesNode.ts`):
	//   EP 0 = RootNode  (DeviceType 0x0016) — system services
	//   EP 1 = Aggregator(DeviceType 0x000E) — Descriptor.PartsList enumerates bridged
	//   EP ≥ 2 = bridged devices
	root := &Endpoint{
		ID:         0,
		DeviceType: deviceTypeRootNode,
		Reachable:  true,
	}
	aggregator := &Endpoint{
		ID:         1,
		DeviceType: deviceTypeAggregator,
		Reachable:  true,
	}
	topology := &Topology{
		Endpoints: []*Endpoint{root, aggregator},
		VendorID:  a.cfg.VendorID,
		ProductID: a.cfg.ProductID,
		NodeLabel: a.cfg.NodeLabel,
	}

	seen := make(map[store.EndpointKey]struct{})
	for _, snap := range snapshots {
		if snap.CentralName == "" {
			return nil, errors.New("endpoint: snapshot CentralName is required")
		}
		for i := range snap.Endpoints {
			ep, err := a.buildEndpoint(ctx, &snap.Endpoints[i])
			if err != nil {
				return nil, err
			}
			seen[ep.SourceKey] = struct{}{}
			topology.Endpoints = append(topology.Endpoints, ep)
		}
	}

	if err := a.gcVanished(ctx, snapshots, seen); err != nil {
		return nil, err
	}

	// Release the state of sources that vanished / were de-exposed this
	// run so a later re-add gets a fresh version (matches matter.js
	// destroying the Datasource on endpoint removal), a running Identify
	// countdown stops, and the registry stays bounded to the live
	// topology. State of endpoints still present in `seen` is retained,
	// keeping their version stable.
	a.states.retain(seen)

	sort.SliceStable(topology.Endpoints, func(i, j int) bool {
		return topology.Endpoints[i].ID < topology.Endpoints[j].ID
	})
	// Stamp every bridged endpoint with the bridge-wide VID/PID so the
	// BridgedDeviceBasicInformation cluster server can read them from
	// the endpoint without a back-pointer to the topology. Skipped for
	// the root + aggregator endpoints (they only carry root-side
	// BasicInformation, not BridgedDeviceBasicInformation).
	for _, ep := range topology.Endpoints {
		if ep == nil || ep.IsRoot() || ep.IsAggregator() {
			continue
		}
		ep.BridgeVendorID = topology.VendorID
		ep.BridgeProductID = topology.ProductID
	}
	return topology, nil
}

// deviceTypeRootNode is the Matter Device Type ID for the root
// endpoint of a Matter Node. Mirrors Matter Device Library §2.1
// ("Root Node" / 0x0016) and matter.js's `RootNodeDt.id`.
const deviceTypeRootNode = 0x0016

// deviceTypeAggregator is the Matter Device Type ID for the
// Aggregator endpoint (EP 1) that hosts bridged sub-endpoints in its
// Descriptor.PartsList. Mirrors Matter Device Library §13.2
// ("Aggregator" / 0x000E) and matter.js's `AggregatorDt.id`.
const deviceTypeAggregator = 0x000E

// buildEndpoint turns one [Spec] into the assembled endpoint:
// it resolves the persisted endpoint id for the spec's stable key and
// binds the per-identity state that has to survive a reassembly.
func (a *Assembler) buildEndpoint(ctx context.Context, spec *Spec) (*Endpoint, error) {
	id, err := a.assignOrReuseID(ctx, spec.StableKey, spec.DeviceType)
	if err != nil {
		return nil, err
	}
	reachable := true
	if spec.Availability != nil {
		reachable = spec.Availability()
	}
	return &Endpoint{
		ID:         id,
		DeviceType: spec.DeviceType,
		Reachable:  reachable,
		// The 32-byte NodeLabel cap is Matter's constraint, so the
		// assembly enforces it however the label was produced.
		FriendlyName:   truncateUTF8(spec.FriendlyName, nodeLabelMaxBytes),
		ChannelAddress: spec.ChannelAddress,
		Availability:   spec.Availability,
		Source:         spec.Source,
		Measurement:    spec.Measurement,
		PowerSource:    spec.PowerSource,
		SourceKey:      spec.StableKey,
		// Reuse the state bound to this stable source key so the
		// endpoint's per-cluster version and Identify server survive
		// reassembly.
		state: a.states.stateFor(spec.StableKey),
		// Bridged endpoints are children of the Aggregator (EP 1).
		// Mirrors chip examples/bridge-app/linux/main.cpp:261-276
		// AddDeviceEndpoint(..., parentEndpointId=1) and matter.js
		// aggregator.add(child) which establishes the same parent chain.
		ParentEndpointID:    1,
		HasParentEndpointID: true,
	}, nil
}

// assignOrReuseID looks up the existing endpoint_id for sourceKey;
// allocates a fresh one otherwise. Updates device_type either way so
// a profile change in the model side migrates cleanly.
func (a *Assembler) assignOrReuseID(ctx context.Context, sourceKey store.EndpointKey, deviceType uint16) (uint16, error) {
	rec, err := a.store.GetEndpoint(ctx, sourceKey)
	switch {
	case err == nil:
		// Already assigned — refresh device_type if it drifted.
		if rec.DeviceType != deviceType {
			rec.DeviceType = deviceType
			if _, err := a.store.UpsertEndpointAssigning(ctx, rec); err != nil {
				return 0, fmt.Errorf("endpoint: refresh device_type: %w", err)
			}
		}
		return rec.EndpointID, nil
	case isNotFound(err):
		// Allocate a fresh ID under a transaction.
		id, err := a.store.UpsertEndpointAssigning(ctx, store.EndpointRecord{
			Key:        sourceKey,
			DeviceType: deviceType,
		})
		if err != nil {
			return 0, fmt.Errorf("endpoint: assign new id: %w", err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("endpoint: lookup: %w", err)
	}
}

// gcVanished removes store rows for sources that no longer appear
// in any snapshot. seen contains every key produced this run; we
// list the persisted keys per central and drop the difference.
//
// Snapshots that are not model-complete are exempt: at daemon boot the
// topology is assembled before the readiness-gated CCU device load has
// populated the model, so a registered central briefly contributes an
// empty (or partial) device list. Treating that as "every device
// vanished" would delete all persisted endpoint-ID rows on each boot
// and renumber the bridged fleet — controllers key their accessory
// cache on the endpoint number, so persisted numbers must survive a
// restart. Mirrors matter.js, which reserves persisted endpoint
// numbers at initialization (packages/node/src/storage/server/
// ServerEndpointStores.ts, assignNumber) and erases one only on
// explicit endpoint deletion (packages/node/src/node/server/
// ServerEndpointInitializer.ts, eraseDescendant).
func (a *Assembler) gcVanished(ctx context.Context, snapshots []Snapshot, seen map[store.EndpointKey]struct{}) error {
	for _, snap := range snapshots {
		if !snap.ModelComplete {
			// The central has not finished its initial device load; an
			// absent source is "not loaded yet", not "vanished". Keep
			// every persisted row until a model-complete snapshot vouches
			// for the fleet.
			a.logger.Debug(
				"matter endpoint gc skipped: model incomplete",
				slog.String("central", snap.CentralName),
			)
			continue
		}
		records, err := a.store.ListEndpoints(ctx, snap.CentralName)
		if err != nil {
			return fmt.Errorf("endpoint: gc list: %w", err)
		}
		for _, rec := range records {
			if _, kept := seen[rec.Key]; kept {
				continue
			}
			if err := a.store.RemoveEndpoint(ctx, rec.Key); err != nil {
				return fmt.Errorf("endpoint: gc remove: %w", err)
			}
			a.logger.Debug(
				"matter endpoint gc",
				slog.String("central", rec.Key.CentralName),
				slog.String("device", rec.Key.DeviceAddress),
				slog.Int("channel", rec.Key.ChannelNo),
				slog.String("dp_kind", string(rec.Key.DPKind)),
				slog.String("dp_key", rec.Key.DPKey),
				slog.Int("endpoint_id", int(rec.EndpointID)),
			)
		}
	}
	return nil
}
