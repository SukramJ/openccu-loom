// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// interface_ports_test.go ingests from a CCU that serves each interface
// process on its own port, the way a real one does: rfd answers
// BidCos-RF on 2001, the HMIPServer answers HomeMatic IP on 2010, and
// each knows only the devices of its protocol family.
//
// The simulator used to serve every device from one endpoint, so a
// per-interface ingest could not go wrong: whichever port it asked, it
// got the whole fleet. The daemon runs one client per (central,
// interface) pair and files what comes back under that interface, so a
// fleet that does not partition is the one shape in which a
// mis-attributed device cannot show up.

package integration

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPerInterfaceIngestFilesDevicesUnderTheirOwnInterface drives the
// production ingest once per interface process and asserts each
// central-side interface holds only its own family.
//
// Filing a HomeMatic IP device under BidCos-RF is not a cosmetic error:
// the interface decides which client carries its writes, so the write
// goes out on a transport the device does not answer on, and the
// failure surfaces as a device that ignores commands rather than as a
// wiring mistake.
func TestPerInterfaceIngestFilesDevicesUnderTheirOwnInterface(t *testing.T) {
	ports := map[string]int{
		"BidCos-RF": godevccu.EphemeralPort,
		"HmIP-RF":   godevccu.EphemeralPort,
	}
	v, err := godevccu.New(godevccu.Config{
		Mode:           godevccu.BackendModeHomegear,
		Host:           "127.0.0.1",
		XMLRPCPort:     godevccu.EphemeralPort,
		Devices:        []string{"HmIP-BSM", "HmIP-BROLL", "HM-LC-Sw1-Pl", "HM-LC-Bl1-FM"},
		InterfacePorts: ports,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	cases := []struct {
		name      string
		iface     hmenum.Interface
		wantModel func(string) bool
	}{
		{
			name:      "HmIP-RF",
			iface:     hmenum.InterfaceHmIPRF,
			wantModel: func(m string) bool { return strings.HasPrefix(m, "HmIP-") },
		},
		{
			name:      "BidCos-RF",
			iface:     hmenum.InterfaceBidCosRF,
			wantModel: func(m string) bool { return strings.HasPrefix(m, "HM-") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, ok := v.InterfaceAddr(tc.name).(*net.TCPAddr)
			if !ok || addr == nil {
				t.Fatalf("interface %s has no listener of its own: %v", tc.name, v.InterfaceAddr(tc.name))
			}

			client := newXMLRPCClient(t, "http://"+addr.String()+"/")
			backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: client}, nil, nil)

			c, err := central.New(central.Config{Name: "ccu-1"})
			if err != nil {
				t.Fatalf("central: %v", err)
			}
			pipeline := adapter.NewDevicePipeline(c)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			logger := slog.New(slog.DiscardHandler)
			if err := pipeline.IngestFromBackend(ctx, tc.name, tc.iface, backend, nil, nil, logger); err != nil {
				t.Fatalf("ingest from %s: %v", tc.name, err)
			}

			devices := c.ModelRegistry.List()
			if len(devices) == 0 {
				t.Fatalf("%s served no devices; a listener that answers with an empty fleet is "+
					"indistinguishable from one the daemon never reached", tc.name)
			}
			for _, d := range devices {
				if !tc.wantModel(d.Model) {
					t.Errorf("%s served %s (%s), which belongs to the other protocol family — "+
						"the interface decides which client carries a write, so the command "+
						"goes out on a transport the device does not answer on",
						tc.name, d.Address, d.Model)
				}
			}
		})
	}
}
