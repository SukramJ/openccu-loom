// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

const sysReadToken = "olt_44444444444444444444444444444444"

func startSystemFake(t *testing.T) *litefake.Fake {
	t.Helper()
	return startFake(t, litefake.Options{Tokens: map[string][]string{
		litefake.DefaultToken: {"*"},
		sysReadToken:          {"system:read"},
	}})
}

// TestSystemEnforcesRouteScopes pins the scopes of the system routes:
// system:read reads, power and backup are separate scopes, a missing
// one is 403 naming it.
func TestSystemEnforcesRouteScopes(t *testing.T) {
	f := startSystemFake(t)
	for _, path := range []string{
		"/api/system/v1/status", "/api/system/v1/time", "/api/system/v1/system-update",
		"/api/system/v1/service-messages", "/api/system/v1/groups", "/api/system/v1/radio/health",
	} {
		if resp, raw := get(t, f, path, sysReadToken); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	cases := []struct{ method, path, scope string }{
		{http.MethodPost, "/api/system/v1/reboot", "power"},
		{http.MethodPost, "/api/system/v1/reboot/recovery", "power"},
		{http.MethodPost, "/api/system/v1/system-update/install", "power"},
		{http.MethodPost, "/api/system/v1/restore/check", "power"},
		{http.MethodGet, "/api/system/v1/backup", "backup"},
		{http.MethodPost, "/api/system/v1/groups", "system:write"},
	}
	for _, tc := range cases {
		resp, raw := send(t, f, tc.method, tc.path, sysReadToken, `{"confirm":true}`)
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(raw), `"scope":"`+tc.scope+`"`) {
			t.Errorf("%s %s: %d %s, want 403 %s", tc.method, tc.path, resp.StatusCode, raw, tc.scope)
		}
	}
	if resp, _ := get(t, f, "/api/system/v1/status", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential: %d", resp.StatusCode)
	}
}

// TestSystemPowerActionsNeedConfirm pins the {"confirm":true} rule and
// the recorded request bodies.
func TestSystemPowerActionsNeedConfirm(t *testing.T) {
	f := startSystemFake(t)
	for _, path := range []string{"/api/system/v1/reboot", "/api/system/v1/halt", "/api/system/v1/reboot/recovery"} {
		if resp, raw := send(t, f, http.MethodPost, path, litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s without confirm: %d %s", path, resp.StatusCode, raw)
		}
		resp, raw := send(t, f, http.MethodPost, path, litefake.DefaultToken, `{"confirm":true}`)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"ok":true`) {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	var confirmed int
	for _, c := range f.Calls() {
		if c.Path == "/api/system/v1/reboot/recovery" && string(c.Body) == `{"confirm":true}` && c.Status == http.StatusOK {
			confirmed++
		}
	}
	if confirmed != 1 {
		t.Errorf("recorded confirmed recovery reboots: %d", confirmed)
	}
}

// TestSystemBackupAndRestore pins the backup download and the restore
// check/apply round trip.
func TestSystemBackupAndRestore(t *testing.T) {
	f := startSystemFake(t)
	resp, raw := get(t, f, "/api/system/v1/backup", litefake.DefaultToken)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(raw, litefake.BackupBlob()) ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), litefake.BackupFileName) {
		t.Fatalf("backup: %d %q %q", resp.StatusCode, raw, resp.Header.Get("Content-Disposition"))
	}
	if resp, raw := send(t, f, http.MethodPost, "/api/system/v1/backup/run", litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusAccepted {
		t.Errorf("backup run: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := send(t, f, http.MethodPost, "/api/system/v1/restore/check", litefake.DefaultToken, ""); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(t, raw) != "corrupt" {
		t.Errorf("empty restore: %d %s", resp.StatusCode, raw)
	}
	resp, raw = send(t, f, http.MethodPost, "/api/system/v1/restore/check", litefake.DefaultToken, string(litefake.BackupBlob()),
		"Content-Type", "application/octet-stream")
	check := decode[struct {
		File  string `json:"file"`
		Check struct {
			OK bool `json:"ok"`
		} `json:"check"`
	}](t, string(raw))
	if resp.StatusCode != http.StatusOK || check.File == "" || !check.Check.OK {
		t.Fatalf("restore check: %d %s", resp.StatusCode, raw)
	}
	resp, raw = send(t, f, http.MethodPost, "/api/system/v1/restore/apply", litefake.DefaultToken, `{"file":"`+check.File+`"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"rebooting":true`) {
		t.Errorf("restore apply: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := send(t, f, http.MethodPost, "/api/system/v1/restore/apply", litefake.DefaultToken, `{"file":"nope"}`); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(t, raw) != "restore-failed" {
		t.Errorf("restore unknown file: %d %s", resp.StatusCode, raw)
	}
}

// TestSystemUpdateStagesAndInstalls pins download → staged → install,
// and 409 when nothing is staged.
func TestSystemUpdateStagesAndInstalls(t *testing.T) {
	f := startSystemFake(t)
	if resp, _ := send(t, f, http.MethodPost, "/api/system/v1/system-update/install", litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("install without staged: %d", resp.StatusCode)
	}
	f.SetUpdateAvailable(&litefake.AvailableUpdate{Version: "1.1.0", Newer: true})
	_, raw := get(t, f, "/api/system/v1/system-update", litefake.DefaultToken)
	if !strings.Contains(string(raw), `"version":"1.1.0"`) || !strings.Contains(string(raw), `"lite":`) {
		t.Errorf("update state: %s", raw)
	}
	if resp, raw := send(t, f, http.MethodPost, "/api/system/v1/system-update/download", litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"version":"1.1.0"`) {
		t.Errorf("download: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := send(t, f, http.MethodPost, "/api/system/v1/system-update/install", litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusOK {
		t.Errorf("install: %d", resp.StatusCode)
	}
}

// TestSystemGroupsLifecycle pins group create/read/update/delete, the
// name rule and unknown-group.
func TestSystemGroupsLifecycle(t *testing.T) {
	f := startSystemFake(t)
	tok := litefake.DefaultToken
	resp, raw := send(t, f, http.MethodPost, "/api/system/v1/groups", tok, `{"name":"Heizung EG","type":"hmip.heating.group","members":["VCU2128127"]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"ref":"VirtualDevices.INT0000001"`) ||
		!strings.Contains(string(raw), `"devices_to_configure":[]`) {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	id := strconv.Itoa(decode[struct {
		ID int `json:"id"`
	}](t, string(raw)).ID)
	if resp, raw := send(t, f, http.MethodPost, "/api/system/v1/groups", tok, `{"name":"a\nb","type":"hmip.heating.group"}`); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), `"field":"name"`) {
		t.Errorf("two-line name: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := send(t, f, http.MethodPut, "/api/system/v1/groups/"+id, tok, `{"members":["A","B"]}`); resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"members":["A","B"]`) {
		t.Errorf("update: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := get(t, f, "/api/system/v1/groups/"+id, tok); resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"device":"INT0000001"`) {
		t.Errorf("detail: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := send(t, f, http.MethodDelete, "/api/system/v1/groups/"+id, tok, `{}`); resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"former_members":["A","B"]`) {
		t.Errorf("delete: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := get(t, f, "/api/system/v1/groups/"+id, tok); resp.StatusCode != http.StatusNotFound || errorCode(t, raw) != "unknown-group" {
		t.Errorf("deleted group: %d %s", resp.StatusCode, raw)
	}
}

// TestSystemServiceMessagesKnob pins what /service-messages reports.
func TestSystemServiceMessagesKnob(t *testing.T) {
	f := startSystemFake(t)
	f.SetServiceMessages([]litefake.ServiceMessage{{
		Interface: "HmIP-RF", Address: "VCU2128127", Channel: "0", Key: "LOW_BAT",
		Value: []byte("true"), Since: "2026-09-27T10:00:00Z", Seen: "event",
	}})
	_, raw := get(t, f, "/api/system/v1/service-messages", sysReadToken)
	if !strings.Contains(string(raw), `"count":1`) || !strings.Contains(string(raw), `"key":"LOW_BAT"`) {
		t.Errorf("service messages: %s", raw)
	}
}
