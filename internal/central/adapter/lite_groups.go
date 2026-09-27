// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// errLiteGroupMembers refuses the heating-group operations that carry
// members. The box's groups API names its members, candidates and the ids
// a create takes without a documented shape, and reading them by an
// assumed one would put devices into the wrong group; until the shape is
// known these operations are not offered.
var errLiteGroupMembers = fmt.Errorf("openccu-lite heating groups: the member format of the box's groups API is not known yet: %w",
	backends.ErrUnsupported)

// liteHeatingGroups is an openccu-lite central's heating-group port over
// the box's groups API: the group list, the group types and deleting a
// group. The ids are the box's; one that is not numeric cannot be
// addressed through the numeric group ids the daemon uses and is reported
// as an error rather than renumbered.
type liteHeatingGroups struct {
	*liteSystem
}

// List implements [central.HeatingGroups]. Members are not listed; see
// [errLiteGroupMembers].
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
		id, err := strconv.Atoi(lg.ID)
		if err != nil {
			return nil, fmt.Errorf("openccu-lite heating group %q: the id is not numeric: %w", lg.ID, backends.ErrUnsupported)
		}
		out = append(out, group.Group{ID: id, Name: lg.Name, TypeID: lg.Type, TypeLabel: lg.TypeLabel})
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

// SuitableMembers implements [central.HeatingGroups].
func (g liteHeatingGroups) SuitableMembers(context.Context, string) (group.SuitableMembers, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsRead, backends.ErrUnsupported); err != nil {
		return group.SuitableMembers{}, err
	}
	return group.SuitableMembers{}, errLiteGroupMembers
}

// Create implements [central.HeatingGroups].
func (g liteHeatingGroups) Create(context.Context, group.CreateInput) (group.Group, error) {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return group.Group{}, err
	}
	return group.Group{}, errLiteGroupMembers
}

// Update implements [central.HeatingGroups].
func (g liteHeatingGroups) Update(context.Context, int, group.UpdateInput) error {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return err
	}
	return errLiteGroupMembers
}

// Delete implements [central.HeatingGroups].
func (g liteHeatingGroups) Delete(ctx context.Context, id int) error {
	if err := g.require(hmenum.FeatureHeatingGroupsWrite, backends.ErrUnsupported); err != nil {
		return err
	}
	_, err := g.client.DeleteGroup(ctx, strconv.Itoa(id))
	if errors.Is(err, occulited.ErrUnknownGroup) {
		return fmt.Errorf("%w: %d", hmerr.ErrGroupNotFound, id)
	}
	return err
}

var _ central.HeatingGroups = liteHeatingGroups{}
