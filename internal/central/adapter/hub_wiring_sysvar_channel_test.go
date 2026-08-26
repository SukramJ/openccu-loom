// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sysvarChannelServer serves SysVar.getAll plus the sysvar-description ReGa
// script so loadSysvars exercises the full description + explicit-channel
// merge. The ReGa result is swappable via regaResult so refresh scenarios
// (script degraded, assignment changed) can be simulated on one server.
type sysvarChannelServer struct {
	srv        *httptest.Server
	regaResult atomic.Value // string: raw script stdout (a JSON array)
}

func newSysvarChannelServer(t *testing.T, regaOut string) *sysvarChannelServer {
	t.Helper()
	s := &sysvarChannelServer{}
	s.regaResult.Store(regaOut)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req["method"] {
		case "SysVar.getAll":
			result = []map[string]any{
				{"id": "4711", "name": "Assigned", "type": "BOOL", "value": "false", "isInternal": false},
				{"id": "4712", "name": "Unassigned", "type": "BOOL", "value": "false", "isInternal": false},
				{"id": "4713", "name": "Legacy", "type": "BOOL", "value": "false", "isInternal": false},
			}
		case "ReGa.runScript":
			result, _ = s.regaResult.Load().(string)
		default:
			result = nil
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newSysvarChannelRunner(t *testing.T, srv *httptest.Server) (*jsonrpc.Client, *rega.Runner) {
	t.Helper()
	jc, err := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	runner, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	return jc, runner
}

// TestLoadSysvarsExplicitChannelAddress verifies the explicit-channel merge
// of the sysvar scan: a script row carrying channel_address (URL-encoded, as
// the ReGa framing emits it) lands decoded on the Sysvar; an empty
// channel_address and a row omitting the field entirely (older CCU firmwares
// and cached script results) both yield an empty explicit channel.
func TestLoadSysvarsExplicitChannelAddress(t *testing.T) {
	t.Parallel()
	s := newSysvarChannelServer(t, `[`+
		`{"id":"4711","description":"","channel_address":"000858A994D482%3A7"},`+
		`{"id":"4712","description":"","channel_address":""},`+
		`{"id":"4713","description":""}`+
		`]`)
	jc, runner := newSysvarChannelRunner(t, s.srv)

	h := hub.NewHub("c")
	if err := loadSysvars(context.Background(), jc, runner, h, nil,
		hubScanOptions{enableSysvarScan: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}

	for name, want := range map[string]string{
		"Assigned":   "000858A994D482:7",
		"Unassigned": "",
		"Legacy":     "",
	} {
		sv, ok := h.Sysvar(name)
		if !ok {
			t.Fatalf("sysvar %q not loaded", name)
		}
		if got := sv.ExplicitChannel(); got != want {
			t.Errorf("ExplicitChannel(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestLoadSysvarsExplicitChannelRefresh verifies refresh semantics of the
// explicit assignment: a changed channel_address propagates, a removed one
// clears — but a degraded script run (unparsable output) keeps the last
// known assignments instead of wiping them.
func TestLoadSysvarsExplicitChannelRefresh(t *testing.T) {
	t.Parallel()
	s := newSysvarChannelServer(t,
		`[{"id":"4711","description":"","channel_address":"000858A994D482:7"}]`)
	jc, runner := newSysvarChannelRunner(t, s.srv)

	h := hub.NewHub("c")
	opts := hubScanOptions{enableSysvarScan: true}
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (initial): %v", err)
	}
	sv, ok := h.Sysvar("Assigned")
	if !ok {
		t.Fatal("sysvar Assigned not loaded")
	}
	if got := sv.ExplicitChannel(); got != "000858A994D482:7" {
		t.Fatalf("initial ExplicitChannel = %q", got)
	}

	// Degraded script run: unparsable output → GetSystemVariableDescriptions
	// errors → the previous assignment must survive the refresh.
	s.regaResult.Store("this is not json")
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (degraded): %v", err)
	}
	if got := sv.ExplicitChannel(); got != "000858A994D482:7" {
		t.Fatalf("degraded refresh cleared the explicit channel: %q", got)
	}

	// Operator moved the assignment: the refresh must propagate it.
	s.regaResult.Store(`[{"id":"4711","description":"","channel_address":"0002EFGH:2"}]`)
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (moved): %v", err)
	}
	if got := sv.ExplicitChannel(); got != "0002EFGH:2" {
		t.Fatalf("moved assignment did not propagate: %q", got)
	}

	// Operator removed the assignment: the refresh must clear it.
	s.regaResult.Store(`[{"id":"4711","description":"","channel_address":""}]`)
	if err := loadSysvars(context.Background(), jc, runner, h, nil, opts); err != nil {
		t.Fatalf("loadSysvars (cleared): %v", err)
	}
	if got := sv.ExplicitChannel(); got != "" {
		t.Fatalf("removed assignment did not clear: %q", got)
	}
}

// TestSysvarExplicitChannelDefaultsEmpty pins the zero value: a sysvar
// constructed without any scan input reports no explicit assignment.
func TestSysvarExplicitChannelDefaultsEmpty(t *testing.T) {
	t.Parallel()
	sv := hub.NewSysvar("c", "Plain", "", hmenum.HubValueTypeString, nil)
	if got := sv.ExplicitChannel(); got != "" {
		t.Fatalf("ExplicitChannel() = %q, want empty", got)
	}
}
