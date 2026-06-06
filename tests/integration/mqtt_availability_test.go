// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMQTTAvailabilityAgainstRealBroker is the end-to-end guard for the
// reachability-based availability model. It boots godevccu, ingests a
// multi-domain device fleet, and drives the real
// [adapter.EventBridge.PublishInitialSnapshot] boot path through a
// production [mqtt.Bridge] connected to a genuine Mosquitto broker
// (dockerised, or the native binary on dev machines). A second client
// subscribes to the whole topic tree and the test asserts the contract:
//
//   - every reachable device publishes its `…/availability` topic as
//     "online" (none as "offline" — godevccu devices are all reachable);
//   - registered data points publish a slot state carrying
//     `"available":true`; and
//   - not-yet-observed data points publish `{"value":null,"available":
//     true}` rather than an empty eviction — the exact regression that
//     left HA entities stuck on `unavailable` under
//     `availability_mode: all`.
func TestMQTTAvailabilityAgainstRealBroker(t *testing.T) {
	mb := startMosquitto(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// --- device pipeline: godevccu → CentralUnit model -------------------
	srv := startMockCCUWithDevices(t, defaultMockDevices)
	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipeline := adapter.NewDevicePipeline(c).WithTranslations(translations, snapshotLocale())
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	if len(c.ModelRegistry.List()) == 0 {
		t.Fatal("ingest produced no devices")
	}

	// --- subscriber BEFORE publishing so retained messages are not missed -
	subClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "avail-sub", KeepAlive: 30 * time.Second, CleanSession: true,
	})
	if err := subClient.Connect(ctx); err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer subClient.Disconnect(ctx) //nolint:errcheck // teardown

	var mu sync.Mutex
	captured := make(map[string][]byte) // topic → last payload
	if err := subClient.Subscribe(ctx, "gh/#", mqtt.QoS1, func(topic string, payload []byte, _ bool) {
		cp := make([]byte, len(payload))
		copy(cp, payload)
		mu.Lock()
		captured[topic] = cp
		mu.Unlock()
	}); err != nil {
		t.Fatalf("subscribe gh/#: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // let SUBACK land

	// --- production bridge + EventBridge boot path ------------------------
	pubClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "avail-pub", KeepAlive: 30 * time.Second, CleanSession: true,
	})
	if err := pubClient.Connect(ctx); err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer pubClient.Disconnect(ctx) //nolint:errcheck // teardown

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "gh",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pubClient)
	wiring := mqtt.NewWiring(bridge, logger)

	eb := adapter.NewEventBridge(reg, nil, wiring)
	eb.Start(ctx)
	defer eb.Stop()

	eb.PublishInitialSnapshot(ctx)

	// --- drain: keep collecting until the topic count stops growing -------
	deadline := time.Now().Add(5 * time.Second)
	prev := -1
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		n := len(captured)
		mu.Unlock()
		if n != prev {
			prev = n
			deadline = time.Now().Add(2 * time.Second)
		}
	}

	// --- classify the captured topics -------------------------------------
	var (
		online, offline               int
		slotAvailableTrue             int
		unobservedAvailable           int
		emptyValueStateRetainedEvicts int
	)
	mu.Lock()
	snapshot := make(map[string][]byte, len(captured))
	for k, v := range captured {
		snapshot[k] = v
	}
	mu.Unlock()

	for topic, payload := range snapshot {
		switch {
		case strings.HasSuffix(topic, "/availability"):
			switch string(payload) {
			case "online":
				online++
			case "offline":
				offline++
			}
		case strings.Contains(string(payload), `"available":true`):
			slotAvailableTrue++
			if strings.Contains(string(payload), `"value":null`) {
				unobservedAvailable++
			}
		case len(payload) == 0 && strings.Contains(topic, "/values/"):
			// A retained empty payload on a VALUES state topic is the old
			// eviction shape — the fix must not produce these anymore.
			emptyValueStateRetainedEvicts++
		}
	}

	t.Logf("availability: online=%d offline=%d | slotAvailableTrue=%d unobservedAvailable=%d emptyEvicts=%d | topics=%d",
		online, offline, slotAvailableTrue, unobservedAvailable, emptyValueStateRetainedEvicts, len(snapshot))

	if online == 0 {
		t.Error("no device published availability=online — reachable devices must be online at boot")
	}
	if offline != 0 {
		t.Errorf("got %d availability=offline publishes — all godevccu devices are reachable", offline)
	}
	if slotAvailableTrue == 0 {
		t.Error("no slot state carried available:true — per-DP availability template would resolve to unavailable")
	}
	if unobservedAvailable == 0 {
		t.Error("no unobserved DP published {value:null, available:true} — the fix should publish available state instead of evicting")
	}
	if emptyValueStateRetainedEvicts != 0 {
		t.Errorf("got %d empty retained VALUES-state evictions — unobserved DPs must publish available state, not evict", emptyValueStateRetainedEvicts)
	}
}
