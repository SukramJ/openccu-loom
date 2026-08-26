// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// multiChannelDescriptions builds one device with n channels, the shape a
// re-ingest after a CCU reboot replays: every channel is rebuilt inside a
// device the north-bound surfaces are already serving.
func multiChannelDescriptions(deviceAddr string, n int) []hmproto.DeviceDescription {
	out := make([]hmproto.DeviceDescription, 0, n+1)
	out = append(out, hmproto.DeviceDescription{Address: deviceAddr, Type: "HmIP-STH"})
	for i := 1; i <= n; i++ {
		out = append(out, hmproto.DeviceDescription{
			Address: deviceAddr + ":" + strconv.Itoa(i),
			Parent:  deviceAddr,
			Type:    "LEVEL",
		})
	}
	return out
}

// TestIngestIsSafeAgainstConcurrentChannelReaders drives the two goroutines a
// running daemon actually has: the bring-up / hot-plug goroutine re-ingesting
// a device that is already in the model registry, and a north-bound reader
// walking the same device's channels to assemble a response.
//
// The re-ingest replaces every channel of a live device. If the pipeline
// publishes a channel before it has filled in the operator-assigned name,
// rooms, functions and ise-id, the reader observes a half-built channel — a
// torn slice header at best, an out-of-range read at worst — and on the
// re-ingest path the replacement channel is briefly live and blank. Run under
// -race this fails on the unsynchronised version; the assertions below also
// catch the blank-channel window without the detector.
func TestIngestIsSafeAgainstConcurrentChannelReaders(t *testing.T) {
	t.Parallel()

	const deviceAddr = "0001ABCD"
	const channels = 12

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// The rooms / functions / names the ingest stamps onto every channel.
	names := map[string]string{}
	rooms := map[string][]string{}
	functions := map[string][]string{}
	descs := multiChannelDescriptions(deviceAddr, channels)
	for i := range descs {
		addr := descs[i].Address
		if descs[i].Parent == "" {
			continue
		}
		names[addr] = "Kanal " + addr
		rooms[addr] = []string{"Wohnzimmer", "Flur"}
		functions[addr] = []string{"Licht"}
		c.DeviceDetails.AddAddressISEID(addr, 1000+i)
	}
	p := NewDevicePipeline(c).WithNames(names).WithRooms(rooms).WithFunctions(functions)

	// Seed the model once so the reader has something to walk and the loop
	// below exercises the re-ingest path (ensureDevice returns the existing
	// device, every channel is replaced in place).
	if err := p.Ingest(context.Background(), "ccu-01-HmIP-RF", hmenum.InterfaceHmIPRF, descs); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	const rounds = 50
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Go(func() {
		defer close(stop)
		for range rounds {
			if err := p.Ingest(context.Background(), "ccu-01-HmIP-RF", hmenum.InterfaceHmIPRF, descs); err != nil {
				t.Errorf("Ingest: %v", err)
				return
			}
		}
	})

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			dev, ok := c.ModelRegistry.Get(deviceAddr)
			if !ok {
				continue
			}
			for _, ch := range dev.Channels() {
				// Exactly what the REST channel summary, the MQTT discovery
				// builder and the alarm candidate scan read.
				if got := ch.Name(); got != names[ch.Address] {
					t.Errorf("channel %s published with name %q, want %q",
						ch.Address, got, names[ch.Address])
					return
				}
				if got := ch.Rooms(); len(got) != 2 {
					t.Errorf("channel %s published with rooms %v, want 2 entries", ch.Address, got)
					return
				}
				if got := ch.Functions(); len(got) != 1 {
					t.Errorf("channel %s published with functions %v, want 1 entry", ch.Address, got)
					return
				}
				if ch.IseID() == 0 {
					t.Errorf("channel %s published without an ise-id", ch.Address)
					return
				}
				_ = ch.Info()
			}
		}
	})

	wg.Wait()
}
