// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeRSSIMatrixTool answers from fields and records the margin.
type fakeRSSIMatrixTool struct {
	centrals  []interfaces.RSSIMatrixCentral
	proposals []interfaces.ReceiverProposal
	margin    int
	calls     int
}

func (f *fakeRSSIMatrixTool) RSSIMatrix(context.Context) ([]interfaces.RSSIMatrixCentral, error) {
	f.calls++
	return f.centrals, nil
}

func (f *fakeRSSIMatrixTool) ReceiverProposal(_ context.Context, margin int) ([]interfaces.ReceiverProposal, error) {
	f.calls++
	f.margin = margin
	return f.proposals, nil
}

// fakeRFAssigner records each assignment.
type fakeRFAssigner struct{ calls []string }

func (f *fakeRFAssigner) AssignRFInterface(_ context.Context, addr, gw string, roaming bool) error {
	r := "pinned"
	if roaming {
		r = "roaming"
	}
	f.calls = append(f.calls, addr+"->"+gw+"/"+r)
	return nil
}

func rssiDeps(m *fakeRSSIMatrixTool, a *fakeRFAssigner, rec audit.Recorder) mcp.Deps {
	devs, _, _ := makeDeviceFixture()
	return mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		RSSIMatrix:  m,
		RFInterface: a,
		Audit:       rec,
		AllowWrites: true,
	}
}

func ip(v int) *int { return &v }

// TestRSSIMatrixToolsProjectAndFilter pins the matrix and proposal
// projections, the central filter, and the default margin reaching the
// service as "use the default".
func TestRSSIMatrixToolsProjectAndFilter(t *testing.T) {
	m := &fakeRSSIMatrixTool{
		centrals: []interfaces.RSSIMatrixCentral{
			{Central: "ccu1", InterfaceID: "ccu1-BidCos-RF", Devices: []interfaces.RSSIMatrixDevice{{
				Address: "DEV", Partners: []interfaces.RSSIMatrixPartner{{Address: "GW1", RxDBm: ip(-60)}},
			}}},
			{Central: "ccu2", InterfaceID: "ccu2-BidCos-RF"},
		},
		proposals: []interfaces.ReceiverProposal{
			{Address: "DEV", Central: "ccu1", Verdict: interfaces.ReceiverKeep},
			{Address: "OTHER", Central: "ccu2", Verdict: interfaces.ReceiverSwitch},
		},
	}
	cs := serveMCPAs(t, auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic},
		rssiDeps(m, &fakeRFAssigner{}, nil))

	res := callTool(t, cs, "get_rssi_matrix", map[string]any{"central_name": "ccu1"})
	if res.IsError {
		t.Fatalf("get_rssi_matrix: %v", res.Content)
	}
	var matrix struct {
		Items []struct {
			Central string `json:"central"`
			Devices []struct {
				Partners []struct {
					RxDBm *int `json:"rx_dbm"`
					TxDBm *int `json:"tx_dbm"`
				} `json:"partners"`
			} `json:"devices"`
		} `json:"items"`
	}
	unmarshalStructured(t, res, &matrix)
	if len(matrix.Items) != 1 || matrix.Items[0].Central != "ccu1" {
		t.Fatalf("items = %+v, want ccu1 only", matrix.Items)
	}
	p := matrix.Items[0].Devices[0].Partners[0]
	if p.RxDBm == nil || *p.RxDBm != -60 || p.TxDBm != nil {
		t.Errorf("partner = %+v", p)
	}

	res = callTool(t, cs, "get_receiver_proposal", map[string]any{})
	if res.IsError {
		t.Fatalf("get_receiver_proposal: %v", res.Content)
	}
	var props struct {
		Items []struct {
			Address string `json:"address"`
			Verdict string `json:"verdict"`
		} `json:"items"`
	}
	unmarshalStructured(t, res, &props)
	if len(props.Items) != 2 || m.margin != -1 {
		t.Fatalf("items = %+v margin = %d, want both with the service default", props.Items, m.margin)
	}
	res = callTool(t, cs, "get_receiver_proposal", map[string]any{"central_name": "ccu2", "margin_db": 10})
	if res.IsError {
		t.Fatalf("get_receiver_proposal: %v", res.Content)
	}
	unmarshalStructured(t, res, &props)
	if len(props.Items) != 1 || props.Items[0].Verdict != "switch" || m.margin != 10 {
		t.Fatalf("items = %+v margin = %d", props.Items, m.margin)
	}
	calls := m.calls
	if res := callTool(t, cs, "get_receiver_proposal", map[string]any{"margin_db": 31}); !res.IsError {
		t.Error("margin_db 31 was accepted")
	}
	if m.calls != calls {
		t.Error("an out-of-range margin reached the service")
	}
}

// TestAssignRFInterfaceToolRecordsAudit pins the call and the audit row.
func TestAssignRFInterfaceToolRecordsAudit(t *testing.T) {
	a := &fakeRFAssigner{}
	buf := audit.NewBuffer(4)
	cs := serveMCPAs(t, auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic},
		rssiDeps(&fakeRSSIMatrixTool{}, a, buf))

	if res := callTool(t, cs, "assign_rf_interface", map[string]any{
		"central_name": "ccu1", "address": "ADDR001", "interface_address": "GW2", "roaming": true,
	}); res.IsError {
		t.Fatalf("assign_rf_interface: %v", res.Content)
	}
	if len(a.calls) != 1 || a.calls[0] != "ADDR001->GW2/roaming" {
		t.Fatalf("calls = %v", a.calls)
	}
	rows := buf.List(4)
	if len(rows) != 1 || rows[0].User != "admin" || rows[0].DeviceAddress != "ADDR001" ||
		rows[0].Action != audit.ActionDeviceRFInterfaceAssign {
		t.Fatalf("audit rows = %+v", rows)
	}
	if res := callTool(t, cs, "assign_rf_interface", map[string]any{"address": "ADDR001"}); !res.IsError {
		t.Error("a missing interface_address was accepted")
	}
	if res := callTool(t, cs, "assign_rf_interface", map[string]any{
		"central_name": "ccu2", "address": "ADDR001", "interface_address": "GW2",
	}); !res.IsError {
		t.Error("a device of another central was accepted")
	}
	if len(a.calls) != 1 {
		t.Fatalf("rejected calls reached the domain: %v", a.calls)
	}
}

// TestRSSIMatrixToolsAreAdminOnly pins the role re-check on all three
// tools: their REST twins are admin-gated.
func TestRSSIMatrixToolsAreAdminOnly(t *testing.T) {
	m := &fakeRSSIMatrixTool{}
	a := &fakeRFAssigner{}
	cs := serveMCPAs(t, auth.Identity{Subject: "op", Role: auth.RoleOperator, Scheme: auth.SchemeBasic},
		rssiDeps(m, a, nil))
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_rssi_matrix", map[string]any{}},
		{"get_receiver_proposal", map[string]any{}},
		{"assign_rf_interface", map[string]any{"address": "ADDR001", "interface_address": "GW1"}},
	} {
		if res := callTool(t, cs, tc.name, tc.args); !res.IsError {
			t.Errorf("%s accepted an operator", tc.name)
		}
	}
	if m.calls != 0 || len(a.calls) != 0 {
		t.Fatalf("domain reached: matrix=%d assign=%v", m.calls, a.calls)
	}
}
