// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
)

type stubTaxonomySource []CentralTaxonomy

func (s stubTaxonomySource) Taxonomies() []CentralTaxonomy { return s }

func liteTree() *taxonomy.Taxonomy {
	return &taxonomy.Taxonomy{Revision: 7, Enums: map[taxonomy.EnumID]*taxonomy.Enum{
		taxonomy.EnumRoom: {ID: taxonomy.EnumRoom, Names: map[string]string{"en": "Rooms"}, Roots: []*taxonomy.Node{
			{ID: "eg", Name: "Erdgeschoss", Children: []*taxonomy.Node{{ID: "kueche", Name: "Küche"}}},
			{ID: "og", Name: "Obergeschoss", Children: []*taxonomy.Node{{ID: "kueche", Name: "Küche"}}},
		}},
	}}
}

// TestTaxonomyEndpointShowsEmptyNodes pins GET /taxonomy: every node is
// listed with its path — also one nothing is assigned to, which is the
// endpoint's reason to exist — with the central's revision and flags; an
// unknown central is 404.
func TestTaxonomyEndpointShowsEmptyNodes(t *testing.T) {
	t.Parallel()
	src := stubTaxonomySource{{Central: "box", Taxonomy: liteTree(), Writable: true, Tree: true}}
	w := httptest.NewRecorder()
	GetTaxonomy(src).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/taxonomy", http.NoBody))
	var body TaxonomyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if len(body.Centrals) != 1 || body.Centrals[0].Revision != 7 || !body.Centrals[0].Tree || !body.Centrals[0].Writable {
		t.Fatalf("centrals = %+v", body.Centrals)
	}
	rooms := body.Centrals[0].Enums[0]
	if rooms.ID != "room" || len(rooms.Nodes) != 2 || rooms.Nodes[1].Children[0].Path != "og/kueche" {
		t.Errorf("room enum = %+v, want og/kueche under og", rooms)
	}

	w = httptest.NewRecorder()
	GetTaxonomy(src).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/taxonomy?central=ghost", http.NoBody))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown central: status %d, want 404", w.Code)
	}
}

// TestDeviceSummaryTaxonomyIsAdditive pins the taxonomy array on the
// device and channel summaries: each assignment with enum, path, name and
// parent path; and a device without assignments carries no taxonomy
// member at all, so its payload is unchanged.
func TestDeviceSummaryTaxonomyIsAdditive(t *testing.T) {
	t.Parallel()
	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.SetTaxonomy([]taxonomy.Assignment{{Ref: taxonomy.Ref{Enum: taxonomy.EnumRoom, Path: "eg/kueche"}, Name: "Küche"}})
	s := toDeviceSummary(d, "box", true)
	want := TaxonomyAssignment{Enum: "room", Path: "eg/kueche", Name: "Küche", ParentPath: "eg"}
	if len(s.Taxonomy) != 1 || s.Taxonomy[0] != want {
		t.Errorf("taxonomy = %+v, want [%+v]", s.Taxonomy, want)
	}

	plain, err := json.Marshal(toDeviceSummary(newTestDevice("0002ABCD", "HmIP-BSM"), "ccu", true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), `"taxonomy"`) {
		t.Errorf("a device without assignments carries a taxonomy member: %s", plain)
	}
}

// TestListRoomsCarriesNodeRefs pins the refs on GET /rooms: two rooms of
// one name are one entry whose refs name both nodes.
func TestListRoomsCarriesNodeRefs(t *testing.T) {
	t.Parallel()
	a := newTestDevice("0001ABCD", "HmIP-BSM")
	a.SetRooms([]string{"Küche"})
	a.SetTaxonomy([]taxonomy.Assignment{{Ref: taxonomy.Ref{Enum: taxonomy.EnumRoom, Path: "eg/kueche"}, Name: "Küche"}})
	b := newTestDevice("0002ABCD", "HmIP-BSM")
	b.SetRooms([]string{"Küche"})
	b.SetTaxonomy([]taxonomy.Assignment{{Ref: taxonomy.Ref{Enum: taxonomy.EnumRoom, Path: "og/kueche"}, Name: "Küche"}})
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": a, "0002ABCD": b}}
	w := httptest.NewRecorder()
	ListRooms(idx).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/rooms", http.NoBody))
	var body []RoomEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(body) != 1 || body[0].DeviceCount != 2 || len(body[0].Refs) != 2 ||
		body[0].Refs[0] != (NodeRef{Central: "ccu-01", Path: "eg/kueche", ParentPath: "eg"}) ||
		body[0].Refs[1].Path != "og/kueche" {
		t.Errorf("rooms = %+v", body)
	}
}
