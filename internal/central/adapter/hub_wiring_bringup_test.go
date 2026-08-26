// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/config"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
)

// wireHubTestLogger keeps WireHub's warn/info stream out of the test output —
// the failure paths under test log deliberately.
func wireHubTestLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// hubWiringCentralConfig points a CentralConfig at the JSON-RPC fake. The
// endpoint is derived from Host + JSONRPCPort, so an httptest server needs
// both halves split out of its URL.
func hubWiringCentralConfig(t *testing.T, serverURL string) config.CentralConfig {
	t.Helper()
	host, port, err := splitHostPort(serverURL)
	if err != nil {
		t.Fatalf("splitHostPort(%q): %v", serverURL, err)
	}
	portNo, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q: %v", port, err)
	}
	return config.CentralConfig{Name: "ccu-01", Host: host, JSONRPCPort: portNo}
}

// TestWireHubFailsWhenDeviceNamesCannotBeLoaded pins the CCU-assigned device
// names as a hard prerequisite of the south-bound bring-up, exactly like the
// CCU serial.
//
// Swallowing the failure produced a central whose every device and channel is
// named by its raw address, with no room and no function, for the whole daemon
// run: ingest reads the name map once per device, and nothing re-applies names
// to an already-built model. The bring-up gate can re-wait and retry within
// seconds — a fleet that has lost its names cannot repair itself at all.
func TestWireHubFailsWhenDeviceNamesCannotBeLoaded(t *testing.T) {
	t.Parallel()

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		// Everything the wiring needs before the name load answers; only
		// Device.listAllDetail is missing, which the fake turns into a 404.
		"ReGa.runScript": func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
	})
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// A previously loaded detail cache must survive a failed reload: wiping it
	// costs the pipeline its only name fallback.
	c.DeviceDetails.AddName("0001ABCD:1", "Wohnzimmer Licht")

	_, _, closer, err := WireHub(
		context.Background(), hubWiringCentralConfig(t, srv.URL), c, wireHubTestLogger(), nil, "en",
	)
	if closer != nil {
		closer()
	}
	if err == nil {
		t.Fatal("WireHub must fail when the CCU cannot serve its device names")
	}
	if got := c.DeviceDetails.GetName("0001ABCD:1"); got != "Wohnzimmer Licht" {
		t.Errorf("device-details cache name = %q after a failed name load, want it untouched", got)
	}
}

// TestWireHubFailsWhenRoomOrFunctionAssignmentsCannotBeLoaded pins rooms and
// functions as hard prerequisites of the bring-up, for the same reason as the
// device names: both are read at the same point and stamped onto each channel
// exactly once by the same pipeline pass, so an empty substitute leaves the
// whole fleet without a room and without a function — on the SPA, REST, MQTT
// `suggested_area` and the alarm room grouping alike — until a full re-ingest.
// A CCU that genuinely defines none still comes up (the companion case below).
func TestWireHubFailsWhenRoomOrFunctionAssignmentsCannotBeLoaded(t *testing.T) {
	t.Parallel()

	listAllDetail := func(_ map[string]any) any {
		return []map[string]any{{
			"id": "1234", "address": "0001ABCD", "name": "Flur",
			"channels": []map[string]any{{"id": "1235", "address": "0001ABCD:1", "name": "Flur Licht"}},
		}}
	}
	for _, tc := range []struct {
		name    string
		methods map[string]func(map[string]any) any
	}{
		{
			name: "rooms",
			methods: map[string]func(map[string]any) any{
				"ReGa.runScript":       func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
				"Device.listAllDetail": listAllDetail,
				// Room.getAll missing -> the fake answers 404.
				"Subsection.getAll": func(_ map[string]any) any { return []map[string]any{} },
			},
		},
		{
			name: "functions",
			methods: map[string]func(map[string]any) any{
				"ReGa.runScript":       func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
				"Device.listAllDetail": listAllDetail,
				"Room.getAll":          func(_ map[string]any) any { return []map[string]any{} },
				// Subsection.getAll missing -> the fake answers 404.
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newJSONRPCFake(t, tc.methods)
			c, err := central.New(central.Config{Name: "ccu-01"})
			if err != nil {
				t.Fatalf("central.New: %v", err)
			}
			_, _, closer, err := WireHub(
				context.Background(), hubWiringCentralConfig(t, srv.URL), c, wireHubTestLogger(), nil, "en",
			)
			if closer != nil {
				closer()
			}
			if err == nil {
				t.Fatalf("WireHub must fail when the CCU cannot serve its %s", tc.name)
			}
		})
	}
}

// TestWireHubLoadsDeviceNamesAndRegistersRefreshJob is the companion to the
// failure pin: with the name payload present the wiring completes, hands the
// names to the caller and seeds the detail cache.
func TestWireHubLoadsDeviceNamesAndRegistersRefreshJob(t *testing.T) {
	t.Parallel()

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript": func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
		"Device.listAllDetail": func(_ map[string]any) any {
			return []map[string]any{{
				"id": "1234", "address": "0001ABCD", "name": "Flur",
				"channels": []map[string]any{{"id": "1235", "address": "0001ABCD:1", "name": "Flur Licht"}},
			}}
		},
		"Room.getAll":       func(_ map[string]any) any { return []map[string]any{} },
		"Subsection.getAll": func(_ map[string]any) any { return []map[string]any{} },
	})
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	_, hubData, closer, err := WireHub(
		context.Background(), hubWiringCentralConfig(t, srv.URL), c, wireHubTestLogger(), nil, "en",
	)
	if closer != nil {
		closer()
	}
	if err != nil {
		t.Fatalf("WireHub: %v", err)
	}
	if got := hubData.Names["0001ABCD:1"]; got != "Flur Licht" {
		t.Errorf("hubData.Names[0001ABCD:1] = %q, want %q", got, "Flur Licht")
	}
	if got := c.DeviceDetails.GetName("0001ABCD"); got != "Flur" {
		t.Errorf("device-details cache name = %q, want %q", got, "Flur")
	}
}

// TestWireHubStaleDeviceDetailsJobStopsTouchingTheCCU pins the re-init path:
// the scheduler cannot unregister a job, so a cache-clear leaves the previous
// generation's device-details job registered. Once its session has been closed
// it must stay silent — a JSON-RPC call through the logged-out client logs
// straight back in and holds a CCU session, from a pool the CCU WebUI shares,
// for the life of the process.
func TestWireHubStaleDeviceDetailsJobStopsTouchingTheCCU(t *testing.T) {
	t.Parallel()

	var detailCalls atomic.Int32
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript": func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
		"Device.listAllDetail": func(_ map[string]any) any {
			detailCalls.Add(1)
			return []map[string]any{{"id": "1234", "address": "0001ABCD", "name": "Flur"}}
		},
		"Room.getAll":       func(_ map[string]any) any { return []map[string]any{} },
		"Subsection.getAll": func(_ map[string]any) any { return []map[string]any{} },
	})
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cc := hubWiringCentralConfig(t, srv.URL)
	logger := wireHubTestLogger()

	_, _, firstCloser, err := WireHub(context.Background(), cc, c, logger, nil, "en")
	if err != nil {
		t.Fatalf("WireHub (first generation): %v", err)
	}
	_, _, secondCloser, err := WireHub(context.Background(), cc, c, logger, nil, "en")
	if err != nil {
		t.Fatalf("WireHub (second generation): %v", err)
	}
	defer secondCloser()

	jobs := make([]scheduler.Job, 0, 2)
	for _, j := range c.Scheduler.Jobs() {
		if j.Name == "devicedetails.refresh.ccu-01" {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != 2 {
		t.Fatalf("device-details jobs registered = %d, want 2 (one per bring-up generation)", len(jobs))
	}

	// Teardown of the first generation, as a re-init performs it.
	firstCloser()

	// Age the cache past the loader's skip window so neither run is suppressed
	// by the freshness gate — at the job's real 5-minute cadence it never is.
	c.DeviceDetails.MarkRefreshed(time.Now().Add(-time.Hour))

	before := detailCalls.Load()
	if err := jobs[0].Run(context.Background()); err != nil {
		t.Fatalf("stale job run: %v", err)
	}
	if got := detailCalls.Load(); got != before {
		t.Errorf("stale device-details job issued %d CCU call(s) after its session was closed, want 0", got-before)
	}
	if err := jobs[1].Run(context.Background()); err != nil {
		t.Fatalf("live job run: %v", err)
	}
	if detailCalls.Load() <= before {
		t.Error("the live device-details job must still refresh the cache")
	}
}

// TestWireHubStaleRefreshHooksStopTouchingTheCCU is the sibling of the
// device-details case for the hub refresh hooks. They close over the same
// JSON-RPC session and stay installed until the next successful WireHub
// replaces them — which a re-init can leave minutes away. A tick in that
// window would call through the logged-out client, which logs transparently
// back in and holds a CCU session the already-executed closer can never
// release.
func TestWireHubStaleRefreshHooksStopTouchingTheCCU(t *testing.T) {
	t.Parallel()

	var sysvarCalls atomic.Int32
	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript":       func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
		"Device.listAllDetail": func(_ map[string]any) any { return []map[string]any{} },
		"Room.getAll":          func(_ map[string]any) any { return []map[string]any{} },
		"Subsection.getAll":    func(_ map[string]any) any { return []map[string]any{} },
		"SysVar.getAll": func(_ map[string]any) any {
			sysvarCalls.Add(1)
			return []map[string]any{}
		},
	})
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	_, _, closer, err := WireHub(
		context.Background(), hubWiringCentralConfig(t, srv.URL), c, wireHubTestLogger(), nil, "en",
	)
	if err != nil {
		t.Fatalf("WireHub: %v", err)
	}

	// Teardown of this generation, as a re-init performs it.
	closer()

	before := sysvarCalls.Load()
	if err := c.Hub.RefreshSysvars(context.Background()); err != nil {
		t.Fatalf("RefreshSysvars after teardown: %v", err)
	}
	if got := sysvarCalls.Load(); got != before {
		t.Errorf("stale sysvar refresh hook issued %d CCU call(s) after its session was closed, want 0", got-before)
	}
}

// TestReconcileDoesNotFeedConnectionLatencyMetric is the negative control for
// the split between the two latency measurements.
//
// The connectivity probe's own duration is the time of one JSON-RPC
// `Interface.listInterfaces` call — a single one-way surface on the
// reconciler's slow cadence. It used to be published as the hub's
// connection-latency metric, so a sensor named for the round-trip to the CCU
// reported a fraction of that path and read as 0 ms on any LAN, because the
// duration was truncated to whole milliseconds.
//
// The producer is now the matched PING→PONG pair
// (TestWirePingPongBusFeedsLatencyFromMatchedPong). This test pins the other
// half of that move: a reconcile still runs the probe and still stamps the
// wire interface ids every connectivity surface keys on, and it leaves the
// latency metric alone. Without it, restoring the old observation would go
// unnoticed — the positive guard would stay green either way, since it never
// runs a reconcile.
func TestReconcileDoesNotFeedConnectionLatencyMetric(t *testing.T) {
	t.Parallel()

	srv := newJSONRPCFake(t, map[string]func(map[string]any) any{
		"ReGa.runScript":       func(_ map[string]any) any { return `{"serial":"VCU1234567"}` },
		"Device.listAllDetail": func(_ map[string]any) any { return []map[string]any{} },
		"Room.getAll":          func(_ map[string]any) any { return []map[string]any{} },
		"Subsection.getAll":    func(_ map[string]any) any { return []map[string]any{} },
		"Interface.listInterfaces": func(_ map[string]any) any {
			return []map[string]any{{"name": "HmIP-RF", "connected": true}}
		},
	})
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.Reconciler = &coordinators.Reconciler{CentralName: "ccu-01", HubModel: c.HubModel, Bus: c.EventBus}

	_, _, closer, err := WireHub(
		context.Background(), hubWiringCentralConfig(t, srv.URL), c, wireHubTestLogger(), nil, "en",
	)
	if err != nil {
		t.Fatalf("WireHub: %v", err)
	}
	defer closer()

	if _, ok := c.HubModel.Metrics.Value(hubmodel.MetricConnectionLatMs); ok {
		t.Fatal("pre-condition: the latency metric must be unobserved before the first reconcile")
	}
	if err := c.Reconciler.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if sample, ok := c.HubModel.Metrics.Value(hubmodel.MetricConnectionLatMs); ok {
		t.Errorf("a reconcile observed the connection-latency metric (%v ms): the JSON-RPC probe duration is "+
			"a one-way surface and must not be published under a name that means the full round-trip", sample.Value)
	}
	// The probe still had to run — otherwise this test would pass against a
	// WireHub that installs no probe at all, which is the same green for a
	// completely different reason. The connectivity aggregate is what the
	// reconcile writes, and it carries the stamped wire id.
	conn := c.HubModel.ConnectivityDataPoints()
	if conn == nil || len(conn.List()) == 0 {
		t.Fatal("the reconcile produced no connectivity entries — the probe never ran, so this test proves nothing")
	}
	if got := conn.List()[0].InterfaceID; got != "ccu-01-HmIP-RF" {
		t.Errorf("connectivity interface id = %q, want the stamped wire id %q", got, "ccu-01-HmIP-RF")
	}
}
