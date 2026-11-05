// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// TestListSchedulesNarrowsToOneDevice pins the `device` query parameter: a
// device page asks for its own schedules to learn whether it carries two
// (HmIP-WGT: heating and switching), and gets exactly those back.
func TestListSchedulesNarrowsToOneDevice(t *testing.T) {
	t.Parallel()
	svc := &stubScheduleService{listDevices: []hmapi.ScheduleDeviceSummary{
		{Central: "ccu", Address: "VCU4105035", Channel: hmapi.ScheduleChannelRef{Number: 7}, Kind: "week_profile"},
		{Central: "ccu", Address: "VCU4105035", Channel: hmapi.ScheduleChannelRef{Number: 8}, Kind: "climate"},
		{Central: "ccu", Address: "VCU0000001", Channel: hmapi.ScheduleChannelRef{Number: 1}, Kind: "climate"},
	}}

	cases := []struct {
		query string
		want  int
	}{
		{"", 3},
		{"?device=VCU4105035", 2},
		{"?device=VCU0000001", 1},
		{"?device=NOSUCHDEVICE", 0},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/schedules"+tc.query, http.NoBody)
		w := httptest.NewRecorder()
		ListSchedules(svc).ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%q: status %d body=%s", tc.query, w.Code, w.Body.String())
		}
		var out ListSchedulesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%q: decode: %v", tc.query, err)
		}
		if out.Items == nil {
			t.Errorf("%q: items is null, want an array", tc.query)
		}
		if len(out.Items) != tc.want {
			t.Errorf("%q: %d items, want %d: %+v", tc.query, len(out.Items), tc.want, out.Items)
		}
	}
}
