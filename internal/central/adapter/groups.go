// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// heatingGroupLister is the narrow capability a backend exposes when it
// can read its CCU's heating-group roster. Only the CCU backend
// implements it (it reads /etc/config/groups.gson via the
// CCU.getHeatingGroupList JSON-RPC method); CUxD / Homegear backends do
// not, so a request routed to one surfaces as unsupported (scoped mode)
// or is skipped (aggregate mode).
type heatingGroupLister interface {
	GetHeatingGroupList(ctx context.Context) (string, error)
}

// CentralGroups pairs a central name with its parsed heating groups.
type CentralGroups struct {
	Central string
	Groups  []group.Group
}

// GroupsDomain serves the read-only heating-group surface. Reads run
// through the target central's primary backend; the write surface
// (create / edit / delete) is handled separately via the CCU jpages
// proxy — see docs/adr/0055-groups-jpages-proxy.md.
type GroupsDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewGroupsDomain wires the live adapter.
func NewGroupsDomain(r *central.Registry, w *client.ValueWriter) *GroupsDomain {
	return &GroupsDomain{registry: r, writer: w}
}

// List returns heating groups grouped per central. When centralName is
// non-empty it scopes to that central and returns
// [hmerr.ErrUnknownCentral] if it is not registered or
// [backends.ErrUnsupported] if its primary backend cannot read groups.
// When centralName is empty it aggregates across every registered
// central, sorted by name, silently skipping centrals whose backend has
// no group capability or whose fetch fails — an offline or non-CCU
// central never fails the whole listing.
func (a *GroupsDomain) List(ctx context.Context, centralName string) ([]CentralGroups, error) {
	if a.registry == nil || a.writer == nil {
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

// groupsOf resolves the central's primary backend, fetches the raw
// groups.gson payload, and parses it.
func (a *GroupsDomain) groupsOf(ctx context.Context, unit *central.Unit) ([]group.Group, error) {
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return nil, err
	}
	lister, ok := backend.(heatingGroupLister)
	if !ok {
		return nil, backends.ErrUnsupported
	}
	raw, err := lister.GetHeatingGroupList(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := group.ParseGroupList(raw)
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
