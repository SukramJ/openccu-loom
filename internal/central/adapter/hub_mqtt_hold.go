// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The hub half of the deferred MQTT retraction across a restart of a central
// (the device half and the reasoning are in eventbridge_hold.go). The
// cache-reset re-pull does not reach it: it reloads the hub model without
// retracting the hub plane.
//
// [HubMQTTPublisher.HoldCentral] holds every hub config of the central
// instead of retracting it and writes `offline` to the central's gate. The
// first hub wiring of the completed generation reconciles: a group the new
// wiring declared again is settled by the publish itself; of the rest
//   - connectivity and install-mode entities of an interface the restart
//     dropped are retracted, the others kept;
//   - programs and system variables the new model lacks are retracted only
//     once the class is settled — the central does not offer it at all, or a
//     complete fetch of it succeeded ([hub.Hub.ProgramsObserved]) — because a
//     failed load is only a warning and leaves the class empty without that
//     saying anything about the CCU;
//   - the central-wide singletons are declared again by every wiring — four
//     unconditionally, four through the feature gate, which retracts those
//     the central does not offer — so what is left of their hold is dropped.

// Hold-group prefixes of the hub plane.
const (
	hubHoldProgram      = "hub/program/"
	hubHoldSysvar       = "hub/sysvar/"
	hubHoldInstallMode  = "hub/installmode/"
	hubHoldConnectivity = "hub/connectivity/"
	hubHoldSingleton    = "hub/singleton"
)

// hubItemGroup is the discovery of one hub entity class member, with the
// model object its raw-plane topics belong to.
type hubItemGroup struct {
	group   string
	items   []mqtt.DiscoveryItem
	program *hub.Program
	sysvar  *hub.Sysvar
}

// hubHold is what a restart held of one central's hub plane: the old program
// and sysvar objects by group (their raw topics are resolved from them) and
// the interfaces the restart dropped.
type hubHold struct {
	programs map[string]*hub.Program
	sysvars  map[string]*hub.Sysvar
	dropped  map[hmenum.Interface]bool
}

// hubPlaneItemGroups builds the hub-plane discovery items of a central from
// its live hub model, grouped for the hold. connectivityInterfaces overrides
// the live client list (see [HubMQTTPublisher.RetractCentral]).
func hubPlaneItemGroups(u *central.Unit, b *mqtt.Bridge, disco *mqtt.DefaultDiscoveryBuilder, connectivityInterfaces []hmenum.Interface) []hubItemGroup {
	centralName := u.Name()
	var groups []hubItemGroup
	if hubModel := u.HubModel; hubModel != nil {
		for _, prog := range hubModel.Programs() {
			if prog == nil || prog.Internal() {
				continue
			}
			groups = append(groups, hubItemGroup{
				group:   hubHoldProgram + prog.ID,
				items:   disco.BuildProgramDiscoveryRoles(centralName, programSpecFor(prog), b.ProgramRoles(centralName, prog)),
				program: prog,
			})
		}
		for _, sv := range hubModel.Sysvars() {
			if sv == nil {
				continue
			}
			spec := sysvarSpecFor(sv)
			groups = append(groups, hubItemGroup{
				group:  hubHoldSysvar + strconv.Itoa(spec.Vid),
				items:  []mqtt.DiscoveryItem{disco.BuildSysvarDiscovery(centralName, spec)},
				sysvar: sv,
			})
		}
		for _, dp := range hubModel.InstallModeDPs() {
			if dp == nil || dp.InterfaceID == "" {
				continue
			}
			groups = append(groups, hubItemGroup{
				group: hubHoldInstallMode + string(BareInterfaceFromWireID(centralName, dp.InterfaceID)),
				items: []mqtt.DiscoveryItem{
					disco.BuildInstallModeSensorDiscovery(centralName, dp.InterfaceID),
					disco.BuildInstallModeButtonDiscovery(centralName, dp.InterfaceID),
				},
			})
		}
	}
	// Connectivity: one binary_sensor per registered interface, keyed by the same
	// `<central>-<iface>` wire id seedConnectivityDiscovery declares.
	var ifaces []hmenum.Interface
	switch {
	case connectivityInterfaces != nil:
		ifaces = connectivityInterfaces
	case u.Clients != nil:
		for _, entry := range u.Clients.List() {
			if entry == nil {
				continue
			}
			iface := entry.Interface
			if iface == "" {
				iface = BareInterfaceFromWireID(centralName, entry.InterfaceID)
			}
			ifaces = append(ifaces, iface)
		}
	}
	for _, iface := range ifaces {
		if iface == "" {
			continue
		}
		groups = append(groups, hubItemGroup{
			group: hubHoldConnectivity + string(iface),
			items: []mqtt.DiscoveryItem{disco.BuildConnectivityDiscovery(centralName, WireInterfaceID(centralName, iface))},
		})
	}
	// Central-wide singletons, declared again by every wiring of the central.
	groups = append(groups, hubItemGroup{group: hubHoldSingleton, items: []mqtt.DiscoveryItem{
		disco.BuildAlarmMessagesDiscovery(centralName),
		disco.BuildServiceMessagesDiscovery(centralName),
		disco.BuildDaemonStatusDiscovery(centralName),
		disco.BuildSystemHealthDiscovery(centralName),
		disco.BuildLastEventAgeDiscovery(centralName),
		disco.BuildConnectionLatencyDiscovery(centralName),
		disco.BuildInboxDiscovery(centralName),
		disco.BuildHubUpdateDiscovery(centralName),
	}})
	return groups
}

// HoldCentral is [HubMQTTPublisher.RetractCentral] for a restart of the
// central: the ReGa poller stops as there, but the hub configs are held
// instead of retracted, the raw topics stay, and the central's gate reads
// `offline` until the restarted central's wiring seeds it again.
//
// dropped names the interfaces the restart removes from the central; their
// connectivity and install-mode entities are retracted once the restarted
// model is whole. A central already held keeps its first baseline.
func (p *HubMQTTPublisher) HoldCentral(u *central.Unit, connectivityInterfaces []hmenum.Interface, dropped map[hmenum.Interface]bool) {
	if p == nil || u == nil || p.wiring == nil {
		return
	}
	p.stopRegaLivenessPoll(u.Name())
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	ctx := context.Background()
	if f := p.fanout.Load(); f != nil {
		ctx = f.ctx
	}
	centralName := u.Name()
	disco := b.DefaultBuilder()
	if disco == nil {
		disco = mqtt.NewDefaultDiscoveryBuilder(b.Topics(), centralName)
	}
	if hi := hubInfoFromUnit(u); hi.Serial != "" {
		disco.SetHubInfoFor(centralName, hi)
	}
	groups := hubPlaneItemGroups(u, b, disco, connectivityInterfaces)

	p.mu.Lock()
	if p.holds == nil {
		p.holds = map[string]*hubHold{}
	}
	h := p.holds[centralName]
	if h == nil {
		h = &hubHold{programs: map[string]*hub.Program{}, sysvars: map[string]*hub.Sysvar{}, dropped: map[hmenum.Interface]bool{}}
		p.holds[centralName] = h
	}
	for _, g := range groups {
		if _, ok := h.programs[g.group]; !ok && g.program != nil {
			h.programs[g.group] = g.program
		}
		if _, ok := h.sysvars[g.group]; !ok && g.sysvar != nil {
			h.sysvars[g.group] = g.sysvar
		}
	}
	for iface := range dropped {
		h.dropped[iface] = true
	}
	p.mu.Unlock()

	p.publish(func() {
		for _, g := range groups {
			b.HoldHubDiscovery(centralName, g.group, g.items)
		}
		b.AnnounceHubStatusOfflineFor(ctx, centralName)
	})
	// As in RetractCentral: the poller is stopped, and the restarted central
	// seeds its liveness from its own first probe.
	p.rega.forget(centralName)
}

// reconcileHeldHub settles a central's held hub plane after the wiring of its
// completed model declared everything it still has. It runs on the fan-out
// worker, behind those declares.
func (p *HubMQTTPublisher) reconcileHeldHub(ctx context.Context, u *central.Unit, b *mqtt.Bridge) {
	centralName := u.Name()
	p.mu.Lock()
	h := p.holds[centralName]
	p.mu.Unlock()
	if h == nil {
		return
	}
	programsLoaded := classLoaded(u, hmenum.FeatureHubPrograms, func() bool { return u.HubModel != nil && u.HubModel.ProgramsObserved() })
	sysvarsLoaded := classLoaded(u, hmenum.FeatureHubSysvars, func() bool { return u.HubModel != nil && u.HubModel.SysvarsObserved() })
	for _, group := range b.HeldDiscoveryGroups(centralName) {
		switch {
		case group == hubHoldSingleton:
			b.DropHeldDiscovery(centralName, group)
		case strings.HasPrefix(group, hubHoldConnectivity), strings.HasPrefix(group, hubHoldInstallMode):
			iface := hmenum.Interface(group[strings.LastIndex(group, "/")+1:])
			if h.dropped[iface] {
				b.RetractHeldDiscovery(ctx, centralName, group)
			} else {
				b.DropHeldDiscovery(centralName, group)
			}
		case strings.HasPrefix(group, hubHoldProgram):
			if programsLoaded {
				b.RetractHeldDiscovery(ctx, centralName, group)
				if prog := h.programs[group]; prog != nil {
					_ = b.RetractProgramTopics(ctx, centralName, prog)
				}
			}
		case strings.HasPrefix(group, hubHoldSysvar):
			if sysvarsLoaded {
				b.RetractHeldDiscovery(ctx, centralName, group)
				if sv := h.sysvars[group]; sv != nil {
					_ = b.RetractSysvarState(ctx, centralName, sv)
				}
			}
		}
	}
	// The update entity's state topic has no producer left once the central
	// no longer offers system updates; the feature gate retracts only its
	// config.
	if f := u.Features(); f.Known() && !f.Available(hmenum.FeatureHubSystemUpdate) {
		_ = b.RetractHubUpdate(ctx, centralName)
	}
	if len(b.HeldDiscoveryGroups(centralName)) == 0 {
		p.mu.Lock()
		if p.holds[centralName] == h {
			delete(p.holds, centralName)
		}
		p.mu.Unlock()
	}
}

// classLoaded reports whether a hub entity class is settled enough to retract
// its absent members: the central does not offer it, or a complete fetch of
// it succeeded.
func classLoaded(u *central.Unit, k hmenum.Feature, observed func() bool) bool {
	if f := u.Features(); f.Known() && !f.Available(k) {
		return true
	}
	return observed()
}

// retractHeldHub retracts everything held of a central's hub plane, raw
// topics of the held programs and sysvars included, and ends the hold. It
// runs on the fan-out worker.
func (p *HubMQTTPublisher) retractHeldHub(ctx context.Context, b *mqtt.Bridge, centralName string) {
	p.mu.Lock()
	h := p.holds[centralName]
	delete(p.holds, centralName)
	p.mu.Unlock()
	p.publish(func() {
		for _, group := range b.HeldDiscoveryGroups(centralName) {
			if strings.HasPrefix(group, "hub/") {
				b.RetractHeldDiscovery(ctx, centralName, group)
			}
		}
		if h == nil {
			return
		}
		for _, prog := range h.programs {
			_ = b.RetractProgramTopics(ctx, centralName, prog)
		}
		for _, sv := range h.sysvars {
			_ = b.RetractSysvarState(ctx, centralName, sv)
		}
	})
}

// ReleaseHeldCentral retracts what a restart held of a central whose adopt
// then failed: nothing comes back under the name, so nothing will settle the
// hold. The gate goes with it, as at a removal.
func (p *HubMQTTPublisher) ReleaseHeldCentral(centralName string) {
	if p == nil || p.wiring == nil {
		return
	}
	b := p.wiring.Bridge()
	if b == nil {
		return
	}
	ctx := context.Background()
	if f := p.fanout.Load(); f != nil {
		ctx = f.ctx
	}
	p.retractHeldHub(ctx, b, centralName)
	p.publish(func() {
		_ = b.RetractHubUpdate(ctx, centralName)
		_ = b.RetractHubStatus(ctx, centralName)
	})
}
