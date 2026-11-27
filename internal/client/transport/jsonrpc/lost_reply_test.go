// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// droppingServer answers every JSON-RPC call, except that the first request
// of each method in drop is read in full and then answered by closing the
// connection: the CCU received the call and the reply was lost.
type droppingServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls map[string]int
}

func newDroppingServer(t *testing.T, drop ...string) *droppingServer {
	t.Helper()
	dropSet := make(map[string]bool, len(drop))
	for _, m := range drop {
		dropSet[m] = true
	}
	s := &droppingServer{calls: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls[env.Method]++
		n := s.calls[env.Method]
		s.mu.Unlock()
		if n == 1 && dropSet[env.Method] {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okResult("session-or-result"))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *droppingServer) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCallResendsAReadOnceAfterALostReply(t *testing.T) {
	t.Parallel()
	srv := newDroppingServer(t, "SysVar.getAll")
	c, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctxTimeout(t), "SysVar.getAll", nil, nil); err != nil {
		t.Fatalf("Call = %v, want the resent read to succeed", err)
	}
	if n := srv.count("SysVar.getAll"); n != 2 {
		t.Fatalf("SysVar.getAll reached the CCU %d times, want 2 (one lost reply, one resend)", n)
	}
}

func TestCallSendsAWriteOnceAfterALostReply(t *testing.T) {
	t.Parallel()
	srv := newDroppingServer(t, "Program.execute")
	c, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Call(ctxTimeout(t), "Program.execute", map[string]any{"id": "1234"}, nil)
	if !errors.Is(err, hmerr.ErrReplyLost) {
		t.Fatalf("Call = %v, want ErrReplyLost", err)
	}
	if n := srv.count("Program.execute"); n != 1 {
		t.Fatalf("Program.execute reached the CCU %d times, want exactly 1", n)
	}
}

// ReGa.runScript is one method for every script, so the method alone never
// allows a resend; only the caller's RepeatAfterLostReply does.
func TestCallResendsAScriptOnlyWhenMarkedRepeatable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts []CallOption
		want int
	}{
		{"unmarked script runs once", nil, 1},
		{"read-only script is resent", []CallOption{RepeatAfterLostReply()}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newDroppingServer(t, "ReGa.runScript")
			c, err := New(Config{Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_ = c.Call(ctxTimeout(t), "ReGa.runScript", map[string]any{"script": "Write(1);"}, nil, tc.opts...)
			if n := srv.count("ReGa.runScript"); n != tc.want {
				t.Fatalf("ReGa.runScript reached the CCU %d times, want %d", n, tc.want)
			}
		})
	}
}

func TestLoginSendsSessionLoginOnceAfterALostReply(t *testing.T) {
	t.Parallel()
	srv := newDroppingServer(t, "Session.login")
	c, err := New(Config{Endpoint: srv.URL, Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(ctxTimeout(t)); err == nil {
		t.Fatal("Login succeeded although its reply was lost")
	}
	if n := srv.count("Session.login"); n != 1 {
		t.Fatalf("Session.login reached the CCU %d times, want exactly 1", n)
	}
}

// A connection that was never established is not a lost reply: Call
// returns it without the resend, and it reads as ErrNoConnection only.
func TestCallReturnsARefusedConnectionAsNoConnection(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c, err := New(Config{Endpoint: "http://" + addr + "/api/homematic.cgi"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Call(ctxTimeout(t), "SysVar.getAll", nil, nil)
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("Call = %v, want ErrNoConnection", err)
	}
	if errors.Is(err, hmerr.ErrReplyLost) {
		t.Fatalf("Call = %v, a refused dial must not read as ErrReplyLost", err)
	}
}
