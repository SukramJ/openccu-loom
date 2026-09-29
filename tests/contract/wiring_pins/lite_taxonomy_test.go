// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
)

// findNode walks a litefake enum tree for path.
func findNode(nodes []litefake.Node, path []string) (litefake.Node, bool) {
	for _, n := range nodes {
		if n.ID != path[0] {
			continue
		}
		if len(path) == 1 {
			return n, true
		}
		return findNode(n.Children, path[1:])
	}
	return litefake.Node{}, false
}

// TestLiteTaxonomyNodeCRUD pins node editing on a lite central through the
// domain the REST handlers call: a node created below a parent lands in
// the box's tree with an id derived from its name, a rename and a move
// land there too, and deleting a node detaches its members instead of
// deleting them.
func TestLiteTaxonomyNodeCRUD(t *testing.T) {
	fake := startFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	ctx := context.Background()
	admin := adapter.NewTaxonomyAdmin(c.reg)

	path, err := admin.CreateNode(ctx, "box", "room", "eg", "Gäste WC")
	if err != nil || path != "eg/gaeste-wc" {
		t.Fatalf("CreateNode = %q, %v; want eg/gaeste-wc", path, err)
	}
	if _, ok := findNode(fake.Meta().Snapshot().Enums["room"].Tree, []string{"eg", "gaeste-wc"}); !ok {
		t.Fatal("the created node is not in the box's tree")
	}
	name := "Gästebad"
	if err := admin.UpdateNode(ctx, "box", "room", path, &name, nil, nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if n, _ := findNode(fake.Meta().Snapshot().Enums["room"].Tree, []string{"eg", "gaeste-wc"}); n.Name != name {
		t.Errorf("node name on the box = %q, want %q", n.Name, name)
	}
	og := "og"
	if err := admin.UpdateNode(ctx, "box", "room", path, nil, &og, nil); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, ok := findNode(fake.Meta().Snapshot().Enums["room"].Tree, []string{"og", "gaeste-wc"}); !ok {
		t.Error("the moved node is not below og on the box")
	}

	// room/eg/wohnzimmer carries the switch; deleting it keeps the object.
	if err := admin.DeleteNode(ctx, "box", "room", "eg/wohnzimmer"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	obj, ok := fake.Meta().Snapshot().Objects[liteSwitchRef]
	if !ok || slices.Contains(obj.Enums, "room/eg/wohnzimmer") {
		t.Errorf("after deleting its room the switch object is %+v (present %v); want it kept, detached", obj, ok)
	}
}

// TestRoomPathsWinOverRoomNames pins assignment by reference on a lite
// central: a room path names one of the two "Küche" nodes, which an
// assignment by name refuses as ambiguous.
func TestRoomPathsWinOverRoomNames(t *testing.T) {
	fake := startFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	d := adapter.NewDeviceAdminDomain(c.reg, client.NewValueWriter())
	if err := d.SetTaxonomyPaths(context.Background(), liteSwitchDevice+":1", "room", []string{"room/og/kueche"}); err != nil {
		t.Fatalf("SetTaxonomyPaths: %v", err)
	}
	got := slices.Sorted(slices.Values(fake.Meta().Snapshot().Objects[liteSwitchChRef].Enums))
	if want := []string{"function/licht", "room/og/kueche"}; !slices.Equal(got, want) {
		t.Errorf("store enums = %v, want %v", got, want)
	}
	dev := liteDevice(t, c.unit, liteSwitchDevice)
	waitFor(t, 10*time.Second, "the path assignment in the model", func() bool {
		for _, a := range dev.Channel(liteSwitchDevice + ":1").Taxonomy() {
			if a.Ref == (taxonomy.Ref{Enum: taxonomy.EnumRoom, Path: "og/kueche"}) {
				return true
			}
		}
		return false
	})
}
