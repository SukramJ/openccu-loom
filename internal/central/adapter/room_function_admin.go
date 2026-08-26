// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
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

// CreateRoom creates a room on the target central, returning its ID.
func (d *RoomFunctionAdminDomain) CreateRoom(ctx context.Context, centralName, name string) (int, error) {
	u, err := d.resolve(centralName)
	if err != nil {
		return 0, err
	}
	return u.HubModel.CreateRoomRemote(ctx, name)
}

// RenameRoom renames a room on the target central.
func (d *RoomFunctionAdminDomain) RenameRoom(ctx context.Context, centralName, oldName, newName string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	return u.HubModel.RenameRoomRemote(ctx, oldName, newName)
}

// DeleteRoom deletes a room on the target central.
func (d *RoomFunctionAdminDomain) DeleteRoom(ctx context.Context, centralName, name string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	return u.HubModel.DeleteRoomRemote(ctx, name)
}

// CreateFunction creates a function (Gewerk) on the target central.
func (d *RoomFunctionAdminDomain) CreateFunction(ctx context.Context, centralName, name string) (int, error) {
	u, err := d.resolve(centralName)
	if err != nil {
		return 0, err
	}
	return u.HubModel.CreateFunctionRemote(ctx, name)
}

// RenameFunction renames a function on the target central.
func (d *RoomFunctionAdminDomain) RenameFunction(ctx context.Context, centralName, oldName, newName string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	return u.HubModel.RenameFunctionRemote(ctx, oldName, newName)
}

// DeleteFunction deletes a function on the target central.
func (d *RoomFunctionAdminDomain) DeleteFunction(ctx context.Context, centralName, name string) error {
	u, err := d.resolve(centralName)
	if err != nil {
		return err
	}
	return u.HubModel.DeleteFunctionRemote(ctx, name)
}
