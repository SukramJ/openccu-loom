// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for the heating-group jpages proxy in ccu_groups.go:
// CreateHeatingGroupDraft, SaveHeatingGroup, DeleteHeatingGroup,
// SuitableHeatingGroupMembers, and the jsEscape / padLeft7 wire helpers.
// See docs/adr/0055-groups-jpages-proxy.md for the wire contract these
// tests pin.

package backends

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newJpagesTestBackend wires a CcuBackend against an httptest server acting
// as the CCU's HMServer jpages endpoint.
func newJpagesTestBackend(t *testing.T, handler http.HandlerFunc) *CcuBackend {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	b := NewCcuBackend(nil, nil, nil)
	b.SetDownloadFirmwareTransport(srv.URL, srv.Client(), func() string { return "SID" })
	return b
}

// writeJpagesResult marshals the {isSuccessful,errorCode,content} wrapper
// jpages endpoints reply with.
func writeJpagesResult(t *testing.T, w http.ResponseWriter, res jpagesResult) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal jpagesResult: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CreateHeatingGroupDraft
// ---------------------------------------------------------------------------

// TestCreateHeatingGroupDraftParsesGroupIDAndTypesFromEditPageHTML verifies
// that the draft id and the assignable group types are scraped from the
// group/create edit-page HTML, matching the shipped GroupEditPage.ftl shape.
func TestCreateHeatingGroupDraftParsesGroupIDAndTypesFromEditPageHTML(t *testing.T) {
	t.Parallel()
	const html = `<script>
self.groupId = 7;
var types = [ new GroupType("hmip.heating.group", translateKey('lblHmip')) ];
</script>`

	var gotMethod, gotPath string
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		writeJpagesResult(t, w, jpagesResult{IsSuccessful: true, Content: html})
	})

	draftID, types, err := b.CreateHeatingGroupDraft(context.Background())
	if err != nil {
		t.Fatalf("CreateHeatingGroupDraft: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/pages/jpages/group/create" {
		t.Errorf("request = %s %s, want GET /pages/jpages/group/create", gotMethod, gotPath)
	}
	if draftID != 7 {
		t.Errorf("draftID = %d, want 7", draftID)
	}
	want := []HeatingGroupType{{ID: "hmip.heating.group", LabelKey: "lblHmip"}}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("types = %+v, want %+v", types, want)
	}
}

// ---------------------------------------------------------------------------
// SaveHeatingGroup
// ---------------------------------------------------------------------------

// TestSaveHeatingGroupPostsFormEncodedJSONBody verifies the request carries
// the WebUI's form content-type (the save handler hangs on
// application/json) and that the JSON body itself carries the escaped name,
// type, members, isNewGroup flag, and derived virtual-device name.
func TestSaveHeatingGroupPostsFormEncodedJSONBody(t *testing.T) {
	t.Parallel()
	var gotContentType string
	var gotBody map[string]any
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		writeJpagesResult(t, w, jpagesResult{IsSuccessful: true, Content: ""})
	})

	in := HeatingGroupSaveInput{
		GroupID:               7,
		Name:                  "Süd",
		TypeID:                "hmip.heating.group",
		ForbidSingleOperation: true,
		MemberIDs:             []string{"000AAA0000001:1", "000AAA0000002:1"},
		IsNew:                 true,
	}
	if err := b.SaveHeatingGroup(context.Background(), in); err != nil {
		t.Fatalf("SaveHeatingGroup: %v", err)
	}

	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded prefix", gotContentType)
	}
	if got := gotBody["groupName"]; got != jsEscape(in.Name) {
		t.Errorf("groupName = %v, want %q", got, jsEscape(in.Name))
	}
	if got := gotBody["groupTypeId"]; got != in.TypeID {
		t.Errorf("groupTypeId = %v, want %q", got, in.TypeID)
	}
	members, ok := gotBody["assignedDevicesIds"].([]any)
	if !ok || len(members) != len(in.MemberIDs) {
		t.Fatalf("assignedDevicesIds = %v, want %v", gotBody["assignedDevicesIds"], in.MemberIDs)
	}
	for i, m := range in.MemberIDs {
		if members[i] != m {
			t.Errorf("assignedDevicesIds[%d] = %v, want %q", i, members[i], m)
		}
	}
	if got := gotBody["isNewGroup"]; got != true {
		t.Errorf("isNewGroup = %v, want true", got)
	}
	wantDeviceName := jsEscape(in.Name + " " + virtualDevicePrefix + padLeft7(in.GroupID))
	if got := gotBody["groupDeviceName"]; got != wantDeviceName {
		t.Errorf("groupDeviceName = %v, want %q", got, wantDeviceName)
	}
}

// TestSaveHeatingGroupSessionExpiredMapsToAuthFailure verifies that
// errorCode "42" (expired/missing sid) is classified as hmerr.ErrAuthFailure.
func TestSaveHeatingGroupSessionExpiredMapsToAuthFailure(t *testing.T) {
	t.Parallel()
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJpagesResult(t, w, jpagesResult{IsSuccessful: false, ErrorCode: "42"})
	})

	err := b.SaveHeatingGroup(context.Background(), HeatingGroupSaveInput{Name: "x", TypeID: "hmip.heating.group"})
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("err = %v, want hmerr.ErrAuthFailure", err)
	}
}

// ---------------------------------------------------------------------------
// DeleteHeatingGroup
// ---------------------------------------------------------------------------

// TestDeleteHeatingGroupSuccess verifies the POST /group/delete happy path.
func TestDeleteHeatingGroupSuccess(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		writeJpagesResult(t, w, jpagesResult{IsSuccessful: true, Content: "[]"})
	})

	if err := b.DeleteHeatingGroup(context.Background(), 7); err != nil {
		t.Fatalf("DeleteHeatingGroup: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/pages/jpages/group/delete" {
		t.Errorf("request = %s %s, want POST /pages/jpages/group/delete", gotMethod, gotPath)
	}
}

// ---------------------------------------------------------------------------
// SuitableHeatingGroupMembers
// ---------------------------------------------------------------------------

// TestSuitableHeatingGroupMembersParsesBarePayload verifies that on a valid
// session the endpoint reply is decoded directly (no
// {isSuccessful,...} wrapper).
func TestSuitableHeatingGroupMembersParsesBarePayload(t *testing.T) {
	t.Parallel()
	const bare = `{"assignableGroupMembers":[{"id":"A:1","serialNumber":"A:1","type":"SWITCH_ACTUATOR"}],"leftoverGroupMembers":[]}`
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bare))
	})

	got, err := b.SuitableHeatingGroupMembers(context.Background(), "hmip.heating.group")
	if err != nil {
		t.Fatalf("SuitableHeatingGroupMembers: %v", err)
	}
	want := SuitableHeatingGroupMembers{
		Assignable: []HeatingGroupMember{{ID: "A:1", SerialNumber: "A:1", Type: "SWITCH_ACTUATOR"}},
		Leftover:   []HeatingGroupMember{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}

// TestSuitableHeatingGroupMembersSessionExpiredMapsToAuthFailure verifies
// that the session-invalid wrapper (errorCode "42") is detected even though
// the happy-path payload has no wrapper at all.
func TestSuitableHeatingGroupMembersSessionExpiredMapsToAuthFailure(t *testing.T) {
	t.Parallel()
	b := newJpagesTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJpagesResult(t, w, jpagesResult{IsSuccessful: false, ErrorCode: "42"})
	})

	_, err := b.SuitableHeatingGroupMembers(context.Background(), "hmip.heating.group")
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("err = %v, want hmerr.ErrAuthFailure", err)
	}
}

// ---------------------------------------------------------------------------
// jsEscape / padLeft7
// ---------------------------------------------------------------------------

// TestJsEscapeMirrorsJavaScriptEscape verifies the Latin-1 %XX escaping
// HMServer expects for umlauts, that spaces are escaped, that '/' stays
// literal (part of the "keep" set), and that plain ASCII passes through.
func TestJsEscapeMirrorsJavaScriptEscape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "umlaut", in: "Süd", want: "S%FCd"},
		{name: "space", in: "a b", want: "a%20b"},
		{name: "umlaut and literal slash", in: "Küche/1", want: "K%FCche/1"},
		{name: "plain ascii unchanged", in: "Buero123", want: "Buero123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := jsEscape(tc.in); got != tc.want {
				t.Errorf("jsEscape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPadLeft7ZeroPadsShortIDsAndPassesLongOnesThrough verifies the virtual
// device serial suffix is zero-padded to 7 digits, and left unchanged once
// the id already spans 7+ digits.
func TestPadLeft7ZeroPadsShortIDsAndPassesLongOnesThrough(t *testing.T) {
	t.Parallel()
	if got := padLeft7(7); got != "0000007" {
		t.Errorf("padLeft7(7) = %q, want 0000007", got)
	}
	if got := padLeft7(12345678); got != "12345678" {
		t.Errorf("padLeft7(12345678) = %q, want 12345678", got)
	}
}
