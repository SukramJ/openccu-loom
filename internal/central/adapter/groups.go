// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CentralGroups pairs a central name with its parsed heating groups.
type CentralGroups struct {
	Central string
	Groups  []group.Group
}

// GroupsDomain serves the heating-group surface through each central's
// heating-group port (a CCU's jpages proxy, an openccu-lite box's groups
// API), resolving the target central from the registry.
type GroupsDomain struct {
	registry *central.Registry
}

// NewGroupsDomain wires the live adapter.
func NewGroupsDomain(r *central.Registry) *GroupsDomain {
	return &GroupsDomain{registry: r}
}

// List returns heating groups grouped per central. When centralName is
// non-empty it scopes to that central and returns
// [hmerr.ErrUnknownCentral] if it is not registered or
// backends.ErrUnsupported if its system cannot read groups.
// When centralName is empty it aggregates across every registered
// central, sorted by name, silently skipping centrals whose backend has
// no group capability or whose fetch fails — an offline or non-CCU
// central never fails the whole listing.
func (a *GroupsDomain) List(ctx context.Context, centralName string) ([]CentralGroups, error) {
	if a.registry == nil {
		return nil, hmerr.ErrUnknownCentral
	}
	if centralName != "" {
		unit, ok := a.registry.Get(centralName)
		if !ok || unit == nil {
			return nil, hmerr.ErrUnknownCentral
		}
		groups, err := a.groupsOf(ctx, unit)
		if err != nil {
			return nil, err
		}
		return []CentralGroups{{Central: unit.Name(), Groups: groups}}, nil
	}
	units := a.registry.List()
	out := make([]CentralGroups, 0, len(units))
	for _, unit := range units {
		if unit == nil {
			continue
		}
		groups, err := a.groupsOf(ctx, unit)
		if err != nil {
			// Aggregate mode is best-effort: a non-CCU or offline
			// central contributes no groups rather than aborting.
			out = append(out, CentralGroups{Central: unit.Name(), Groups: []group.Group{}})
			continue
		}
		out = append(out, CentralGroups{Central: unit.Name(), Groups: groups})
	}
	return out, nil
}

// groupsOf reads the central's groups through its port and resolves each
// member's names from the live device model.
func (a *GroupsDomain) groupsOf(ctx context.Context, unit *central.Unit) ([]group.Group, error) {
	port, err := groupsPortOf(unit)
	if err != nil {
		return nil, err
	}
	groups, err := port.List(ctx)
	if err != nil {
		return nil, err
	}
	enrichGroupMembers(unit, groups)
	return groups, nil
}

// enrichGroupMembers resolves each group member's device/channel name, model and
// rooms from the live device model so the overview shows members by name instead
// of their bare address. Best-effort: an unresolved member keeps only its
// address. Shares resolveMemberIdentity with the suitable-members enrichment, so
// a member addressed by its bare device address resolves the same way here.
func enrichGroupMembers(unit *central.Unit, groups []group.Group) {
	for gi := range groups {
		members := groups[gi].Members
		for mi := range members {
			id := resolveMemberIdentity(unit, members[mi].Address)
			members[mi].DeviceName = id.DeviceName
			members[mi].DeviceModel = id.DeviceModel
			members[mi].ChannelName = id.ChannelName
			members[mi].Rooms = id.Rooms
		}
	}
}
