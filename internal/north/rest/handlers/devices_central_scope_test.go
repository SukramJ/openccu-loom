// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// scopedDeviceIndex is a DeviceIndex with one model per central, so one
// address can be held by two centrals as two distinct devices, the way the
// virtual remotes, the INT000* devices and CUxD devices repeat across CCUs.
// Its lookups follow the production adapter: a bound context consults the
// bound central alone, an unbound one every central; Released combines every
// consulted central's hold.
type scopedDeviceIndex struct {
	models   map[string]map[string]*device.Device // central → address → device
	withheld map[string]map[string]bool           // central → addresses held back
}

func (s *scopedDeviceIndex) consulted(ctx context.Context) []string {
	if name, ok := hmreqctx.CentralScope(ctx); ok {
		if _, known := s.models[name]; known {
			return []string{name}
		}
		return nil
	}
	names := make([]string, 0, len(s.models))
	for name := range s.models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *scopedDeviceIndex) Devices() []*device.Device {
	var out []*device.Device
	for _, name := range s.consulted(context.Background()) {
		for _, d := range s.models[name] {
			out = append(out, d)
		}
	}
	return out
}

func (s *scopedDeviceIndex) Device(ctx context.Context, address string) (*device.Device, bool) {
	var found *device.Device
	holders := 0
	for _, name := range s.consulted(ctx) {
		if d, ok := s.models[name][address]; ok {
			found = d
			holders++
		}
	}
	return found, holders == 1
}

func (s *scopedDeviceIndex) CentralOf(address string) string {
	for _, name := range s.consulted(context.Background()) {
		if _, ok := s.models[name][address]; ok {
			return name
		}
	}
	return ""
}

func (*scopedDeviceIndex) SerialSuffix(string) string { return "" }

func (s *scopedDeviceIndex) Released(ctx context.Context, address string) bool {
	for _, name := range s.consulted(ctx) {
		if _, ok := s.models[name][address]; ok && s.withheld[name][address] {
			return false
		}
	}
	return true
}

// releasedOnAOnly holds the repeated address on ccu-a and ccu-b, released on
// ccu-a and still withheld on ccu-b, plus one device only ccu-b holds.
func releasedOnAOnly() *scopedDeviceIndex {
	return &scopedDeviceIndex{
		models: map[string]map[string]*device.Device{
			"ccu-a": {"HmIP-RCV-1": newTestDevice("HmIP-RCV-1", "HmIP-RCV-50")},
			"ccu-b": {
				"HmIP-RCV-1": newTestDevice("HmIP-RCV-1", "HmIP-RCV-50"),
				"0001ABCD":   newTestDevice("0001ABCD", "HmIP-BSM"),
			},
		},
		withheld: map[string]map[string]bool{"ccu-b": {"HmIP-RCV-1": true}},
	}
}

// TestGetDevice_ReleasedComesFromTheBoundCentral pins that a device read
// bound to a central answers `released` from that central's hold alone,
// while the unbound read keeps the combining rule (a hold anywhere wins).
func TestGetDevice_ReleasedComesFromTheBoundCentral(t *testing.T) {
	t.Parallel()
	idx := releasedOnAOnly()
	cases := []struct {
		central      string
		wantStatus   int
		wantReleased bool
	}{
		{"ccu-a", http.StatusOK, true},
		{"ccu-b", http.StatusOK, false},
		// Unbound, the repeated address resolves to nothing (ambiguous), so
		// the combining rule is pinned through the list below instead.
		{"", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		t.Run("central="+tc.central, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/HmIP-RCV-1", http.NoBody)
			ctx := chiContext(req, map[string]string{"addr": "HmIP-RCV-1"})
			// The device-route guard binds the request before the handler
			// runs (middleware.DeviceCentral); this is its effect.
			req = req.WithContext(hmreqctx.WithCentralScope(ctx, tc.central))
			w := httptest.NewRecorder()
			GetDevice(idx, nil).ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			if w.Code != http.StatusOK {
				return
			}
			var got DeviceDetail
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Released != tc.wantReleased || got.Central != tc.central {
				t.Errorf("released=%v central=%q, want released=%v central=%q",
					got.Released, got.Central, tc.wantReleased, tc.central)
			}
		})
	}
}

// TestListDevices_CentralFilterUsesTheScopedResolution pins the list half:
// with `?central=` the list is that central's devices, including one whose
// address the first central by name repeats, and each carries that central's
// `released`; the unbound list keeps the address-keyed answers.
func TestListDevices_CentralFilterUsesTheScopedResolution(t *testing.T) {
	t.Parallel()
	idx := releasedOnAOnly()
	cases := []struct {
		query string
		want  map[string]bool // address → released, for exactly these items
	}{
		{"?central=ccu-a", map[string]bool{"HmIP-RCV-1": true}},
		{"?central=ccu-b", map[string]bool{"HmIP-RCV-1": false, "0001ABCD": true}},
		{"?central=ccu-b&released_only=true", map[string]bool{"0001ABCD": true}},
		{"?central=ccu-z", map[string]bool{}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices"+tc.query, http.NoBody)
			w := httptest.NewRecorder()
			ListDevices(idx).ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
			}
			var body struct {
				Items []DeviceSummary `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := make(map[string]bool, len(body.Items))
			for _, it := range body.Items {
				got[it.Address] = it.Released
			}
			if len(got) != len(tc.want) || len(body.Items) != len(tc.want) {
				t.Fatalf("items = %+v, want %v", body.Items, tc.want)
			}
			for addr, rel := range tc.want {
				if r, ok := got[addr]; !ok || r != rel {
					t.Errorf("%s: released=%v present=%v, want released=%v", addr, r, ok, rel)
				}
			}
		})
	}

	t.Run("unbound keeps the combining rule", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
		w := httptest.NewRecorder()
		ListDevices(idx).ServeHTTP(w, req)
		var body struct {
			Items []DeviceSummary `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, it := range body.Items {
			if it.Address == "HmIP-RCV-1" && it.Released {
				t.Errorf("unbound HmIP-RCV-1 reads released; a hold on ccu-b must win: %+v", it)
			}
		}
	})
}
