// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// tools_write_report_test.go pins that write_paramset and
// write_link_paramset hand the post-write read-back report to the MCP
// client. An interface process may answer ok and still clamp a value; an
// assistant that only sees {"ok": true} cannot tell.

package mcp_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

type writeReportOut struct {
	OK     bool `json:"ok"`
	Report *struct {
		Written             []string `json:"written"`
		ReadbackDivergences []struct {
			Parameter string  `json:"parameter"`
			Sent      float64 `json:"sent"`
			Stored    float64 `json:"stored"`
		} `json:"readback_divergences"`
		ReadbackError string `json:"readback_error"`
	} `json:"report"`
}

func clampedReport() *interfaces.ParamsetWriteReport {
	return &interfaces.ParamsetWriteReport{
		Written:     []string{"MIN_SETPOINT"},
		Divergences: []interfaces.ParamsetDivergence{{Parameter: "MIN_SETPOINT", Sent: 10.0, Stored: 4.5}},
	}
}

func assertClampedReport(t *testing.T, out writeReportOut) {
	t.Helper()
	if !out.OK {
		t.Fatal("expected ok=true")
	}
	if out.Report == nil {
		t.Fatal("expected a report in the structured output")
	}
	if len(out.Report.Written) != 1 || out.Report.Written[0] != "MIN_SETPOINT" {
		t.Fatalf("written = %v, want [MIN_SETPOINT]", out.Report.Written)
	}
	if len(out.Report.ReadbackDivergences) != 1 {
		t.Fatalf("readback_divergences = %+v, want one entry", out.Report.ReadbackDivergences)
	}
	d := out.Report.ReadbackDivergences[0]
	if d.Parameter != "MIN_SETPOINT" || d.Sent != 10.0 || d.Stored != 4.5 {
		t.Fatalf("divergence = %+v, want {MIN_SETPOINT 10 4.5}", d)
	}
}

func TestWriteParamset_ReportsReadbackDivergence(t *testing.T) {
	ps := newFakeParamsets()
	ps.putReport = clampedReport()
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   &fakeEditLocks{allow: true},
		AllowWrites: true,
	})
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
		"edit_token":   "tok",
	})
	if res.IsError {
		t.Fatalf("write_paramset returned error: %v", res.Content)
	}
	var out writeReportOut
	unmarshalStructured(t, res, &out)
	assertClampedReport(t, out)
}

func TestWriteParamset_ValuesWriteCarriesNoReport(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		AllowWrites: true,
	})
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "VALUES",
		"values":       map[string]any{"LEVEL": 0.5},
	})
	if res.IsError {
		t.Fatalf("write_paramset returned error: %v", res.Content)
	}
	var out writeReportOut
	unmarshalStructured(t, res, &out)
	if !out.OK || out.Report != nil {
		t.Fatalf("VALUES write: got ok=%v report=%+v, want ok=true and no report", out.OK, out.Report)
	}
}

func TestWriteLinkParamset_ReportsReadbackDivergence(t *testing.T) {
	ps := newFakeParamsets()
	ps.putLinkReport = clampedReport()
	devs, _, _ := makeDeviceFixture()
	cs := connect(t, mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   &fakeEditLocks{allow: true},
		AllowWrites: true,
	})
	defer cs.Close()

	res := callTool(t, cs, "write_link_paramset", map[string]any{
		"central_name":             "ccu1",
		"receiver_channel_address": "ADDR001",
		"sender_channel_address":   "ADDR002",
		"values":                   map[string]any{"MIN_SETPOINT": 10.0},
		"edit_token":               "tok",
	})
	if res.IsError {
		t.Fatalf("write_link_paramset returned error: %v", res.Content)
	}
	var out writeReportOut
	unmarshalStructured(t, res, &out)
	assertClampedReport(t, out)
}
