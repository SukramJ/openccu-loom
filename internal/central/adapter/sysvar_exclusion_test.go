// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestSysvarExclusionAppliesTheModelRuleAtFetchTime pins the effect, not
// the predicate: the sysvar fetch path must run the hub model's
// exclusion rule, so CCU scratch values (OldVal/pcCCUID) and the fixed
// alarm/service-message IDs never reach the hub model and therefore
// never reach REST, MQTT discovery, Matter or external clients.
//
// The "401" entry is load-bearing: it proves the ID half is an equality
// check and not a prefix or substring match, which is the property that
// makes the fold behaviour-neutral.
//
// Altitude: this pins loadSysvars, the one place the rule is applied.
// The edges that reach it — WireHub's boot call and the Sysvars refresh
// hook — are pinned elsewhere, not by this test.
func TestSysvarExclusionAppliesTheModelRuleAtFetchTime(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req["method"] == "SysVar.getAll" {
			result = []map[string]any{
				{"id": "1235", "name": "svCounterOldVal_51323", "type": "FLOAT", "value": "1.0"},
				{"id": "1236", "name": "pcCCUID", "type": "STRING", "value": "\"x\""},
				{"id": "40", "name": "Alarmmeldungen", "type": "ALARM", "value": "false"},
				{"id": "401", "name": "Temperatur Garten", "type": "FLOAT", "value": "21.5"},
			}
		}
		resp, _ := json.Marshal(map[string]any{"result": result, "error": nil})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	h := hub.NewHub("ccu-01")
	if err := loadSysvars(context.Background(), newScanClient(t, srv), nil, h, nil,
		hubScanOptions{enableSysvarScan: true}); err != nil {
		t.Fatalf("loadSysvars: %v", err)
	}

	names := make([]string, 0, len(h.Sysvars()))
	for _, sv := range h.Sysvars() {
		names = append(names, sv.LegacyName())
	}
	if len(names) != 1 || names[0] != "Temperatur Garten" {
		t.Fatalf("hub sysvars after fetch = %v, want exactly [Temperatur Garten]", names)
	}
}
