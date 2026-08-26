// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHubMessageSummariesCarryDisplayName pins that the MCP message tools
// expose the localized label alongside the raw CCU code.
//
// Name is the raw string the CCU reports ("LOWBAT"); DisplayName is what the
// hub refresh resolved out of the translation catalogues ("Battery low"). REST
// carries both. An assistant answering "what is wrong with my house" off the
// MCP surface can only quote what the tool returns, so dropping DisplayName
// there means every answer names the code rather than the condition — a
// capability that exists on REST and is invisible to assistant-driven
// workflows.
func TestHubMessageSummariesCarryDisplayName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		v    any
	}{
		{"service", serviceMessageSummary{Name: "LOWBAT", DisplayName: "Battery low"}},
		{"alarm", alarmMessageSummary{Name: "ALARMLOW", DisplayName: "Alarm low"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), `"display_name":"`) {
				t.Errorf("%s summary drops the localized label: %s", tc.name, raw)
			}
			if !strings.Contains(string(raw), `"name":"`) {
				t.Errorf("%s summary must keep the raw code too: %s", tc.name, raw)
			}
		})
	}
}
