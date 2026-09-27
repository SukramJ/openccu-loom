// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

func startMetaFake(t *testing.T) *litefake.Fake {
	t.Helper()
	return startFake(t, litefake.Options{
		Meta:                  litefake.DefaultMeta(),
		MetaHeartbeatInterval: 100 * time.Millisecond,
		Tokens: map[string][]string{
			litefake.DefaultToken: {"*"},
			metaToken:             {"meta:read"},
		},
	})
}

// metaEvent is a change-stream event.
type metaEvent struct {
	Revision int    `json:"revision"`
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Enum     string `json:"enum"`
	Path     string `json:"path"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// nextData returns the next data frame of a metadata stream, asserting
// it carries neither id nor event name.
func nextData(t *testing.T, s *sseStream) metaEvent {
	t.Helper()
	deadline := time.Now().Add(waitFrame)
	for {
		fr, ok := s.next(t, time.Until(deadline))
		if !ok {
			t.Fatal("meta stream ended")
		}
		if fr.data == "" {
			continue
		}
		if fr.hasID || fr.event != "" {
			t.Errorf("meta frame carries id/event: %+v", fr)
		}
		return decode[metaEvent](t, fr.data)
	}
}

type revisionBody struct {
	Revision int `json:"revision"`
}

// TestMetaFixtureServesSnapshot pins the bundled fixture as served by
// /snapshot: revision, the room tree with full paths, named objects.
func TestMetaFixtureServesSnapshot(t *testing.T) {
	f := startMetaFake(t)
	resp, body := get(t, f, "/api/meta/v1/snapshot", metaToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, body)
	}
	doc := decode[litefake.Document](t, string(body))
	if doc.Format != 1 || doc.Revision != 1 || len(doc.Objects) != 5 {
		t.Errorf("snapshot header %d/%d/%d", doc.Format, doc.Revision, len(doc.Objects))
	}
	lamp := doc.Objects["BidCos-RF.VCU0000321:1"]
	if lamp.Name != "Stehlampe Schalter" || lamp.Enums[0] != "room/eg/wohnzimmer" {
		t.Errorf("object %+v", lamp)
	}
	room := doc.Enums["room"]
	if room.Name["de"] != "Räume" || len(room.Tree) != 2 || room.Tree[0].Children[1].ID != "kueche" {
		t.Errorf("room enum %+v", room)
	}
	if !strings.Contains(string(body), `"tree"`) || strings.Contains(string(body), `"orphaned"`) {
		t.Errorf("snapshot body shape: %s", body)
	}

	resp, body = get(t, f, "/api/meta/v1/objects?enum=room/eg/kueche", metaToken)
	objs := decode[struct {
		Revision int                        `json:"revision"`
		Objects  map[string]json.RawMessage `json:"objects"`
	}](t, string(body))
	if resp.StatusCode != http.StatusOK || len(objs.Objects) != 2 {
		t.Errorf("objects?enum: %d %s", resp.StatusCode, body)
	}
	// A subtree query: room/eg holds no object directly, but its
	// children wohnzimmer and kueche hold four; og/kueche's object is
	// outside the subtree.
	_, body = get(t, f, "/api/meta/v1/objects?enum=room/eg", metaToken)
	sub := decode[struct {
		Objects map[string]json.RawMessage `json:"objects"`
	}](t, string(body))
	if len(sub.Objects) != 4 || sub.Objects["BidCos-RF.VCU0000321:1"] == nil || sub.Objects["VirtualDevices.INT0000001"] != nil {
		t.Errorf("subtree query room/eg: %s", body)
	}
	resp, body = get(t, f, "/api/meta/v1/objects?enum=room/nowhere", metaToken)
	if resp.StatusCode != http.StatusUnprocessableEntity || errorCode(t, body) != "unknown-path" {
		t.Errorf("bad enum filter: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/meta/v1/objects/BidCos-RF.VCU0000321%3A1", metaToken)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ref":"BidCos-RF.VCU0000321:1"`) {
		t.Errorf("percent-encoded ref: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/meta/v1/objects/BidCos-RF.NOPE", metaToken)
	if resp.StatusCode != http.StatusNotFound || errorCode(t, body) != "unknown-object" {
		t.Errorf("missing object: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/meta/v1/enums/nope/tree", metaToken)
	if resp.StatusCode != http.StatusNotFound || errorCode(t, body) != "unknown-enum" {
		t.Errorf("missing enum: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/meta/v1/enums/room/tree", metaToken)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"enum":"room"`) {
		t.Errorf("tree: %d %s", resp.StatusCode, body)
	}
}

// TestMetaUnchangedMutationIs304WithETag pins the unchanged rule: 304,
// empty body, revision only in ETag, no revision bump; a real change
// answers 200 with the new revision in body and ETag.
func TestMetaUnchangedMutationIs304WithETag(t *testing.T) {
	f := startMetaFake(t)
	path := "/api/meta/v1/objects/BidCos-RF.VCU0000321"

	resp, body := send(t, f, http.MethodPatch, path, litefake.DefaultToken, `{"name":"Stehlampe"}`)
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("ETag") != "1" {
		t.Fatalf("unchanged: %d %q ETag %q", resp.StatusCode, body, resp.Header.Get("ETag"))
	}
	resp, body = send(t, f, http.MethodPatch, path, litefake.DefaultToken, `{"name":"  Leselampe "}`)
	if resp.StatusCode != http.StatusOK || decode[revisionBody](t, string(body)).Revision != 2 || resp.Header.Get("ETag") != "2" {
		t.Fatalf("changed: %d %s ETag %q", resp.StatusCode, body, resp.Header.Get("ETag"))
	}
	if got := f.Meta().Snapshot().Objects["BidCos-RF.VCU0000321"].Name; got != "Leselampe" {
		t.Errorf("name not trimmed/stored: %q", got)
	}
}

// TestMetaIfMatchConflict pins If-Match: a stale revision is 409
// revision-conflict and changes nothing; the current one passes.
func TestMetaIfMatchConflict(t *testing.T) {
	f := startMetaFake(t)
	path := "/api/meta/v1/objects/HmIP-RF.VCU2128127"
	resp, body := send(t, f, http.MethodPatch, path, litefake.DefaultToken, `{"name":"X"}`, "If-Match", "0")
	if resp.StatusCode != http.StatusConflict || errorCode(t, body) != "revision-conflict" {
		t.Fatalf("stale If-Match: %d %s", resp.StatusCode, body)
	}
	if f.Meta().Revision() != 1 {
		t.Errorf("a refused write bumped the revision")
	}
	if resp, body := send(t, f, http.MethodPatch, path, litefake.DefaultToken, `{"name":"X"}`, "If-Match", "1"); resp.StatusCode != http.StatusOK {
		t.Errorf("current If-Match: %d %s", resp.StatusCode, body)
	}
}

// TestMetaObjectWriteRules pins PATCH-creates-with-name, the invalid
// name, orphaned 403, unknown-field 422, unknown-path 422, PUT reset
// and DELETE.
func TestMetaObjectWriteRules(t *testing.T) {
	f := startMetaFake(t)
	tok := litefake.DefaultToken
	base := "/api/meta/v1/objects/"
	cases := []struct {
		method, ref, body string
		status            int
		code              string
	}{
		{http.MethodPatch, "HmIP-RF.NEW0001", `{"enums":["room/eg/kueche"]}`, 422, "invalid-name"},
		{http.MethodPatch, "HmIP-RF.NEW0001", `{"name":"   "}`, 422, "invalid-name"},
		{http.MethodPatch, "HmIP-RF.NEW0001", `{"name":"bad\u0007name"}`, 422, "invalid-name"},
		{http.MethodPatch, "HmIP-RF.VCU2128127", `{"orphaned":true}`, 403, "forbidden"},
		{http.MethodPatch, "HmIP-RF.VCU2128127", `{"colour":"red"}`, 422, "invalid-body"},
		{http.MethodPatch, "HmIP-RF.VCU2128127", `{"enums":["room/keller"]}`, 422, "unknown-path"},
		{http.MethodPatch, "HmIP-RF.VCU2128127", `{"meta":{"Bad NS":1}}`, 422, "invalid-id"},
		{http.MethodDelete, "HmIP-RF.NOPE", `{}`, 404, "unknown-object"},
	}
	for _, tc := range cases {
		resp, body := send(t, f, tc.method, base+tc.ref, tok, tc.body)
		if resp.StatusCode != tc.status || errorCode(t, body) != tc.code {
			t.Errorf("%s %s %s: %d %s, want %d %s", tc.method, tc.ref, tc.body, resp.StatusCode, body, tc.status, tc.code)
		}
	}

	if resp, body := send(t, f, http.MethodPatch, base+"HmIP-RF.NEW0001%3A1", tok, `{"name":"Neu","meta":{"loom":{"a":1}}}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH create: %d %s", resp.StatusCode, body)
	}
	if resp, _ := send(t, f, http.MethodPatch, base+"HmIP-RF.NEW0001%3A1", tok, `{"meta":{"loom":null}}`); resp.StatusCode != http.StatusOK {
		t.Errorf("PATCH meta null: %d", resp.StatusCode)
	}
	if m := f.Meta().Snapshot().Objects["HmIP-RF.NEW0001:1"].Meta; len(m) != 0 {
		t.Errorf("namespace not deleted: %v", m)
	}
	if resp, _ := send(t, f, http.MethodPut, base+"BidCos-RF.VCU0000321%3A1", tok, `{"name":"Nur Name"}`); resp.StatusCode != http.StatusOK {
		t.Errorf("PUT: %d", resp.StatusCode)
	}
	o := f.Meta().Snapshot().Objects["BidCos-RF.VCU0000321:1"]
	if len(o.Enums) != 0 || len(o.Meta) != 0 {
		t.Errorf("PUT did not reset optionals: %+v", o)
	}
	if resp, _ := send(t, f, http.MethodDelete, base+"BidCos-RF.VCU0000321%3A1", tok, `{}`); resp.StatusCode != http.StatusOK {
		t.Errorf("DELETE: %d", resp.StatusCode)
	}
	if resp, body := send(t, f, http.MethodPatch, base+"HmIP-RF.VCU2128127", metaToken, `{"name":"x"}`); resp.StatusCode != http.StatusForbidden ||
		!strings.Contains(string(body), `"scope":"meta:write"`) {
		t.Errorf("meta:read writing: %d %s", resp.StatusCode, body)
	}
}

// TestMetaNodeDeleteNeedsDetach pins has-members: deleting a node whose
// subtree objects reference is refused with the refs, and with
// members=detach removes the node and strips the paths.
func TestMetaNodeDeleteNeedsDetach(t *testing.T) {
	f := startMetaFake(t)
	resp, body := send(t, f, http.MethodDelete, "/api/meta/v1/enums/room/nodes/eg", litefake.DefaultToken, `{}`)
	if resp.StatusCode != http.StatusConflict || errorCode(t, body) != "has-members" {
		t.Fatalf("delete with members: %d %s", resp.StatusCode, body)
	}
	detail := decode[struct {
		Detail struct {
			Refs []string `json:"refs"`
		} `json:"detail"`
	}](t, string(body))
	if len(detail.Detail.Refs) != 4 {
		t.Errorf("refs %v", detail.Detail.Refs)
	}
	resp, body = send(t, f, http.MethodDelete, "/api/meta/v1/enums/room/nodes/eg?members=detach", litefake.DefaultToken, `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detach: %d %s", resp.StatusCode, body)
	}
	doc := f.Meta().Snapshot()
	if got := doc.Objects["BidCos-RF.VCU0000321"].Enums; len(got) != 1 || got[0] != "function/licht" {
		t.Errorf("paths after detach %v", got)
	}
	if len(doc.Enums["room"].Tree) != 1 {
		t.Errorf("tree after delete %+v", doc.Enums["room"].Tree)
	}
}

// TestMetaEnumAndNodeLifecycle pins enum create/rename/delete and node
// create, rename, move (with object paths rewritten) and the id, depth
// and conflict checks.
func TestMetaEnumAndNodeLifecycle(t *testing.T) {
	f := startMetaFake(t)
	tok := litefake.DefaultToken
	if resp, body := send(t, f, http.MethodPost, "/api/meta/v1/enums", tok, `{"id":"zone","name":{"en":"Zones"}}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("enum create: %d %s", resp.StatusCode, body)
	}
	if resp, body := send(t, f, http.MethodPost, "/api/meta/v1/enums", tok, `{"id":"Zone!","name":{"en":"x"}}`); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(t, body) != "invalid-id" {
		t.Errorf("bad enum id: %d %s", resp.StatusCode, body)
	}
	if resp, _ := send(t, f, http.MethodPatch, "/api/meta/v1/enums/zone", tok, `{"name":{"en":"Zones"}}`); resp.StatusCode != http.StatusNotModified {
		t.Errorf("unchanged enum rename: %d", resp.StatusCode)
	}
	if resp, body := send(t, f, http.MethodPost, "/api/meta/v1/enums/zone/nodes", tok, `{"parent":null,"id":"a","name":"A"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("root node: %d %s", resp.StatusCode, body)
	}
	if resp, body := send(t, f, http.MethodPost, "/api/meta/v1/enums/zone/nodes", tok, `{"parent":"room/eg","id":"b","name":"B"}`); resp.StatusCode != http.StatusUnprocessableEntity || errorCode(t, body) != "unknown-path" {
		t.Errorf("foreign parent: %d %s", resp.StatusCode, body)
	}
	parent := "zone/a"
	for depth := 2; depth <= 9; depth++ {
		id := "d" + strconv.Itoa(depth)
		resp, body := send(t, f, http.MethodPost, "/api/meta/v1/enums/zone/nodes", tok, `{"parent":"`+parent+`","id":"`+id+`","name":"N"}`)
		if depth <= 8 && resp.StatusCode != http.StatusCreated {
			t.Fatalf("depth %d: %d %s", depth, resp.StatusCode, body)
		}
		if depth == 9 && resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("depth 9 accepted: %d %s", resp.StatusCode, body)
		}
		parent += "/" + id
	}

	// og/kueche cannot move under eg, which already has a kueche.
	resp, body := send(t, f, http.MethodPatch, "/api/meta/v1/enums/room/nodes/og/kueche", tok, `{"parent":"room/eg"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("colliding move: %d %s", resp.StatusCode, body)
	}
	resp, body = send(t, f, http.MethodPatch, "/api/meta/v1/enums/room/nodes/og/kueche", tok, `{"parent":null,"name":"Küche oben"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move to root: %d %s", resp.StatusCode, body)
	}
	if got := f.Meta().Snapshot().Objects["VirtualDevices.INT0000001"].Enums[0]; got != "room/kueche" {
		t.Errorf("path not rewritten: %q", got)
	}
	if resp, _ := send(t, f, http.MethodDelete, "/api/meta/v1/enums/zone", tok, `{}`); resp.StatusCode != http.StatusOK {
		t.Errorf("enum delete: %d", resp.StatusCode)
	}
}

// TestMetaBulkIsOneRevision pins /objects:bulk: several changes, one
// revision, one event per changed object sharing it.
func TestMetaBulkIsOneRevision(t *testing.T) {
	f := startMetaFake(t)
	s, _ := openRaw(t, f, "/api/meta/v1/events/sse", litefake.DefaultToken)
	if fr, _ := s.next(t, waitFrame); fr.comment != "connected" {
		t.Fatalf("first frame %+v", fr)
	}
	resp, body := send(t, f, http.MethodPost, "/api/meta/v1/objects:bulk", litefake.DefaultToken,
		`{"set":{"HmIP-RF.VCU2128127":{"name":"A"},"HmIP-RF.NEW1":{"name":"B"}},"delete":["BidCos-RF.VCU0000321"]}`)
	if resp.StatusCode != http.StatusOK || decode[revisionBody](t, string(body)).Revision != 2 {
		t.Fatalf("bulk: %d %s", resp.StatusCode, body)
	}
	kinds := map[string]string{}
	for range 3 {
		ev := nextData(t, s)
		if ev.Revision != 2 {
			t.Errorf("event revision %d, want 2", ev.Revision)
		}
		kinds[ev.Ref] = ev.Kind
	}
	if kinds["BidCos-RF.VCU0000321"] != "object.deleted" || kinds["HmIP-RF.NEW1"] != "object.updated" {
		t.Errorf("bulk events %v", kinds)
	}
	resp, _ = send(t, f, http.MethodPost, "/api/meta/v1/objects:bulk", litefake.DefaultToken,
		`{"set":{"HmIP-RF.VCU2128127":{"name":"A"}},"delete":["BidCos-RF.MISSING"]}`)
	if resp.StatusCode != http.StatusNotFound || f.Meta().Revision() != 2 {
		t.Errorf("atomic bulk refusal: %d rev %d", resp.StatusCode, f.Meta().Revision())
	}
}

// TestMetaStreamReplaysSinceAndResyncs pins the change stream's resume:
// replay after since, resync for a future, negative, non-integer or
// no-longer-retained since, and after a restart.
func TestMetaStreamReplaysSinceAndResyncs(t *testing.T) {
	f := startMetaFake(t)
	if err := f.Meta().Rename("HmIP-RF.VCU2128127", "Eins"); err != nil {
		t.Fatal(err)
	}
	if err := f.Meta().Assign("HmIP-RF.VCU2128127", "room/og/kueche"); err != nil {
		t.Fatal(err)
	}

	s, resp := openRaw(t, f, "/api/meta/v1/events/sse?since=2", metaToken)
	if s == nil || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if ev := nextData(t, s); ev.Revision != 3 || ev.Kind != "object.updated" {
		t.Errorf("replay %+v", ev)
	}
	fr, _ := s.next(t, waitFrame)
	if fr.comment != "ping" {
		t.Errorf("after replay %+v, want ': ping'", fr)
	}
	if err := f.Meta().RenameNode("room", "eg", "EG"); err != nil {
		t.Fatal(err)
	}
	if ev := nextData(t, s); ev.Kind != "node.updated" || ev.Path != "room/eg" || ev.Revision != 4 {
		t.Errorf("live %+v", ev)
	}

	for _, since := range []string{"99", "-1", "abc"} {
		r, _ := openRaw(t, f, "/api/meta/v1/events/sse?since="+since, metaToken)
		if ev := nextData(t, r); ev.Kind != "resync" || ev.Revision != 4 {
			t.Errorf("since=%s: %+v", since, ev)
		}
	}
	if err := f.RestartBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, _ := openRaw(t, f, "/api/meta/v1/events/sse?since=3", metaToken)
	if ev := nextData(t, r); ev.Kind != "resync" {
		t.Errorf("after restart: %+v", ev)
	}
}

// TestMetaStreamLogBound pins the retained-log bound: a since older than
// the log resyncs.
func TestMetaStreamLogBound(t *testing.T) {
	f := startFake(t, litefake.Options{Meta: litefake.DefaultMeta(), MetaLog: 2})
	for i := range 3 {
		if err := f.Meta().Rename("HmIP-RF.VCU2128127", "N"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := openRaw(t, f, "/api/meta/v1/events/sse?since=1", litefake.DefaultToken)
	if ev := nextData(t, r); ev.Kind != "resync" {
		t.Errorf("since older than the log: %+v", ev)
	}
	r, _ = openRaw(t, f, "/api/meta/v1/events/sse?since=2", litefake.DefaultToken)
	if ev := nextData(t, r); ev.Kind != "object.updated" || ev.Revision != 3 {
		t.Errorf("since within the log: %+v", ev)
	}
}

// TestMetaHandleEmitsLikeHTTP pins the test handle: node moves and seeds
// emit the same events the HTTP mutations do.
func TestMetaHandleEmitsLikeHTTP(t *testing.T) {
	f := startMetaFake(t)
	s, _ := openRaw(t, f, "/api/meta/v1/events/sse", litefake.DefaultToken)
	if err := f.Meta().MoveNode("room", "og/kueche", ""); err != nil {
		t.Fatal(err)
	}
	moved := nextData(t, s)
	updated := nextData(t, s)
	if moved.Kind != "node.moved" || moved.From != "room/og/kueche" || moved.To != "room/kueche" ||
		updated.Kind != "object.updated" || updated.Ref != "VirtualDevices.INT0000001" || moved.Revision != updated.Revision {
		t.Errorf("move events %+v %+v", moved, updated)
	}
	if err := f.Meta().DeleteNode("function", "licht", false); err == nil {
		t.Error("DeleteNode with members succeeded without detach")
	}
	rev := f.Meta().Seed(*litefake.DefaultMeta())
	if ev := nextData(t, s); ev.Kind != "import" || ev.Revision != rev {
		t.Errorf("seed %+v", ev)
	}
}

// TestMetaStreamsAreNotCountedAgainstStreamLimit pins that metadata
// streams leave the lite-rpc per-token limit untouched.
func TestMetaStreamsAreNotCountedAgainstStreamLimit(t *testing.T) {
	f := startMetaFake(t)
	for i := range 3 {
		if s, resp := openRaw(t, f, "/api/meta/v1/events/sse", litefake.DefaultToken); s == nil {
			t.Fatalf("meta stream %d: %d", i, resp.StatusCode)
		}
	}
	if s, resp := openStream(t, f, litefake.DefaultToken, "", ""); s == nil {
		t.Errorf("lite-rpc stream after three meta streams: %d", resp.StatusCode)
	}
}

// TestFakeRequiresContentLengthOnWrites pins the web server rule: a
// write without Content-Length is 411.
func TestFakeRequiresContentLengthOnWrites(t *testing.T) {
	f := startMetaFake(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		f.URL()+"/api/meta/v1/objects/HmIP-RF.VCU2128127", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+litefake.DefaultToken)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusLengthRequired {
		t.Errorf("DELETE without body: %d, want 411", r.StatusCode)
	}
	if resp, _ := send(t, f, http.MethodDelete, "/api/meta/v1/objects/HmIP-RF.VCU2128127", litefake.DefaultToken, `{}`); resp.StatusCode != http.StatusOK {
		t.Errorf("DELETE with {}: %d", resp.StatusCode)
	}
}
