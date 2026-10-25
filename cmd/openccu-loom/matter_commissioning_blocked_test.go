// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
)

// TestMatterRuntimeShutsCommissioningOnABrokenAttestationChain pins the
// effect through the daemon's own Matter wiring: with a configured
// attestation chain that does not load, the node page family's window
// route answers 409, and the matter.bridge health component reports the
// cause.
func TestMatterRuntimeShutsCommissioningOnABrokenAttestationChain(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Attestation.DACPath = filepath.Join(t.TempDir(), "missing-dac.pem")
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-blocked", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-blocked")
	tracker := health.NewTracker()
	ctx, cancel := context.WithCancel(context.Background())
	wiring, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, openTestLoomDB(t), tracker, nil, nil, nil,
		slog.New(slog.DiscardHandler), nil,
	)
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	if wiring.nodeUI.Node == nil {
		t.Fatal("matter runtime produced no node on a broken attestation chain; the bridge must keep running")
	}

	// The operator's window goes through go-fabric-ui's route over the
	// node; the facade refuses it with node.ErrCommissioningBlocked, which
	// the route answers as 409 (go-fabric-ui's contract: "409 when a
	// window is open or commissioning is blocked"). The empty topology is
	// lifted so the refusal measured is the attestation's, not the
	// endpoint count's (503).
	opts := wiring.nodeUI
	opts.AllowEmptyTopology = true
	rec := serveNodeAPI(t, opts, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":300}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("POST /node/pairing/window = %d body=%s, want 409 while commissioning is blocked", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, c := range tracker.Snapshot() {
			if c.Name == matterHealthComponentName && c.Status == health.StatusUnhealthy {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("health component %q never reported unhealthy: %+v", matterHealthComponentName, tracker.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fakeMatterStatus is a listening, enabled bridge for the probe.
type fakeMatterStatus struct{}

func (fakeMatterStatus) MatterStatus(context.Context) mcp.MatterStatus {
	return mcp.MatterStatus{Enabled: true, Listening: true}
}

// TestMatterHealthProbeReportsBlockedCommissioning: a listening bridge whose
// commissioning is shut reports unhealthy with the cause; without a cause
// it reports healthy.
func TestMatterHealthProbeReportsBlockedCommissioning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		blocked error
		want    health.Status
	}{
		{"blocked", errors.New("attestation: open dac.pem: no such file"), health.StatusUnhealthy},
		{"open", nil, health.StatusHealthy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tracker := health.NewTracker()
			probeMatterOnce(context.Background(), fakeMatterStatus{}, tc.blocked, tracker)
			var got health.Status
			for _, c := range tracker.Snapshot() {
				if c.Name == matterHealthComponentName {
					got = c.Status
				}
			}
			if got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}
