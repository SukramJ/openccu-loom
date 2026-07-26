// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
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

// CreateGroup implements handlers.GroupsWriter.
func (a *groupsAdapter) CreateGroup(ctx context.Context, central string, req handlers.CreateGroupRequest) (handlers.GroupEntry, error) {
	if a.domain == nil {
		return handlers.GroupEntry{}, backends.ErrUnsupported
	}
	g, err := a.domain.Create(ctx, central, group.CreateInput{
		TypeID:                req.TypeID,
		Name:                  req.Name,
		ForbidSingleOperation: req.ForbidSingleOperation,
		MemberIDs:             req.Members,
	})
	if err != nil {
		return handlers.GroupEntry{}, err
	}
	return mapGroup(g), nil
}

// UpdateGroup implements handlers.GroupsWriter.
func (a *groupsAdapter) UpdateGroup(ctx context.Context, central string, id int, req handlers.UpdateGroupRequest) error {
	if a.domain == nil {
		return backends.ErrUnsupported
	}
	return a.domain.Update(ctx, central, id, group.UpdateInput{
		Name:                  req.Name,
		ForbidSingleOperation: req.ForbidSingleOperation,
		MemberIDs:             req.Members,
	})
}

// DeleteGroup implements handlers.GroupsWriter.
func (a *groupsAdapter) DeleteGroup(ctx context.Context, central string, id int) error {
	if a.domain == nil {
		return backends.ErrUnsupported
	}
	return a.domain.Delete(ctx, central, id)
}

// SuitableMembers implements handlers.GroupsWriter.
func (a *groupsAdapter) SuitableMembers(ctx context.Context, central, typeID string) (handlers.SuitableMembersResponse, error) {
	if a.domain == nil {
		return handlers.SuitableMembersResponse{}, backends.ErrUnsupported
	}
	res, err := a.domain.SuitableMembers(ctx, central, typeID)
	if err != nil {
		return handlers.SuitableMembersResponse{}, err
	}
	return handlers.SuitableMembersResponse{
		Assignable: mapMemberCandidates(res.Assignable),
		Leftover:   mapMemberCandidates(res.Leftover),
	}, nil
}

// GroupTypes implements handlers.GroupsWriter.
func (a *groupsAdapter) GroupTypes(ctx context.Context, central string) ([]handlers.GroupTypeEntry, error) {
	if a.domain == nil {
		return nil, backends.ErrUnsupported
	}
	types, err := a.domain.Types(ctx, central)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.GroupTypeEntry, 0, len(types))
	for _, t := range types {
		out = append(out, handlers.GroupTypeEntry{ID: t.ID, LabelKey: t.LabelKey})
	}
	return out, nil
}

func mapMemberCandidates(in []group.MemberCandidate) []handlers.SuitableMemberEntry {
	out := make([]handlers.SuitableMemberEntry, 0, len(in))
	for i := range in {
		m := &in[i]
		out = append(out, handlers.SuitableMemberEntry{
			Address:       m.Address,
			Serial:        m.Serial,
			Type:          m.Type,
			DeviceAddress: m.DeviceAddress,
			DeviceName:    m.DeviceName,
			DeviceModel:   m.DeviceModel,
			ChannelName:   m.ChannelName,
			ChannelNo:     m.ChannelNo,
			Rooms:         m.Rooms,
			Functions:     m.Functions,
			ConfigPending: m.ConfigPending,
		})
	}
	return out
}

func mapGroup(g group.Group) handlers.GroupEntry {
	return mapGroups([]group.Group{g})[0]
}

func mapGroups(groups []group.Group) []handlers.GroupEntry {
	out := make([]handlers.GroupEntry, 0, len(groups))
	for _, g := range groups {
		members := make([]handlers.GroupMemberEntry, 0, len(g.Members))
		for _, m := range g.Members {
			members = append(members, handlers.GroupMemberEntry{
				Address:     m.Address,
				TypeID:      m.TypeID,
				DeviceName:  m.DeviceName,
				DeviceModel: m.DeviceModel,
				ChannelName: m.ChannelName,
				Rooms:       m.Rooms,
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
