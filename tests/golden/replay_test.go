// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

var updateGolden = flag.Bool("update", false, "update golden fixtures")

// graphSnapshot is the shape we compare against the golden file.
type graphSnapshot struct {
	Devices []graphDevice `json:"devices"`
}

type graphDevice struct {
	Address  string   `json:"address"`
	Model    string   `json:"model"`
	Firmware string   `json:"firmware"`
	Channels []string `json:"channels"`
}

// TestReplayListDevicesSmall feeds a recorded listDevices reply into
// the DevicePipeline and compares the resulting graph to a golden
// fixture. `-update` rewrites the fixture from the current output.
func TestReplayListDevicesSmall(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(thisFile)
	inputPath := filepath.Join(root, "sessions", "list_devices_small.json")
	goldenPath := filepath.Join(root, "sessions", "expected_graph.json")

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	var descsRaw []map[string]any
	if err := json.Unmarshal(raw, &descsRaw); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	descs := make([]hmproto.DeviceDescription, 0, len(descsRaw))
	for _, m := range descsRaw {
		buf, _ := json.Marshal(m)
		var dd hmproto.DeviceDescription
		if err := json.Unmarshal(buf, &dd); err != nil {
			t.Fatalf("dd unmarshal: %v", err)
		}
		descs = append(descs, dd)
	}

	c, _ := central.New(central.Config{Name: "replay"})
	p := adapter.NewDevicePipeline(c)
	if err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, descs); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	snap := graphSnapshot{}
	for _, d := range c.ModelRegistry.List() {
		gd := graphDevice{Address: d.Address, Model: d.Model, Firmware: d.Firmware().Info().Current}
		for _, ch := range d.Channels() {
			gd.Channels = append(gd.Channels, ch.Address)
		}
		sort.Strings(gd.Channels)
		snap.Devices = append(snap.Devices, gd)
	}

	got, _ := json.MarshalIndent(snap, "", "  ")
	got = append(got, '\n')

	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil { //nolint:gosec // fixture file
			t.Fatalf("update: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	// Strip CRs so the byte-exact comparison survives Windows checkouts
	// with core.autocrlf=true (LF→CRLF rewrite at clone time).
	if !bytes.Equal(got, stripCR(want)) {
		t.Fatalf("golden mismatch:\nWANT:\n%s\n\nGOT:\n%s", want, got)
	}
}
