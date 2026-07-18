// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// paramEcho mounts a route tree mirroring the shapes the real router
// uses (top-level params, nested subrouters, multi-param routes) and
// echoes the URL params a handler observes.
func paramEcho(t *testing.T) (mux *chi.Mux, seenParams map[string]string) {
	t.Helper()
	seen := map[string]string{}
	record := func(names ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			for _, n := range names {
				seen[n] = chi.URLParam(r, n)
			}
		}
	}
	r := chi.NewRouter()
	r.Use(decodedPathRouting)
	r.Route("/api/v1", func(pr chi.Router) {
		pr.Put("/devices/{addr}/link-ps/{peer}", record("addr", "peer"))
		pr.Post("/alarm/outputs/{id}/test", record("id"))
		pr.Post("/devices/{addr}/cdps/{name}/{operation}", record("addr", "name", "operation"))
		pr.Put("/rooms/{room}", record("room"))
	})
	return r, seen
}

// TestDecodedPathRouting_URLParamsArriveDecoded pins the central
// contract: a conformant client percent-encodes path segments
// (encodeURIComponent in the SPA), and every handler must observe the
// DECODED value from chi.URLParam. Without the routing middleware chi
// routes on the raw path and hands the still-encoded segment to the
// handler, which broke the custom-DP invoke path, the alarm output
// test fire, and the LINK paramset write in turn.
func TestDecodedPathRouting_URLParamsArriveDecoded(t *testing.T) {
	t.Parallel()
	r, seen := paramEcho(t)
	srv := httptest.NewServer(r)
	defer srv.Close()

	do := func(method, path string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status = %d, want 200", method, path, resp.StatusCode)
		}
	}

	// Channel addresses: encodeURIComponent turns ':' into %3A.
	do(http.MethodPut, "/api/v1/devices/0052E409A90362%3A4/link-ps/0052E409A90362%3A1")
	if seen["addr"] != "0052E409A90362:4" || seen["peer"] != "0052E409A90362:1" {
		t.Fatalf("link-ps params = %q/%q, want decoded channel addresses", seen["addr"], seen["peer"])
	}

	// Alarm output IDs embed '|' (%7C) and ':' (%3A).
	do(http.MethodPost, "/api/v1/alarm/outputs/OttoLoom%7C00245A49949662%3A1%7Cacoustic_siren/test")
	if seen["id"] != "OttoLoom|00245A49949662:1|acoustic_siren" {
		t.Fatalf("alarm output id = %q, want decoded pipe-separated id", seen["id"])
	}

	// Channel-group CDP wire names embed '@' (%40).
	do(http.MethodPost, "/api/v1/devices/ABC0000000%3A0/cdps/STATE%403/invoke")
	if seen["name"] != "STATE@3" {
		t.Fatalf("cdp name = %q, want STATE@3", seen["name"])
	}

	// Room and sysvar names carry non-ASCII (K%C3%BCche = Küche) and
	// spaces (%20).
	do(http.MethodPut, "/api/v1/rooms/K%C3%BCche%20unten")
	if seen["room"] != "Küche unten" {
		t.Fatalf("room = %q, want decoded UTF-8 name with space", seen["room"])
	}

	// Unencoded separators keep working — most ad-hoc API clients send
	// ':' and '|' literally.
	do(http.MethodPut, "/api/v1/devices/0052E409A90362:4/link-ps/0052E409A90362:1")
	if seen["addr"] != "0052E409A90362:4" {
		t.Fatalf("literal addr = %q, want unchanged channel address", seen["addr"])
	}
}
