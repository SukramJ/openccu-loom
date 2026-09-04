// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// scopeCapturingRebooter records the request scope the router handed the
// domain, which is what the log handler reads.
type scopeCapturingRebooter struct {
	central string
}

func (s *scopeCapturingRebooter) RebootCCU(ctx context.Context, _ string) error {
	rc, _ := hmreqctx.FromContext(ctx)
	s.central = rc.CentralName
	return nil
}

// TestCentralNamedRouteCarriesTheCentralScope pins that a route whose path
// names the central puts that name into the request scope.
//
// The scope is what every downstream slog record is stamped with. A daemon
// that hosts one CCU gets the name at boot from ReqContextWithCentral, so a
// missing per-route resolution is invisible there and shows only on a
// multi-CCU installation — where two CCUs rebooting produce log lines that
// cannot be told apart.
//
// It goes through NewRouter rather than the middleware directly: the
// middleware only works when it is attached per route, because a chi Use
// chain runs before the URL parameter it reads exists. Calling it by hand
// would pass with the router mounting nothing.
func TestCentralNamedRouteCarriesTheCentralScope(t *testing.T) {
	t.Parallel()

	rebooter := &scopeCapturingRebooter{}
	mw := auth.NewMiddleware(nil, nil)
	r := NewRouter(Deps{
		StartedAt: time.Now(),
		CCUReboot: rebooter,
		AuthResolve: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := auth.ContextWithIdentity(req.Context(),
					auth.Identity{Subject: "u", Role: auth.RoleAdmin})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		},
		AuthRequire:  mw.Require,
		RequireAdmin: func(next http.Handler) http.Handler { return mw.RequireRole(auth.RoleAdmin, next) },
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/system/ccu/attic/reboot", http.NoBody))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rebooter.central != "attic" {
		t.Errorf("request scope carried central_name=%q, want %q", rebooter.central, "attic")
	}
}
