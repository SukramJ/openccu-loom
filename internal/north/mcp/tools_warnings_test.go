// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

type fakeWarningsSource struct{ active []warnings.Warning }

func (f *fakeWarningsSource) Active() []warnings.Warning { return f.active }

// TestListWarningsMirrorsTheAggregate verifies list_warnings projects the
// aggregator's active set — id, severity, central, message key and args —
// without inventing or dropping rows.
func TestListWarningsMirrorsTheAggregate(t *testing.T) {
	src := &fakeWarningsSource{active: []warnings.Warning{
		{ID: "health:mqtt", Severity: warnings.SeverityError, MessageKey: "warnings.health.unhealthy", Args: map[string]string{"component": "mqtt"}},
		{ID: "servicemsg:alpha", Severity: warnings.SeverityWarning, Central: "alpha", MessageKey: "warnings.service_messages", Args: map[string]string{"central": "alpha", "count": "3"}},
	}}
	cs := connect(t, mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"alpha"}},
		Devices:  newFakeDevices(),
		Warnings: src,
	})
	defer cs.Close()

	res := callTool(t, cs, "list_warnings", map[string]any{})
	if res.IsError {
		t.Fatalf("list_warnings returned error: %v", res.Content)
	}
	var out struct {
		Warnings []struct {
			ID       string            `json:"id"`
			Severity string            `json:"severity"`
			Central  string            `json:"central"`
			Message  string            `json:"message_key"`
			Args     map[string]string `json:"args"`
		} `json:"warnings"`
	}
	unmarshalStructured(t, res, &out)
	if len(out.Warnings) != 2 {
		t.Fatalf("want 2 warnings, got %d", len(out.Warnings))
	}
	if w := out.Warnings[0]; w.ID != "health:mqtt" || w.Severity != "error" || w.Message != "warnings.health.unhealthy" || w.Args["component"] != "mqtt" {
		t.Errorf("first row: %+v", w)
	}
	if w := out.Warnings[1]; w.Central != "alpha" || w.Args["count"] != "3" {
		t.Errorf("second row: %+v", w)
	}
}

// TestListWarningsAbsentWithoutASource pins that a wiring without the
// aggregate does not offer the tool at all — absence is detectable,
// not an empty answer.
func TestListWarningsAbsentWithoutASource(t *testing.T) {
	cs := connect(t, mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"alpha"}},
		Devices:  newFakeDevices(),
	})
	defer cs.Close()
	if toolNames(t, cs)["list_warnings"] {
		t.Fatal("list_warnings offered without a Warnings source")
	}
}
