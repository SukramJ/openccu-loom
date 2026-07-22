// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// groupsAdapter maps the adapter-layer heating-group domain onto the
// REST/WS read DTOs. It satisfies handlers.GroupsReader (and the WS
// GroupsQuery). Backend resolution and groups.gson parsing live in
// [adapter.GroupsDomain]; this shim only translates the typed result
// into the transport-facing shape.
type groupsAdapter struct {
	domain *adapter.GroupsDomain
}

func newGroupsAdapter(d *adapter.GroupsDomain) *groupsAdapter {
	return &groupsAdapter{domain: d}
}

// List implements handlers.GroupsReader.
func (a *groupsAdapter) List(ctx context.Context, central string) ([]handlers.GroupCentralEntry, error) {
	if a.domain == nil {
		return []handlers.GroupCentralEntry{}, nil
	}
	sets, err := a.domain.List(ctx, central)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.GroupCentralEntry, 0, len(sets))
	for _, s := range sets {
		out = append(out, handlers.GroupCentralEntry{
			Central: s.Central,
			Groups:  mapGroups(s.Groups),
		})
	}
	return out, nil
}

func mapGroups(groups []group.Group) []handlers.GroupEntry {
	out := make([]handlers.GroupEntry, 0, len(groups))
	for _, g := range groups {
		members := make([]handlers.GroupMemberEntry, 0, len(g.Members))
		for _, m := range g.Members {
			members = append(members, handlers.GroupMemberEntry{
				Address: m.Address,
				TypeID:  m.TypeID,
			})
		}
		out = append(out, handlers.GroupEntry{
			ID:                    g.ID,
			Name:                  g.Name,
			GroupDeviceName:       g.GroupDeviceName,
			ForbidSingleOperation: g.ForbidSingleOperation,
			TypeID:                g.TypeID,
			TypeLabel:             g.TypeLabel,
			Members:               members,
		})
	}
	return out
}
