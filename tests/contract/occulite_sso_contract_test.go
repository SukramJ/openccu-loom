// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

// The box-shell single sign-on (ADR 0079), pinned end to end against the
// fake box: session ids come from the box's own login route, the verifier
// is built the way the composition root builds it (an occulited client
// with no token of its own, asking the open state route with the session
// id under test as its bearer), and the resolver under test is the
// production middleware.

const (
	ssoAdminUser = "sso-admin"
	ssoPlainUser = "sso-user"
	ssoPassword  = "sso-password"
	// ssoUnissuedSID has the exact shape of a box session id but was never
	// handed out by the fake.
	ssoUnissuedSID = "AAAAAAAAAAAAAAAAAAAAAAAAAA"
)

// ssoVerifier mirrors the composition root's adapter from the occulited
// client to the auth port (cmd/openccu-loom/occulite_sso_wiring.go); cmd
// is a main package and cannot be imported.
type ssoVerifier struct {
	c *occulited.Client
}

func (v ssoVerifier) VerifySession(ctx context.Context, sessionID string) (auth.OcculiteSession, error) {
	st, err := v.c.AuthStateOf(ctx, sessionID)
	if err != nil {
		return auth.OcculiteSession{}, err
	}
	return auth.OcculiteSession{
		Authenticated: st.Authenticated,
		User:          st.User,
		Role:          st.Role,
		AuthOff:       st.AuthOff,
		Public:        st.Public,
	}, nil
}

// ssoLogin opens a box session for user through the box's login route.
func ssoLogin(t *testing.T, c *occulited.Client, user string) string {
	t.Helper()
	res, err := c.Login(context.Background(), user, ssoPassword)
	if err != nil {
		t.Fatalf("Login(%s): %v", user, err)
	}
	// The box mints session ids with crypto/rand.Text — 26 characters of
	// A-Z and 2-7 (occulited internal/auth/auth.go IsSessionID; the gate's
	// SID_PATTERN in deploy/lighttpd/occulite-gate.lua). An id of any other
	// shape never reaches the verifier, so every later assertion would hold
	// for the wrong reason; fail here, naming the cause.
	if !ssoIsBoxSessionID(res.SID) {
		t.Fatalf("Login(%s) sid %q is not a box session id (26 chars of A-Z, 2-7)", user, res.SID)
	}
	return res.SID
}

func ssoIsBoxSessionID(v string) bool {
	if len(v) != 26 {
		return false
	}
	for i := range len(v) {
		if c := v[i]; (c < 'A' || c > 'Z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// ssoProbe runs one request carrying sid through mw and reports the
// identity the probe handler saw, if any.
func ssoProbe(t *testing.T, mw http.Handler, probe *auth.Identity, sid string) (auth.Identity, bool) {
	t.Helper()
	*probe = auth.Identity{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", http.NoBody)
	req.Header.Set(auth.OcculiteSessionHeader, sid)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("probe status = %d, want 204 (the resolver must never reject)", rec.Code)
	}
	return *probe, probe.Subject != ""
}

func TestOcculiteSSOAgainstLiteFake(t *testing.T) {
	f := startLiteFake(t, litefake.Options{
		Accounts: []litefake.Account{
			{Username: ssoAdminUser, Password: ssoPassword, Role: "admin", Level: "administer", AccountID: "acc-admin", Scopes: []string{"*"}},
			{Username: ssoPlainUser, Password: ssoPassword, Role: "user", Level: "operate", AccountID: "acc-user", Scopes: []string{"rpc:read"}},
		},
	})
	box := liteClient(t, f, "")

	var seen auth.Identity
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			seen = id
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mw := auth.OcculiteSSOPassthrough(auth.OcculiteSSOTrust{Enabled: true, Verifier: ssoVerifier{c: box}}, nil)(probe)

	t.Run("admin session resolves to admin", func(t *testing.T) {
		id, ok := ssoProbe(t, mw, &seen, ssoLogin(t, box, ssoAdminUser))
		if !ok {
			t.Fatal("no identity for a live admin session")
		}
		if id.Scheme != auth.SchemeOcculite || id.Role != auth.RoleAdmin || id.Subject != "occulite:"+ssoAdminUser {
			t.Fatalf("identity = %+v, want scheme occulite, role admin, subject occulite:%s", id, ssoAdminUser)
		}
		if !strings.HasPrefix(id.Subject, "occulite:") {
			t.Fatalf("subject %q lacks the occulite: prefix", id.Subject)
		}
	})

	t.Run("user session resolves to operator", func(t *testing.T) {
		id, ok := ssoProbe(t, mw, &seen, ssoLogin(t, box, ssoPlainUser))
		if !ok {
			t.Fatal("no identity for a live user session")
		}
		if id.Scheme != auth.SchemeOcculite || id.Role != auth.RoleOperator || id.Subject != "occulite:"+ssoPlainUser {
			t.Fatalf("identity = %+v, want scheme occulite, role operator, subject occulite:%s", id, ssoPlainUser)
		}
	})

	t.Run("never-issued session id resolves nothing", func(t *testing.T) {
		if id, ok := ssoProbe(t, mw, &seen, ssoUnissuedSID); ok {
			t.Fatalf("identity %+v for a session the box never issued", id)
		}
	})

	t.Run("auth-off box confirms nothing", func(t *testing.T) {
		// A fresh login, so the resolver's positive cache cannot answer
		// for it and the verdict must come from the box's auth-off state.
		sid := ssoLogin(t, box, ssoAdminUser)
		f.SetAuthOff(true)
		t.Cleanup(func() { f.SetAuthOff(false) })
		if id, ok := ssoProbe(t, mw, &seen, sid); ok {
			t.Fatalf("identity %+v from an auth-off answer that is not about the presented id", id)
		}
	})
}
