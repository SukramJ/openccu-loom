// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//   - not-yet-observed VALUES data points publish
//     `{"value":null,"available":false}` rather than an empty eviction.
//
// The last bullet pins a convention that was deliberately reversed, so it
// says which side it holds and why. The original fix published unobserved
// data points as `available:true`, because an empty retained payload left
// the per-entity availability template with nothing to read and HA stuck
// every entity on `unavailable` under `availability_mode: all`. Gating
// availability on the full validity chain then reversed the `available`
// half for reference parity: an unobserved point is not a confirmed
// reading, so it publishes as unavailable with a `null` value (CHANGELOG
// 0.5.x, `notes/parity/by_design.md`).
//
// What survived both is the part that actually caused the outage: the slot
// topic must carry a JSON body. An unobserved point publishing
// `{"value":null,"available":false}` is the decided behaviour; an
// unobserved point publishing nothing at all is the regression.
//
// The bullet is scoped to the VALUES plane for a reason worth keeping.
// When the reversal landed, this assertion still required
// `available:true` — and stayed green for six weeks, because the
// CALCULATED plane had not been gated yet and its unobserved points still
// matched. The assertion was measuring a plane it did not name, and passed
// for a reason unrelated to what it claimed. It only went red once
// calculated validity was gated on source validity too, at which point the
// last payload of the old shape disappeared. Naming the plane is what stops
// that from recurring.
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
	// Production order: the bring-up latches southbound-ready AFTER the
	// ingest (and its visibility passes) completed; the snapshot pass gates
	// on the latch and skips a mid-bring-up central entirely.
	c.MarkSouthboundReady()

	// --- subscriber BEFORE publishing so retained messages are not missed -
	subClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "avail-sub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := subClient.Connect(ctx); err != nil {
		t.Fatalf("subscriber connect: %v", err)
	}
	defer subClient.Disconnect(ctx) //nolint:errcheck // teardown

	var mu sync.Mutex
	captured := make(map[string][]byte) // topic → last payload
	if _, err := subClient.Subscribe(ctx, "gh/#", mqtt.QoS1, mqtt.LegacyHandler(func(topic string, payload []byte, _ bool) {
		cp := make([]byte, len(payload))
		copy(cp, payload)
		mu.Lock()
		captured[topic] = cp
		mu.Unlock()
	})); err != nil {
		t.Fatalf("subscribe gh/#: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // let SUBACK land

	// --- production bridge + EventBridge boot path ------------------------
	pubClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: mb.URL(), ClientID: "avail-pub", KeepAlive: 30 * time.Second, CleanStart: true,
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
		online, offline                  int
		slotAvailableTrue                int
		unobservedUnavailable            int
		unobservedUnavailableOtherPlanes int
		emptyValueStateRetainedEvicts    int
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
		case strings.Contains(string(payload), `"value":null,"available":false`):
			// An unobserved data point: no confirmed reading, but a body
			// the availability template can read. Counted per plane so the
			// VALUES assertion cannot be satisfied by another plane, which
			// is how the previous version of it stayed green after the
			// behaviour it pinned had already changed.
			if strings.Contains(topic, "/values/") {
				unobservedUnavailable++
			} else {
				unobservedUnavailableOtherPlanes++
			}
		case len(payload) == 0 && strings.Contains(topic, "/values/"):
			// A retained empty payload on a VALUES state topic is the old
			// eviction shape — the fix must not produce these anymore.
			emptyValueStateRetainedEvicts++
		}
	}

	t.Logf("availability: online=%d offline=%d | slotAvailableTrue=%d unobservedUnavailable=%d (values) %d (other planes) emptyEvicts=%d | topics=%d",
		online, offline, slotAvailableTrue, unobservedUnavailable,
		unobservedUnavailableOtherPlanes, emptyValueStateRetainedEvicts, len(snapshot))

	if online == 0 {
		t.Error("no device published availability=online — reachable devices must be online at boot")
	}
	if offline != 0 {
		t.Errorf("got %d availability=offline publishes — all godevccu devices are reachable", offline)
	}
	if slotAvailableTrue == 0 {
		t.Error("no slot state carried available:true — per-DP availability template would resolve to unavailable")
	}
	if unobservedUnavailable == 0 {
		t.Error("no unobserved VALUES DP published {value:null, available:false} — an unobserved point " +
			"must still publish a readable body; publishing nothing is what left HA entities " +
			"stuck on unavailable")
	}
	if emptyValueStateRetainedEvicts != 0 {
		t.Errorf("got %d empty retained VALUES-state evictions — unobserved DPs must publish available state, not evict", emptyValueStateRetainedEvicts)
	}
}
