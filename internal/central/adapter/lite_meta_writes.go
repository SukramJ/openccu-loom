// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
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

// wire installs the rename hooks on the central; the room and function
// writes reach the hub through [liteHubWriter].
func (w *liteMetaWriter) wire() {
	w.unit.SetRenameDeviceFn(w.rename)
	w.unit.SetRenameDeviceBatchFn(w.renameBatch)
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

// require refuses a write whose feature the token does not grant, before
// anything reaches the box.
func (w *liteMetaWriter) require(k hmenum.Feature) error {
	return w.unit.Features().Require(w.unit.Name(), k, nil)
}

// rename sets one object's name; a PATCH with a name creates an object
// the store does not hold yet.
func (w *liteMetaWriter) rename(ctx context.Context, address, name string) error {
	if err := w.require(hmenum.FeatureDeviceRename); err != nil {
		return err
	}
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
	if err := w.require(hmenum.FeatureDeviceRename); err != nil {
		return err
	}
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
	if err := w.require(hmenum.FeatureTaxonomyAssign); err != nil {
		return err
	}
	wanted, err := w.resolve(enum, names, notFound)
	if err != nil {
		return err
	}
	return w.assignPaths(ctx, address, enum, wanted)
}

// SetTaxonomyRefs implements [hub.TaxonomyPathMutator]: the address's
// nodes of enum become refs, every other enum's stay.
func (w *liteMetaWriter) SetTaxonomyRefs(ctx context.Context, address string, enum taxonomy.EnumID, refs []taxonomy.Ref) error {
	if err := w.require(hmenum.FeatureTaxonomyAssign); err != nil {
		return err
	}
	tax := w.taxonomy()
	wanted := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.Enum != enum {
			return fmt.Errorf("%w: %s is not a node of %s", hmerr.ErrValidation, r, enum)
		}
		if _, ok := tax.Node(r); !ok {
			return fmt.Errorf("%w: no node %s", hmerr.ErrValidation, r)
		}
		wanted = append(wanted, r.String())
	}
	return w.assignPaths(ctx, address, enum, wanted)
}

// assignPaths writes the address's node paths of enum (full paths,
// "room/eg/kueche"), keeping the other enums'.
func (w *liteMetaWriter) assignPaths(ctx context.Context, address string, enum taxonomy.EnumID, wanted []string) error {
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

// taxonomy is the mirrored taxonomy, or nil before it is loaded.
func (w *liteMetaWriter) taxonomy() *taxonomy.Taxonomy {
	if w.unit.DeviceDetails == nil {
		return nil
	}
	return w.unit.DeviceDetails.Taxonomy()
}

// resolve maps display names to full node paths of enum.
func (w *liteMetaWriter) resolve(enum taxonomy.EnumID, names []string, notFound error) ([]string, error) {
	tax := w.taxonomy()
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

// requireEdit refuses a node change the token cannot make: every change
// needs the edit feature, one that nests nodes the tree feature too.
func (w *liteMetaWriter) requireEdit(nested bool) error {
	if err := w.require(hmenum.FeatureTaxonomyEdit); err != nil {
		return err
	}
	if nested {
		return w.require(hmenum.FeatureTaxonomyTree)
	}
	return nil
}

// CreateNode implements [hub.TaxonomyAdmin]. The node id is derived from
// the name (see [liteNodeID]) and made unique among its siblings.
func (w *liteMetaWriter) CreateNode(ctx context.Context, enum taxonomy.EnumID, parent taxonomy.Path, name string) (taxonomy.Ref, error) {
	if err := w.requireEdit(parent != ""); err != nil {
		return taxonomy.Ref{}, err
	}
	siblings := map[string]bool{}
	tax := w.taxonomy()
	if parent == "" {
		if tax != nil && tax.Enums[enum] != nil {
			for _, n := range tax.Enums[enum].Roots {
				siblings[n.ID] = true
			}
		}
	} else {
		p, ok := tax.Node(taxonomy.Ref{Enum: enum, Path: parent})
		if !ok {
			return taxonomy.Ref{}, fmt.Errorf("%w: no node %s/%s", hmerr.ErrValidation, enum, parent)
		}
		for _, n := range p.Children {
			siblings[n.ID] = true
		}
	}
	id := liteNodeID(name, siblings)
	nc := occulited.NodeCreate{ID: id, Name: name}
	ref := taxonomy.Root(enum, id)
	if parent != "" {
		full := string(enum) + "/" + string(parent)
		nc.Parent = &full
		ref = taxonomy.Ref{Enum: enum, Path: parent}.Child(id)
	}
	if _, err := w.client.CreateNode(ctx, string(enum), nc, occulited.WriteOptions{}); err != nil {
		return taxonomy.Ref{}, fmt.Errorf("openccu-lite create %s node: %w", enum, err)
	}
	return ref, nil
}

// RenameNode implements [hub.TaxonomyAdmin].
func (w *liteMetaWriter) RenameNode(ctx context.Context, r taxonomy.Ref, name string) error {
	if err := w.requireEdit(false); err != nil {
		return err
	}
	if _, err := w.client.PatchNode(ctx, string(r.Enum), string(r.Path), occulited.NodePatch{Name: &name}, occulited.WriteOptions{}); err != nil {
		return fmt.Errorf("openccu-lite rename node %s: %w", r, err)
	}
	return nil
}

// MoveNode implements [hub.TaxonomyAdmin]; parent "" moves to the root.
func (w *liteMetaWriter) MoveNode(ctx context.Context, r taxonomy.Ref, parent taxonomy.Path, position *int) error {
	if err := w.requireEdit(true); err != nil {
		return err
	}
	patch := occulited.NodePatch{Move: true, Position: position}
	if parent != "" {
		patch.Parent = string(r.Enum) + "/" + string(parent)
	}
	if _, err := w.client.PatchNode(ctx, string(r.Enum), string(r.Path), patch, occulited.WriteOptions{}); err != nil {
		return fmt.Errorf("openccu-lite move node %s: %w", r, err)
	}
	return nil
}

// DeleteNode implements [hub.TaxonomyAdmin]. The node's members are
// detached, not deleted: deleting a room keeps its devices, as on a CCU.
func (w *liteMetaWriter) DeleteNode(ctx context.Context, r taxonomy.Ref) error {
	if err := w.requireEdit(false); err != nil {
		return err
	}
	if _, err := w.client.DeleteNode(ctx, string(r.Enum), string(r.Path), true, occulited.WriteOptions{}); err != nil {
		return fmt.Errorf("openccu-lite delete node %s: %w", r, err)
	}
	return nil
}

// liteNodeIDMax is the longest node id the box accepts.
const liteNodeIDMax = 32

// liteNodeID derives a node id from a display name — lower case, German
// umlauts and ß spelled out, every other run of characters outside
// [a-z0-9] one hyphen, at most 32 characters — and appends -2, -3, …
// until it is not taken among the siblings.
func liteNodeID(name string, taken map[string]bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		var out string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = string(r)
		case r == 'ä':
			out = "ae"
		case r == 'ö':
			out = "oe"
		case r == 'ü':
			out = "ue"
		case r == 'ß':
			out = "ss"
		default:
			dash = b.Len() > 0
			continue
		}
		if dash {
			b.WriteByte('-')
			dash = false
		}
		b.WriteString(out)
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "node"
	}
	if len(base) > liteNodeIDMax {
		base = strings.TrimRight(base[:liteNodeIDMax], "-")
	}
	id := base
	for n := 2; taken[id]; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > liteNodeIDMax {
			stem = strings.TrimRight(stem[:liteNodeIDMax-len(suffix)], "-")
		}
		id = stem + suffix
	}
	return id
}
