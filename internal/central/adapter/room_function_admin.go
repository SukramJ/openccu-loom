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
)

// RoomFunctionAdminDomain implements the REST room/function entity-CRUD
// port by resolving the target central's hub model and dispatching the
// create/rename/delete Rega scripts. Rooms and functions (Gewerke) are
// per-CCU objects, so every call resolves a central — by name, or the
// sole central when only one is configured.
type RoomFunctionAdminDomain struct {
	registry *central.Registry
}

// NewRoomFunctionAdminDomain constructs the domain.
func NewRoomFunctionAdminDomain(r *central.Registry) *RoomFunctionAdminDomain {
	return &RoomFunctionAdminDomain{registry: r}
}

// resolve returns the hub model of the named central, or — when central
// is empty — the sole configured central's hub.
func (d *RoomFunctionAdminDomain) resolve(centralName string) (*central.Unit, error) {
	if d == nil || d.registry == nil {
		return nil, hub.ErrCentralNotFound
	}
	units := d.registry.List()
	if centralName == "" {
		if len(units) == 1 {
			return units[0], nil
		}
		return nil, hub.ErrCentralAmbiguous
	}
	for _, u := range units {
		if u.Name() == centralName {
			return u, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", hub.ErrCentralNotFound, centralName)
}

// CreateRoom creates a room on the target central.
func (d *RoomFunctionAdminDomain) CreateRoom(ctx context.Context, centralName, name string) (hub.CreatedNode, error) {
	return d.create(ctx, centralName, taxonomy.EnumRoom, name)
}

// RenameRoom renames a room on the target central.
func (d *RoomFunctionAdminDomain) RenameRoom(ctx context.Context, centralName, oldName, newName string) error {
	return d.rename(ctx, centralName, taxonomy.EnumRoom, oldName, newName)
}

// DeleteRoom deletes a room on the target central.
func (d *RoomFunctionAdminDomain) DeleteRoom(ctx context.Context, centralName, name string) error {
	return d.remove(ctx, centralName, taxonomy.EnumRoom, name)
}

// CreateFunction creates a function (Gewerk) on the target central.
func (d *RoomFunctionAdminDomain) CreateFunction(ctx context.Context, centralName, name string) (hub.CreatedNode, error) {
	return d.create(ctx, centralName, taxonomy.EnumFunction, name)
}

// RenameFunction renames a function on the target central.
func (d *RoomFunctionAdminDomain) RenameFunction(ctx context.Context, centralName, oldName, newName string) error {
	return d.rename(ctx, centralName, taxonomy.EnumFunction, oldName, newName)
}

// DeleteFunction deletes a function on the target central.
func (d *RoomFunctionAdminDomain) DeleteFunction(ctx context.Context, centralName, name string) error {
	return d.remove(ctx, centralName, taxonomy.EnumFunction, name)
}

// A system with a taxonomy node admin (openccu-lite) takes these verbs as
// node operations on a root node; its node ids are not numbers, so a
// created node carries no legacy id. A CCU takes them through its room
// and function scripts, as always.

func (d *RoomFunctionAdminDomain) create(ctx context.Context, centralName string, e taxonomy.EnumID, name string) (hub.CreatedNode, error) {
	u, err := d.resolve(centralName)
	if err != nil {
		return hub.CreatedNode{}, err
	}
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		ref, err := admin.CreateNode(ctx, e, "", name)
		return hub.CreatedNode{Ref: ref}, err
	}
	var id int
	if e == taxonomy.EnumRoom {
		id, err = u.HubModel.CreateRoomRemote(ctx, name)
	} else {
		id, err = u.HubModel.CreateFunctionRemote(ctx, name)
	}
	if err != nil {
		return hub.CreatedNode{}, err
	}
	return hub.CreatedNode{LegacyID: id, Ref: taxonomy.Root(e, strconv.Itoa(id))}, nil
}

func (d *RoomFunctionAdminDomain) rename(ctx context.Context, centralName string, e taxonomy.EnumID, oldName, newName string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		r, err := nodeByName(u, e, oldName)
		if err != nil {
			return err
		}
		return admin.RenameNode(ctx, r, newName)
	}
	if e == taxonomy.EnumRoom {
		return u.HubModel.RenameRoomRemote(ctx, oldName, newName)
	}
	return u.HubModel.RenameFunctionRemote(ctx, oldName, newName)
}

func (d *RoomFunctionAdminDomain) remove(ctx context.Context, centralName string, e taxonomy.EnumID, name string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	if admin, ok := u.HubModel.TaxonomyAdminRemote(); ok {
		r, err := nodeByName(u, e, name)
		if err != nil {
			return err
		}
		return admin.DeleteNode(ctx, r)
	}
	if e == taxonomy.EnumRoom {
		return u.HubModel.DeleteRoomRemote(ctx, name)
	}
	return u.HubModel.DeleteFunctionRemote(ctx, name)
}

// nodeByName resolves a display name to its node; a name no node carries
// is not found, one several nodes carry is ambiguous and names them.
func nodeByName(u *central.Unit, e taxonomy.EnumID, name string) (taxonomy.Ref, error) {
	var tax *taxonomy.Taxonomy
	if u.DeviceDetails != nil {
		tax = u.DeviceDetails.Taxonomy()
	}
	refs := tax.FindByName(e, name)
	switch len(refs) {
	case 0:
		if e == taxonomy.EnumRoom {
			return taxonomy.Ref{}, fmt.Errorf("%w: %q", hub.ErrRoomNotFound, name)
		}
		return taxonomy.Ref{}, fmt.Errorf("%w: %q", hub.ErrFunctionNotFound, name)
	case 1:
		return refs[0], nil
	default:
		return taxonomy.Ref{}, &taxonomy.AmbiguousNameError{Enum: e, Name: name, Candidates: refs}
	}
}
