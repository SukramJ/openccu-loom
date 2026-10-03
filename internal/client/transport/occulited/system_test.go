// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func TestSystemReads(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	h, err := c.Health(ctx)
	if err != nil || !h.OK || h.Release == "" || h.Version == "" {
		t.Errorf("health %+v %v", h, err)
	}
	st, err := c.Status(ctx)
	if err != nil || st.Hostname != litefake.DefaultHostname || st.Timezone == "" || len(st.Raw) == 0 {
		t.Errorf("status %+v %v", st, err)
	}
	tm, err := c.Time(ctx)
	if err != nil || tm.Zone == "" || !tm.HasNTP {
		t.Errorf("time %+v %v", tm, err)
	}
	f.SetServiceMessages([]litefake.ServiceMessage{{
		Interface: "HmIP-RF", Address: "VCU2128127", Channel: "0", Key: "LOW_BAT",
		Value: []byte("true"), Since: "2026-09-26T10:00:00Z", Seen: "event",
	}})
	sm, err := c.ServiceMessages(ctx)
	if err != nil || sm.Count != 1 || sm.Messages[0].Key != "LOW_BAT" || sm.Messages[0].Since.IsZero() {
		t.Errorf("service messages %+v %v", sm, err)
	}
}

func TestSystemUpdateFlow(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	u, err := c.SystemUpdate(ctx)
	if err != nil || u.Running.Lite == "" || u.Staged != nil || u.Feed != nil {
		t.Fatalf("update %+v %v", u, err)
	}
	_, err = c.InstallSystemUpdate(ctx)
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Errorf("install without staged: %v", err)
	}
	f.SetUpdateAvailable(&litefake.AvailableUpdate{Version: "1.1.0", Newer: true, Size: 10})
	u, err = c.CheckSystemUpdate(ctx)
	if err != nil || u.Feed == nil || u.Feed.Available == nil || !u.Feed.Available.Newer {
		t.Fatalf("check %+v %v", u, err)
	}
	staged, err := c.DownloadSystemUpdate(ctx)
	if err != nil || staged.Version != "1.1.0" {
		t.Errorf("download %+v %v", staged, err)
	}
	if ans, err := c.InstallSystemUpdate(ctx); err != nil || !ans.OK {
		t.Errorf("install %+v %v", ans, err)
	}
}

func TestSystemPowerSendsConfirm(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	for name, fn := range map[string]func(context.Context) (occulited.PowerAnswer, error){
		"/api/system/v1/reboot": c.Reboot, "/api/system/v1/halt": c.Halt, "/api/system/v1/reboot/recovery": c.RebootRecovery,
	} {
		if ans, err := fn(ctx); err != nil || !ans.OK {
			t.Errorf("%s: %+v %v", name, ans, err)
		}
		found := false
		for _, call := range f.Calls() {
			if call.Path == name && string(call.Body) == `{"confirm":true}` {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no confirmed call recorded", name)
		}
	}
	scoped := newClient(t, f.URL(), "reader")
	f.SetTokens(map[string][]string{"reader": {"system:read"}})
	_, err := scoped.Reboot(ctx)
	var sm *hmerr.ScopeMissingError
	if !errors.As(err, &sm) || sm.Scope != "power" {
		t.Errorf("reboot without power: %v", err)
	}
}

func TestBackupAndRestore(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	b, err := c.DownloadBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(b.Body)
	_ = b.Body.Close()
	if err != nil || !bytes.Equal(data, litefake.BackupBlob()) || b.FileName != litefake.BackupFileName || b.Size != int64(len(data)) {
		t.Fatalf("backup %q %d %v", b.FileName, b.Size, err)
	}
	run, err := c.RunBackup(ctx, "")
	if err != nil || !run.Started || run.Instance == "" {
		t.Errorf("run %+v %v", run, err)
	}
	check, err := c.CheckRestore(ctx, b.FileName, bytes.NewReader(data))
	if err != nil || !check.Check.OK || check.File == "" || !check.Encryption.CreatedHere {
		t.Fatalf("check %+v %v", check, err)
	}
	apply, err := c.ApplyRestore(ctx, occulited.RestoreApplyRequest{File: check.File})
	if err != nil || !apply.OK || !apply.Rebooting {
		t.Errorf("apply %+v %v", apply, err)
	}
	_, err = c.CheckRestore(ctx, "empty.sbk", strings.NewReader(""))
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "corrupt" {
		t.Errorf("empty archive: %v", err)
	}
}

func TestDispositionFileNameIsReducedToABaseName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../etc/box.sbk.age"`)
		_, _ = io.WriteString(w, "x")
	}))
	defer srv.Close()
	b, err := newClient(t, srv.URL, "t").DownloadBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Body.Close()
	if b.FileName != "box.sbk.age" || b.Size != -1 && b.Size != 1 {
		t.Errorf("file name %q size %d", b.FileName, b.Size)
	}
}

func TestBackupTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/v1/backup/targets" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"nightly":true,"targets":[{"id":"usb","name":"USB","kind":"usb","enabled":true,`+
			`"state":{"state":"running"},"last_backup":{"at":"2026-09-26T02:00:00Z","ok":false,"state":"failed","error":"full","encrypted":false}}]}`)
	}))
	defer srv.Close()
	bt, err := newClient(t, srv.URL, "t").BackupTargets(context.Background())
	if err != nil || len(bt.Targets) != 1 || bt.Targets[0].State.State != "running" || bt.Targets[0].LastBackup == nil ||
		bt.Targets[0].LastBackup.Error != "full" || !bytes.Contains(bt.Raw, []byte("nightly")) {
		t.Errorf("targets %+v %v", bt, err)
	}
}

func TestGroupsCRUD(t *testing.T) {
	const member = "0000000000AA01:1"
	f := startFake(t, litefake.Options{GroupCandidates: map[string][]litefake.GroupMember{
		"hmip.heating.group": {{ID: member, Serial: member, Type: "SENSOR_WINDOW"}},
	}})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	types, err := c.GroupTypes(ctx)
	if err != nil || len(types.Types) != 2 {
		t.Fatalf("types %+v %v", types, err)
	}
	created, err := c.CreateGroup(ctx, occulited.GroupCreate{Name: "OG", Type: types.Types[1].ID, Members: []string{member}})
	if err != nil || created.ID == 0 || created.Ref != "VirtualDevices."+created.Device ||
		len(created.Members) != 1 || created.Members[0].ID != member || len(created.DevicesToConfigure) != 1 {
		t.Fatalf("create %+v %v", created, err)
	}
	name := "Obergeschoss"
	forbid := true
	upd, err := c.UpdateGroup(ctx, created.ID, occulited.GroupUpdate{Name: &name, ForbidSingleOperation: &forbid})
	if err != nil || upd.Name != name || !upd.ForbidSingleOperation {
		t.Errorf("update %+v %v", upd, err)
	}
	list, err := c.Groups(ctx)
	if err != nil || len(list.Groups) != 1 {
		t.Errorf("list %+v %v", list, err)
	}
	detail, err := c.Group(ctx, created.ID)
	if err != nil || len(detail.Members) != 1 || detail.Members[0] != (occulited.GroupMember{ID: member, Serial: member, Type: "SENSOR_WINDOW"}) {
		t.Errorf("detail %+v %v", detail, err)
	}
	del, err := c.DeleteGroup(ctx, created.ID)
	if err != nil || del.Deleted != created.ID || len(del.FormerMembers) != 1 || del.FormerMembers[0].ID != member {
		t.Errorf("delete %+v %v", del, err)
	}
	_, err = c.Group(ctx, created.ID)
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "unknown-group" {
		t.Errorf("deleted group: %v", err)
	}
}

// boxGroupsAnswer is the shape a real openccu-lite box answers GET /groups
// and GET /groups/types with (occulited 1.0.0-dev.30, read on 2026-09-28;
// names and addresses neutralised): group ids are JSON numbers, and a
// candidate member is an object naming a channel address.
const boxGroupsAnswer = `{"devices_to_configure":[],"groups":[
 {"id":4,"name":"Upstairs","type":"hmip.heating.group","type_label":"HmIP-Heizungssteuerung","device":"INT0000004","ref":"VirtualDevices.INT0000004"},
 {"id":5,"name":"Downstairs","type":"HomeMatic.heating","type_label":"Heating_Control","device":"INT0000005","ref":"VirtualDevices.INT0000005"}]}`

const boxGroupTypesAnswer = `{"types":[
 {"id":"HomeMatic.heating","label":"Heating_Control","assignable":[],"leftover":[]},
 {"id":"hmip.heating.group","label":"HmIP-Heizungssteuerung","assignable":[
  {"id":"0000000000AA01:1","serial":"0000000000AA01:1","type":"SENSOR_WINDOW"}],"leftover":[]}]}`

// TestGroupsDecodeTheBoxShape pins the groups answers to what a real box
// sends. The fake answered string ids, which is why a client decoding them
// as strings passed every test and failed against the first box.
func TestGroupsDecodeTheBoxShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/system/v1/groups":
			_, _ = io.WriteString(w, boxGroupsAnswer)
		case "/api/system/v1/groups/types":
			_, _ = io.WriteString(w, boxGroupTypesAnswer)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "t")
	list, err := c.Groups(context.Background())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(list.Groups) != 2 || list.Groups[0].ID != 4 || list.Groups[1].ID != 5 || list.Groups[0].Name != "Upstairs" {
		t.Errorf("groups %+v", list.Groups)
	}
	types, err := c.GroupTypes(context.Background())
	if err != nil || len(types.Types) != 2 || len(types.Types[1].Assignable) != 1 {
		t.Errorf("types %+v %v", types, err)
	}
}

// TestGroupDeleteDecodesTheBoxShape pins DELETE /groups/{id} to what a
// real box answers (read on 2026-09-28): "deleted" is the deleted group's
// id as a JSON number, not a flag, so a client decoding a boolean reported
// a failure for a group the box had deleted.
func TestGroupDeleteDecodesTheBoxShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/system/v1/groups/6" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"deleted":6,"former_members":[]}`)
	}))
	defer srv.Close()
	del, err := newClient(t, srv.URL, "t").DeleteGroup(context.Background(), 6)
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if del.Deleted != 6 {
		t.Errorf("deleted = %v, want the group id 6", del.Deleted)
	}
}
