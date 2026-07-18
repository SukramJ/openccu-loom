// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// The CCU's session pool is a small, shared, daemon-external resource: it is
// the same pool the WebUI logs into. A daemon that strands sessions does not
// merely degrade itself — it locks the operator out of their own CCU with
// "invalid credentials or too many sessions".
//
// These tests pin the session wire contract against a CCU stand-in that
// answers exactly as the firmware's session handlers do
// (WebUI/www/api/methods/session/{login,renew,logout}.tcl):
//
//	Session.login  -> JSON string  (the session id)
//	Session.renew  -> JSON boolean true; extends IN PLACE, no new id
//	Session.logout -> JSON boolean true
//
// The return TYPES are the load-bearing part. Decoding Session.renew as
// anything but a boolean makes every renewal look like a failure, and the
// client then abandons a healthy session and opens a replacement — one leaked
// CCU session per freshness window, forever.

// ccuSessionStub is a CCU stand-in that tracks its session pool.
type ccuSessionStub struct {
	mu      sync.Mutex
	logins  int
	renews  int
	logouts int
	seq     int
	open    map[string]bool
}

func (s *ccuSessionStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	s.open = map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch env.Method {
		case "Session.login":
			s.logins++
			s.seq++
			sid := fmt.Sprintf("sid-%d", s.seq)
			s.open[sid] = true
			_, _ = fmt.Fprintf(w, `{"result":%q,"error":null}`, sid)
		case "Session.renew":
			s.renews++
			_, _ = fmt.Fprint(w, `{"result":true,"error":null}`)
		case "Session.logout":
			s.logouts++
			if sid, ok := env.Params["_session_id_"].(string); ok {
				delete(s.open, sid)
			}
			_, _ = fmt.Fprint(w, `{"result":true,"error":null}`)
		default:
			_, _ = fmt.Fprint(w, `{"result":null,"error":null}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *ccuSessionStub) openSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

// TestJSONRPCClientHoldsExactlyOneCCUSession locks the core invariant: one
// jsonrpc.Client occupies exactly ONE slot in the CCU's session pool for its
// entire life, no matter how many calls it makes or how long it runs. The
// client renews that session rather than replacing it.
//
// A regression here is invisible in a unit test that mocks the transport and
// only shows up as a CCU that slowly runs out of sessions — hence the
// contract test.
func TestJSONRPCClientHoldsExactlyOneCCUSession(t *testing.T) {
	stub := &ccuSessionStub{}
	c, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: stub.server(t).URL,
		Username: "Admin",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx := context.Background()

	// Cold start plus a long run of calls. Renew() is exercised directly so
	// the test does not have to wait out the freshness window in wall time;
	// it is the same round-trip Call() makes once the window has elapsed.
	if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
		t.Fatalf("cold call: %v", err)
	}
	sid := c.SessionID()
	if sid == "" {
		t.Fatal("no session established after the first call")
	}

	for i := range 20 {
		if err := c.Renew(ctx); err != nil {
			t.Fatalf("renew %d: %v", i+1, err)
		}
		if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}

	if got := c.SessionID(); got != sid {
		t.Errorf("session id changed from %q to %q — Session.renew extends in place and must not mint a new session", sid, got)
	}
	stub.mu.Lock()
	logins := stub.logins
	stub.mu.Unlock()
	if logins != 1 {
		t.Errorf("Session.login called %d times over the client's life, want exactly 1 — every extra login burns a CCU session slot", logins)
	}
	if open := stub.openSessions(); open != 1 {
		t.Errorf("%d sessions left open on the CCU, want 1", open)
	}
}

// TestJSONRPCClientReleasesSessionOnLogout locks that a client which shuts
// down hands its slot back. The central's teardown path relies on this.
func TestJSONRPCClientReleasesSessionOnLogout(t *testing.T) {
	stub := &ccuSessionStub{}
	c, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: stub.server(t).URL,
		Username: "Admin",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if open := stub.openSessions(); open != 1 {
		t.Fatalf("%d sessions open after login, want 1", open)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if open := stub.openSessions(); open != 0 {
		t.Errorf("%d sessions still open on the CCU after Logout, want 0", open)
	}
	if c.SessionID() != "" {
		t.Errorf("session id %q survived Logout", c.SessionID())
	}
}
