// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	// Drive eviction with virtual time via the store's now seam instead of
	// a real sleep racing the 10ms TTL.
	vnow := time.Now()
	store.now = func() time.Time { return vnow }
	sess, _ := store.Issue(Identity{Subject: "bob"})
	vnow = vnow.Add(20 * time.Millisecond) // advance past the TTL
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("expired session must evict")
	}
}

// TestSessionRevokeBySubjectRemovesAllForSubject verifies that
// RevokeBySubject evicts every session belonging to a subject and leaves
// sessions for other subjects untouched.
func TestSessionRevokeBySubjectRemovesAllForSubject(t *testing.T) {
	store := NewSessionStore()
	a1, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue a1: %v", err)
	}
	a2, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue a2: %v", err)
	}
	b1, err := store.Issue(Identity{Subject: "bob"})
	if err != nil {
		t.Fatalf("issue b1: %v", err)
	}

	if n := store.RevokeBySubject("alice"); n != 2 {
		t.Fatalf("RevokeBySubject count=%d want 2", n)
	}
	if store.Lookup(a1.ID) != nil {
		t.Error("alice session a1 still present after RevokeBySubject")
	}
	if store.Lookup(a2.ID) != nil {
		t.Error("alice session a2 still present after RevokeBySubject")
	}
	if store.Lookup(b1.ID) == nil {
		t.Error("bob session was evicted by RevokeBySubject(alice)")
	}
}

// TestSessionRevokeBySubjectExceptPreservesNamedSession verifies that
// RevokeBySubjectExcept evicts every session for the subject except the
// one whose id is passed as keepSID.
func TestSessionRevokeBySubjectExceptPreservesNamedSession(t *testing.T) {
	store := NewSessionStore()
	keep, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue keep: %v", err)
	}
	other, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue other: %v", err)
	}

	if n := store.RevokeBySubjectExcept("alice", keep.ID); n != 1 {
		t.Fatalf("RevokeBySubjectExcept count=%d want 1", n)
	}
	if store.Lookup(keep.ID) == nil {
		t.Error("kept session was revoked by RevokeBySubjectExcept")
	}
	if store.Lookup(other.ID) != nil {
		t.Error("other session for subject still present after RevokeBySubjectExcept")
	}
}

// TestSessionRevokeBySubjectIgnoresSubjectCasing verifies that a
// credential change made against the canonical (lower-cased) subject
// still evicts a session issued under a differently-cased spelling.
// Anything else lets a stolen cookie outlive the password reset that was
// meant to kill it.
func TestSessionRevokeBySubjectIgnoresSubjectCasing(t *testing.T) {
	store := NewSessionStore()
	sess, err := store.Issue(Identity{Subject: "Markus"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if n := store.RevokeBySubject("markus"); n != 1 {
		t.Fatalf("RevokeBySubject count=%d want 1", n)
	}
	if store.Lookup(sess.ID) != nil {
		t.Error("session for differently-cased subject survived the revocation")
	}
}

// TestSessionRevokeBySubjectEmptySubjectNoOp verifies that an empty
// subject is a no-op — it must never wipe every session in the store.
func TestSessionRevokeBySubjectEmptySubjectNoOp(t *testing.T) {
	store := NewSessionStore()
	sess, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if n := store.RevokeBySubject(""); n != 0 {
		t.Errorf("RevokeBySubject(\"\") count=%d want 0", n)
	}
	if store.Lookup(sess.ID) == nil {
		t.Error("session evicted by empty-subject RevokeBySubject")
	}
	if n := store.RevokeBySubjectExcept("", sess.ID); n != 0 {
		t.Errorf("RevokeBySubjectExcept(\"\", ...) count=%d want 0", n)
	}
}

// TestSessionIdleTTLEvictsIdleSession verifies that with IdleTTL set, a
// session looked up within the idle window survives and refreshes its
// idle clock, while one idle beyond the window since the last successful
// Lookup is evicted on the next Lookup.
func TestSessionIdleTTLEvictsIdleSession(t *testing.T) {
	store := NewSessionStore()
	store.IdleTTL = 10 * time.Millisecond
	vnow := time.Now()
	store.now = func() time.Time { return vnow }

	sess, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Looked up within the idle window: survives and refreshes lastSeen.
	vnow = vnow.Add(5 * time.Millisecond)
	if got := store.Lookup(sess.ID); got == nil {
		t.Fatal("session evicted within idle window")
	}

	// Idle beyond the window since the last successful Lookup: evicted.
	vnow = vnow.Add(20 * time.Millisecond)
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("session survived beyond IdleTTL")
	}
}

// TestSessionIdleTTLDisabledPreservesAbsoluteTTLOnly verifies that
// IdleTTL==0 disables the idle check (a session survives however long it
// sits between lookups) while the absolute TTL still applies unchanged.
func TestSessionIdleTTLDisabledPreservesAbsoluteTTLOnly(t *testing.T) {
	store := NewSessionStore()
	store.TTL = 100 * time.Millisecond
	store.IdleTTL = 0
	vnow := time.Now()
	store.now = func() time.Time { return vnow }

	sess, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Advance close to (but before) the absolute TTL; with IdleTTL disabled
	// this must not evict even though it would exceed a typical idle window.
	vnow = vnow.Add(90 * time.Millisecond)
	if got := store.Lookup(sess.ID); got == nil {
		t.Fatal("session evicted despite IdleTTL disabled and within absolute TTL")
	}

	// Past the absolute TTL: evicted regardless of IdleTTL.
	vnow = vnow.Add(20 * time.Millisecond)
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("session survived past absolute TTL")
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
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCSRFMiddlewarePassesSafeMethods(t *testing.T) {
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
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
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x=1"))
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
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x=1"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "match-me"})
	req.Header.Set(CSRFHeaderName, "match-me")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 204 || hit != 1 {
		t.Fatalf("status=%d hit=%d", rr.Code, hit)
	}
}

// --- fake SessionPersistence for persistence-wiring tests ---

type fakeSessionPersist struct {
	mu             sync.Mutex
	saved          map[string]*Session
	saveCalls      int
	deleteCalls    []string
	purgeCallCount int   // how many times DeleteExpiredSessions was called
	loadErr        error // returned by LoadActiveSessions when set
	preloaded      []*Session
	sentinelCount  int
}

func newFakePersist() *fakeSessionPersist {
	return &fakeSessionPersist{saved: make(map[string]*Session)}
}

func (f *fakeSessionPersist) SaveSession(_ context.Context, sess *Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	cp := *sess
	f.saved[sess.ID] = &cp
	return nil
}

func (f *fakeSessionPersist) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, id)
	delete(f.saved, id)
	return nil
}

func (f *fakeSessionPersist) LoadActiveSessions(_ context.Context, _ time.Time) ([]*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.preloaded, nil
}

func (f *fakeSessionPersist) DeleteExpiredSessions(_ context.Context, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgeCallCount++
	return f.sentinelCount, nil
}

func (f *fakeSessionPersist) savedByID(id string) *Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saved[id]
}

func (f *fakeSessionPersist) saveCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveCalls
}

func (f *fakeSessionPersist) deleteCallsFor(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deleteCalls {
		if d == id {
			return true
		}
	}
	return false
}

func (f *fakeSessionPersist) purgeWasCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.purgeCallCount > 0
}

// discardLogger builds a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestPersistentSessionStoreIssueSaves verifies that Issue calls
// SaveSession on the persistence layer and the entry is findable there.
func TestPersistentSessionStoreIssueSaves(t *testing.T) {
	fake := newFakePersist()
	store, err := NewPersistentSessionStore(fake, discardLogger())
	if err != nil {
		t.Fatalf("NewPersistentSessionStore: %v", err)
	}

	sess, err := store.Issue(Identity{Subject: "alice", Role: RoleOperator})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if fake.saveCallCount() < 1 {
		t.Error("SaveSession was not called after Issue")
	}
	if fake.savedByID(sess.ID) == nil {
		t.Errorf("session %q not found in fake persistence", sess.ID)
	}
}

// TestPersistentSessionStoreRevokeDeletes verifies that Revoke calls
// DeleteSession and the entry is removed from the persistence layer.
func TestPersistentSessionStoreRevokeDeletes(t *testing.T) {
	fake := newFakePersist()
	store, err := NewPersistentSessionStore(fake, discardLogger())
	if err != nil {
		t.Fatalf("NewPersistentSessionStore: %v", err)
	}

	sess, err := store.Issue(Identity{Subject: "bob", Role: RoleViewer})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	store.Revoke(sess.ID)

	if !fake.deleteCallsFor(sess.ID) {
		t.Error("DeleteSession was not called after Revoke")
	}
	if fake.savedByID(sess.ID) != nil {
		t.Error("session still present in fake after Revoke")
	}
}

// TestNewPersistentSessionStoreHydrates verifies that a pre-seeded
// persistence is hydrated into in-memory state during construction so
// Lookup succeeds without a re-issue.
func TestNewPersistentSessionStoreHydrates(t *testing.T) {
	fake := newFakePersist()
	now := time.Now()
	preloaded := &Session{
		ID:       "hydrated-id",
		Identity: Identity{Subject: "carol", Role: RoleAdmin},
		Created:  now.Add(-time.Minute),
		Expires:  now.Add(time.Hour),
	}
	fake.preloaded = []*Session{preloaded}

	store, err := NewPersistentSessionStore(fake, discardLogger())
	if err != nil {
		t.Fatalf("NewPersistentSessionStore: %v", err)
	}
	// Anchor virtual time so the hydrated session is not expired.
	store.now = func() time.Time { return now }

	got := store.Lookup(preloaded.ID)
	if got == nil {
		t.Fatal("Lookup returned nil for hydrated session")
	}
	if got.Identity.Subject != "carol" {
		t.Errorf("Subject=%q want carol", got.Identity.Subject)
	}
}

// TestNewPersistentSessionStorePropagatesHydrationError verifies that a
// LoadActiveSessions error is returned by the constructor.
func TestNewPersistentSessionStorePropagatesHydrationError(t *testing.T) {
	fake := newFakePersist()
	fake.loadErr = errors.New("db down")

	_, err := NewPersistentSessionStore(fake, discardLogger())
	if err == nil {
		t.Fatal("expected error from NewPersistentSessionStore, got nil")
	}
	if !errors.Is(err, fake.loadErr) && err.Error() != fake.loadErr.Error() {
		t.Errorf("error=%v want db down", err)
	}
}

// TestPurgeExpiredEvictsAndDelegates verifies that PurgeExpired both
// sweeps the in-memory map and calls DeleteExpiredSessions on the
// persistence layer, returning the sentinel count from the fake.
func TestPurgeExpiredEvictsAndDelegates(t *testing.T) {
	fake := newFakePersist()
	fake.sentinelCount = 7
	store, err := NewPersistentSessionStore(fake, discardLogger())
	if err != nil {
		t.Fatalf("NewPersistentSessionStore: %v", err)
	}

	vnow := time.Now()
	store.TTL = 10 * time.Millisecond
	store.now = func() time.Time { return vnow }

	sess, err := store.Issue(Identity{Subject: "dave"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Advance time past TTL so the session is considered expired.
	vnow = vnow.Add(20 * time.Millisecond)

	count, err := store.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if count != 7 {
		t.Errorf("PurgeExpired count=%d want 7 (sentinel)", count)
	}
	if !fake.purgeWasCalled() {
		t.Error("DeleteExpiredSessions was not called")
	}
	// In-memory item must be gone.
	if store.Lookup(sess.ID) != nil {
		t.Error("expired session still in memory after PurgeExpired")
	}
}

// TestPurgeExpiredNoOpSafeWithNilPersist verifies that PurgeExpired is
// safe when the store has no persistence layer: it sweeps memory and
// returns (0, nil).
func TestPurgeExpiredNoOpSafeWithNilPersist(t *testing.T) {
	store := NewSessionStore()
	vnow := time.Now()
	store.TTL = 10 * time.Millisecond
	store.now = func() time.Time { return vnow }

	sess, err := store.Issue(Identity{Subject: "eve"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	vnow = vnow.Add(20 * time.Millisecond)

	count, err := store.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if count != 0 {
		t.Errorf("count=%d want 0 for nil persist", count)
	}
	if store.Lookup(sess.ID) != nil {
		t.Error("expired session still in memory after PurgeExpired")
	}
}

// TestSessionMiddlewareDefersToResolvedIdentity pins the precedence rule that
// a Bearer/Basic identity resolved by an earlier middleware is NOT overridden
// by a session cookie. This is the remote-proxy downgrade: the proxy injects
// an admin Bearer while the browser's lower-role session cookie rides along;
// the session must not win, or admin/operator actions 403 "insufficient role".
func TestSessionMiddlewareDefersToResolvedIdentity(t *testing.T) {
	sessions := NewSessionStore()
	viewer, err := sessions.Issue(Identity{Subject: "viewer-user", Role: RoleViewer})
	if err != nil {
		t.Fatalf("issue viewer session: %v", err)
	}
	tokens := NewMemoryTokenStore(map[string]Identity{
		"admintoken": {Subject: "admin", Role: RoleAdmin},
	})
	mw := NewMiddleware(nil, tokens)

	var got Identity
	var seen bool
	terminal := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = IdentityFrom(r.Context())
	})
	// The production wiring: Resolve (Bearer/Basic) runs first, then the
	// session resolver runs inner. Both write the same context key.
	chain := mw.Resolve(SessionMiddleware(sessions)(terminal))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rssi", http.NoBody)
	req.Header.Set("Authorization", "Bearer admintoken")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: viewer.ID})
	chain.ServeHTTP(httptest.NewRecorder(), req)

	if !seen {
		t.Fatal("no identity resolved")
	}
	if got.Role != RoleAdmin {
		t.Fatalf("role=%q scheme=%q, want admin (Bearer must win over session)", got.Role, got.Scheme)
	}
	if got.Scheme != SchemeBearer {
		t.Errorf("scheme=%q, want bearer", got.Scheme)
	}
}

// TestSessionMiddlewareResolvesWhenUnauthenticated is the regression guard for
// the normal cookie-only login flow: with no earlier identity, the session
// cookie must still resolve to its identity.
func TestSessionMiddlewareResolvesWhenUnauthenticated(t *testing.T) {
	sessions := NewSessionStore()
	sess, err := sessions.Issue(Identity{Subject: "op", Role: RoleOperator})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var got Identity
	var seen bool
	terminal := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = IdentityFrom(r.Context())
	})
	chain := SessionMiddleware(sessions)(terminal)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	chain.ServeHTTP(httptest.NewRecorder(), req)

	if !seen || got.Subject != "op" || got.Role != RoleOperator {
		t.Fatalf("session identity not resolved: seen=%v got=%+v", seen, got)
	}
	if got.Scheme != SchemeSession {
		t.Errorf("scheme=%q, want session", got.Scheme)
	}
}
