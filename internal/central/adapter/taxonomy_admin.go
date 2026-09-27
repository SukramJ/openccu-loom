// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TaxonomyAdmin edits the taxonomy nodes of a central. A system whose
// taxonomies nest (openccu-lite) edits them through its own node admin; a
// CCU's rooms and functions are flat and edited through its room and
// function scripts, so a node below a parent, a move, and any enum but
// rooms and functions are refused there with the tree feature.
type TaxonomyAdmin struct {
	rooms *RoomFunctionAdminDomain
}

// NewTaxonomyAdmin wires the domain over the central registry.
func NewTaxonomyAdmin(r *central.Registry) *TaxonomyAdmin {
	return &TaxonomyAdmin{rooms: NewRoomFunctionAdminDomain(r)}
}

// CreateNode adds a node named name below parent (a path inside the enum,
// "" for the root) and returns its path.
func (a *TaxonomyAdmin) CreateNode(ctx context.Context, centralName, enum, parent, name string) (string, error) {
	u, err := a.rooms.resolve(centralName)
	if err != nil {
		return "", err
	}
	e := taxonomy.EnumID(enum)
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		ref, err := admin.CreateNode(ctx, e, taxonomy.Path(parent), name)
		return string(ref.Path), err
	}
	if err := flatOnly(u, e, parent != ""); err != nil {
		return "", err
	}
	var id int
	if e == taxonomy.EnumRoom {
		id, err = u.HubModel.CreateRoomRemote(ctx, name)
	} else {
		id, err = u.HubModel.CreateFunctionRemote(ctx, name)
	}
	if err != nil {
		return "", err
	}
	return strconv.Itoa(id), nil
}

// UpdateNode renames the node at path when name is set, and moves it below
// parent ("" for the root) at position when parent is set.
func (a *TaxonomyAdmin) UpdateNode(ctx context.Context, centralName, enum, path string, name, parent *string, position *int) error {
	u, err := a.rooms.resolve(centralName)
	if err != nil {
		return err
	}
	r := taxonomy.Ref{Enum: taxonomy.EnumID(enum), Path: taxonomy.Path(path)}
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		if name != nil {
			if err := admin.RenameNode(ctx, r, *name); err != nil {
				return err
			}
		}
		if parent != nil || position != nil {
			to := taxonomy.Path("")
			if parent != nil {
				to = taxonomy.Path(*parent)
			} else if p, ok := r.Parent(); ok {
				to = p.Path
			}
			return admin.MoveNode(ctx, r, to, position)
		}
		return nil
	}
	if err := flatOnly(u, r.Enum, parent != nil || position != nil); err != nil {
		return err
	}
	if name == nil {
		return nil
	}
	current, err := flatNodeName(u, r)
	if err != nil {
		return err
	}
	if r.Enum == taxonomy.EnumRoom {
		return u.HubModel.RenameRoomRemote(ctx, current, *name)
	}
	return u.HubModel.RenameFunctionRemote(ctx, current, *name)
}

// DeleteNode removes the node at path; the addresses assigned to it keep
// existing and lose the assignment.
func (a *TaxonomyAdmin) DeleteNode(ctx context.Context, centralName, enum, path string) error {
	u, err := a.rooms.resolve(centralName)
	if err != nil {
		return err
	}
	r := taxonomy.Ref{Enum: taxonomy.EnumID(enum), Path: taxonomy.Path(path)}
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		return admin.DeleteNode(ctx, r)
	}
	if err := flatOnly(u, r.Enum, false); err != nil {
		return err
	}
	current, err := flatNodeName(u, r)
	if err != nil {
		return err
	}
	if r.Enum == taxonomy.EnumRoom {
		return u.HubModel.DeleteRoomRemote(ctx, current)
	}
	return u.HubModel.DeleteFunctionRemote(ctx, current)
}

// flatOnly refuses what a flat taxonomy cannot do: nest or move a node
// (the tree feature, which a CCU does not offer), or edit an enum other
// than rooms and functions.
func flatOnly(u *central.Unit, e taxonomy.EnumID, nests bool) error {
	if nests {
		if err := u.Features().Require(u.Name(), hmenum.FeatureTaxonomyTree, hub.ErrNoRoomMutator); err != nil {
			return err
		}
	}
	if e != taxonomy.EnumRoom && e != taxonomy.EnumFunction {
		return fmt.Errorf("%w: central %s has no enum %q", hmerr.ErrValidation, u.Name(), e)
	}
	return nil
}

// flatNodeName resolves a flat node's current display name, which the
// CCU's room and function scripts are keyed by.
func flatNodeName(u *central.Unit, r taxonomy.Ref) (string, error) {
	var tax *taxonomy.Taxonomy
	if u.DeviceDetails != nil {
		tax = u.DeviceDetails.Taxonomy()
	}
	n, ok := tax.Node(r)
	if !ok {
		if r.Enum == taxonomy.EnumRoom {
			return "", fmt.Errorf("%w: %s", hub.ErrRoomNotFound, r)
		}
		return "", fmt.Errorf("%w: %s", hub.ErrFunctionNotFound, r)
	}
	return n.Name, nil
}
