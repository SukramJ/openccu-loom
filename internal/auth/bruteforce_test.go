// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeThrottle is an in-memory [BasicAuthThrottle] whose budget is an integer
// token count. Budget reports ok while tokens remain; Charge spends one. It
// records call counts so the guard's peek/charge discipline can be asserted.
type fakeThrottle struct {
	budget      int
	retryAfter  int
	budgetCalls int
	charges     int
}

func (f *fakeThrottle) Budget(*http.Request) (allowed bool, retryAfter int) {
	f.budgetCalls++
	if f.budget > 0 {
		return true, f.retryAfter
	}
	return false, f.retryAfter
}

func (f *fakeThrottle) Charge(*http.Request) {
	f.charges++
	if f.budget > 0 {
		f.budget--
	}
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

// TestGuardBasicAuth_NoCredentials_NotThrottled covers requirement (c): a
// request that carries no Basic credentials is not a guess — the guard passes
// it through and never consults the throttle.
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
	if th.budgetCalls != 0 || th.charges != 0 {
		t.Fatalf("throttle consulted for a credential-less request: budgetCalls=%d charges=%d", th.budgetCalls, th.charges)
	}
}

// TestGuardBasicAuth_ValidCredential_CostsNothing covers requirement (b): a
// successful verification within budget passes through and charges nothing.
func TestGuardBasicAuth_ValidCredential_CostsNothing(t *testing.T) {
	t.Parallel()
	th := &fakeThrottle{budget: 5}
	h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, basicRequest(true))

	if w.Code != http.StatusOK {
		t.Fatalf("valid credential got %d, want 200", w.Code)
	}
	if th.charges != 0 {
		t.Fatalf("valid credential charged the budget: charges=%d", th.charges)
	}
	if th.budget != 5 {
		t.Fatalf("valid credential consumed budget: remaining=%d want 5", th.budget)
	}
}

// TestGuardBasicAuth_FailedGuess_ChargesOnce asserts a wrong credential within
// budget charges exactly one token and still falls through to the normal 401
// flow (here: the downstream handler runs).
func TestGuardBasicAuth_FailedGuess_ChargesOnce(t *testing.T) {
	t.Parallel()
	th := &fakeThrottle{budget: 5}
	called := false
	h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusUnauthorized)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, basicRequest(false))

	if !called {
		t.Fatal("failed guess within budget did not reach the downstream handler")
	}
	if th.charges != 1 {
		t.Fatalf("failed guess charged %d tokens, want exactly 1", th.charges)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 (validity still revealed while within budget)", w.Code)
	}
}

// TestGuardBasicAuth_Exhausted_Returns429BeforeReveal covers requirement (d):
// once the source's budget is spent, EVERY Basic attempt — valid or not — is
// answered 429 before the downstream handler can reveal validity, and the 429
// consumes nothing.
func TestGuardBasicAuth_Exhausted_Returns429BeforeReveal(t *testing.T) {
	t.Parallel()
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{true: "valid", false: "invalid"}[resolved], func(t *testing.T) {
			t.Parallel()
			th := &fakeThrottle{budget: 0, retryAfter: 7}
			called := false
			h := GuardBasicAuth(th)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, basicRequest(resolved))

			if called {
				t.Fatal("downstream handler ran despite an exhausted budget — validity could leak")
			}
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("exhausted budget got %d, want 429", w.Code)
			}
			if ra := w.Header().Get("Retry-After"); ra != "7" {
				t.Fatalf("Retry-After = %q, want 7", ra)
			}
			if th.charges != 0 {
				t.Fatalf("429 path charged the budget: charges=%d", th.charges)
			}
		})
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
