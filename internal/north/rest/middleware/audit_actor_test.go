// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TestAuditActor pins who the change log names: always the authenticated
// principal, plus the asserted person only from an entry credential, and a
// 400 for an assertion the log must not store.
func TestAuditActor(t *testing.T) {
	type want struct {
		status     int
		hasActor   bool
		user, obo  string
		handlerRan bool
	}
	cases := []struct {
		name    string
		id      *auth.Identity
		headers []string
		want    want
	}{
		{"no identity", nil, []string{"Alex"}, want{status: 200, handlerRan: true}},
		{
			"bearer without header", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer}, nil,
			want{200, true, "ha", "", true},
		},
		{
			"bearer asserts", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Alex (ha:abc)"},
			want{200, true, "ha", "Alex (ha:abc)", true},
		},
		{
			"box token asserts", &auth.Identity{Subject: "occulite-token:ha", Scheme: auth.SchemeOcculiteToken},
			[]string{"Alex"},
			want{200, true, "occulite-token:ha", "Alex", true},
		},
		{
			"session is ignored", &auth.Identity{Subject: "bob", Scheme: auth.SchemeSession},
			[]string{"Mallory"},
			want{200, true, "bob", "", true},
		},
		{
			"basic is ignored", &auth.Identity{Subject: "bob", Scheme: auth.SchemeBasic},
			[]string{"Mallory"},
			want{200, true, "bob", "", true},
		},
		{
			"too long", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{strings.Repeat("a", 129)},
			want{status: 400},
		},
		{
			"exactly 128 bytes", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{strings.Repeat("a", 128)},
			want{200, true, "ha", strings.Repeat("a", 128), true},
		},
		{
			"control character", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Alex\x07"},
			want{status: 400},
		},
		{
			"bidi override", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Alex\u202e"},
			want{status: 400},
		},
		{
			"zero-width space", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Al\u200bex"},
			want{status: 400},
		},
		{
			"invalid utf-8", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Alex\xff"},
			want{status: 400},
		},
		{
			"empty", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{""},
			want{status: 400},
		},
		{
			"sent twice", &auth.Identity{Subject: "ha", Scheme: auth.SchemeBearer},
			[]string{"Alex", "Bob"},
			want{status: 400},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				ran   bool
				got   audit.Actor
				found bool
			)
			h := AuditActor(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ran = true
				got, found = audit.ActorFrom(r.Context())
			}))
			req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/A/paramsets/MASTER", http.NoBody)
			for _, v := range tc.headers {
				req.Header.Add(OnBehalfOfHeader, v)
			}
			if tc.id != nil {
				req = req.WithContext(auth.ContextWithIdentity(req.Context(), *tc.id))
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want.status)
			}
			if ran != tc.want.handlerRan || found != tc.want.hasActor {
				t.Fatalf("handler ran %v (want %v), actor %v (want %v)", ran, tc.want.handlerRan, found, tc.want.hasActor)
			}
			if got.User != tc.want.user || got.OnBehalfOf != tc.want.obo {
				t.Fatalf("actor = %+v, want user %q on_behalf_of %q", got, tc.want.user, tc.want.obo)
			}
		})
	}
}
