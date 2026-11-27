// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package contract

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// TestRouterStampsTheAuditActor pins that the production router mounts the
// actor middleware: a change made through it by an entry credential lands in
// the change log with the principal and the asserted person, and the same
// header from a browser session is dropped. The middleware's own test drives
// it in isolation and stays green when the router never mounts it; this one
// does not.
func TestRouterStampsTheAuditActor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme auth.Scheme
		want   string
	}{
		{"entry credential", auth.SchemeBearer, "Alex (ha:1)"},
		{"browser session", auth.SchemeSession, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := audit.NewBuffer(10)
			deps := fullyWiredRouterDeps()
			deps.AuditRecorder = buf
			deps.AuthResolve = func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					id := auth.Identity{Subject: "homematicip_local", Scheme: tc.scheme, Role: auth.RoleAdmin}
					next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
				})
			}
			router := rest.NewRouter(deps)

			body := `{"central":"ccu","interface_id":"HmIP-RF","channel":"A:1","parameter":"STATE","record":true}`
			req := httptest.NewRequest(http.MethodPut, "/api/v1/measurements/recording", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-On-Behalf-Of", "Alex (ha:1)")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d body %.200s", rec.Code, rec.Body.String())
			}
			rows := buf.List(10)
			if len(rows) != 1 {
				t.Fatalf("recorded %d rows, want 1", len(rows))
			}
			if rows[0].User != "homematicip_local" || rows[0].OnBehalfOf != tc.want {
				t.Fatalf("row user %q on_behalf_of %q, want %q / %q",
					rows[0].User, rows[0].OnBehalfOf, "homematicip_local", tc.want)
			}
		})
	}
}
