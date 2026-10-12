// SPDX-License-Identifier: MIT
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
	"github.com/SukramJ/go-fabric/secure/sessionparams"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterTCPModeMapsOntoBridgeRoles pins, for every accepted value of
// north.matter.tcp, the bridge roles the daemon configures and the
// transports both handshakes state: the TCP roles and SupportedTransports
// bitmap, plus the MaxTcpMessageSize that accompanies any TCP role.
func TestMatterTCPModeMapsOntoBridgeRoles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode    string
		want    matterbridge.TCPConfig
		bitmap  uint16
		maxSize uint32
	}{
		{config.MatterTCPOff, matterbridge.TCPConfig{}, 0, 0},
		{config.MatterTCPIncoming, matterbridge.TCPConfig{Incoming: true}, sessionparams.TransportTCPServer, sessionparams.DefaultMaxTCPMessageSize},
		{config.MatterTCPOutgoing, matterbridge.TCPConfig{Outgoing: true}, sessionparams.TransportTCPClient, sessionparams.DefaultMaxTCPMessageSize},
		{
			config.MatterTCPBoth,
			matterbridge.TCPConfig{Incoming: true, Outgoing: true},
			sessionparams.TransportTCPServer | sessionparams.TransportTCPClient, sessionparams.DefaultMaxTCPMessageSize,
		},
	}
	if len(cases) != len(config.MatterTCPModes) {
		t.Fatalf("table covers %d modes, config.MatterTCPModes lists %d", len(cases), len(config.MatterTCPModes))
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			got := matterTCPConfig(tc.mode)
			if got != tc.want {
				t.Fatalf("matterTCPConfig(%q) = %+v, want %+v", tc.mode, got, tc.want)
			}
			if bits := matterTransportsBitmap(got); bits != tc.bitmap {
				t.Errorf("matterTransportsBitmap = %#x, want %#x", bits, tc.bitmap)
			}
			p := paseResponderParams(tc.mode)
			if p.SupportedTransports != tc.bitmap {
				t.Errorf("paseResponderParams.SupportedTransports = %#x, want %#x", p.SupportedTransports, tc.bitmap)
			}
			if p.MaxTCPMessageSize != tc.maxSize {
				t.Errorf("paseResponderParams.MaxTCPMessageSize = %d, want %d", p.MaxTCPMessageSize, tc.maxSize)
			}
			// The MRP triplet must survive the transports being added.
			if p.SessionIdleInterval != 500 || p.SessionActiveInterval != 300 || p.SessionActiveThreshold != 4000 {
				t.Errorf("MRP triplet = %d/%d/%d, want 500/300/4000",
					p.SessionIdleInterval, p.SessionActiveInterval, p.SessionActiveThreshold)
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

			_, portStr, err := net.SplitHostPort(bundle.bridge.LocalAddr())
			if err != nil {
				t.Fatalf("bridge LocalAddr %q: %v", bundle.bridge.LocalAddr(), err)
			}
			port, err := strconv.Atoi(portStr)
			if err != nil || port == 0 {
				t.Fatalf("bridge bound no usable port: %q", bundle.bridge.LocalAddr())
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
			noop, ok := bundle.advertiser.(*mdns.Noop)
			if !ok {
				t.Fatalf("advertiser is %T, want *mdns.Noop for mdns_advertise=noop", bundle.advertiser)
			}
			bundle.bridge.AnnounceFabric(ctx, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, 0x1122)
			var op *mdns.Service
			for _, svc := range noop.Active() {
				if svc.ServiceType == mdns.ServiceTypeOperational {
					op = &svc
					break
				}
			}
			if op == nil {
				t.Fatalf("no operational _matter._tcp record published; active = %+v", noop.Active())
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
