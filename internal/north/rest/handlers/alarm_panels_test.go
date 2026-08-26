// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
)

// stubbedPanelsFixture substitutes a fixed Panels() result over an
// otherwise real alarmPanelFixture. ListAlarmPanels only calls Panels(),
// so the rest of the AlarmPanel facade can stay the fixture's real
// engine/manager/stores/reload.
type stubbedPanelsFixture struct {
	*alarmPanelFixture
	panels []alarmpanel.Panel
}

func (s stubbedPanelsFixture) Panels() []alarmpanel.Panel { return s.panels }

var _ AlarmPanel = stubbedPanelsFixture{}

// TestListAlarmPanels_AlwaysSerializesBothCodePolicyKeys verifies GET
// /alarm/panels carries code_arm_required and code_disarm_required as
// always-present JSON keys (no omitempty) — the SPA and MQTT-adjacent
// clients rely on the false value being explicit, not merely absent,
// so a client can distinguish "no policy" from "field not sent by this
// server version".
func TestListAlarmPanels_AlwaysSerializesBothCodePolicyKeys(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	stub := stubbedPanelsFixture{
		alarmPanelFixture: fx,
		panels: []alarmpanel.Panel{
			{
				UniqueID:           alarmpanel.PanelUniqueID("eg"),
				ZoneID:             "eg",
				Name:               "Erdgeschoss",
				State:              "disarmed",
				Available:          true,
				CodeArmRequired:    true,
				CodeDisarmRequired: false,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/panels", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmPanels(stub).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("panels = %+v, want exactly one entry", raw)
	}
	entry := raw[0]

	armVal, ok := entry["code_arm_required"]
	if !ok {
		t.Fatalf("response missing code_arm_required key: %+v", entry)
	}
	if armVal != true {
		t.Errorf("code_arm_required = %v, want true", armVal)
	}

	disarmVal, ok := entry["code_disarm_required"]
	if !ok {
		t.Fatalf("response missing code_disarm_required key: %+v", entry)
	}
	if disarmVal != false {
		t.Errorf("code_disarm_required = %v, want false", disarmVal)
	}
}
