// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

func TestExpandScopesAppliesTheImplications(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stored []string
		want   []string
	}{
		{[]string{"rpc:admin"}, []string{"rpc:admin", "rpc:configure", "rpc:operate", "rpc:read"}},
		{[]string{"meta:write", "system:write"}, []string{"led", "meta:read", "meta:write", "system:read", "system:write"}},
		{[]string{"addons:write"}, []string{"addons:write", "system:read"}},
		{[]string{"auth:admin"}, []string{"auth:admin", "self"}},
		{[]string{"power"}, []string{"power"}},
		{nil, []string{}},
	}
	for _, tc := range cases {
		if got := occulited.SortedScopes(occulited.ExpandScopes(tc.stored)); !slices.Equal(got, tc.want) {
			t.Errorf("ExpandScopes(%v) = %v, want %v", tc.stored, got, tc.want)
		}
	}
	all := occulited.ExpandScopes([]string{"*"})
	for _, s := range []string{"power", "backup", "radio:keys", "rpc:admin", "meta:write", "self"} {
		if !all[s] {
			t.Errorf("* does not grant %s", s)
		}
	}
}

// TestAuthStateReportsTheTokensScopes reads the auth state of a scoped
// token from the fake box: authenticated, the stored scopes, and a granted
// set with the implications. An unknown token is not authenticated and is
// granted nothing.
func TestAuthStateReportsTheTokensScopes(t *testing.T) {
	t.Parallel()
	const token = "olt_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f := startFake(t, litefake.Options{Tokens: map[string][]string{token: {"rpc:configure", "meta:read"}}})

	st, err := newClient(t, f.URL(), token).AuthState(context.Background())
	if err != nil {
		t.Fatalf("AuthState: %v", err)
	}
	if !st.Authenticated || !slices.Contains(st.Scopes, "rpc:configure") {
		t.Fatalf("state = %+v", st)
	}
	granted := st.Granted()
	for _, s := range []string{"rpc:configure", "rpc:operate", "rpc:read", "meta:read"} {
		if !granted[s] {
			t.Errorf("granted set lacks %s: %v", s, occulited.SortedScopes(granted))
		}
	}
	if granted["power"] || granted["meta:write"] {
		t.Errorf("granted set holds scopes the token does not have: %v", occulited.SortedScopes(granted))
	}

	unknown, err := newClient(t, f.URL(), "olt_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb").AuthState(context.Background())
	if err != nil {
		t.Fatalf("AuthState with an unknown token: %v", err)
	}
	if unknown.Authenticated || len(unknown.Granted()) != 0 {
		t.Errorf("an unknown token reads as %+v", unknown)
	}
}

// TestLoginVerifiesAccountsWithoutTheToken pins the account check: the
// right password opens a session carrying the account's level, a wrong one
// is an authentication failure, and the logout closes the session.
func TestLoginVerifiesAccountsWithoutTheToken(t *testing.T) {
	t.Parallel()
	f := startFake(t, litefake.Options{Accounts: []litefake.Account{{
		Username: "anna", Password: "correct horse", Role: "user", Level: "operate", AccountID: "a1",
	}}})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()

	res, err := c.Login(ctx, "anna", "correct horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.SID == "" || res.Level != "operate" || res.User != "anna" {
		t.Fatalf("login result = %+v", res)
	}
	if err := c.Logout(ctx, res.SID); err != nil {
		t.Errorf("Logout: %v", err)
	}
	if _, err := c.Login(ctx, "anna", "wrong"); !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Errorf("wrong password: err = %v, want ErrAuthFailure", err)
	}
}

// TestPairingYieldsATokenOnceApproved runs the whole client pairing against
// the fake box: the request, the first poll that reveals the nonce, the
// administrator approving with the code the client shows, and the one
// approved answer carrying the token — which then authenticates.
func TestPairingYieldsATokenOnceApproved(t *testing.T) {
	t.Parallel()
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), "")
	ctx := context.Background()

	p, err := c.StartPairing(ctx, occulited.PairingRequest{
		App: "openccu-loom", AppVersion: "test", Instance: "loom", Name: "OpenCCU-Loom (loom)",
		Access:  occulited.PairingAccessControl,
		Purpose: map[string]string{"devices": "switch devices", "names": "show names", "system": "show status"},
	})
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	if len(p.Code) != 6 || p.Fingerprint != "" {
		t.Fatalf("pairing = code %q fingerprint %q, want six digits and no fingerprint over HTTP", p.Code, p.Fingerprint)
	}
	res, err := c.PollPairing(ctx, p, 0)
	if err != nil || res.State != occulited.PairingPending {
		t.Fatalf("first poll = %+v, %v; want pending", res, err)
	}
	if _, err := f.ApprovePairing(p.ID, p.Code); err != nil {
		t.Fatalf("ApprovePairing with the client's code: %v", err)
	}
	var approved occulited.PairingResult
	deadline := time.Now().Add(10 * time.Second)
	for approved.State != occulited.PairingApproved {
		if time.Now().After(deadline) {
			t.Fatal("the pairing never reported approved")
		}
		approved, err = c.PollPairing(ctx, p, 2*time.Second)
		var apiErr *occulited.APIError
		if errors.As(err, &apiErr) && apiErr.Code == occulited.CodeSlowDown {
			time.Sleep(apiErr.RetryAfter)
			continue
		}
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if approved.Token == "" || slices.Contains(approved.Scopes, "power") || !slices.Contains(approved.Scopes, "rpc:operate") {
		t.Fatalf("approved = %+v, want a token with rpc:operate and without power", approved)
	}
	st, err := newClient(t, f.URL(), approved.Token).AuthState(ctx)
	if err != nil || !st.Authenticated {
		t.Fatalf("the paired token does not authenticate: %+v, %v", st, err)
	}
}

// TestPairingAbortsOnAFingerprintMismatch is the negative control of the
// interception check: a box that reports a certificate fingerprint the
// connection did not see (here: any fingerprint over plain HTTP) aborts
// the pairing instead of showing a code.
func TestPairingAbortsOnAFingerprintMismatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"0123456789abcdef","poll":"ab","nonce":"00ff","expires_in":300,"interval":2,"fingerprint":"aa"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := newClient(t, srv.URL, "").StartPairing(context.Background(), occulited.PairingRequest{
		App: "openccu-loom", Access: occulited.PairingAccessRead,
	})
	if !errors.Is(err, occulited.ErrPairingFingerprintMismatch) {
		t.Fatalf("StartPairing = %v, want ErrPairingFingerprintMismatch", err)
	}
}
