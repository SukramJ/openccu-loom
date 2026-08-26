// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestWSHubQuery_ListSysvars_IntegerBounds pins the type-aware bound
// conversion on the WS descriptor: an INTEGER sysvar carries its bounds in
// ParamValue.Int, not .Float, so the old path (reading .Float raw) reported
// 0/0. min=1/max=100 must round-trip as 1/100, matching the REST summary.
func TestWSHubQuery_ListSysvars_IntegerBounds(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)

	sv := hub.NewSysvar("test-ccu", "Counter", "", hmenum.HubValueTypeInteger, nil)
	minV := hmtypes.IntValue(1)
	maxV := hmtypes.IntValue(100)
	sv.ApplyMeta(hub.SysvarMeta{
		ValueType: hmenum.HubValueTypeInteger,
		Min:       &minV,
		Max:       &maxV,
	})
	h.PutSysvar(sv)

	got, err := q.ListSysvars(context.Background())
	if err != nil {
		t.Fatalf("ListSysvars: %v", err)
	}
	var entry map[string]any
	for _, e := range got {
		if e["name"] == "Counter" {
			entry = e
			break
		}
	}
	if entry == nil {
		t.Fatalf("Counter sysvar not in list: %v", got)
	}
	if mn, ok := entry["min"].(float64); !ok || mn != 1.0 {
		t.Errorf("min=%v want 1.0 (INTEGER bound read via .Int, not .Float)", entry["min"])
	}
	if mx, ok := entry["max"].(float64); !ok || mx != 100.0 {
		t.Errorf("max=%v want 100.0 (INTEGER bound read via .Int, not .Float)", entry["max"])
	}
}

// TestWSHubQuery_ListSysvars_RaceWithApplyMeta drives the WS list path
// concurrently with the descriptor rewrite the 30 s hub refresh performs via
// Sysvar.ApplyMeta. The old path read the ten descriptor fields straight off
// the struct while ApplyMeta rewrote them under the sysvar's own lock — a data
// race the -race detector flags. Reading the same Meta() snapshot REST/MQTT
// use keeps it clean.
func TestWSHubQuery_ListSysvars_RaceWithApplyMeta(t *testing.T) {
	q, h := liveHubQuery(t)

	sv := hub.NewSysvar("test-ccu", "Counter", "", hmenum.HubValueTypeInteger, nil)
	h.PutSysvar(sv)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			mn := hmtypes.IntValue(i)
			mx := hmtypes.IntValue(i + 100)
			sv.ApplyMeta(hub.SysvarMeta{
				ValueType:   hmenum.HubValueTypeInteger,
				Description: "desc",
				Unit:        "unit",
				ValueList:   []string{"a", "b"},
				IsVisible:   true,
				IsLogged:    true,
				ValueName0:  "off",
				ValueName1:  "on",
				Min:         &mn,
				Max:         &mx,
			})
		}
	}()

	for range 3000 {
		if _, err := q.ListSysvars(context.Background()); err != nil {
			t.Fatalf("ListSysvars: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
