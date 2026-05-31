// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newJSONRPCFake spins up an httptest.Server that dispatches by JSON-RPC
// method name. Each handler receives the raw params map and returns a value
// that is JSON-marshalled into the "result" field.
func newJSONRPCFake(t *testing.T, routes map[string]func(params map[string]any) any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		handler, ok := routes[env.Method]
		if !ok {
			http.Error(w, "unknown method: "+env.Method, http.StatusNotFound)
			return
		}
		result := handler(env.Params)
		raw, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":`))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte(`}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newJSONRPCClient creates a jsonrpc.Client pointed at the given URL.
func newJSONRPCClient(t *testing.T, url string) *jsonrpc.Client {
	t.Helper()
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: url})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	return jc
}

// ---------------------------------------------------------------------------
// TestBuildAssignmentsBasic
// ---------------------------------------------------------------------------

// entry is a minimal stand-in used by the table tests below.
type testEntry struct {
	name       string
	channelIDs []string
}

func decodeTestEntry(e testEntry) (name string, channelIDs []string) { return e.name, e.channelIDs }

func TestBuildAssignmentsBasic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		entries      []testEntry
		iseToAddress IseAddressMap
		want         AssignmentMap
	}{
		{
			name: "single room one channel stamps channel and device",
			entries: []testEntry{
				{name: "Wohnzimmer", channelIDs: []string{"101"}},
			},
			iseToAddress: IseAddressMap{"101": "ABC123:1"},
			want: AssignmentMap{
				"ABC123:1": {"Wohnzimmer"},
				"ABC123":   {"Wohnzimmer"},
			},
		},
		{
			name: "single function two channels same device deduplicates device entry",
			entries: []testEntry{
				{name: "Heizung", channelIDs: []string{"201", "202"}},
			},
			iseToAddress: IseAddressMap{
				"201": "DEV001:1",
				"202": "DEV001:2",
			},
			want: AssignmentMap{
				"DEV001:1": {"Heizung"},
				"DEV001:2": {"Heizung"},
				"DEV001":   {"Heizung"}, // stamped once, not twice
			},
		},
		{
			name: "unknown ISE-ID is silently skipped",
			entries: []testEntry{
				{name: "Keller", channelIDs: []string{"999"}},
			},
			iseToAddress: IseAddressMap{}, // 999 not present
			want:         AssignmentMap{},
		},
		{
			name: "empty entry name skips entire entry",
			entries: []testEntry{
				{name: "", channelIDs: []string{"301"}},
			},
			iseToAddress: IseAddressMap{"301": "XYZ:1"},
			want:         AssignmentMap{},
		},
		{
			name: "channel in two rooms gets both names sorted",
			entries: []testEntry{
				{name: "Zebra", channelIDs: []string{"401"}},
				{name: "Alpha", channelIDs: []string{"401"}},
			},
			iseToAddress: IseAddressMap{"401": "QRS:3"},
			want: AssignmentMap{
				"QRS:3": {"Alpha", "Zebra"}, // alphabetical
				"QRS":   {"Alpha", "Zebra"},
			},
		},
		{
			name: "multi-name slices are sorted alphabetically",
			entries: []testEntry{
				{name: "Mango", channelIDs: []string{"501"}},
				{name: "Apple", channelIDs: []string{"501"}},
				{name: "Banana", channelIDs: []string{"501"}},
			},
			iseToAddress: IseAddressMap{"501": "DEV002:1"},
			want: AssignmentMap{
				"DEV002:1": {"Apple", "Banana", "Mango"},
				"DEV002":   {"Apple", "Banana", "Mango"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildAssignments(tc.entries, tc.iseToAddress, decodeTestEntry)
			if len(got) != len(tc.want) {
				t.Fatalf("len(AssignmentMap)=%d, want %d: %v", len(got), len(tc.want), got)
			}
			for addr, wantNames := range tc.want {
				gotNames, ok := got[addr]
				if !ok {
					t.Errorf("missing key %q in result", addr)
					continue
				}
				if !slices.Equal(gotNames, wantNames) {
					t.Errorf("addr %q: got %v, want %v", addr, gotNames, wantNames)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestLoadRoomAssignmentsViaHTTPFake
// ---------------------------------------------------------------------------

func TestLoadRoomAssignmentsViaHTTPFake(t *testing.T) {
	t.Parallel()

	iseMap := IseAddressMap{
		"11": "HEQ0001:1",
		"12": "HEQ0001:2",
		"13": "HEQ0002:1",
	}

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"Room.getAll": func(_ map[string]any) any {
			return []map[string]any{
				{"id": "r1", "name": "Wohnzimmer", "channelIds": []string{"11", "12"}},
				{"id": "r2", "name": "Schlafzimmer", "channelIds": []string{"13"}},
			}
		},
	})
	jc := newJSONRPCClient(t, srv.URL)

	got, err := loadRoomAssignments(context.Background(), jc, iseMap)
	if err != nil {
		t.Fatalf("loadRoomAssignments: %v", err)
	}

	checks := map[string][]string{
		"HEQ0001:1": {"Wohnzimmer"},
		"HEQ0001:2": {"Wohnzimmer"},
		"HEQ0001":   {"Wohnzimmer"},
		"HEQ0002:1": {"Schlafzimmer"},
		"HEQ0002":   {"Schlafzimmer"},
	}
	for addr, want := range checks {
		gotNames, ok := got[addr]
		if !ok {
			t.Errorf("missing addr %q", addr)
			continue
		}
		if !slices.Equal(gotNames, want) {
			t.Errorf("addr %q: got %v, want %v", addr, gotNames, want)
		}
	}
	if len(got) != len(checks) {
		t.Errorf("AssignmentMap has %d entries, want %d: %v", len(got), len(checks), got)
	}
}

// ---------------------------------------------------------------------------
// TestLoadFunctionAssignmentsViaHTTPFake
// ---------------------------------------------------------------------------

func TestLoadFunctionAssignmentsViaHTTPFake(t *testing.T) {
	t.Parallel()

	iseMap := IseAddressMap{
		"21": "HEQ0010:1",
		"22": "HEQ0011:1",
	}

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"Subsection.getAll": func(_ map[string]any) any {
			return []map[string]any{
				{"id": "s1", "name": "Licht", "channelIds": []string{"21", "22"}},
			}
		},
	})
	jc := newJSONRPCClient(t, srv.URL)

	got, err := loadFunctionAssignments(context.Background(), jc, iseMap)
	if err != nil {
		t.Fatalf("loadFunctionAssignments: %v", err)
	}

	checks := map[string][]string{
		"HEQ0010:1": {"Licht"},
		"HEQ0010":   {"Licht"},
		"HEQ0011:1": {"Licht"},
		"HEQ0011":   {"Licht"},
	}
	for addr, want := range checks {
		gotNames, ok := got[addr]
		if !ok {
			t.Errorf("missing addr %q", addr)
			continue
		}
		if !slices.Equal(gotNames, want) {
			t.Errorf("addr %q: got %v, want %v", addr, gotNames, want)
		}
	}
	if len(got) != len(checks) {
		t.Errorf("AssignmentMap has %d entries, want %d: %v", len(got), len(checks), got)
	}
}

// ---------------------------------------------------------------------------
// TestLoadDeviceNamesReturnsBothMaps
// ---------------------------------------------------------------------------

func TestLoadDeviceNamesReturnsBothMaps(t *testing.T) {
	t.Parallel()

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"Device.listAllDetail": func(_ map[string]any) any {
			return []map[string]any{
				{
					"address": "HEQ0100",
					"name":    "Thermostat",
					"id":      "1001",
					"channels": []map[string]any{
						{"address": "HEQ0100:1", "name": "Kanal 1", "id": "1002"},
						{"address": "HEQ0100:2", "name": "Kanal 2", "id": "1003"},
					},
				},
				{
					"address": "HEQ0200",
					"name":    "Lampe",
					"id":      "2001",
					"channels": []map[string]any{
						{"address": "HEQ0200:1", "name": "Lichtkanal", "id": "2002"},
					},
				},
			}
		},
	})
	jc := newJSONRPCClient(t, srv.URL)

	names, iseMap, err := loadDeviceNames(context.Background(), jc)
	if err != nil {
		t.Fatalf("loadDeviceNames: %v", err)
	}

	// --- NameMap assertions ---
	wantNames := NameMap{
		"HEQ0100":   "Thermostat",
		"HEQ0100:1": "Kanal 1",
		"HEQ0100:2": "Kanal 2",
		"HEQ0200":   "Lampe",
		"HEQ0200:1": "Lichtkanal",
	}
	if len(names) != len(wantNames) {
		t.Errorf("NameMap len=%d, want %d", len(names), len(wantNames))
	}
	for addr, want := range wantNames {
		if got := names[addr]; got != want {
			t.Errorf("names[%q]=%q, want %q", addr, got, want)
		}
	}

	// --- IseAddressMap assertions ---
	wantIse := IseAddressMap{
		"1001": "HEQ0100",
		"1002": "HEQ0100:1",
		"1003": "HEQ0100:2",
		"2001": "HEQ0200",
		"2002": "HEQ0200:1",
	}
	if len(iseMap) != len(wantIse) {
		t.Errorf("IseAddressMap len=%d, want %d", len(iseMap), len(wantIse))
	}
	for ise, want := range wantIse {
		if got := iseMap[ise]; got != want {
			t.Errorf("iseMap[%q]=%q, want %q", ise, got, want)
		}
	}
}
