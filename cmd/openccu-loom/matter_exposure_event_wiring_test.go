// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestOperatorSelectionChangeReachesTheWebSocketHub asserts the effect of
// the exposure event wiring through the composition root: an operator's
// PUT /node/exposure, served by go-fabric-ui over the options
// wireMatterRuntime builds, puts `node.exposure_changed` on the WebSocket
// hub the SPA listens to. go-fabric-ui publishes its own exposure event
// into a hub of the handler's since 0.10, which the daemon's event source
// does not see, so without the selection reporting the change itself the
// topic stays silent and the SPA's selection view does not refresh.
// The negative control is the refused update: an unknown key stores
// nothing and puts nothing on the hub.
func TestOperatorSelectionChangeReachesTheWebSocketHub(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-exposure", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-exposure")
	unit, ok := reg.Get("ccu-exposure")
	if !ok {
		t.Fatal("the test registry did not hold the central it was built with")
	}
	unit.ModelRegistry.Put(switchDeviceWithUnnamedChannel("0001EXP01", "Stehlampe", 3))

	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	wiring, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, openTestLoomDB(t), nil, health.NewTracker(), nil, nil, nil,
		slog.New(slog.DiscardHandler), hub, nil,
	)
	// One cleanup, cancel first: see TestMatterBootEventsAreReadableAfterWiring.
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	if wiring.nodeUI.Exposure == nil {
		t.Fatal("the matter runtime handed the node page family no selection; without it this pin asserts nothing")
	}
	api := newNodeAPI(t, wiring.nodeUI)

	exposureEvents := func() int {
		n := 0
		for _, ev := range hub.Replay(0, func(topic string) bool { return topic == nodeTopicExposureChanged }).Events {
			if ev.Topic == nodeTopicExposureChanged {
				n++
			}
		}
		return n
	}

	rec := serveNodeAPIOn(t, api, http.MethodGet, "/api/v1/node/exposure", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /node/exposure = %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) == 0 {
		t.Fatalf("GET /node/exposure listed no candidate (err %v): %s", err, rec.Body.String())
	}

	// Negative control: a refused update stores nothing and reports nothing.
	if rec := serveNodeAPIOn(t, api, http.MethodPut, "/api/v1/node/exposure", `{"items":[{"key":"no-such-key","enabled":true}]}`); rec.Code < 400 {
		t.Fatalf("PUT /node/exposure with an unknown key = %d, want a refusal", rec.Code)
	}
	if n := exposureEvents(); n != 0 {
		t.Fatalf("a refused update put %d %s frame(s) on the hub, want 0", n, nodeTopicExposureChanged)
	}

	body := fmt.Sprintf(`{"items":[{"key":%q,"enabled":true}]}`, list.Items[0].Key)
	if rec := serveNodeAPIOn(t, api, http.MethodPut, "/api/v1/node/exposure", body); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("PUT /node/exposure = %d %s", rec.Code, rec.Body.String())
	}
	if n := exposureEvents(); n != 1 {
		t.Fatalf("the operator's selection change put %d %s frame(s) on the hub, want 1 — "+
			"the selection the exposure routes write through does not report its change, "+
			"and go-fabric-ui's own event never reaches the daemon's event source", n, nodeTopicExposureChanged)
	}
	if got := hub.Replay(0, func(topic string) bool { return topic == nodeTopicExposureChanged }).Events; !strings.Contains(fmt.Sprint(got[0].Payload), list.Items[0].Key) {
		t.Errorf("the frame's payload %v does not name the changed key %q", got[0].Payload, list.Items[0].Key)
	}
}
