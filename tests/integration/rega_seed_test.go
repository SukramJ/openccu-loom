// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The boot seed is the one path that decides whether a freshly started
// daemon knows any values at all. Every data point starts unobserved;
// `fetch_all_device_data` is what fills them before the first event
// arrives, and a device whose value never arrives shows as unavailable
// on every north-bound plane until something happens to it.
//
// Until the simulator returned the script's real shape — a JSON object
// keyed `<iface>.<channel>.<param>`, percent-encoded — the path could
// not be exercised hermetically at all: the simulator answered with an
// array, so the run stopped at the parse step and the daemon logged
// `pipeline.seed.failed` on every boot of the test suite. The tests
// below run the production ingest and assert on the model it leaves
// behind.

// seedFixture is one entry the simulator's ReGa engine will serve.
type seedFixture struct {
	channel string
	param   string
	value   any
}

// TestRegaSeedReachesTheModel drives the production ingest — the same
// call the daemon makes when a central comes up — and asserts the
// seeded values land on the data points, decoded.
func TestRegaSeedReachesTheModel(t *testing.T) {
	const (
		blindDevice  = "VCU8537918" // HmIP-BROLL
		blindChannel = blindDevice + ":4"
	)

	m := startMockCCUOpenCCU(t)
	// The value cache the ReGa engine reads is disjoint from the RPC
	// write path by design (a CCU seeds it from its own persistence,
	// which the simulator has no equivalent of), so the fixture is
	// written into it directly.
	for _, f := range []seedFixture{
		{blindChannel, "LEVEL", 0.42},
		{blindChannel, "ACTIVITY_STATE", 0},
	} {
		m.v.State().SetDeviceValue(f.channel, f.param, f.value)
	}

	c := ingestFromMock(t, m)

	dev, ok := c.ModelRegistry.Get(blindDevice)
	if !ok {
		t.Fatalf("device %s not in the model after ingest", blindDevice)
	}
	ch := dev.Channel(blindChannel)
	if ch == nil {
		t.Fatalf("channel %s missing", blindChannel)
	}
	dp := ch.Parameter(hmenum.ParameterLevel)
	if dp == nil {
		t.Fatalf("LEVEL data point missing on %s", blindChannel)
	}
	// A data point the seed reached carries a raw value; one it missed
	// is indistinguishable from a device that has never spoken.
	raw, ok := dp.RawValue()
	if !ok {
		t.Fatalf("LEVEL on %s is still unobserved after the boot seed — the daemon starts "+
			"blind and the entity reads unavailable until the device happens to send", blindChannel)
	}
	if got, want := toFloat(t, raw), 0.42; got != want {
		t.Errorf("LEVEL = %v, want %v — the seed reached the model but the value did not "+
			"survive the script's encoding", got, want)
	}
}

// TestRegaSeedSkipsEdgeTriggerParameters pins the exclusion that keeps
// a boot from replaying a keypress.
//
// The script emits every data point carrying a timestamp, and a button
// acquires one on its first press and keeps it forever. Seeding that
// value marks the point observed with a press that happened at some
// unknown time in the past — which the boot-time snapshot then
// publishes as though it had just occurred.
func TestRegaSeedSkipsEdgeTriggerParameters(t *testing.T) {
	const (
		blindDevice = "VCU8537918"
		keyChannel  = blindDevice + ":1" // KEY_TRANSCEIVER
	)

	m := startMockCCUOpenCCU(t)
	m.v.State().SetDeviceValue(keyChannel, string(hmenum.ParameterPressShort), true)

	c := ingestFromMock(t, m)

	dev, ok := c.ModelRegistry.Get(blindDevice)
	if !ok {
		t.Fatalf("device %s not in the model after ingest", blindDevice)
	}
	ch := dev.Channel(keyChannel)
	if ch == nil {
		t.Fatalf("channel %s missing — the fixture no longer carries the button channel this "+
			"test needs, and a skip here would read as a passing exclusion", keyChannel)
	}
	dp := ch.Parameter(hmenum.ParameterPressShort)
	if dp == nil {
		t.Fatalf("PRESS_SHORT missing on %s — without the data point the assertion below "+
			"cannot distinguish an exclusion from an absence", keyChannel)
	}
	if _, ok := dp.RawValue(); ok {
		t.Errorf("PRESS_SHORT on %s was seeded — the boot snapshot now carries a keypress "+
			"nobody made, which every subscriber replays as a fresh press", keyChannel)
	}
}

// toFloat narrows the numeric wire value a data point reports.
func toFloat(t *testing.T, v any) float64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		t.Fatalf("LEVEL raw value has type %T, want a number", v)
		return 0
	}
}

// ingestFromMock runs the production ingest against the simulator with
// a real ReGa runner wired, so the seed runs the way it does in the
// daemon rather than being invoked directly.
func ingestFromMock(t *testing.T, m *mockCCU) *central.Unit {
	t.Helper()

	runner, jsonClient := newRegaRunner(t, m.JSONRPCURL(), "Admin", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	if err := jsonClient.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}

	xmlClient := newXMLRPCClient(t, m.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c)
	logger := slog.New(slog.DiscardHandler)
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF,
		backend, nil, runner, logger); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return c
}
