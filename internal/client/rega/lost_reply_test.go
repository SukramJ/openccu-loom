// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package rega

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newScriptDroppingServer answers ReGa.runScript with "1", except that it
// reads the first request in full and closes the connection: the CCU ran
// the script and its reply was lost.
func newScriptDroppingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var runs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if runs.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		raw, _ := json.Marshal(struct {
			Result any `json:"result"`
		}{Result: "1"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &runs
}

// The runner is where the script identity meets the JSON-RPC client, so the
// resend policy is pinned here: a read-only script runs again after a lost
// reply, a state-changing one runs exactly once.
func TestRunnerResendsOnlyReadOnlyScriptsAfterALostReply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		script  hmenum.RegaScript
		params  map[string]string
		want    int32
		wantErr bool
	}{
		{"read-only script is resent", hmenum.RegaScriptGetUserLevel, map[string]string{"username": "Admin"}, 2, false},
		{"program state change runs once", hmenum.RegaScriptSetProgramState, map[string]string{"id": "1234", "state": "1"}, 1, true},
		{"firmware update check runs once", hmenum.RegaScriptGetSystemUpdateInfo, nil, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, runs := newScriptDroppingServer(t)
			r := newRunner(t, srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := r.Run(ctx, tc.script, tc.params)
			if tc.wantErr && !errors.Is(err, hmerr.ErrReplyLost) {
				t.Fatalf("Run = %v, want ErrReplyLost", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Run = %v, want the resent script to succeed", err)
			}
			if n := runs.Load(); n != tc.want {
				t.Fatalf("%s reached the CCU %d times, want %d", tc.script, n, tc.want)
			}
		})
	}
}
