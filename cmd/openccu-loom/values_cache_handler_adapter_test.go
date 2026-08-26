// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// TestValuesCacheRoutesReportUnreadyWithoutStore pins the disabled-cache path
// through the router the composition root mounts: with
// `persistence.values_cache.enabled: false` the store is nil, and the
// values-cache endpoints must answer 503 service_unready.
//
// The trap this guards is boxing, not the guard itself. Both the mount
// condition (`d.ValuesCache != nil`) and the handler's own nil check compare an
// INTERFACE against nil, so a constructor that hands back a typed nil pointer
// satisfies neither: the routes mount, the handler proceeds, and the first
// method call dereferences the nil receiver — a panic per request, recovered
// into a 500, on documented configuration.
func TestValuesCacheRoutesReportUnreadyWithoutStore(t *testing.T) {
	t.Parallel()

	router := rest.NewRouter(rest.Deps{
		ValuesCache:  newValuesCacheHandlerAdapter(nil),
		DeviceLookup: newDeviceLookupAdapter(nil),
	})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/values-cache/stats"},
		{http.MethodPost, "/api/v1/admin/values-cache/reset"},
		{http.MethodPost, "/api/v1/devices/00021BE9957782/values-cache/reset"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, http.NoBody))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: got %d, want 503 service_unready, body %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
