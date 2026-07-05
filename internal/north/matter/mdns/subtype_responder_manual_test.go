// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestManualSubtypeRepro publishes a commissionable service through the
// production Zeroconf + SubtypeResponder path and keeps it alive for 60 s
// so an external `dns-sd -B _matterc._udp,_L3840` can probe the subtype
// PTR answer on the local network. Gated behind an env var — this emits
// real multicast and needs a human (or script) watching from outside.
func TestManualSubtypeRepro(t *testing.T) {
	if os.Getenv("OPENCCU_SUBTYPE_REPRO") == "" {
		t.Skip("manual repro; set OPENCCU_SUBTYPE_REPRO=1 to run")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	z := NewZeroconf()
	r, err := NewSubtypeResponder(logger)
	if err != nil {
		t.Fatalf("NewSubtypeResponder: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	z.AttachSubtypeResponder(r)
	defer func() { _ = z.Close() }()

	svc := BuildCommissionableService(CommissionableServiceConfig{
		InstanceID:        [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x01, 0x02},
		Discriminator:     3840,
		VendorID:          0xFFF1,
		ProductID:         0x8000,
		CommissioningMode: 1,
		DeviceTypeID:      0x0016,
		DeviceName:        "repro",
		Port:              15540,
	})
	if err := z.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	t.Logf("published instance=%s subtypes=%v — probe now", svc.InstanceName, svc.Subtypes)
	time.Sleep(60 * time.Second)
}
