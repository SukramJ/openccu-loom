// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func liteAccountsFake(t *testing.T) (*litefake.Fake, liteAccountVerifier) {
	t.Helper()
	f := startTestFake(t, litefake.Options{Accounts: []litefake.Account{
		{Username: "admin", Password: "pw-a", Level: "administer"},
		{Username: "config", Password: "pw-c", Level: "configure"},
		{Username: "operator", Password: "pw-o", Level: "operate"},
		{Username: "reader", Password: "pw-r", Level: "read"},
	}})
	c, err := occulited.New(occulited.Config{BaseURL: f.URL(), Token: litefake.DefaultToken})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	return f, liteAccountVerifier{client: c}
}

// TestLiteAccountVerifierMapsLevels pins the level mapping of a delegated
// login on openccu-lite: each account level lands on the CCU user level
// the role mapping expects, and a wrong password is an auth failure.
func TestLiteAccountVerifierMapsLevels(t *testing.T) {
	t.Parallel()
	_, v := liteAccountsFake(t)
	for _, tc := range []struct {
		user, pass string
		want       int
	}{
		{"admin", "pw-a", 8}, {"config", "pw-c", 2}, {"operator", "pw-o", 2}, {"reader", "pw-r", 1},
	} {
		got, err := v.Verify(context.Background(), tc.user, tc.pass)
		if err != nil || got != tc.want {
			t.Errorf("Verify(%s) = %d, %v; want %d", tc.user, got, err, tc.want)
		}
	}
	if _, err := v.Verify(context.Background(), "admin", "wrong"); !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Errorf("wrong password: err = %v, want ErrAuthFailure", err)
	}
}

// TestLiteAccountVerifierLogsOut pins that a delegated login leaves no
// session open on the box: the session is only proof of the credentials.
func TestLiteAccountVerifierLogsOut(t *testing.T) {
	t.Parallel()
	f, v := liteAccountsFake(t)
	if _, err := v.Verify(context.Background(), "admin", "pw-a"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n := f.Sessions(); n != 0 {
		t.Errorf("%d sessions left open on the box after the check", n)
	}
}

// TestCCUAccountVerifierUnchanged pins the CCU path of the auth domain: a
// central without an installed account verifier is checked the two-step
// way — a JSON-RPC login with the user's own credentials, then the level
// through the privileged session.
func TestCCUAccountVerifierUnchanged(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var logins []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
			ID     any            `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		var result any = true
		if req.Method == "Session.login" {
			mu.Lock()
			logins = append(logins, req.Params["username"].(string))
			mu.Unlock()
			result = "sid-1"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": req.ID, "result": result, "error": nil})
	}))
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	unit, err := central.New(central.Config{Name: "ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cc := config.CentralConfig{Name: "ccu", Host: host, JSONRPCPort: port}
	d := NewCCUAuthDomain(reg, stubResolver(map[string]config.CentralConfig{"ccu": cc}, "ccu"), nil)

	_, err = d.Verify(context.Background(), "ccu", "alice", "secret")
	mu.Lock()
	defer mu.Unlock()
	if len(logins) != 1 || logins[0] != "alice" {
		t.Fatalf("JSON-RPC logins = %v, want one with the user's own name", logins)
	}
	// The unit has no hub writer, so the second step answers that no level
	// reader is wired — which proves the level came from the second step.
	if !errors.Is(err, hub.ErrNoUserLevelReader) {
		t.Errorf("err = %v, want the level step's ErrNoUserLevelReader", err)
	}
}
