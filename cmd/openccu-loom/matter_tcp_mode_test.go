// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/mdns"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterTCPModeMapsOntoBridgeRoles: the four config values map onto
// the bridge's TCP roles. The session parameters that advertise those
// roles to a peer (SupportedTransports in Sigma2 and PBKDFParamResponse)
// are the node facade's, derived from node.Network.TCP (go-fabric #89).
func TestMatterTCPModeMapsOntoBridgeRoles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode string
		want matterbridge.TCPConfig
	}{
		{config.MatterTCPOff, matterbridge.TCPConfig{}},
		{config.MatterTCPIncoming, matterbridge.TCPConfig{Incoming: true}},
		{config.MatterTCPOutgoing, matterbridge.TCPConfig{Outgoing: true}},
		{config.MatterTCPBoth, matterbridge.TCPConfig{Incoming: true, Outgoing: true}},
	}
	if len(cases) != len(config.MatterTCPModes) {
		t.Fatalf("table covers %d modes, config.MatterTCPModes lists %d", len(cases), len(config.MatterTCPModes))
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			if got := matterTCPConfig(tc.mode); got != tc.want {
				t.Fatalf("matterTCPConfig(%q) = %+v, want %+v", tc.mode, got, tc.want)
			}
		})
	}
}

// TestMatterTCPModeUnknownValueFallsBackToOff pins the consumer's
// fail-closed reading of a value the validator would have refused.
func TestMatterTCPModeUnknownValueFallsBackToOff(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "Incoming", "true"} {
		if got := matterTCPConfig(mode); got != (matterbridge.TCPConfig{}) {
			t.Errorf("matterTCPConfig(%q) = %+v, want TCP off", mode, got)
		}
	}
}

// TestMatterTCPModeReachesTheRunningBridge boots the bridge through
// startMatterBridge — the daemon's production path — once per mode and
// observes the effect from outside: whether a TCP connection to the
// bridge's bound port is accepted, and whether the operational mDNS
// record carries the T key. `off` is the negative control for both: it
// must fail the same assertions `incoming` passes.
func TestMatterTCPModeReachesTheRunningBridge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode       string
		acceptsTCP bool
		wantT      string // "" = key absent
	}{
		{config.MatterTCPOff, false, ""},
		{config.MatterTCPIncoming, true, "4"},
		{config.MatterTCPOutgoing, false, "2"},
		{config.MatterTCPBoth, true, "6"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.North.Matter.Enabled = true
			cfg.North.Matter.MDNSAdvertise = "noop"
			cfg.North.Matter.Listen = ":0"
			cfg.North.Matter.TCP = tc.mode
			cfg.DataDir = t.TempDir()
			cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}}}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}

			reg := buildTestRegistry(t, "ccu-01")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			db := openTestLoomDB(t)
			bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
			if bundle == nil {
				t.Fatal("startMatterBridge returned nil")
			}
			t.Cleanup(bundle.stop)

			_, portStr, err := net.SplitHostPort(bundle.node.Bridge().LocalAddr())
			if err != nil {
				t.Fatalf("bridge LocalAddr %q: %v", bundle.node.Bridge().LocalAddr(), err)
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port == 0 {
				t.Fatalf("bridge bound no usable port: %q", bundle.node.Bridge().LocalAddr())
			}

			// (i) A TCP connection to the UDP listener's port.
			conn, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", portStr), 2*time.Second)
			if conn != nil {
				_ = conn.Close()
			}
			if accepted := dialErr == nil; accepted != tc.acceptsTCP {
				t.Errorf("TCP connect to port %d: accepted=%v (err=%v), want accepted=%v", port, accepted, dialErr, tc.acceptsTCP)
			}

			// (ii) The operational record. AnnounceFabric is what the
			// daemon calls once a fabric is installed; the record is read
			// back from the advertiser the daemon built.
			if bundle.node.MDNSEnabled() {
				t.Fatal("the node publishes on the network, want its no-op advertiser for mdns_advertise=noop")
			}
			bundle.node.Bridge().AnnounceFabric(ctx, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, 0x1122)
			var op *mdns.Service
			for _, svc := range bundle.node.MDNSServices() {
				if svc.ServiceType == mdns.ServiceTypeOperational {
					op = &svc
					break
				}
			}
			if op == nil {
				t.Fatalf("no operational _matter._tcp record published; active = %+v", bundle.node.MDNSServices())
			}
			gotT, present := "", false
			for _, r := range op.TXT {
				if r.Key == "T" {
					gotT, present = r.Value, true
				}
			}
			switch {
			case tc.wantT == "" && present:
				t.Errorf("operational TXT carries T=%q, want the key absent", gotT)
			case tc.wantT != "" && !present:
				t.Errorf("operational TXT lacks T, want T=%q (TXT=%+v)", tc.wantT, op.TXT)
			case tc.wantT != "" && gotT != tc.wantT:
				t.Errorf("operational TXT T=%q, want %q", gotT, tc.wantT)
			}
		})
	}
}
