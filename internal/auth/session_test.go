// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionIssueAndLookup(t *testing.T) {
	store := NewSessionStore()
	sess, err := store.Issue(Identity{Subject: "alice", Role: RoleOperator})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if sess.ID == "" || sess.Identity.Subject != "alice" {
		t.Fatalf("sess=%+v", sess)
	}
	if got := store.Lookup(sess.ID); got == nil || got.Identity.Subject != "alice" {
		t.Fatalf("lookup miss: %+v", got)
	}
}

func TestSessionExpiresEvicts(t *testing.T) {
	store := NewSessionStore()
	store.TTL = 10 * time.Millisecond
	store.now = time.Now
	sess, _ := store.Issue(Identity{Subject: "bob"})
	time.Sleep(20 * time.Millisecond)
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("expired session must evict")
	}
}

func TestSessionMiddlewareAttachesIdentity(t *testing.T) {
	store := NewSessionStore()
	sess, _ := store.Issue(Identity{Subject: "alice", Role: RoleViewer})

	handler := SessionMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.Subject != "alice" || id.Scheme != SchemeSession {
			t.Fatalf("id=%+v ok=%v", id, ok)
		}
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCSRFMiddlewarePassesSafeMethods(t *testing.T) {
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), CSRFCookieName) {
		t.Fatalf("csrf cookie missing: %s", rr.Header().Get("Set-Cookie"))
	}
}

func TestCSRFMiddlewareRejectsPostWithoutToken(t *testing.T) {
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next should not run")
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader("x=1"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "expected"})
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCSRFMiddlewareAcceptsMatchingHeader(t *testing.T) {
	hit := 0
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader("x=1"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "match-me"})
	req.Header.Set(CSRFHeaderName, "match-me")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 204 || hit != 1 {
		t.Fatalf("status=%d hit=%d", rr.Code, hit)
	}
}
