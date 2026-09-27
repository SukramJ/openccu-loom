// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// The bundled metadata fixture names the fake's BidCos-RF switch
// "Stehlampe" in room/eg/wohnzimmer and function/licht, and has two rooms
// called "Küche" (room/eg/kueche and room/og/kueche).
const (
	liteSwitchDevice = "VCU0000321"
	liteSwitchRef    = "BidCos-RF.VCU0000321"
	liteSwitchChRef  = "BidCos-RF.VCU0000321:1"
)

// startMetaCentral boots a lite central against a fake carrying the
// bundled metadata store and waits for it to be ready.
func startMetaCentral(t *testing.T, opts litefake.Options) (*litefake.Fake, *central.Unit) {
	t.Helper()
	if opts.Meta == nil {
		opts.Meta = litefake.DefaultMeta()
	}
	fake := startFake(t, opts)
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	return fake, unit
}

func liteDevice(t *testing.T, unit *central.Unit, address string) *device.Device {
	t.Helper()
	dev, ok := unit.ModelRegistry.Get(address)
	if !ok {
		t.Fatalf("%s is not in the model", address)
	}
	return dev
}

// TestLiteMetadataNamesStampedAtIngest pins that the box's metadata
// reaches the model at bring-up: the device carries its name, its room and
// function by the directly assigned node's display name, and the full
// node paths.
func TestLiteMetadataNamesStampedAtIngest(t *testing.T) {
	_, unit := startMetaCentral(t, litefake.Options{})
	dev := liteDevice(t, unit, liteSwitchDevice)
	if dev.Name() != "Stehlampe" {
		t.Errorf("device name = %q, want Stehlampe", dev.Name())
	}
	if ch := dev.Channel(liteSwitchDevice + ":1"); ch == nil || ch.Name() != "Stehlampe Schalter" {
		name := "<no channel>"
		if ch != nil {
			name = ch.Name()
		}
		t.Errorf("channel :1 name = %q, want Stehlampe Schalter", name)
	}
	if got := dev.Rooms(); !slices.Equal(got, []string{"Wohnzimmer"}) {
		t.Errorf("rooms = %v, want [Wohnzimmer] — only the directly assigned node, not its floor", got)
	}
	if got := dev.Functions(); !slices.Equal(got, []string{"Licht"}) {
		t.Errorf("functions = %v, want [Licht]", got)
	}
	want := []taxonomy.Ref{{Enum: taxonomy.EnumFunction, Path: "licht"}, {Enum: taxonomy.EnumRoom, Path: "eg/wohnzimmer"}}
	if got := dev.TaxonomyRefs(); !slices.Equal(got, want) {
		t.Errorf("taxonomy refs = %v, want %v", got, want)
	}
}

// TestLiteMetadataStreamRenamesLive pins the change stream: a rename made
// on the box reaches the running model and is published as a metadata
// change, without a restart.
func TestLiteMetadataStreamRenamesLive(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	changed := make(chan struct{}, 8)
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.DeviceMetadataChangedEvent) {
		if e.Address == liteSwitchDevice {
			changed <- struct{}{}
		}
	})
	t.Cleanup(unsub)
	if err := fake.Meta().Rename(liteSwitchRef, "Leselampe"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	dev := liteDevice(t, unit, liteSwitchDevice)
	waitFor(t, 10*time.Second, "the rename to reach the model", func() bool { return dev.Name() == "Leselampe" })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Error("the rename reached the model but no DeviceMetadataChangedEvent was published")
	}
}

// TestLiteMetaGapResnapshots pins the gap detection: when the box drops
// a revision from the change stream (a subscriber queue overflow), the
// mirror notices at the next revision and re-reads the snapshot, so the
// lost change still arrives.
func TestLiteMetaGapResnapshots(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	// Let the stream connect before the drop, so the dropped revision is
	// one the mirror would otherwise have received.
	time.Sleep(500 * time.Millisecond)
	fake.Deviate(litefake.DeviateMetaDropEvent, true)
	if err := fake.Meta().Rename(liteSwitchRef, "Verloren"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := fake.Meta().Rename(liteSwitchChRef, "Danach"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	dev := liteDevice(t, unit, liteSwitchDevice)
	ch := dev.Channel(liteSwitchDevice + ":1")
	waitFor(t, 10*time.Second, "the change after the gap", func() bool { return ch.Name() == "Danach" })
	waitFor(t, 5*time.Second, "the dropped rename via the re-read snapshot", func() bool { return dev.Name() == "Verloren" })
}

// TestLiteRenameWritesMetaObject pins the persistent rename on a lite
// central: the name lands in the box's store, not only in the model.
func TestLiteRenameWritesMetaObject(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	if err := unit.RenameDevice(context.Background(), liteSwitchDevice, "Bücherregal"); err != nil {
		t.Fatalf("RenameDevice: %v", err)
	}
	if got := fake.Meta().Snapshot().Objects[liteSwitchRef].Name; got != "Bücherregal" {
		t.Errorf("store name = %q, want Bücherregal", got)
	}
	if err := unit.RenameDeviceWithChannels(context.Background(), liteSwitchDevice, "Regal", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels: %v", err)
	}
	snap := fake.Meta().Snapshot()
	if got := snap.Objects[liteSwitchRef].Name; got != "Regal" {
		t.Errorf("store device name after the batch = %q, want Regal", got)
	}
	if got := snap.Objects[liteSwitchChRef].Name; got == "Stehlampe Schalter" {
		t.Errorf("the batch rename left the channel's store name at %q", got)
	}
}

// TestLiteSetRoomsKeepsOtherEnums pins the room write: the object's room
// paths are replaced, every other enum's paths survive.
func TestLiteSetRoomsKeepsOtherEnums(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	if err := unit.HubModel.SetDeviceRoomsRemote(context.Background(), liteSwitchDevice+":1", []string{"Erdgeschoss"}); err != nil {
		t.Fatalf("SetDeviceRoomsRemote: %v", err)
	}
	obj := fake.Meta().Snapshot().Objects[liteSwitchChRef]
	got := slices.Sorted(slices.Values(obj.Enums))
	if want := []string{"function/licht", "room/eg"}; !slices.Equal(got, want) {
		t.Errorf("store enums = %v, want %v — the function assignment must survive a room write", got, want)
	}
}

// TestLiteSetRoomsAmbiguousNameIsConflict pins that a room name several
// nodes carry is refused with the candidates, not resolved to one of
// them.
func TestLiteSetRoomsAmbiguousNameIsConflict(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	before := fake.Meta().Revision()
	err := unit.HubModel.SetDeviceRoomsRemote(context.Background(), liteSwitchDevice, []string{"Küche"})
	amb, ok := errors.AsType[*taxonomy.AmbiguousNameError](err)
	if !ok {
		t.Fatalf("err = %v, want *taxonomy.AmbiguousNameError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Errorf("candidates = %v, want room/eg/kueche and room/og/kueche", amb.Candidates)
	}
	if after := fake.Meta().Revision(); after != before {
		t.Errorf("the store moved from revision %d to %d on a refused write", before, after)
	}
}

// TestLiteMetadataWithoutMetaReadScopeIsAbsentNotFatal pins that a token
// without meta:read brings the central up without names, with the
// taxonomy feature reported missing and why.
func TestLiteMetadataWithoutMetaReadScopeIsAbsentNotFatal(t *testing.T) {
	fake := startFake(t, litefake.Options{
		Meta:   litefake.DefaultMeta(),
		Tokens: map[string][]string{litefake.DefaultToken: {"rpc:read"}},
	})
	unit := startLiteCentral(t, fake, litefake.DefaultToken)
	waitLiteReady(t, unit)
	if dev := liteDevice(t, unit, liteSwitchDevice); dev.Name() == "Stehlampe" {
		t.Error("the device carries the store's name although the token cannot read it")
	}
	st := unit.Features().State(hmenum.FeatureTaxonomyRead)
	if st.Available || st.Reason != hmenum.FeatureReasonMissingScope || st.Scope != "meta:read" {
		t.Errorf("taxonomy.read = %+v, want missing_scope meta:read", st)
	}
}

// TestLiteNodeRenameReachesAssignments pins that a node renamed on the box
// reaches the devices assigned to it: the tree change re-reads the
// snapshot, the assignment's stamped name follows, and the room names the
// north-bound surfaces read change with it.
func TestLiteNodeRenameReachesAssignments(t *testing.T) {
	fake, unit := startMetaCentral(t, litefake.Options{})
	dev := liteDevice(t, unit, liteSwitchDevice)
	if err := fake.Meta().RenameNode("room", "eg/wohnzimmer", "Wohnraum"); err != nil {
		t.Fatalf("RenameNode: %v", err)
	}
	waitFor(t, 10*time.Second, "the renamed node on the device", func() bool {
		for _, a := range dev.Taxonomy() {
			if a.Ref.String() == "room/eg/wohnzimmer" && a.Name == "Wohnraum" {
				return true
			}
		}
		return false
	})
	if got := dev.Rooms(); !slices.Equal(got, []string{"Wohnraum"}) {
		t.Errorf("rooms = %v, want [Wohnraum]", got)
	}
}
