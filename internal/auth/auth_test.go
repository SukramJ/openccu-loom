// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuthSuccess(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("alice", "s3cret", RoleOperator)

	id, err := us.AuthenticateBasic(context.Background(), "alice", "s3cret")
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if id.Subject != "alice" || id.Role != RoleOperator || id.Scheme != SchemeBasic {
		t.Fatalf("id=%+v", id)
	}
}

func TestBasicAuthCaseInsensitiveUsername(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("Alice", "s", RoleViewer)
	if _, err := us.AuthenticateBasic(context.Background(), "ALICE", "s"); err != nil {
		t.Fatalf("case mismatch: %v", err)
	}
}

func TestBasicAuthWrongPassword(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("alice", "s", RoleViewer)
	if _, err := us.AuthenticateBasic(context.Background(), "alice", "x"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryTokenStore(t *testing.T) {
	ts := NewMemoryTokenStore(map[string]Identity{
		"tok-1": {Subject: "ci", Role: RoleOperator},
	})
	id, err := ts.AuthenticateToken(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if id.Subject != "ci" || id.Scheme != SchemeBearer {
		t.Fatalf("id=%+v", id)
	}
	if _, err := ts.AuthenticateToken(context.Background(), "bad"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err=%v", err)
	}
}

func TestRoleHierarchy(t *testing.T) {
	admin := Identity{Role: RoleAdmin}
	op := Identity{Role: RoleOperator}
	viewer := Identity{Role: RoleViewer}

	if !admin.HasRole(RoleOperator) || !admin.HasRole(RoleViewer) || !admin.HasRole(RoleAdmin) {
		t.Fatal("admin covers all")
	}
	if !op.HasRole(RoleOperator) || !op.HasRole(RoleViewer) || op.HasRole(RoleAdmin) {
		t.Fatal("operator covers viewer, not admin")
	}
	if !viewer.HasRole(RoleViewer) || viewer.HasRole(RoleOperator) || viewer.HasRole(RoleAdmin) {
		t.Fatal("viewer")
	}
}

func TestMiddlewareResolvesBasic(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("alice", "s", RoleViewer)
	mw := NewMiddleware(us, nil)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.Subject != "alice" {
			t.Fatalf("id=%+v ok=%v", id, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := mw.Resolve(next)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("alice", "s")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestMiddlewareResolvesBearer(t *testing.T) {
	ts := NewMemoryTokenStore(map[string]Identity{"xyz": {Subject: "ci", Role: RoleAdmin}})
	mw := NewMiddleware(nil, ts)
	hit := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := IdentityFrom(r.Context()); !ok || id.Role != RoleAdmin {
			t.Fatalf("id=%+v", id)
		}
		hit++
	})
	h := mw.Resolve(next)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if hit != 1 {
		t.Fatalf("hit=%d", hit)
	}
}

func TestMiddlewareRequireRejects(t *testing.T) {
	mw := NewMiddleware(nil, nil)
	h := mw.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not run")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("status=%d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("challenge header missing")
	}
}

func TestMiddlewareRequireRole(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("viewer", "s", RoleViewer)
	mw := NewMiddleware(us, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := mw.Resolve(mw.RequireRole(RoleOperator, next))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("viewer", "s")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("status=%d", rr.Code)
	}
}
