// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeThrottle is an in-memory [BasicAuthThrottle] whose budget is an integer
// token count. Budget reports ok while tokens remain; a reservation spends
// one and its refund returns it. It records call counts so the
// reserve-before-verify discipline can be asserted.
type fakeThrottle struct {
	budget      int
	retryAfter  int
	budgetCalls int
	reserves    int
	refunds     int
	charges     int
}

func (f *fakeThrottle) Budget(*http.Request) (allowed bool, retryAfter int) {
	f.budgetCalls++
	if f.budget > 0 {
		return true, f.retryAfter
	}
	return false, f.retryAfter
}

func (f *fakeThrottle) ReserveBasicAttempt(*http.Request) (refund func(), ok bool) {
	f.reserves++
	if f.budget <= 0 {
		return nil, false
	}
	f.budget--
	return func() { f.refunds++; f.budget++ }, true
}

func (f *fakeThrottle) Charge(*http.Request) {
	f.charges++
	if f.budget > 0 {
		f.budget--
	}
}

// countingBasicUsers records how often a password verification was requested
// and answers according to whether the password matches.
type countingBasicUsers struct {
	password string
	calls    int
}

func (c *countingBasicUsers) AuthenticateBasic(_ context.Context, username, password string) (Identity, error) {
	c.calls++
	if password != c.password {
		return Identity{}, ErrUnauthenticated
	}
	return Identity{Subject: username, Scheme: SchemeBasic, Role: RoleAdmin}, nil
}

// wrongPasswordRequest builds a Basic request [countingBasicUsers] refuses —
// the shape a guessing sweep produces.
func wrongPasswordRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	r.SetBasicAuth("alice", "not-the-password")
	return r
}

// basicRequest builds a request carrying HTTP Basic credentials, optionally
// with an already-resolved identity attached (the state Resolve would leave
// after a valid verification).
func basicRequest(resolved bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	r.SetBasicAuth("alice", "hunter2")
	if resolved {
		r = r.WithContext(ContextWithIdentity(r.Context(), Identity{Subject: "alice", Role: RoleAdmin}))
	}
	return r
}

// TestGuardBasicAuth_NoCredentials_NotThrottled covers the "is this a
// credential attempt?" test: a request that carries no Basic credentials is
// not a guess — the guard passes it through and never consults the throttle.
func TestGuardBasicAuth_NoCredentials_NotThrottled(t *testing.T) {
	t.Parallel()
	th := &fakeThrottle{budget: 0} // even an exhausted budget must not bite here
	called := false
	h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody))

	if !called {
		t.Fatal("next handler not called for a credential-less request")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	if th.budgetCalls != 0 || th.reserves != 0 {
		t.Fatalf("throttle consulted for a credential-less request: budgetCalls=%d reserves=%d", th.budgetCalls, th.reserves)
	}
}

// TestGuardBasicAuth_ResolvedCredential_PassesThrough asserts a request whose
// credential already verified is never turned into a 429 — the verification
// ran under its own reservation, so the budget has nothing left to say.
func TestGuardBasicAuth_ResolvedCredential_PassesThrough(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{5, 0} {
		th := &fakeThrottle{budget: budget}
		h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, basicRequest(true))

		if w.Code != http.StatusOK {
			t.Fatalf("verified credential with budget %d got %d, want 200", budget, w.Code)
		}
		if th.budget != budget {
			t.Fatalf("verified credential consumed budget: remaining=%d want %d", th.budget, budget)
		}
	}
}

// TestGuardBasicAuth_Exhausted_Returns429BeforeReveal asserts that once the
// source's budget is spent, an unresolved Basic attempt is answered 429
// before the downstream handler can reveal anything, and the 429 consumes
// nothing.
func TestGuardBasicAuth_Exhausted_Returns429BeforeReveal(t *testing.T) {
	t.Parallel()
	th := &fakeThrottle{budget: 0, retryAfter: 7}
	called := false
	h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, basicRequest(false))

	if called {
		t.Fatal("downstream handler ran despite an exhausted budget — validity could leak")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted budget got %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "7" {
		t.Fatalf("Retry-After = %q, want 7", ra)
	}
	if th.reserves != 0 {
		t.Fatalf("the 429 path took a reservation: reserves=%d", th.reserves)
	}
}

// TestGuardBasicAuth_NilThrottle_Disabled asserts a nil throttle turns the
// guard into a pass-through (test fixtures / builds without the limiter).
func TestGuardBasicAuth_NilThrottle_Disabled(t *testing.T) {
	t.Parallel()
	called := false
	h := GuardBasicAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, basicRequest(false))
	if !called || w.Code != http.StatusOK {
		t.Fatalf("nil throttle did not pass through: called=%v code=%d", called, w.Code)
	}
}

// TestResolveRefusesTheVerificationWhenTheBudgetIsSpent is the ordering that
// makes the throttle worth anything: the password verification is the
// expensive operation, so a source out of budget must never reach the user
// store at all. Accounting for the attempt afterwards let any number of
// concurrent attempts pass the same check and run the key derivation in
// parallel.
func TestResolveRefusesTheVerificationWhenTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	users := &countingBasicUsers{password: "hunter2"}
	th := &fakeThrottle{budget: 0}
	m := NewMiddleware(users, nil)
	m.BasicThrottle = th

	var resolved bool
	h := m.Resolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, resolved = IdentityFrom(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), basicRequest(false))

	if users.calls != 0 {
		t.Fatalf("password verifications = %d with an empty budget, want 0", users.calls)
	}
	if resolved {
		t.Fatal("an identity was resolved without a verification")
	}
}

// TestResolveChargesEveryAttemptAndRefundsTheValidOne pins both directions of
// the accounting: a wrong password costs the source a token, a right one
// costs nothing.
func TestResolveChargesEveryAttemptAndRefundsTheValidOne(t *testing.T) {
	t.Parallel()
	users := &countingBasicUsers{password: "hunter2"}
	th := &fakeThrottle{budget: 5}
	m := NewMiddleware(users, nil)
	m.BasicThrottle = th
	h := m.Resolve(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	wrong := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	wrong.SetBasicAuth("alice", "not-the-password")
	h.ServeHTTP(httptest.NewRecorder(), wrong)
	if th.budget != 4 {
		t.Fatalf("budget after a failed guess = %d, want 4", th.budget)
	}

	right := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	right.SetBasicAuth("alice", "hunter2")
	h.ServeHTTP(httptest.NewRecorder(), right)
	if th.budget != 4 {
		t.Fatalf("budget after a valid credential = %d, want the refund to keep it at 4", th.budget)
	}
	if th.refunds != 1 {
		t.Fatalf("refunds = %d, want exactly the one valid verification", th.refunds)
	}
}

// TestGuardChargesOnlyWhatTheResolverDidNotAccountFor pins the two accounting
// points against each other. Charging before the verification is where the
// cost is bounded, but the guard must still bound a mount whose resolver has
// no throttle wired — and neither may charge the same attempt twice, or a
// source would burn its budget at double rate.
func TestGuardChargesOnlyWhatTheResolverDidNotAccountFor(t *testing.T) {
	t.Parallel()
	users := &countingBasicUsers{password: "hunter2"}

	t.Run("resolver accounted", func(t *testing.T) {
		t.Parallel()
		th := &fakeThrottle{budget: 5}
		m := NewMiddleware(users, nil)
		m.BasicThrottle = th
		h := m.Resolve(GuardBasicAuth(th)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
		h.ServeHTTP(httptest.NewRecorder(), wrongPasswordRequest())
		if th.charges != 0 {
			t.Fatalf("guard charged an attempt the resolver already accounted for: charges=%d", th.charges)
		}
		if th.budget != 4 {
			t.Fatalf("budget = %d after one failed guess, want 4 (charged exactly once)", th.budget)
		}
	})

	t.Run("resolver has no throttle", func(t *testing.T) {
		t.Parallel()
		th := &fakeThrottle{budget: 5}
		m := NewMiddleware(users, nil)
		h := m.Resolve(GuardBasicAuth(th)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
		h.ServeHTTP(httptest.NewRecorder(), wrongPasswordRequest())
		if th.charges != 1 {
			t.Fatalf("charges = %d, want the guard to bound a resolver that cannot", th.charges)
		}
	})
}

// TestResolveWithoutThrottleStillVerifies keeps the unwired path working: a
// middleware with no throttle behaves exactly as it did before one existed.
func TestResolveWithoutThrottleStillVerifies(t *testing.T) {
	t.Parallel()
	users := &countingBasicUsers{password: "hunter2"}
	m := NewMiddleware(users, nil)
	var resolved bool
	h := m.Resolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, resolved = IdentityFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	req.SetBasicAuth("alice", "hunter2")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !resolved || users.calls != 1 {
		t.Fatalf("resolved=%v verifications=%d, want a verified identity", resolved, users.calls)
	}
}
