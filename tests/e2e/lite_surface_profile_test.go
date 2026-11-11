// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestBoxShellSessionSelectsTheLiteProfile pins, through the built binary,
// which surface profile a request gets on an openccu-lite box: the lite
// profile for a session the box confirms, the full standalone profile for
// someone signed in with a daemon account — on the direct port or inside
// the shell frame — and nothing for a session id the box does not know.
//
// The handler test hands the resolver an identity it made up, which proves
// only that the classifier honours one. Here the identity has to come out
// of the daemon's own authentication chain, verified against the box.
//
// Not parallel: the daemon child inherits the test process environment,
// which t.Setenv steers.
func TestBoxShellSessionSelectsTheLiteProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	box, err := litefake.Start(ctx, litefake.Options{
		Accounts: []litefake.Account{{Username: "anna", Password: "box-secret", Role: "admin", Level: "administer"}},
	})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = box.Close() })

	dir := t.TempDir()
	version := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(version, []byte("VERSION=1.0.0\nVARIANT=lite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "openccu-loom.api")
	if err := os.WriteFile(token, []byte(litefake.DefaultToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCCU_LOOM_DEPLOYMENT", "ccu-addon")
	t.Setenv("OPENCCU_LOOM_SUPERVISOR", "1")
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_VERSION_FILE", version)
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_TOKEN_FILE", token)
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_BASE_URL", box.URL())

	h := harness.Start(t, harness.Options{NoCentral: true})
	sid := boxLogin(t, box, "anna", "box-secret")

	cases := []struct {
		name        string
		session     string // the header the box gate adds
		daemonLogin bool   // a credential of the daemon's own
		wantStatus  int
		wantProfile string
		wantOrigin  string
		wantUsers   bool
	}{
		{
			name: "through the box shell", session: sid,
			wantStatus: http.StatusOK, wantProfile: "lite", wantOrigin: "lite_shell", wantUsers: false,
		},
		{
			name: "direct, with a daemon account", daemonLogin: true,
			wantStatus: http.StatusOK, wantProfile: "standalone", wantOrigin: "direct", wantUsers: true,
		},
		{
			// Signed in to the daemon inside the shell frame: the daemon's
			// accounts are the login, so its account tabs must stay.
			name: "daemon account inside the shell frame", session: sid, daemonLogin: true,
			wantStatus: http.StatusOK, wantProfile: "standalone", wantOrigin: "direct", wantUsers: true,
		},
		{
			// Same header, same shape, but the box never issued it: no
			// identity, so no payload at all — and no lite profile.
			name: "a session the box does not know", session: flipLastHex(sid),
			wantStatus: http.StatusUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, got := fetchSurfaces(t, h, tc.session, tc.daemonLogin)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if status != http.StatusOK {
				return
			}
			if got.Profile != tc.wantProfile {
				t.Errorf("profile = %q, want %q", got.Profile, tc.wantProfile)
			}
			if got.Origin != tc.wantOrigin {
				t.Errorf("origin = %q, want %q", got.Origin, tc.wantOrigin)
			}
			users, known := got.Effective["settings.users"]
			if !known {
				t.Fatal("effective map carries no settings.users")
			}
			if users != tc.wantUsers {
				t.Errorf("settings.users visible = %v, want %v", users, tc.wantUsers)
			}
			// The device editor is what the UI is on the box for.
			if !got.Effective["device.configure"] {
				t.Error("device.configure is hidden")
			}
		})
	}
}

type surfacesPayload struct {
	Profile   string          `json:"profile"`
	Origin    string          `json:"origin"`
	Effective map[string]bool `json:"effective"`
}

// fetchSurfaces reads GET /ui/surfaces: with the box session header the
// gate would add when session is set, and with the harness's own admin
// credential when daemonLogin is set.
func fetchSurfaces(t *testing.T, h *harness.Harness, session string, daemonLogin bool) (int, surfacesPayload) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.RESTBase()+"/api/v1/ui/surfaces", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		req.Header.Set("X-Occulite-Session", session)
	}
	if daemonLogin {
		req.SetBasicAuth(harness.AdminUser, harness.AdminPass)
	}
	resp, err := h.REST().HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/ui/surfaces: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out surfacesPayload
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, out
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode surfaces: %v", err)
	}
	return resp.StatusCode, out
}

// boxLogin signs in at the box as its shell does and returns the session id.
func boxLogin(t *testing.T, box *litefake.Fake, user, pass string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": user, "password": pass})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, box.URL()+"/api/auth/v1/login", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := box.Client().Do(req)
	if err != nil {
		t.Fatalf("box login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("box login: status %d", resp.StatusCode)
	}
	var out struct {
		SID string `json:"sid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.SID == "" {
		t.Fatalf("box login: no session id (%v)", err)
	}
	return out.SID
}

// flipLastHex returns sid with its last hex digit changed: a well-formed
// session id the box did not issue.
func flipLastHex(sid string) string {
	last := sid[len(sid)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	return sid[:len(sid)-1] + string(repl)
}
