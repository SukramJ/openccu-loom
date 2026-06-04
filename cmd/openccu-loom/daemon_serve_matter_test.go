// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestDaemonServe_WithMatterEnabled starts the daemon with Matter enabled
// and a temp data dir so the full Matter wiring path is exercised.
func TestDaemonServe_WithMatterEnabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Discriminator = 0xF00
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

// TestDaemonServe_WithMQTTEnabled starts the daemon with MQTT enabled
// so the MQTT wiring paths (lifecycle, wiring) are exercised.
func TestDaemonServe_WithMQTTEnabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "" // no broker → noop client
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}
