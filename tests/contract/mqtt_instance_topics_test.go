// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/build"
	mqtt "github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestMQTTInstanceTopicsOnConnect pins what the daemon says about itself on
// every broker (re)connect under mqtt-smarthome 2.0 (ADR 0083), driven
// through the real [mqtt.Bridge.AnnounceOnline]:
//
//   - `<name>/connected` is the plain integer level, retained — never the
//     old `online` marker, which the availability template
//     `value | int(0) >= 2` would read as 0 and every entity would stay
//     unavailable;
//   - `<name>/info` is the retained introspection document carrying the
//     spec's own fields and this daemon's `commit`, `build_date` and the
//     LIVE `centrals` list — the folded `bridge/health` document, whose
//     centrals were resolved per call for the same reason: a CCU adopted at
//     runtime must appear without a restart.
func TestMQTTInstanceTopicsOnConnect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	centrals := make([]string, 0, 2)
	centrals = append(centrals, "ccu-a")
	pub := &securityPlanePublisher{}
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a", RawEnabled: true,
		CentralNamesSupplier: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(centrals)
		},
	}, pub)

	if err := bridge.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("AnnounceOnline: %v", err)
	}
	conn, ok := findSecurityTopic(pub, "openccu-loom/connected")
	if !ok {
		t.Fatalf("AnnounceOnline published nothing on openccu-loom/connected; records: %d", len(pub.records()))
	}
	if !conn.retain {
		t.Errorf("connected published with retain=false, want true")
	}
	// No central has reported reachable yet, so the daemon claims no more
	// than the broker connection: level 1.
	if string(conn.payload) != "1" {
		t.Errorf("connected = %q before any central is reachable, want %q", conn.payload, "1")
	}

	info := instanceInfo(t, pub)
	for key, want := range map[string]any{
		"name":        mqtt.InstanceName,
		"version":     build.Version,
		"spec":        "2.0",
		"commit":      build.Commit,
		"build_date":  build.BuildDate,
		"maintenance": true,
	} {
		if info[key] != want {
			t.Errorf("info.%s = %#v, want %#v", key, info[key], want)
		}
	}
	for _, key := range []string{"go", "pid", "started"} {
		if _, ok := info[key]; !ok {
			t.Errorf("info carries no %q: %v", key, info)
		}
	}
	// `status` was bridge/health's redundant liveness field; ADR 0083 drops it.
	if _, ok := info["status"]; ok {
		t.Errorf("info still carries bridge/health's `status` field: %v", info)
	}
	if got := info["centrals"]; !slices.Equal(anyStrings(got), []string{"ccu-a"}) {
		t.Errorf("info.centrals = %#v, want [ccu-a]", got)
	}

	// A central adopted at runtime: the next announce must name it.
	mu.Lock()
	centrals = append(centrals, "ccu-b")
	mu.Unlock()
	if err := bridge.AnnounceOnline(context.Background()); err != nil {
		t.Fatalf("second AnnounceOnline: %v", err)
	}
	info = instanceInfo(t, pub)
	if got := info["centrals"]; !slices.Equal(anyStrings(got), []string{"ccu-a", "ccu-b"}) {
		t.Errorf("info.centrals after adoption = %#v, want [ccu-a ccu-b] — the list was captured, not resolved", got)
	}

	// The will is the level-0 counterpart on the same topic.
	will, err := bridge.LastWill()
	if err != nil {
		t.Fatalf("LastWill: %v", err)
	}
	if will.Topic != "openccu-loom/connected" || string(will.Payload) != "0" || !will.Retain {
		t.Errorf("LastWill = {%q, %q, retain=%v}, want {openccu-loom/connected, 0, true}",
			will.Topic, will.Payload, will.Retain)
	}
}

// instanceInfo decodes the latest `<name>/info` publish.
func instanceInfo(t *testing.T, pub *securityPlanePublisher) map[string]any {
	t.Helper()
	rec, ok := findSecurityTopic(pub, "openccu-loom/info")
	if !ok {
		t.Fatal("AnnounceOnline published nothing on openccu-loom/info")
	}
	if !rec.retain {
		t.Errorf("info published with retain=false, want true")
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.payload, &doc); err != nil {
		t.Fatalf("info is not a JSON object: %v (%s)", err, rec.payload)
	}
	return doc
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}
