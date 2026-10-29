// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	"github.com/SukramJ/go-fabric/eligibility"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// serveNodeAPI mounts go-fabric-ui's node routes over opts as the router
// does (rest.NodeAPI under /api/v1, with the boot-window replacement),
// with every request admitted as admin, and serves one request. It is the
// test's way to drive a window, a selection or a read the way the
// operator does, through the handler, rather than through a seam of the
// daemon's own.
func serveNodeAPI(t *testing.T, opts fabricui.Options, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveNodeAPIOn(t, newNodeAPI(t, opts), method, path, body)
}

// newNodeAPI is the handler serveNodeAPI serves one request on; a test
// that drives several requests against one window keeps it.
func newNodeAPI(t *testing.T, opts fabricui.Options) http.Handler {
	t.Helper()
	opts.Prefix = "/api/v1"
	opts.Admin = func(*http.Request) bool { return true }
	h, err := rest.NodeAPI(opts)
	if err != nil {
		t.Fatalf("mount node API: %v", err)
	}
	return h
}

func serveNodeAPIOn(t *testing.T, mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type recordingAudit struct{ entries []audit.Entry }

func (a *recordingAudit) Record(e audit.Entry)   { a.entries = append(a.entries, e) }
func (a *recordingAudit) List(int) []audit.Entry { return a.entries }

// observedNode is a node the bridge observes: it hands the registered
// observers to the test, which fires the transitions a node reports.
type observedNode struct {
	window    node.WindowSnapshot
	observers []node.Observers
}

func (n *observedNode) Observe(o node.Observers) func() {
	i := len(n.observers)
	n.observers = append(n.observers, o)
	return func() { n.observers[i] = node.Observers{} }
}

func (n *observedNode) Window() node.WindowSnapshot { return n.window }

// TestWireMatterNodeEventsPublishesEveryEventTypeUnderTheNodeTopic pins the
// WebSocket bridge: each of go-fabric-ui's event types reaches the hub as
// `node.<type>` with the stream's payload — the fabric and window
// transitions from the node's own observers, which go-fabric-ui no longer
// routes through the event source, the endpoint and exposure changes from
// the event source — and only a removed fabric lands in the audit ledger.
// The negative control is the unsubscribe: after it, the hub sees nothing.
func TestWireMatterNodeEventsPublishesEveryEventTypeUnderTheNodeTopic(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	src := fabricui.NewEvents()
	n := &observedNode{}
	rec := &recordingAudit{}
	unsub := wireMatterNodeEvents(src, n, hub, rec)
	if len(n.observers) != 1 {
		t.Fatalf("the bridge registered %d node observers, want 1", len(n.observers))
	}
	obs := n.observers[0]

	events := []fabricui.Event{
		{Type: fabricui.EventFabricAdded, Data: fabricui.FabricEvent{FabricIndex: 2, VendorID: 0x1349, VendorName: eligibility.VendorName(0x1349), Label: "Home"}},
		{Type: fabricui.EventWindowOpened, Data: fabricui.WindowEvent{Discriminator: 3840, DurationSeconds: 300}},
		{Type: fabricui.EventWindowClosed, Data: fabricui.WindowEvent{}},
		{Type: fabricui.EventEndpointsChanged, Data: struct{}{}},
		{Type: fabricui.EventExposureChanged, Data: fabricui.ExposureEvent{Keys: []string{"ccu|000A|4|custom|switch"}}},
		{Type: fabricui.EventFabricRemoved, Data: fabricui.FabricEvent{FabricIndex: 2}},
	}
	if err := obs.OnFabricAdded(node.FabricInfo{FabricIndex: 2, VendorID: 0x1349, Label: "Home"}); err != nil {
		t.Fatal(err)
	}
	if err := obs.OnWindowTransition(node.WindowSnapshot{Open: true, Discriminator: 3840, Timeout: 300 * time.Second}); err != nil {
		t.Fatal(err)
	}
	// A repeated snapshot of the same state is no transition.
	if err := obs.OnWindowTransition(node.WindowSnapshot{Open: true, Discriminator: 3840, Timeout: 300 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := obs.OnWindowTransition(node.WindowSnapshot{}); err != nil {
		t.Fatal(err)
	}
	src.Publish(events[3])
	src.Publish(events[4])
	if err := obs.OnFabricRemoved(node.FabricInfo{FabricIndex: 2}); err != nil {
		t.Fatal(err)
	}
	// The hub's replay ring is what a subscriber resuming from cursor 0
	// reads; it holds every published frame in order.
	got := hub.Replay(0, func(topic string) bool { return strings.HasPrefix(topic, nodeTopicPrefix) }).Events
	if len(got) != len(events) {
		t.Fatalf("hub holds %d node frames, want %d: %+v", len(got), len(events), got)
	}
	for i, ev := range events {
		want := nodeTopicPrefix + ev.Type
		frame := got[i]
		if frame.Topic != want || frame.Type != want {
			t.Errorf("frame %d topic/type = %q/%q, want %q", i, frame.Topic, frame.Type, want)
		}
		if frame.When.IsZero() {
			t.Errorf("frame %d carries no timestamp", i)
		}
		gotPayload, _ := json.Marshal(frame.Payload)
		wantPayload, _ := json.Marshal(ev.Data)
		if !bytes.Equal(gotPayload, wantPayload) {
			t.Errorf("%s payload = %s, want the stream's %s", want, gotPayload, wantPayload)
		}
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionMatterFabricRevoke || rec.entries[0].User != nodeAuditActor || rec.entries[0].Note != "fabric_index=2" {
		t.Errorf("audit = %+v, want one matter_fabric_revoke by %q for fabric 2", rec.entries, nodeAuditActor)
	}

	unsub()
	src.Publish(fabricui.Event{Type: fabricui.EventEndpointsChanged, Data: struct{}{}})
	if n.observers[0].OnFabricAdded != nil {
		t.Fatal("the unsubscribe left the node observer registered")
	}
	if after := hub.Replay(0, nil).Events; len(after) != len(events) {
		t.Fatalf("a frame reached the hub after the unsubscribe: %d frames, want %d", len(after), len(events))
	}
}

// TestWireMatterNodeEventsWithoutASourceIsANoop: a disabled bridge has no
// event source and no node; the bridge must not dereference either.
func TestWireMatterNodeEventsWithoutASourceIsANoop(t *testing.T) {
	t.Parallel()
	wireMatterNodeEvents(nil, nil, ws.NewHub(), nil)()
}
