// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestExportDefinitionEscapesRequestDeadline pins that the definition
// export runs without the router-wide request deadline: it fans one
// getParamsetDescription call per (channel, paramset) out to the CCU, so
// a large device serialises far more than 30 s of RPCs. Its sibling
// device route stays bounded.
func TestExportDefinitionEscapesRequestDeadline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		path         string
		wantDeadline bool
	}{
		{"export-definition", "/api/v1/devices/ABC123/export-definition", false},
		{"other-addr", "/api/v1/devices/0001D3C99B37F2/export-definition", false},
		{"device detail", "/api/v1/devices/ABC123", true},
		{"log stream", "/api/v1/diagnostics/logs/stream", false},
		{"eventbus tap", "/api/v1/diagnostics/eventbus/tap", false},
		{"snapshot", "/api/v1/snapshot", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var seen bool
			h := timeoutExceptStreaming(30 * time.Second)(http.HandlerFunc(
				func(_ http.ResponseWriter, r *http.Request) {
					_, seen = r.Context().Deadline()
				},
			))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, http.NoBody))
			if seen != tc.wantDeadline {
				t.Fatalf("%s: context deadline present = %v, want %v", tc.path, seen, tc.wantDeadline)
			}
		})
	}
}
