// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// liteMetaWriter persists names and room / function assignments of an
// openccu-lite central in the box's metadata store. The store echoes each
// write on its change stream, and the mirror restamps the model from it;
// the callers stamp the model eagerly as they do for a CCU, so the echo
// finds nothing left to change.
type liteMetaWriter struct {
	client *occulited.Client
	unit   *central.Unit
}

// wire installs the writer on the central: the rename hooks and the
// hub's assignment mutators.
func (w *liteMetaWriter) wire() {
	w.unit.SetRenameDeviceFn(w.rename)
	w.unit.SetRenameDeviceBatchFn(w.renameBatch)
	if w.unit.HubModel != nil {
		w.unit.HubModel.SetAssignmentMutators(w, w)
	}
}

// objectRef is the store's key for an address: "<interface>.<address>".
// The interface comes from the model; an address the central does not
// know has no ref.
func (w *liteMetaWriter) objectRef(address string) (string, error) {
	if w.unit.ModelRegistry != nil {
		if dev, ok := w.unit.ModelRegistry.Get(hmtypes.DeviceAddress(address)); ok && dev != nil && dev.Interface != "" {
			return string(dev.Interface) + "." + address, nil
		}
	}
	if w.unit.DeviceDetails != nil {
		for _, a := range []string{address, hmtypes.DeviceAddress(address)} {
			if iface, ok := w.unit.DeviceDetails.GetInterface(a); ok {
				return string(iface) + "." + address, nil
			}
		}
	}
	return "", fmt.Errorf("openccu-lite metadata: %s is not a known device or channel", address)
}

// rename sets one object's name; a PATCH with a name creates an object
// the store does not hold yet.
func (w *liteMetaWriter) rename(ctx context.Context, address, name string) error {
	ref, err := w.objectRef(address)
	if err != nil {
		return err
	}
	if _, err := w.client.PatchObject(ctx, ref, occulited.ObjectPatch{Name: &name}, occulited.WriteOptions{}); err != nil {
		return fmt.Errorf("openccu-lite rename %s: %w", address, err)
	}
	return nil
}

// renameBatch renames a device and its channels under one store revision.
func (w *liteMetaWriter) renameBatch(ctx context.Context, rename central.DeviceRename) error {
	set := make(map[string]occulited.ObjectPatch, len(rename.Channels)+1)
	for _, r := range append([]central.Rename{rename.Device}, rename.Channels...) {
		ref, err := w.objectRef(r.Address)
		if err != nil {
			return err
		}
		name := r.Name
		set[ref] = occulited.ObjectPatch{Name: &name}
	}
	if _, err := w.client.Bulk(ctx, occulited.BulkRequest{Set: set}, occulited.WriteOptions{}); err != nil {
		return fmt.Errorf("openccu-lite rename %s: %w", rename.Device.Address, err)
	}
	return nil
}

// SetDeviceRooms implements [hub.RoomMutator].
func (w *liteMetaWriter) SetDeviceRooms(ctx context.Context, address string, rooms []string) error {
	return w.assign(ctx, address, taxonomy.EnumRoom, rooms, hub.ErrRoomNotFound)
}

// SetDeviceFunctions implements [hub.FunctionMutator].
func (w *liteMetaWriter) SetDeviceFunctions(ctx context.Context, address string, functions []string) error {
	return w.assign(ctx, address, taxonomy.EnumFunction, functions, hub.ErrFunctionNotFound)
}

// liteAssignAttempts bounds the read-modify-write of an assignment: one
// retry after a revision conflict, then the conflict is the answer.
const liteAssignAttempts = 2

// assign replaces the address's nodes of one enum with the nodes named
// names and keeps every other enum's paths (functions, favourites,
// floors) as they are. Names resolve against the mirrored taxonomy; a name
// no node carries is notFound, a name several nodes carry is a
// [*taxonomy.AmbiguousNameError] listing them.
//
// The write is based on the object's revision (If-Match). Someone editing
// the same store in between turns it into a conflict; it is retried once
// on a fresh read, and a second conflict is returned.
func (w *liteMetaWriter) assign(ctx context.Context, address string, enum taxonomy.EnumID, names []string, notFound error) error {
	wanted, err := w.resolve(enum, names, notFound)
	if err != nil {
		return err
	}
	ref, err := w.objectRef(address)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err = w.assignOnce(ctx, ref, address, enum, wanted)
		if !errors.Is(err, occulited.ErrRevisionConflict) || attempt >= liteAssignAttempts {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("openccu-lite %s assignment of %s: %w", enum, address, err)
	}
	return nil
}

func (w *liteMetaWriter) assignOnce(ctx context.Context, ref, address string, enum taxonomy.EnumID, wanted []string) error {
	ans, err := w.client.Object(ctx, ref)
	switch {
	case errors.Is(err, occulited.ErrUnknownObject):
		// The store holds only named objects; a PATCH carrying the
		// current display name creates this one.
		name := w.displayName(address)
		enums := wanted
		_, err = w.client.PatchObject(ctx, ref, occulited.ObjectPatch{Name: &name, Enums: &enums}, occulited.WriteOptions{})
		return err
	case err != nil:
		return err
	}
	enums := make([]string, 0, len(ans.Object.Enums)+len(wanted))
	for _, p := range ans.Object.Enums {
		if r, perr := taxonomy.ParseRef(p); perr == nil && r.Enum == enum {
			continue
		}
		enums = append(enums, p)
	}
	enums = append(enums, wanted...)
	rev := ans.Revision
	_, err = w.client.PatchObject(ctx, ref, occulited.ObjectPatch{Enums: &enums}, occulited.WriteOptions{IfMatch: &rev})
	return err
}

// resolve maps display names to full node paths of enum.
func (w *liteMetaWriter) resolve(enum taxonomy.EnumID, names []string, notFound error) ([]string, error) {
	var tax *taxonomy.Taxonomy
	if w.unit.DeviceDetails != nil {
		tax = w.unit.DeviceDetails.Taxonomy()
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		refs := tax.FindByName(enum, name)
		switch len(refs) {
		case 0:
			return nil, fmt.Errorf("%w: %q", notFound, name)
		case 1:
			out = append(out, refs[0].String())
		default:
			return nil, &taxonomy.AmbiguousNameError{Enum: enum, Name: name, Candidates: refs}
		}
	}
	return out, nil
}

// displayName is the name the model shows for address, or the address.
func (w *liteMetaWriter) displayName(address string) string {
	if w.unit.ModelRegistry != nil {
		if dev, ok := w.unit.ModelRegistry.Get(hmtypes.DeviceAddress(address)); ok && dev != nil {
			if address == dev.Address {
				if n := dev.Name(); n != "" {
					return n
				}
			} else if ch := dev.Channel(address); ch != nil && ch.Name() != "" {
				return ch.Name()
			}
		}
	}
	return address
}
