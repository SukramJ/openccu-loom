// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// fakeWSAlarmCodeAdmin is an in-memory AlarmCodeAdmin double for the
// alarm_panel.codes_* command family. It never hashes anything — the
// argon2id path is internal/alarm/codes's own responsibility — it only
// proves the command handlers wire args <-> facade calls correctly.
type fakeWSAlarmCodeAdmin struct {
	codes  map[string]hmapi.AlarmCode
	nextID int
}

func newFakeWSAlarmCodeAdmin() *fakeWSAlarmCodeAdmin {
	return &fakeWSAlarmCodeAdmin{codes: map[string]hmapi.AlarmCode{}}
}

func (f *fakeWSAlarmCodeAdmin) ListCodes(context.Context) ([]hmapi.AlarmCode, error) {
	out := make([]hmapi.AlarmCode, 0, len(f.codes))
	for i := range f.codes {
		out = append(out, f.codes[i])
	}
	return out, nil
}

func (f *fakeWSAlarmCodeAdmin) GetCode(_ context.Context, id string) (hmapi.AlarmCode, bool, error) {
	c, ok := f.codes[id]
	return c, ok, nil
}

func (f *fakeWSAlarmCodeAdmin) CreateCode(_ context.Context, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, error) {
	f.nextID++
	id := "c" + string(rune('0'+f.nextID))
	c := hmapi.AlarmCode{ID: id, Name: req.Name, Kind: req.Kind, Duress: req.Duress, Perms: req.Perms, Zones: req.Zones, Enabled: req.Enabled}
	f.codes[id] = c
	return c, nil
}

func (f *fakeWSAlarmCodeAdmin) UpdateCode(_ context.Context, id string, req hmapi.AlarmCodeRequest) (hmapi.AlarmCode, bool, error) {
	if _, ok := f.codes[id]; !ok {
		return hmapi.AlarmCode{}, false, nil
	}
	c := hmapi.AlarmCode{ID: id, Name: req.Name, Kind: req.Kind, Duress: req.Duress, Perms: req.Perms, Zones: req.Zones, Enabled: req.Enabled}
	f.codes[id] = c
	return c, true, nil
}

func (f *fakeWSAlarmCodeAdmin) DeleteCode(_ context.Context, id string) (bool, error) {
	if _, ok := f.codes[id]; !ok {
		return false, nil
	}
	delete(f.codes, id)
	return true, nil
}

// codesRouter wires just the alarm_panel.* family with a codes admin
// (stubAlarmPanel, defined in role_gate_test.go, is enough — none of
// the codes_* handlers touch AlarmPanelQuery).
func codesRouter(admin AlarmCodeAdmin) *Router {
	r := NewRouter()
	RegisterAlarmPanelCommands(r, AlarmPanelCommandsConfig{Panel: stubAlarmPanel{}, Codes: admin})
	return r
}

// TestAlarmPanelCodesListCreateUpdateDeleteDispatch drives the whole
// codes_* command family through one fake admin: create, list (sees
// the new row), update (name changes), delete (row gone).
func TestAlarmPanelCodesListCreateUpdateDeleteDispatch(t *testing.T) {
	admin := newFakeWSAlarmCodeAdmin()
	r := codesRouter(admin)

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.codes_create", map[string]any{
		"code": map[string]any{"name": "Markus", "kind": "pin", "pin": "1234", "perms": map[string]any{"disarm": true}},
	})
	if res.Error != nil {
		t.Fatalf("codes_create: %+v", res.Error)
	}
	created, ok := res.Data.(hmapi.AlarmCode)
	if !ok || created.ID == "" || created.Name != "Markus" {
		t.Fatalf("codes_create data = %+v", res.Data)
	}

	res = r.Dispatch(opCtx(), "alarm_panel.codes_list", nil)
	if res.Error != nil {
		t.Fatalf("codes_list: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("codes_list data type %T", res.Data)
	}
	list, ok := data["codes"].([]hmapi.AlarmCode)
	if !ok || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("codes_list codes = %+v, want [%s]", data["codes"], created.ID)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.codes_update", map[string]any{
		"id":   created.ID,
		"code": map[string]any{"name": "Markus Renamed", "kind": "pin", "perms": map[string]any{"disarm": true}},
	})
	if res.Error != nil {
		t.Fatalf("codes_update: %+v", res.Error)
	}
	updated, ok := res.Data.(hmapi.AlarmCode)
	if !ok || updated.Name != "Markus Renamed" {
		t.Fatalf("codes_update data = %+v", res.Data)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.codes_delete", map[string]any{"id": created.ID})
	if res.Error != nil {
		t.Fatalf("codes_delete: %+v", res.Error)
	}
	deleteData, ok := res.Data.(map[string]any)
	if !ok || deleteData["deleted"] != true {
		t.Fatalf("codes_delete data = %+v", res.Data)
	}
	if _, ok := admin.codes[created.ID]; ok {
		t.Error("code still present in the admin after codes_delete")
	}
}

// TestAlarmPanelCodesCommands_NilAdmin_Unavailable asserts every
// codes_* command answers "unavailable" (not a panic) when the codes
// facade is not wired — the daemon leaves Codes nil until migration
// 028 / the store adapter is up.
func TestAlarmPanelCodesCommands_NilAdmin_Unavailable(t *testing.T) {
	r := codesRouter(nil)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"alarm_panel.codes_list", nil},
		{"alarm_panel.codes_create", map[string]any{"code": map[string]any{"name": "x", "kind": "pin"}}},
		{"alarm_panel.codes_update", map[string]any{"id": "c1", "code": map[string]any{"name": "x", "kind": "pin"}}},
		{"alarm_panel.codes_delete", map[string]any{"id": "c1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := dispatchJSON(opCtx(), t, r, tc.name, tc.args)
			if res.Error == nil || res.Error.Code != "unavailable" {
				t.Fatalf("%s with nil admin = %+v, want unavailable", tc.name, res.Error)
			}
		})
	}
}

// TestAlarmPanelCodesCreate_ValidationErrors mirrors the REST surface's
// decodeAlarmCodeRequest validation: a missing name or unrecognized
// kind is a bad_request before the call ever reaches the admin.
func TestAlarmPanelCodesCreate_ValidationErrors(t *testing.T) {
	r := codesRouter(newFakeWSAlarmCodeAdmin())

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.codes_create", map[string]any{
		"code": map[string]any{"kind": "pin"},
	})
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("missing name = %+v, want bad_request", res.Error)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.codes_create", map[string]any{
		"code": map[string]any{"name": "x", "kind": "bogus"},
	})
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("invalid kind = %+v, want bad_request", res.Error)
	}
}

// TestAlarmPanelCodesUpdate_MissingIDIsBadRequest asserts the id-carries
// the-target contract: an update without an id is a client error, not a
// blind admin.UpdateCode("") call.
func TestAlarmPanelCodesUpdate_MissingIDIsBadRequest(t *testing.T) {
	r := codesRouter(newFakeWSAlarmCodeAdmin())
	res := dispatchJSON(opCtx(), t, r, "alarm_panel.codes_update", map[string]any{
		"code": map[string]any{"name": "x", "kind": "pin"},
	})
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("missing id = %+v, want bad_request", res.Error)
	}
}

// TestAlarmPanelCodesUpdateDelete_UnknownIDIsNotFound covers both write
// verbs against an id the admin does not know.
func TestAlarmPanelCodesUpdateDelete_UnknownIDIsNotFound(t *testing.T) {
	r := codesRouter(newFakeWSAlarmCodeAdmin())

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.codes_update", map[string]any{
		"id": "missing", "code": map[string]any{"name": "x", "kind": "pin"},
	})
	if res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("update unknown id = %+v, want not_found", res.Error)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.codes_delete", map[string]any{"id": "missing"})
	if res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("delete unknown id = %+v, want not_found", res.Error)
	}
}

// alarmPanelCodesCommands is every alarm_panel.codes_* command with a
// minimal valid argument body, for the role-gate sweep below.
var alarmPanelCodesCommands = []struct {
	name string
	args string
}{
	{"alarm_panel.codes_list", `{}`},
	{"alarm_panel.codes_create", `{"code":{"name":"x","kind":"pin"}}`},
	{"alarm_panel.codes_update", `{"id":"c1","code":{"name":"x","kind":"pin"}}`},
	{"alarm_panel.codes_delete", `{"id":"c1"}`},
}

// TestAlarmPanelCodesCommandsRequireOperatorRole asserts codes_* is
// gated identically to the arm/disarm/silence family — including
// codes_list, which stays operator-only rather than viewer-open since
// alarm codes are security material (writeCommandRoles's own comment).
// An unattributed context is unauthorized, a viewer identity is
// forbidden, and an operator identity clears the gate.
func TestAlarmPanelCodesCommandsRequireOperatorRole(t *testing.T) {
	r := codesRouter(newFakeWSAlarmCodeAdmin())
	for _, tc := range alarmPanelCodesCommands {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Dispatch(context.Background(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorUnauthorized {
				t.Fatalf("unauthenticated dispatch = %+v, want unauthorized", res.Error)
			}
			res = r.Dispatch(viewerCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorForbidden {
				t.Fatalf("viewer dispatch = %+v, want forbidden", res.Error)
			}
			res = r.Dispatch(opCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error != nil && (res.Error.Code == CommandErrorUnauthorized || res.Error.Code == CommandErrorForbidden) {
				t.Fatalf("operator dispatch blocked by role gate: %+v", res.Error)
			}
		})
	}
}
