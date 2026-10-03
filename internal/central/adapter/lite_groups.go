// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteHeatingGroups is an openccu-lite central's heating-group port over
// the box's groups API. The box numbers its groups, so its ids are the
// daemon's; a member is named by the id the box lists it under, which is
// the channel address.
type liteHeatingGroups struct {
	*liteSystem
}

// liteGroupMembers maps the box's members onto the domain's; the names
// are resolved from the device model by the caller.
func liteGroupMembers(in []occulited.GroupMember) []group.Member {
	out := make([]group.Member, 0, len(in))
	for _, m := range in {
		out = append(out, group.Member{Address: m.ID, TypeID: m.Type})
	}
	return out
}

// liteGroupOf maps a group's detail onto the domain's group. The detail
// carries no type label, so it is looked up among the types the detail
// lists.
func liteGroupOf(d occulited.GroupDetail) group.Group {
	label := ""
	for _, t := range d.Types {
		if t.ID == d.Type {
			label = t.Label
			break
		}
	}
	return group.Group{
		ID: d.ID, Name: d.Name, GroupDeviceName: d.DeviceName,
		ForbidSingleOperation: d.ForbidSingleOperation,
		TypeID:                d.Type, TypeLabel: label,
		Members: liteGroupMembers(d.Members),
	}
}

// List implements [central.HeatingGroups]. The box's list names no
// members, so each group's detail is read for them; a group deleted in
// between is left out.
func (g liteHeatingGroups) List(ctx context.Context) ([]group.Group, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsRead, backends.ErrUnsupported); err != nil {
		return nil, err
	}
	ans, err := g.client.Groups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]group.Group, 0, len(ans.Groups))
	for _, lg := range ans.Groups {
		detail, err := g.client.Group(ctx, lg.ID)
		if errors.Is(err, occulited.ErrUnknownGroup) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, liteGroupOf(detail))
	}
	return out, nil
}

// Types implements [central.HeatingGroups].
func (g liteHeatingGroups) Types(ctx context.Context) ([]group.Type, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsRead, backends.ErrUnsupported); err != nil {
		return nil, err
	}
	ans, err := g.client.GroupTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]group.Type, 0, len(ans.Types))
	for _, t := range ans.Types {
		out = append(out, group.Type{ID: t.ID, LabelKey: t.Label})
	}
	return out, nil
}

// SuitableMembers implements [central.HeatingGroups]: the channels the
// box lists for the type — free ones as assignable, ones that belong to
// a group of the type already as leftover. A type the box does not offer
// has neither.
func (g liteHeatingGroups) SuitableMembers(ctx context.Context, typeID string) (group.SuitableMembers, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsRead, backends.ErrUnsupported); err != nil {
		return group.SuitableMembers{}, err
	}
	ans, err := g.client.GroupTypes(ctx)
	if err != nil {
		return group.SuitableMembers{}, err
	}
	for _, t := range ans.Types {
		if t.ID == typeID {
			return group.SuitableMembers{
				Assignable: g.candidates(t.Assignable),
				Leftover:   g.candidates(t.Leftover),
			}, nil
		}
	}
	return group.SuitableMembers{Assignable: []group.MemberCandidate{}, Leftover: []group.MemberCandidate{}}, nil
}

func (g liteHeatingGroups) candidates(in []occulited.GroupMember) []group.MemberCandidate {
	out := make([]group.MemberCandidate, 0, len(in))
	for _, m := range in {
		c := group.MemberCandidate{Address: m.ID, Serial: m.Serial, Type: m.Type, DeviceAddress: deviceOf(m.ID)}
		enrichCandidate(&c, g.unit)
		out = append(out, c)
	}
	return out
}

// notAssigned names the members a write asked for that the box's answer
// does not hold.
func notAssigned(asked []string, got []occulited.GroupMember) []string {
	var missing []string
	for _, id := range asked {
		if !slices.ContainsFunc(got, func(m occulited.GroupMember) bool { return m.ID == id }) {
			missing = append(missing, id)
		}
	}
	return missing
}

// Create implements [central.HeatingGroups]. The box answers a create as
// done even when it left a member out, so the answer is compared with the
// members asked for; a group made without all of them is deleted again
// and the create fails, leaving nothing behind.
func (g liteHeatingGroups) Create(ctx context.Context, in group.CreateInput) (group.Group, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return group.Group{}, err
	}
	forbid := in.ForbidSingleOperation
	written, err := g.client.CreateGroup(ctx, occulited.GroupCreate{
		Name: in.Name, Type: in.TypeID, Members: in.MemberIDs, ForbidSingleOperation: &forbid,
	})
	if err != nil {
		return group.Group{}, err
	}
	if missing := notAssigned(in.MemberIDs, written.Members); len(missing) > 0 {
		err := &hmerr.GroupMembersNotAssignedError{Members: missing}
		if _, delErr := g.client.DeleteGroup(ctx, written.ID); delErr != nil {
			return group.Group{}, errors.Join(err, fmt.Errorf("group %d was made without them and could not be removed: %w", written.ID, delErr))
		}
		return group.Group{}, err
	}
	return liteGroupOf(written.GroupDetail), nil
}

// Update implements [central.HeatingGroups]: the name, the flag and the
// members as a whole list. A member the box left out is reported; the
// group then holds the other members of the new list.
func (g liteHeatingGroups) Update(ctx context.Context, id int, in group.UpdateInput) error {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return err
	}
	members := in.MemberIDs
	if members == nil {
		members = []string{}
	}
	forbid := in.ForbidSingleOperation
	body := occulited.GroupUpdate{Members: &members, ForbidSingleOperation: &forbid}
	if in.Name != "" {
		body.Name = &in.Name
	}
	written, err := g.client.UpdateGroup(ctx, id, body)
	if errors.Is(err, occulited.ErrUnknownGroup) {
		return fmt.Errorf("%w: %d", hmerr.ErrGroupNotFound, id)
	}
	if err != nil {
		return err
	}
	if missing := notAssigned(members, written.Members); len(missing) > 0 {
		return &hmerr.GroupMembersNotAssignedError{Members: missing}
	}
	return nil
}

// Delete implements [central.HeatingGroups].
func (g liteHeatingGroups) Delete(ctx context.Context, id int) error {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := g.client.DeleteGroup(ctx, id)
	if errors.Is(err, occulited.ErrUnknownGroup) {
		return fmt.Errorf("%w: %d", hmerr.ErrGroupNotFound, id)
	}
	return err
}

var _ central.HeatingGroups = liteHeatingGroups{}
