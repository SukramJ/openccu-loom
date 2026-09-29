// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

func openEvents(t *testing.T, c *occulited.Client, opts occulited.EventsOptions) *occulited.Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if opts.Backoff == (occulited.Backoff{}) {
		opts.Backoff = fastBackoff()
	}
	s := c.Events(ctx, opts)
	t.Cleanup(func() {
		cancel()
		<-s.Done()
	})
	return s
}

func TestEventsHelloThenEvent(t *testing.T) {
	f := startFake(t, litefake.Options{HeartbeatInterval: 100 * time.Millisecond})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	s := openEvents(t, c, occulited.EventsOptions{Interfaces: []string{bidcosRF}, Types: []string{"event"}, Keys: []string{"STATE"}})

	open := <-s.Messages()
	if open.Kind != occulited.KindOpen {
		t.Fatalf("first message %+v, want open", open)
	}
	if m := <-s.Messages(); m.Kind != occulited.KindComment || m.Comment != "connected" {
		t.Fatalf("second message %+v, want the connected comment", m)
	}
	hello := nextMsg(t, s)
	if hello.Kind != occulited.KindHello || hello.Hello.BootID != f.BootID() ||
		hello.ID != hello.Hello.BootID+"-"+strconv.FormatUint(hello.Hello.Seq, 10) {
		t.Fatalf("hello %s id %q", hello.Data, hello.ID)
	}
	if len(hello.Hello.Interfaces) != 3 || hello.Hello.Buffer.Events != 5000 {
		t.Errorf("hello data %+v", hello.Hello)
	}
	fireSwitch(t, f, true)
	ev := nextMsg(t, s)
	if ev.Kind != occulited.KindEvent || ev.Event.Address != switchChannel || ev.Event.Key != "STATE" ||
		string(ev.Event.Value) != "true" || ev.Event.TS.IsZero() || ev.Event.Batch == 0 {
		t.Fatalf("event %+v %+v", ev, ev.Event)
	}
	if s.Position() != ev.ID || !strings.HasPrefix(ev.ID, f.BootID()+"-") {
		t.Errorf("position %q, event id %q", s.Position(), ev.ID)
	}
	if m := waitKind(t, s, occulited.KindComment); m.Comment != "ping" {
		t.Errorf("heartbeat comment %q", m.Comment)
	}
}

func TestEventsSendsFilters(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	s := openEvents(t, c, occulited.EventsOptions{
		Interfaces: []string{bidcosRF, hmipRF}, Types: []string{"event", "newDevices"},
		Addresses: []string{"VCU0000321"}, Keys: []string{"STATE"}, Devices: true,
	})
	waitKind(t, s, occulited.KindHello)
	snap := waitKind(t, s, occulited.KindNewDevices)
	if !snap.Devices.IsSnapshot() || snap.ID != "" || snap.Devices.Interface != bidcosRF {
		t.Errorf("devices snapshot %+v %+v", snap, snap.Devices)
	}
	f.DropStreams()
	waitKind(t, s, occulited.KindClosed)
	var query string
	for _, call := range f.Calls() {
		if call.Path == "/api/rpc/v1/events" {
			query = call.RawQuery
		}
	}
	for _, want := range []string{"interface=BidCos-RF%2CHmIP-RF", "type=event%2CnewDevices", "address=VCU0000321", "key=STATE", "devices=1"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
}

// TestEventsResumeAfterDrop reconnects with the held position and gets
// exactly the messages published while it was away.
func TestEventsResumeAfterDrop(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	s := openEvents(t, c, occulited.EventsOptions{Types: []string{"event"}, Keys: []string{"STATE"}, Backoff: occulited.Backoff{Initial: 300 * time.Millisecond, Max: 300 * time.Millisecond, Jitter: 0.01}})
	waitKind(t, s, occulited.KindHello)
	fireSwitch(t, f, true)
	first := waitKind(t, s, occulited.KindEvent)

	f.DropStreams()
	closed := waitKind(t, s, occulited.KindClosed)
	if !errors.Is(closed.Err, occulited.ErrStreamEnded) {
		t.Errorf("closed with %v", closed.Err)
	}
	fireSwitch(t, f, false)
	fireSwitch(t, f, true)
	hello := waitKind(t, s, occulited.KindHello)
	a, b := nextMsg(t, s), nextMsg(t, s)
	if string(a.Event.Value) != "false" || string(b.Event.Value) != "true" || a.ID <= first.ID || b.ID > hello.ID {
		t.Errorf("replay %s %s (%s), hello %s", a.Event.Value, b.Event.Value, b.ID, hello.ID)
	}
	if s.Position() != b.ID {
		t.Errorf("position %q after the replay ended at %q", s.Position(), b.ID)
	}
	if s.Duplicates() != 0 {
		t.Errorf("%d duplicates", s.Duplicates())
	}
}

func TestEventsResyncBootMovesToTheNewBoot(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	s := openEvents(t, c, occulited.EventsOptions{})
	waitKind(t, s, occulited.KindHello)
	oldBoot := f.BootID()
	if err := f.RestartBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	hello := waitKind(t, s, occulited.KindHello)
	if hello.Hello.BootID == oldBoot {
		t.Fatal("same boot after restart")
	}
	rs := nextMsg(t, s)
	if rs.Kind != occulited.KindResync || rs.Resync.Reason != occulited.ResyncBoot || rs.ID != "" {
		t.Fatalf("after hello %+v", rs)
	}
	if !strings.HasPrefix(s.Position(), hello.Hello.BootID+"-") {
		t.Errorf("position %q not on the new boot %s", s.Position(), hello.Hello.BootID)
	}
}

func TestEventsClassifiesRefusedConnections(t *testing.T) {
	f := startFake(t, litefake.Options{Tokens: map[string][]string{"meta": {"meta:read"}}})
	bo := fastBackoff()

	s := openEvents(t, newClient(t, f.URL(), "nope"), occulited.EventsOptions{Backoff: bo})
	m := waitKind(t, s, occulited.KindClosed)
	if !errors.Is(m.Err, hmerr.ErrAuthFailure) || m.RetryIn != bo.Unauthorized {
		t.Errorf("401: %v retry %v", m.Err, m.RetryIn)
	}

	s = openEvents(t, newClient(t, f.URL(), "meta"), occulited.EventsOptions{Backoff: bo})
	m = waitKind(t, s, occulited.KindClosed)
	var sm *hmerr.ScopeMissingError
	if !errors.As(m.Err, &sm) || sm.Scope != "rpc:read" || m.RetryIn != bo.Unauthorized {
		t.Errorf("403: %v retry %v", m.Err, m.RetryIn)
	}

	f.SetReady(false)
	s = openEvents(t, newClient(t, f.URL(), litefake.DefaultToken), occulited.EventsOptions{Backoff: bo})
	m = waitKind(t, s, occulited.KindClosed)
	if !errors.Is(m.Err, hmerr.ErrNoConnection) || m.RetryIn != 5*time.Second {
		t.Errorf("503: %v retry %v (Retry-After 5 s)", m.Err, m.RetryIn)
	}
}

func TestEventsStopsWithTheContext(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx, cancel := context.WithCancel(context.Background())
	s := c.Events(ctx, occulited.EventsOptions{})
	waitKind(t, s, occulited.KindHello)
	cancel()
	select {
	case <-s.Done():
	case <-time.After(waitMsg):
		t.Fatal("reader did not stop")
	}
	for range s.Messages() {
		// drain: the channel must be closed
	}
}

func TestBackoffDelay(t *testing.T) {
	b := occulited.Backoff{Initial: time.Second, Max: 8 * time.Second, Jitter: 0.2, Unauthorized: time.Minute, TooManyStreams: 30 * time.Second, Starting: 5 * time.Second}
	generic := errors.New("x")
	for failures, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 10: 8 * time.Second} {
		d := b.Delay(generic, failures)
		if d < want*8/10 || d > want*12/10 {
			t.Errorf("failure %d: %v, want %v ±20 %%", failures, d, want)
		}
	}
	cases := map[*occulited.APIError]time.Duration{
		{Status: http.StatusUnauthorized}:                                                           time.Minute,
		{Status: http.StatusForbidden, Scope: "rpc:read"}:                                           time.Minute,
		{Status: http.StatusTooManyRequests, Code: "too-many-streams"}:                              30 * time.Second,
		{Status: http.StatusTooManyRequests, Code: "too-many-streams", RetryAfter: 7 * time.Second}: 7 * time.Second,
		{Status: http.StatusServiceUnavailable, Code: "starting"}:                                   5 * time.Second,
		{Status: http.StatusServiceUnavailable, Code: "starting", RetryAfter: 9 * time.Second}:      9 * time.Second,
	}
	for e, want := range cases {
		if got := b.Delay(e, 3); got != want {
			t.Errorf("%+v: %v, want %v", e, got, want)
		}
	}
	if d := (occulited.Backoff{}).Delay(generic, 1); d < 800*time.Millisecond || d > 1200*time.Millisecond {
		t.Errorf("default first delay %v", d)
	}
}

func TestStateAndInterfaces(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	ctx := context.Background()
	fireSwitch(t, f, true)
	deadline := time.Now().Add(waitMsg)
	var page occulited.StatePage
	for time.Now().Before(deadline) {
		var err error
		page, err = c.State(ctx, occulited.StateQuery{Interface: bidcosRF, Datapoint: "STATE", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(page.Entries) == 0 || page.EventID == "" || !page.Entries[0].Confirmed || string(page.Entries[0].Value) != "true" {
		t.Errorf("state page %+v", page)
	}
	ifs, err := c.Interfaces(ctx)
	if err != nil || len(ifs) != 3 || ifs[0].Name != bidcosRF || !ifs[0].Running || ifs[0].URLPath != "/api/rpc/v1/xmlrpc/BidCos-RF" {
		t.Errorf("interfaces %+v %v", ifs, err)
	}
}
