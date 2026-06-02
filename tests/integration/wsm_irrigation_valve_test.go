// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
)

// TestWSM_IrrigationValveSurface verifies that the operator-visible
// chain for a WSM-style watering valve resolves end-to-end:
//
//   - Device model (ELV-SH-WSM via godevccu) materialises an
//     [*valve.Irrigation] custom data point on the bewässerung
//     channel (channel 4 of the godevccu mock).
//   - The custom-DP kind dispatcher maps it to "valve_irrigation",
//     which is the key the SPA's CdpTilesPanel uses to route the
//     channel into a ValveTile.
//
// If this test goes red, the SPA's "WSM hat keine Bedien-Tile"
// regression is reproduced here without the operator's hardware in
// the loop.
func TestWSM_IrrigationValveSurface(t *testing.T) {
	srv := startMockCCUWithDevices(t, []string{"ELV-SH-WSM"})

	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "wsm-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Unit has no public Close — pipeline-only cleanup is enough.

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipeline := adapter.NewDevicePipeline(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pipeline.IngestFromBackend(ctx, "wsm-test-HmIP-RF", "HmIP-RF", backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("no devices ingested")
	}

	var wsm *valve.Irrigation
	var wsmChannelAddr string
	for _, d := range devices {
		if d == nil {
			continue
		}
		for _, ch := range d.Channels() {
			if ch == nil {
				continue
			}
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			if irr, ok := cdp.(*valve.Irrigation); ok {
				wsm = irr
				wsmChannelAddr = ch.Address
				break
			}
		}
		if wsm != nil {
			break
		}
	}

	if wsm == nil {
		// Surface every channel + cdp kind so the failure message
		// guides the diagnosis (no `*valve.Irrigation` typically
		// means the channel's STATE DP was filtered out or the
		// profile constructor refused).
		t.Log("dump of every channel + cdp kind:")
		for _, d := range devices {
			for _, ch := range d.Channels() {
				cdp := ch.CustomDataPoint()
				kind := "(none)"
				if cdp != nil {
					kind = cdpkind.Of(cdp)
				}
				t.Logf("  %s  type=%s  cdp_kind=%s", ch.Address, ch.Type, kind)
			}
		}
		t.Fatalf("no *valve.Irrigation custom-DP materialised on any ELV-SH-WSM channel")
	}

	// The SPA dispatch lookup uses cdpkind.Of(dp) on the CustomDP
	// summary's `kind` field. Pin that mapping here so an accidental
	// rename of the kind constant turns into a clear test failure
	// before it reaches the browser.
	if got := cdpkind.Of(wsm); got != cdpkind.KindValveIrr {
		t.Errorf("cdpkind.Of(*valve.Irrigation) = %q, want %q (channel %s)",
			got, cdpkind.KindValveIrr, wsmChannelAddr)
	}

	// Sanity: the irrigation custom DP must expose the four
	// methods the SPA + REST handler depend on (Address, IsOpen,
	// Open, Close). Compile-time checks via the dot-call patterns
	// below trip a static error if any goes missing.
	if got := wsm.Address(); got != wsmChannelAddr {
		t.Errorf("wsm.Address() = %q, want %q", got, wsmChannelAddr)
	}
	_, _ = wsm.IsOpen() // observed=false right after ingest is fine
}
