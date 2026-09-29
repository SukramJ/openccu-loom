// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

const (
	lampRef    = "BidCos-RF.VCU0000321"
	lampChRef  = "BidCos-RF.VCU0000321:1"
	metaWriter = "olt_writer"
)

func metaFake(t *testing.T) (*litefake.Fake, *occulited.Client) {
	t.Helper()
	f := startFake(t, litefake.Options{Meta: litefake.DefaultMeta(), Tokens: map[string][]string{metaWriter: {"meta:write"}}})
	return f, newClient(t, f.URL(), metaWriter)
}

func TestMetaReads(t *testing.T) {
	_, c := metaFake(t)
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lamp, ok := snap.Objects[lampChRef]
	if snap.Format != 1 || !ok || lamp.Name != "Stehlampe Schalter" || len(snap.Enums["room"].Tree) == 0 {
		t.Fatalf("snapshot %+v", snap)
	}
	if string(lamp.Meta["loom"]) != `{"icon":"floor-lamp"}` {
		t.Errorf("meta %s", lamp.Meta["loom"])
	}
	obj, err := c.Object(ctx, lampChRef)
	if err != nil || obj.Ref != lampChRef || obj.Object.Name != lamp.Name {
		t.Errorf("Object %+v %v", obj, err)
	}
	notOrphaned := false
	page, err := c.Objects(ctx, occulited.ObjectsQuery{Enum: "room/eg", Orphaned: &notOrphaned})
	if err != nil || len(page.Objects) != 4 {
		t.Errorf("Objects(room/eg) %d %v", len(page.Objects), err)
	}
	_, err = c.Objects(ctx, occulited.ObjectsQuery{Enum: "room/nowhere"})
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity || apiErr.Code != "unknown-path" {
		t.Errorf("unknown path: %v", err)
	}
	enums, err := c.Enums(ctx)
	if err != nil || enums.Enums["function"].Name["en"] == "" {
		t.Errorf("Enums %+v %v", enums, err)
	}
	tree, err := c.EnumTree(ctx, "room")
	if err != nil || tree.Enum != "room" || len(tree.Tree) != 2 {
		t.Errorf("EnumTree %+v %v", tree, err)
	}
	_, err = c.Object(ctx, "BidCos-RF.NOPE")
	if !errors.As(err, &apiErr) || apiErr.Code != "unknown-object" {
		t.Errorf("unknown object: %v", err)
	}
}

func TestMetaObjectWrites(t *testing.T) {
	f, c := metaFake(t)
	ctx := context.Background()
	rev0 := f.Meta().Revision()
	name := "Leselampe"
	m, err := c.PatchObject(ctx, lampChRef, occulited.ObjectPatch{Name: &name}, occulited.WriteOptions{})
	if err != nil || !m.Changed || m.Revision != rev0+1 {
		t.Fatalf("patch %+v %v", m, err)
	}
	again, err := c.PatchObject(ctx, lampChRef, occulited.ObjectPatch{Name: &name}, occulited.WriteOptions{})
	if err != nil || again.Changed || again.Revision != m.Revision {
		t.Errorf("unchanged patch %+v %v", again, err)
	}
	stale := rev0
	_, err = c.PatchObject(ctx, lampChRef, occulited.ObjectPatch{Name: &name}, occulited.WriteOptions{IfMatch: &stale})
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "revision-conflict" {
		t.Errorf("stale If-Match: %v", err)
	}
	m, err = c.PutObject(ctx, "HmIP-RF.VCU2128127:1", occulited.ObjectWrite{Name: "Kanal 1", Enums: []string{"room/og/kueche"}}, occulited.WriteOptions{})
	if err != nil || !m.Changed {
		t.Errorf("put %+v %v", m, err)
	}
	bulkName := "Stehlampe neu"
	m, err = c.Bulk(ctx, occulited.BulkRequest{
		Set:    map[string]occulited.ObjectPatch{lampRef: {Name: &bulkName, Meta: map[string]json.RawMessage{"loom": json.RawMessage(`{"x":1}`)}}},
		Delete: []string{"HmIP-RF.VCU2128127:1"},
	}, occulited.WriteOptions{})
	if err != nil || !m.Changed {
		t.Errorf("bulk %+v %v", m, err)
	}
	snap := f.Meta().Snapshot()
	if snap.Objects[lampRef].Name != bulkName || string(snap.Objects[lampRef].Meta["loom"]) != `{"x":1}` {
		t.Errorf("after bulk %+v", snap.Objects[lampRef])
	}
	if _, ok := snap.Objects["HmIP-RF.VCU2128127:1"]; ok {
		t.Error("bulk delete did not delete")
	}
	if m, err = c.DeleteObject(ctx, lampChRef, occulited.WriteOptions{}); err != nil || !m.Changed {
		t.Errorf("delete %+v %v", m, err)
	}
}

func TestMetaEnumAndNodeWrites(t *testing.T) {
	f, c := metaFake(t)
	ctx := context.Background()
	no := occulited.WriteOptions{}
	if _, err := c.CreateEnum(ctx, "floor", map[string]string{"en": "Floors"}, no); err != nil {
		t.Fatalf("CreateEnum: %v", err)
	}
	if _, err := c.PatchEnum(ctx, "floor", map[string]string{"en": "Levels"}, no); err != nil {
		t.Errorf("PatchEnum: %v", err)
	}
	if _, err := c.CreateNode(ctx, "room", occulited.NodeCreate{ID: "keller", Name: "Keller"}, no); err != nil {
		t.Fatalf("CreateNode root: %v", err)
	}
	parent := "room/keller"
	if _, err := c.CreateNode(ctx, "room", occulited.NodeCreate{Parent: &parent, ID: "werkstatt", Name: "Werkstatt"}, no); err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	newName := "Hobbyraum"
	if _, err := c.PatchNode(ctx, "room", "keller/werkstatt", occulited.NodePatch{Name: &newName}, no); err != nil {
		t.Errorf("PatchNode rename: %v", err)
	}
	if _, err := c.PatchNode(ctx, "room", "keller/werkstatt", occulited.NodePatch{Move: true}, no); err != nil {
		t.Errorf("PatchNode move to root: %v", err)
	}
	_, err := c.DeleteNode(ctx, "room", "eg", false, no)
	var apiErr *occulited.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "has-members" || len(apiErr.Detail) == 0 {
		t.Errorf("delete with members: %v", err)
	}
	if _, err := c.DeleteNode(ctx, "room", "eg", true, no); err != nil {
		t.Errorf("delete detach: %v", err)
	}
	if _, err := c.DeleteEnum(ctx, "floor", false, no); err != nil {
		t.Errorf("DeleteEnum: %v", err)
	}
	tree := f.Meta().Snapshot().Enums["room"].Tree
	ids := map[string]bool{}
	for _, n := range tree {
		ids[n.ID] = true
	}
	if !ids["werkstatt"] || !ids["keller"] || ids["eg"] {
		t.Errorf("room tree roots %v", ids)
	}
}

func TestNodePatchRendersParent(t *testing.T) {
	name := "x"
	for _, tc := range []struct {
		p    occulited.NodePatch
		want string
	}{
		{occulited.NodePatch{Name: &name}, `{"name":"x"}`},
		{occulited.NodePatch{Move: true}, `{"parent":null}`},
		{occulited.NodePatch{Move: true, Parent: "room/eg"}, `{"parent":"room/eg"}`},
	} {
		b, err := json.Marshal(tc.p)
		if err != nil || string(b) != tc.want {
			t.Errorf("%+v → %s %v, want %s", tc.p, b, err, tc.want)
		}
	}
	if got := occulited.EscapeRef("BidCos-RF.JEQ0230153:1"); got != "BidCos-RF.JEQ0230153%3A1" {
		t.Errorf("EscapeRef %q", got)
	}
}

func TestMetaStreamDeliversChangesAndResumes(t *testing.T) {
	f, c := metaFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.SetMetaHeartbeatInterval(time.Hour)
	s := c.MetaEvents(ctx, occulited.MetaStreamOptions{HeartbeatTimeout: 300 * time.Millisecond, Backoff: fastBackoff()})
	waitMeta(t, s, occulited.MetaOpen)
	if err := f.Meta().Rename(lampRef, "A"); err != nil {
		t.Fatal(err)
	}
	ev := waitMeta(t, s, occulited.MetaChange)
	if ev.Event.Kind != "object.updated" || ev.Event.Ref != lampRef || ev.Event.Revision != f.Meta().Revision() {
		t.Fatalf("change %+v", ev.Event)
	}
	if rev, ok := s.Revision(); !ok || rev != ev.Event.Revision {
		t.Errorf("revision %d %v", rev, ok)
	}
	// The fake's heartbeat is slower than the reader's timeout, so the
	// reader drops the connection and comes back with ?since=.
	waitMeta(t, s, occulited.MetaClosed)
	if err := f.Meta().Rename(lampRef, "B"); err != nil {
		t.Fatal(err)
	}
	next := waitMeta(t, s, occulited.MetaChange)
	if next.Event.Revision != ev.Event.Revision+1 {
		t.Errorf("revision %d after %d", next.Event.Revision, ev.Event.Revision)
	}

	if err := f.RestartBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.Meta().Rename(lampRef, "C"); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-s.Done()
	// A stream is recorded once it ended; wait for the server side.
	want := "since=" + strconv.Itoa(ev.Event.Revision)
	sinceSent := false
	for end := time.Now().Add(waitMsg); !sinceSent && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		for _, call := range f.Calls() {
			sinceSent = sinceSent || call.Path == "/api/meta/v1/events/sse" && call.RawQuery == want
		}
	}
	if !sinceSent {
		t.Errorf("no reconnect with %s in %+v", want, f.Calls())
	}

	// A reader starting at a revision the (restarted) box no longer
	// retains gets a resync.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	old := 1
	s2 := c.MetaEvents(ctx2, occulited.MetaStreamOptions{Since: &old, Backoff: fastBackoff()})
	rs := waitMeta(t, s2, occulited.MetaResync)
	if rs.Event.Revision != f.Meta().Revision() {
		t.Errorf("resync revision %d, store %d", rs.Event.Revision, f.Meta().Revision())
	}
}

func TestMetaStreamHeartbeatTimeout(t *testing.T) {
	f, c := metaFake(t)
	f.SetMetaHeartbeatInterval(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := c.MetaEvents(ctx, occulited.MetaStreamOptions{HeartbeatTimeout: 150 * time.Millisecond, Backoff: fastBackoff()})
	m := waitMeta(t, s, occulited.MetaClosed)
	if !errors.Is(m.Err, occulited.ErrHeartbeatTimeout) {
		t.Errorf("closed with %v", m.Err)
	}
}

func waitMeta(t *testing.T, s *occulited.MetaStream, kind occulited.MetaKind) occulited.MetaMessage {
	t.Helper()
	deadline := time.After(waitMsg)
	for {
		select {
		case m, ok := <-s.Messages():
			if !ok {
				t.Fatal("meta stream closed")
			}
			if m.Kind == kind {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s in time", kind)
		}
	}
}
