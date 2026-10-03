// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestLiteGroupWriteNamesTheMembersTheBoxDropped pins, through the built
// binary, what an operator learns when an openccu-lite box answers a
// heating-group write as done while leaving a member out: a 422 whose
// detail names the member, and no half-made group on the box.
//
// The box in this run has no candidate for any group type, so it drops
// every member a write names — the same answer a real box gives for a
// member its group type cannot take. A handler test cannot prove the
// route: the refusal starts in the lite port, three layers below it.
func TestLiteGroupWriteNamesTheMembersTheBoxDropped(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{Backend: harness.BackendOpenCCULite})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	const member = "0000000000FFFF:1"
	do := func(method, path, body string) (int, []byte) {
		t.Helper()
		var rd io.Reader = http.NoBody
		if body != "" {
			rd = bytes.NewBufferString(body)
		}
		req, err := h.REST().NewRequest(method, path, rd)
		if err != nil {
			t.Fatalf("request %s %s: %v", method, path, err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := h.REST().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}
	groups := "/api/v1/groups?central=" + liteCentral

	status, raw := do(http.MethodPost, groups, `{"type_id":"hmip.heating.group","name":"Obergeschoss","members":["`+member+`"]}`)
	var prob struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &prob)
	if status != http.StatusUnprocessableEntity || prob.Code != "validation" || !strings.Contains(prob.Detail, member) {
		t.Fatalf("create naming a member the box drops: status %d body %s; want 422 validation naming %s", status, raw, member)
	}

	status, raw = do(http.MethodGet, groups, "")
	if status != http.StatusOK || strings.Contains(string(raw), "Obergeschoss") {
		t.Errorf("the refused create left a group behind: status %d body %s", status, raw)
	}

	// A group without members is made; giving it the member afterwards is
	// refused the same way.
	status, raw = do(http.MethodPost, groups, `{"type_id":"hmip.heating.group","name":"Erdgeschoss","members":[]}`)
	if status != http.StatusCreated {
		t.Fatalf("create without members: status %d body %s", status, raw)
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(raw, &created)
	status, raw = do(http.MethodPut, "/api/v1/groups/"+itoa(created.ID)+"?central="+liteCentral, `{"name":"Erdgeschoss","members":["`+member+`"]}`)
	_ = json.Unmarshal(raw, &prob)
	if status != http.StatusUnprocessableEntity || !strings.Contains(prob.Detail, member) {
		t.Errorf("update naming a member the box drops: status %d body %s; want 422 naming %s", status, raw, member)
	}
}

// TestLiteGroupWriteOverWebSocketNamesTheMembersTheBoxDropped pins the same
// refusal on the WebSocket command plane: groups.create naming a member the
// box drops answers bad_request with the member in the message, not
// internal_error. The caller asked for a member the group type cannot take;
// the router decides the code, so only a run through the built binary shows
// what a client actually receives.
func TestLiteGroupWriteOverWebSocketNamesTheMembersTheBoxDropped(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{Backend: harness.BackendOpenCCULite})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	wsc, err := h.REST().DialWS("/api/v1/events")
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	defer func() { _ = wsc.Close() }()

	const member = "0000000000FFFF:1"
	res, err := wsc.Call("group-create", "groups.create", map[string]any{
		"central": liteCentral,
		"type_id": "hmip.heating.group",
		"name":    "Obergeschoss",
		"members": []string{member},
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("groups.create: %v", err)
	}
	if res.Error == nil || res.Error.Code != "bad_request" || !strings.Contains(res.Error.Message, member) {
		t.Fatalf("groups.create naming a member the box drops: error %+v data %s; want bad_request naming %s", res.Error, res.Data, member)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
