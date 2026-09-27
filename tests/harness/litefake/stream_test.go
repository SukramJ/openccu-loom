// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

const waitFrame = 5 * time.Second

// helloPayload is the data of the hello message.
type helloPayload struct {
	BootID     string `json:"boot_id"`
	Seq        uint64 `json:"seq"`
	Interfaces []struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		State      string `json:"state"`
		Registered bool   `json:"registered"`
	} `json:"interfaces"`
	Buffer struct {
		Seconds int `json:"seconds"`
		Events  int `json:"events"`
	} `json:"buffer"`
}

// readHello consumes ": connected" and the hello frame.
func readHello(t *testing.T, s *sseStream) (frame, helloPayload) {
	t.Helper()
	first, ok := s.next(t, waitFrame)
	if !ok || first.comment != "connected" || first.event != "" {
		t.Fatalf("first frame %+v, want the ': connected' comment", first)
	}
	hello, ok := s.next(t, waitFrame)
	if !ok || hello.event != "hello" || !hello.hasID {
		t.Fatalf("second frame %+v, want hello with an id", hello)
	}
	return hello, decode[helloPayload](t, hello.data)
}

// TestFakeStreamsHelloThenEvents pins the stream opening and a live
// event: headers, ": connected", hello first with "<boot>-<seq>" as id,
// then a device-originated value as an event frame with id.
func TestFakeStreamsHelloThenEvents(t *testing.T) {
	f := startFake(t, litefake.Options{})
	s, resp := openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", "")
	if s == nil {
		t.Fatalf("events: %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-store",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s %q, want %q", k, got, want)
		}
	}
	hello, h := readHello(t, s)
	if h.BootID != f.BootID() || hello.id != f.BootID()+"-"+itoa(h.Seq) {
		t.Errorf("hello id %q, data %+v", hello.id, h)
	}
	if len(h.Interfaces) != 3 || h.Buffer.Seconds != 300 || h.Buffer.Events != 5000 {
		t.Errorf("hello %s", hello.data)
	}
	for _, row := range h.Interfaces {
		if !row.Registered || row.State != "up" || row.URL == "" {
			t.Errorf("hello interface %+v", row)
		}
	}

	fireSwitch(t, f, true)
	ev := s.nextMessage(t, waitFrame)
	if ev.event != "event" || !ev.hasID || !strings.HasPrefix(ev.id, f.BootID()+"-") {
		t.Fatalf("event frame %+v", ev)
	}
	p := decode[eventPayload](t, ev.data)
	if p.Interface != bidcosRF || p.Address != switchChannel || p.Key != "STATE" || string(p.Value) != "true" {
		t.Errorf("event %s", ev.data)
	}
	if !p.Confirmed || p.Batch == 0 || p.TS == "" {
		t.Errorf("event metadata %s", ev.data)
	}
}

func itoa(n uint64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// TestFakeResumesFromLastEventID pins the replay: reconnecting with the
// id of a seen event replays exactly the later events, once each.
func TestFakeResumesFromLastEventID(t *testing.T) {
	f := startFake(t, litefake.Options{})
	s, _ := openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", "")
	readHello(t, s)
	fireSwitch(t, f, true)
	first := s.nextMessage(t, waitFrame)
	s.close()

	fireSwitch(t, f, false)
	fireSwitch(t, f, true)

	r, _ := openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", first.id)
	readHello(t, r)
	second := r.nextMessage(t, waitFrame)
	third := r.nextMessage(t, waitFrame)
	if second.id == first.id || third.id == second.id {
		t.Fatalf("replayed ids %q %q after %q", second.id, third.id, first.id)
	}
	if v := decode[eventPayload](t, second.data).Value; string(v) != "false" {
		t.Errorf("first replayed value %s, want false", v)
	}
	if v := decode[eventPayload](t, third.data).Value; string(v) != "true" {
		t.Errorf("second replayed value %s, want true", v)
	}
	select {
	case fr := <-r.frames:
		if fr.event == "event" {
			t.Errorf("extra event after the replay: %+v", fr)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// resyncReason reads the frame after hello and returns its reason,
// asserting it carries no id.
func resyncReason(t *testing.T, s *sseStream) string {
	t.Helper()
	fr := s.nextMessage(t, waitFrame)
	if fr.event != "resync" {
		t.Fatalf("frame %+v, want resync", fr)
	}
	if fr.hasID {
		t.Errorf("resync carries id %q", fr.id)
	}
	return decode[struct {
		Reason string `json:"reason"`
	}](t, fr.data).Reason
}

// TestFakeSendsResyncBootGapAndOverflow pins the three resync reasons:
// an id of another boot, a position the ring no longer covers, and a
// stream that lost messages (resync at the next heartbeat, then end).
func TestFakeSendsResyncBootGapAndOverflow(t *testing.T) {
	f := startFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})

	s, _ := openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", "0123456789abcdef-1")
	readHello(t, s)
	if got := resyncReason(t, s); got != "boot" {
		t.Errorf("foreign boot id: %q", got)
	}
	s.close()

	fireSwitch(t, f, true)
	fireSwitch(t, f, false)
	old := f.BootID() + "-1"
	f.ForceGap()
	s, _ = openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", old)
	readHello(t, s)
	if got := resyncReason(t, s); got != "gap" {
		t.Errorf("uncovered position: %q", got)
	}
	s.close()

	// A client already at the newest position gets neither a replay
	// nor a resync, even with the ring emptied.
	s, _ = openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", f.LastEventID())
	readHello(t, s)
	fr, _ := s.next(t, waitFrame)
	if fr.event != "" || fr.comment != "ping" {
		t.Errorf("up-to-date resume: %+v, want only heartbeats", fr)
	}
	s.close()

	s, _ = openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", "")
	readHello(t, s)
	f.ForceOverflow()
	seen := s.expectEnd(t, waitFrame)
	if len(seen) == 0 || seen[len(seen)-1].event != "resync" ||
		!strings.Contains(seen[len(seen)-1].data, `"overflow"`) || seen[len(seen)-1].hasID {
		t.Errorf("overflow: frames %+v", seen)
	}

	prevBoot := f.LastEventID()
	if err := f.RestartBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ = openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", prevBoot)
	readHello(t, s)
	if got := resyncReason(t, s); got != "boot" {
		t.Errorf("after restart: %q", got)
	}
}

// TestFakeLimitsStreamsPerToken pins the stream limits: a third stream
// of one token and any stream past the total answer 429
// too-many-streams; a slot frees once a stream closes.
func TestFakeLimitsStreamsPerToken(t *testing.T) {
	f := startFake(t, litefake.Options{
		StreamsTotal: 3,
		Tokens:       map[string][]string{litefake.DefaultToken: {"*"}, readToken: {"rpc:read"}},
	})
	a, _ := openStream(t, f, litefake.DefaultToken, "", "")
	b, _ := openStream(t, f, litefake.DefaultToken, "", "")
	if a == nil || b == nil {
		t.Fatal("first two streams refused")
	}
	readHello(t, a)
	readHello(t, b)
	if s, resp := openStream(t, f, litefake.DefaultToken, "", ""); s != nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third stream per token: %d", resp.StatusCode)
	}
	c, _ := openStream(t, f, readToken, "", "")
	if c == nil {
		t.Fatal("another token's first stream refused")
	}
	readHello(t, c)
	resp, body := get(t, f, "/api/rpc/v1/events", readToken)
	if resp.StatusCode != http.StatusTooManyRequests || errorCode(t, body) != "too-many-streams" ||
		!strings.Contains(string(body), "3 in total") {
		t.Errorf("total limit: %d %s", resp.StatusCode, body)
	}

	a.close()
	deadline := time.Now().Add(waitFrame)
	for f.OpenStreams() > 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s, resp := openStream(t, f, litefake.DefaultToken, "", ""); s == nil {
		t.Errorf("slot not freed after close: %d", resp.StatusCode)
	}
}

// TestFakeStreamHeartbeatAndRevocation pins ": ping" at the heartbeat
// interval and the end of a stream whose token was revoked.
func TestFakeStreamHeartbeatAndRevocation(t *testing.T) {
	f := startFake(t, litefake.Options{})
	f.SetHeartbeatInterval(100 * time.Millisecond)
	s, _ := openStream(t, f, litefake.DefaultToken, "type=event&key=STATE", "")
	readHello(t, s)
	fr, ok := s.next(t, waitFrame)
	if !ok || fr.comment != "ping" {
		t.Fatalf("frame %+v, want ': ping'", fr)
	}
	f.SetTokens(map[string][]string{readToken: {"rpc:read"}})
	s.expectEnd(t, waitFrame)
}

// TestFakeFiltersStreamMessages pins the filters: address matches the
// device and its channels, key applies to events only, hello always
// passes, and DropStreams ends the stream.
func TestFakeFiltersStreamMessages(t *testing.T) {
	f := startFake(t, litefake.Options{})
	s, _ := openStream(t, f, litefake.DefaultToken, "address=VCU0000321&key=STATE&type=event,interface", "")
	readHello(t, s)
	other, _ := openStream(t, f, litefake.DefaultToken, "address=VCU9999999&type=event", "")
	readHello(t, other)

	fireSwitch(t, f, true)
	if err := f.SetInterfaceDown(bidcosRF, true); err != nil {
		t.Fatal(err)
	}
	ev := s.nextMessage(t, waitFrame)
	if ev.event != "event" || decode[eventPayload](t, ev.data).Address != switchChannel {
		t.Errorf("first %+v", ev)
	}
	iface := s.nextMessage(t, waitFrame)
	if iface.event != "interface" || !strings.Contains(iface.data, `"state":"down"`) {
		t.Errorf("second %+v", iface)
	}
	select {
	case fr := <-other.frames:
		if fr.event != "" {
			t.Errorf("filtered stream got %+v", fr)
		}
	case <-time.After(200 * time.Millisecond):
	}

	f.DropStreams()
	s.expectEnd(t, waitFrame)
}

// TestFakeStreamsDeviceSnapshots pins ?devices=1: one id-less
// newDevices frame per interface carrying full descriptions, and the
// device-list messages the subscriber saw at init carrying addresses.
func TestFakeStreamsDeviceSnapshots(t *testing.T) {
	f := startFake(t, litefake.Options{})
	s, _ := openStream(t, f, litefake.DefaultToken, "devices=1&interface=BidCos-RF&type=newDevices", "")
	readHello(t, s)
	snap := s.nextMessage(t, waitFrame)
	if snap.event != "newDevices" || snap.hasID {
		t.Fatalf("snapshot frame %+v", snap)
	}
	p := decode[struct {
		Interface string            `json:"interface"`
		Devices   []json.RawMessage `json:"devices"`
	}](t, snap.data)
	if p.Interface != bidcosRF || len(p.Devices) == 0 || !strings.Contains(snap.data, `"ADDRESS":"VCU0000321"`) {
		t.Errorf("snapshot %s", snap.data)
	}

	r, _ := openStream(t, f, litefake.DefaultToken, "interface=BidCos-RF&type=newDevices", f.BootID()+"-0")
	readHello(t, r)
	list := r.nextMessage(t, waitFrame)
	if !list.hasID || !strings.Contains(list.data, `"addresses":[`) || strings.Contains(list.data, "PARAMSETS") {
		t.Errorf("device-list message %+v", list)
	}
}
