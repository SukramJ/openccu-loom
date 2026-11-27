// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/endpoint"
	"thingfabric.dev/go-fabric/node"

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
// An endpoint is held as the go-fabric device the walk described, which
// the node binds. A described device that differs from the held one in
// what the wire sees (device type, source, parts, stated identity, …), or
// a model source's device built anew, replaces it under the same key,
// which keeps the endpoint number. A changed name is stated on the held
// device ([fabricdevice.Device.Update]) and served at once; a
// controller-written NodeLabel is the node's persistence and wins over
// it. Reachability has one input, [fabricdevice.Device.SetReachable]:
// [Reconciler.SetReachable] forwards the model's flips to the held
// devices of a physical device, and a device is stated the model's
// availability before the node binds it.
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

	// reachMu guards reach and orders a reachability flip against a
	// central's batch: [Reconciler.SetReachable] holds it while it tells
	// the devices, and a sync holds it from the seed of the devices it
	// binds until the index names the devices the node then serves. A flip
	// therefore lands before the seed, which reads it, or after the index
	// is current, and reaches exactly the device the node serves — during
	// a Replace both the old and the new device are bound
	// (node.Batch.Replace attaches the new one at once and detaches the old
	// one after the reassembly), and each would report the flip. The bus's
	// dispatch waits for at most one central's batch, never for a walk.
	reachMu sync.Mutex
	// reach indexes the devices a reachability flip of a physical device
	// reaches: scope → device address → device.
	reach map[string]map[string]map[*fabricdevice.Device]struct{}
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
	dev     *fabricdevice.Device
	key     fabricdevice.SourceKey
	address string
	name    string
	id      endpointIdentity
}

// endpointIdentity is what a sync compares to decide whether a held
// device must be replaced: the fields the wire sees that the walk may
// change between two syncs, the name excepted — a name is stated on the
// held device. The source and the measurement are compared by identity —
// a profile holds its device, so the source is stable while the data
// point lives, and a sensor's measurement is the data point itself; a
// device the walk builds itself is new on every walk and is not part of
// the identity. A model source's device is: the source builds a new one
// only when a fact the device is built with changed.
type endpointIdentity struct {
	deviceType     uint16
	deviceAddress  string
	channelAddress string
	source         any
	measurement    any
	power          any
	kept           *fabricdevice.Device
	parts          int
	stated         statedIdentity
}

// statedIdentity is what the walk states of the physical device in
// BridgedDeviceBasicInformation (deviceIdentity), the strings an endpoint
// is replaced for when the CCU reports a new value — above all the
// installed firmware after an update.
type statedIdentity struct {
	serialNumber    string
	productName     string
	productLabel    string
	softwareVersion string
}

func statedOf(spec endpoint.Spec) statedIdentity {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	st := spec.BridgedBasicInformation
	return statedIdentity{
		serialNumber:    deref(st.SerialNumber),
		productName:     deref(st.ProductName),
		productLabel:    deref(st.ProductLabel),
		softwareVersion: deref(st.SoftwareVersionString),
	}
}

// valueChanged reports whether a stated string changed in a way the node
// cannot see in the endpoint's surface, so the reconciler raises the
// device's ConfigurationVersion itself. A serial number or software version
// that appears or disappears changes the cluster's AttributeList, and the
// node raises the version for that surface drift on its own; only a change
// from one value to another is left to the reconciler. A product name or
// label the host does not state falls back to the device address and stays
// listed, so every change of it, stated or not, is a value change.
func (s statedIdentity) valueChanged(next statedIdentity) bool {
	both := func(a, b string) bool { return a != "" && b != "" && a != b }
	return both(s.serialNumber, next.serialNumber) ||
		both(s.softwareVersion, next.softwareVersion) ||
		s.productName != next.productName ||
		s.productLabel != next.productLabel
}

func identityOf(e Endpoint, spec endpoint.Spec) endpointIdentity {
	id := endpointIdentity{
		deviceType:     spec.DeviceType,
		deviceAddress:  spec.DeviceAddress,
		channelAddress: spec.ChannelAddress,
		parts:          len(spec.Parts),
		stated:         statedOf(spec),
	}
	if spec.Measurement != nil {
		id.measurement = spec.Measurement
	} else {
		id.source = spec.Source
	}
	if spec.PowerSource != nil {
		id.power = spec.PowerSource
	}
	if e.Kept {
		id.kept = e.Device
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
		reach:  map[string]map[string]map[*fabricdevice.Device]struct{}{},
	}
}

// SetReachable states whether the physical device address of centralName
// is reachable on every bridged device the reconciler holds for it: the
// BridgedDeviceBasicInformation Reachable attribute, and on a change the
// ReachableChanged event ([fabricdevice.Device.SetReachable], which
// reports nothing for a repeated value). It waits for a batch in progress,
// not for a walk (see reachMu).
func (r *Reconciler) SetReachable(ctx context.Context, centralName, address string, reachable bool) {
	r.reachMu.Lock()
	defer r.reachMu.Unlock()
	for d := range r.reach[centralName][address] {
		if err := d.SetReachable(ctx, reachable); err != nil && !errors.Is(err, fabricdevice.ErrNotBridged) {
			r.logger.Warn("matter.reconciler.reachable",
				slog.String("central", centralName), slog.String("device", address), slog.Any("err", err))
		}
	}
}

// seed states the model's availability on a device the node is about to
// bind: a new device starts reachable, and the node reads the device's
// reachability when it assembles it. The model's state changes before its
// flip is published, so a seed under reachMu reads every flip the index
// will not forward. r.reachMu is held.
func (r *Reconciler) seed(ctx context.Context, scope, address string, e Endpoint) {
	if e.Available == nil {
		return
	}
	if err := e.Device.SetReachable(ctx, e.Available()); err != nil && !errors.Is(err, fabricdevice.ErrNotBridged) {
		r.logger.Warn("matter.reconciler.reachable_seed",
			slog.String("central", scope), slog.String("device", address), slog.Any("err", err))
	}
}

// reindex sets the index of the physical devices addresses of scope to
// the devices held for them, dropping a replaced or removed device. r.mu
// and r.reachMu are held.
func (r *Reconciler) reindex(scope string, addresses map[string]struct{}) {
	if len(addresses) == 0 {
		return
	}
	now := make(map[string]map[*fabricdevice.Device]struct{}, len(addresses))
	for _, h := range r.held[scope] {
		if _, ok := addresses[h.address]; !ok {
			continue
		}
		set := now[h.address]
		if set == nil {
			set = map[*fabricdevice.Device]struct{}{}
			now[h.address] = set
		}
		set[h.dev] = struct{}{}
	}
	devs := r.reach[scope]
	if devs == nil {
		devs = map[string]map[*fabricdevice.Device]struct{}{}
		r.reach[scope] = devs
	}
	for addr := range addresses {
		if set, ok := now[addr]; ok {
			devs[addr] = set
		} else {
			delete(devs, addr)
		}
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
// dropped as well.
//
// A device stays queued for the next sync when its stored keys could not
// be read, or when a Forget of it applied but its erasure could not be
// recorded — go-fabric's answer to that is to make the Forget again
// (node.BatchCallError.Applied). A Forget the batch refused after fn — a
// key another endpoint still serves — is not queued again, and a key the
// reconciler held under it stays held. The erasures a failed reassembly
// leaves are go-fabric's: recorded, and run by the next batch. r.mu is
// held.
func (r *Reconciler) forgetDevices(ctx context.Context, scope string, devs map[string]struct{}) error {
	held := r.held[scope]
	keys := map[string]endpoint.SourceKey{}
	owner := map[string]string{} // key → device address
	var errs []error
	for addr := range devs {
		if r.keys != nil {
			stored, err := r.keys.DeviceKeys(ctx, scope, addr)
			if err != nil {
				errs = append(errs, fmt.Errorf("stored keys of %s: %w", addr, err))
				r.QueueForget(scope, addr)
			}
			for _, k := range stored {
				keys[k.String()] = k
				owner[k.String()] = addr
			}
		}
		for key, h := range held {
			if h.address == addr {
				keys[key] = h.key
				owner[key] = addr
			}
		}
	}
	if len(keys) == 0 {
		return errors.Join(errs...)
	}
	forgotten := map[string]*heldEndpoint{}
	err := r.agg.Partition(scope).Batch(ctx, func(b *node.Batch) error {
		var errs []error
		for key, k := range keys {
			if err := b.Forget(k); err != nil {
				errs = append(errs, fmt.Errorf("forget %s: %w", key, err))
				continue
			}
			if h, ok := held[key]; ok {
				forgotten[key] = h
				delete(held, key)
			}
		}
		return errors.Join(errs...)
	})
	if err != nil {
		errs = append(errs, batchFailure(err))
		undone, unfinished := batchCalls(err)
		for key := range undone {
			if h, ok := forgotten[key]; ok {
				held[key] = h
			}
		}
		for key := range unfinished {
			if addr, ok := owner[key]; ok {
				r.QueueForget(scope, addr)
			}
		}
	}
	r.reachMu.Lock()
	r.reindex(scope, devs)
	r.reachMu.Unlock()
	r.logger.Info("matter.reconciler.forget",
		slog.String("central", scope), slog.Int("devices", len(devs)), slog.Int("keys", len(keys)))
	return errors.Join(errs...)
}

// Sync describes snapshots and applies the changes to the Aggregator, one
// batch per central. Every change that applies stays applied whatever
// else fails; the result joins the errors of the walk, the Aggregator and
// the reassemblies, and is [ErrNotServed] where the node does not serve
// the described topology. Before the node starts a sync only changes the
// set; after it, each changed central reassembles once, and a central
// whose earlier reassembly failed reassembles even without a change.
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
		return errors.Join(append(errs, fmt.Errorf("%w: %w", ErrNotServed, err))...)
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
			errs = append(errs, batchFailure(err))
			be, ok := errors.AsType[*node.BatchError](err)
			if !ok || be.Fn != nil {
				// A Forget refused at the call, or no batch ran: settle
				// nothing, the next sync asks the store again.
				return errors.Join(errs...)
			}
			// Every Forget the result does not name stands — after a
			// failed reassembly too, whose erasures go-fabric records and
			// runs with the next batch; a named one is tried again.
			undone, unfinished := batchCalls(err)
			settled = slices.DeleteFunc(settled, func(key string) bool {
				_, u := undone[key]
				_, f := unfinished[key]
				return u || f
			})
		} else {
			r.logger.Info("matter.reconciler.forget_superseded",
				slog.String("central", d.Scope), slog.Int("keys", len(forget)))
		}
	}
	for _, key := range settled {
		r.pruned[key] = struct{}{}
	}
	return errors.Join(errs...)
}

// syncCentral applies one central's described devices to its partition.
// A device the node is about to bind is first stated the model's
// availability ([Reconciler.seed]); from the seed until the index names
// the devices the node serves, reachMu holds every flip back. A device replaced because a stated
// device value changed (a firmware update) raises its device's
// ConfigurationVersion once the batch is applied, as matter.js leaves it
// to the bridge to decide which change is a configuration change
// (BridgedDeviceBasicInformationServer increaseConfigurationVersion). A
// changed name is stated on the held device after the batch. r.mu is held.
func (r *Reconciler) syncCentral(ctx context.Context, d Described) error {
	held := r.held[d.Scope]
	if held == nil {
		held = map[string]*heldEndpoint{}
		r.held[d.Scope] = held
	}
	type change = syncChange
	var (
		changes []change
		renames []*heldEndpoint
		names   []string
		touched = map[string]struct{}{}
	)
	seen := make(map[string]struct{}, len(d.Endpoints))
	for _, e := range d.Endpoints {
		if e.Device == nil {
			continue
		}
		spec := e.Spec()
		if spec.StableKey == nil {
			continue
		}
		key := spec.StableKey.String()
		seen[key] = struct{}{}
		next := &heldEndpoint{
			dev: e.Device, key: spec.StableKey, address: spec.DeviceAddress,
			name: spec.FriendlyName, id: identityOf(e, spec),
		}
		switch h, ok := held[key]; {
		case !ok:
			changes = append(changes, change{key: key, e: e, next: next})
		case h.id != next.id:
			changes = append(changes, change{key: key, e: e, next: next, replace: true, raise: h.id.stated.valueChanged(next.id.stated)})
		case h.name != next.name:
			renames, names = append(renames, h), append(names, next.name)
		default:
			// Unchanged in what the wire sees; the held device stays.
		}
	}
	r.reachMu.Lock()
	for _, c := range changes {
		r.seed(ctx, d.Scope, c.next.address, c.e)
		touched[c.next.address] = struct{}{}
	}
	var raise []fabricdevice.SourceKey
	before := maps.Clone(held)
	err := r.agg.Partition(d.Scope).Batch(ctx, func(b *node.Batch) error {
		var errs []error
		for _, c := range changes {
			if c.replace {
				if err := b.Replace(c.next.key, c.e.Device); err != nil {
					errs = append(errs, fmt.Errorf("replace %s: %w", c.key, err))
					continue
				}
				if c.raise {
					raise = append(raise, c.next.key)
				}
			} else if err := b.Add(c.e.Device); err != nil {
				errs = append(errs, fmt.Errorf("add %s: %w", c.key, err))
				continue
			}
			held[c.key] = c.next
		}
		for key, h := range held {
			if _, ok := seen[key]; ok {
				continue
			}
			touched[h.address] = struct{}{}
			if err := b.Remove(h.key); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", key, err))
				continue
			}
			delete(held, key)
		}
		return errors.Join(errs...)
	})
	if err != nil {
		raise = r.resettle(d.Scope, err, before, raise)
	}
	r.reindex(d.Scope, touched)
	r.reachMu.Unlock()
	var errs []error
	if err != nil {
		errs = append(errs, batchFailure(err))
	}
	for i, h := range renames {
		name := names[i]
		if uerr := h.dev.Update(ctx, func(st *fabricdevice.HostStatement) { st.Name = name }); uerr != nil {
			errs = append(errs, fmt.Errorf("rename %s: %w", h.key, uerr))
			continue
		}
		h.name = name
	}
	if len(raise) > 0 {
		if rerr := r.agg.IncreaseConfigurationVersions(ctx, raise...); rerr != nil && !errors.Is(rerr, node.ErrNotStarted) {
			errs = append(errs, fmt.Errorf("raise configuration version: %w", rerr))
		}
	}
	return errors.Join(errs...)
}

// syncChange is one key a sync adds or replaces.
type syncChange struct {
	key     string
	e       Endpoint
	next    *heldEndpoint
	replace bool
	raise   bool
}

// resettle corrects what the reconciler holds for scope after a batch
// returned err. Every call that answered nil set held at the call; a call
// the batch refused or undid after fn returned is named, not applied, in
// node.BatchError.Calls (go-fabric 0.28) and is taken back: an Add is not
// held, a Replace or a Remove keeps the device held before the batch — the
// one the partition still holds — so the next sync tries the key again. An
// applied call whose store work failed stands, and so does every call of
// a batch whose reassembly failed: the set holds them, and the next batch
// serves them. Any other result — fn's own error, which names only calls
// refused at the call and never held, or a stopped node, which ran no fn —
// leaves held as fn left it. A ConfigurationVersion is raised only for a
// replacement that stands. r.mu and r.reachMu are held.
func (r *Reconciler) resettle(scope string, err error, before map[string]*heldEndpoint, raise []fabricdevice.SourceKey) []fabricdevice.SourceKey {
	undone, _ := batchCalls(err)
	if len(undone) == 0 {
		return raise
	}
	held := r.held[scope]
	for key := range undone {
		if h := before[key]; h != nil {
			held[key] = h
		} else {
			delete(held, key)
		}
	}
	return slices.DeleteFunc(raise, func(k fabricdevice.SourceKey) bool {
		_, u := undone[k.String()]
		return u
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
	// No later walk names the scope again, so its findings would never
	// be pruned by one.
	r.asm.findings.dropScope(scope)
	r.reachMu.Lock()
	delete(r.reach, scope)
	r.reachMu.Unlock()
	part := r.agg.Partition(scope)
	var errs []error
	if len(held) > 0 {
		err := part.Batch(ctx, func(b *node.Batch) error {
			var errs []error
			for key, h := range held {
				if err := b.Remove(h.key); err != nil {
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
