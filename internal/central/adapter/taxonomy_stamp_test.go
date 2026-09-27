// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestHubBringUpStampsTheFlatTaxonomy pins the bring-up path of a CCU: the
// cache WireHub populates carries the flat taxonomy and the member refs next
// to the room and function names, and the names are what they were without
// the taxonomy.
func TestHubBringUpStampsTheFlatTaxonomy(t *testing.T) {
	t.Parallel()
	ise := IseAddressMap{"100": "DEV1", "101": "DEV1:1", "102": "DEV1:2"}
	roomEntries := []roomEntry{{ID: "50", Name: "Küche", ChannelIDs: []string{"101"}}}
	fnEntries := []subsectionEntry{{ID: "70", Name: "Licht", ChannelIDs: []string{"101", "102"}}}
	rooms := buildAssignments(roomEntries, ise, func(r roomEntry) (string, []string) { return r.Name, r.ChannelIDs })
	functions := buildAssignments(fnEntries, ise, func(s subsectionEntry) (string, []string) { return s.Name, s.ChannelIDs })
	enums := devicedetails.CCUFlatEnums(
		[]devicedetails.FlatEntry{{ID: "50", Name: "Küche", MemberIDs: []string{"101"}}},
		[]devicedetails.FlatEntry{{ID: "70", Name: "Licht", MemberIDs: []string{"101", "102"}}},
	)

	withTax := devicedetails.New()
	populateDeviceDetailsCache(withTax, NameMap{"DEV1": "Herd"}, ise, rooms, functions, enums)
	without := devicedetails.New()
	populateDeviceDetailsCache(without, NameMap{"DEV1": "Herd"}, ise, rooms, functions, nil)

	for _, addr := range []string{"DEV1", "DEV1:1", "DEV1:2"} {
		if !slices.Equal(withTax.GetChannelRooms(addr), without.GetChannelRooms(addr)) ||
			!slices.Equal(withTax.GetFunctions(addr), without.GetFunctions(addr)) ||
			withTax.GetName(addr) != without.GetName(addr) {
			t.Errorf("%s: the taxonomy changed a name reader", addr)
		}
	}
	if got := withTax.GetDeviceRooms("DEV1"); !slices.Equal(got, without.GetDeviceRooms("DEV1")) {
		t.Errorf("device rooms changed: %v", got)
	}
	if got := refStrs(withTax.Refs("DEV1:1")); !slices.Equal(got, []string{"function/70", "room/50"}) {
		t.Errorf("Refs(DEV1:1) = %v", got)
	}
	if got := refStrs(withTax.DeviceRefs("DEV1")); !slices.Equal(got, []string{"function/70", "room/50"}) {
		t.Errorf("DeviceRefs(DEV1) = %v", got)
	}
	if n, ok := withTax.Taxonomy().Node(taxonomy.Root(taxonomy.EnumRoom, "50")); !ok || n.Name != "Küche" {
		t.Errorf("room node 50 = %+v, %v", n, ok)
	}
}

// TestIngestAndRestampCarryTaxonomyRefs pins that the model carries what the
// cache holds: the pipeline stamps a channel's direct refs and a device's
// aggregate at ingest, and the periodic restamp follows a change.
func TestIngestAndRestampCarryTaxonomyRefs(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-tax"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.DeviceDetails.AddRef("0001TAX:1", taxonomy.Root(taxonomy.EnumRoom, "50"))

	p := NewDevicePipeline(c)
	b := &paramsetFakeOps{
		listDevicesFn: func(context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "0001TAX", Type: "HmIP-PS"},
				{Address: "0001TAX:1", Parent: "0001TAX", Type: "SWITCH"},
			}, nil
		},
		getParamsetDescriptionFn: func(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			return nil, nil
		},
		getParamsetFn: func(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
	if err := p.IngestFromBackend(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, b, &fakeWriter{}, nil, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	dev, ok := c.ModelRegistry.Get("0001TAX")
	if !ok {
		t.Fatal("device missing after ingest")
	}
	if got := refStrs(dev.TaxonomyRefs()); !slices.Equal(got, []string{"room/50"}) {
		t.Errorf("device refs at ingest = %v, want [room/50]", got)
	}
	if got := refStrs(dev.Channel("0001TAX:1").TaxonomyRefs()); !slices.Equal(got, []string{"room/50"}) {
		t.Errorf("channel refs at ingest = %v, want [room/50]", got)
	}

	if n := restampDeviceDetails(c, nil); n != 0 {
		t.Fatalf("restamp of an unchanged cache changed %d device(s)", n)
	}
	c.DeviceDetails.AddRef("0001TAX:1", taxonomy.Root(taxonomy.EnumFunction, "70"))
	if n := restampDeviceDetails(c, nil); n != 1 {
		t.Fatalf("restamp after a new ref changed %d device(s), want 1", n)
	}
	if got := refStrs(dev.Channel("0001TAX:1").TaxonomyRefs()); !slices.Equal(got, []string{"function/70", "room/50"}) {
		t.Errorf("channel refs after restamp = %v", got)
	}
	if got := refStrs(dev.TaxonomyRefs()); !slices.Equal(got, []string{"function/70", "room/50"}) {
		t.Errorf("device refs after restamp = %v", got)
	}
}

func refStrs(refs []taxonomy.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.String())
	}
	return out
}
