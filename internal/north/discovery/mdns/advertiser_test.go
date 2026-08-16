// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestServiceValidate_PortInRange(t *testing.T) {
	t.Parallel()
	s := Service{Port: 8119}
	if err := s.Validate(); err != nil {
		t.Fatalf("port 8119 should validate: %v", err)
	}
}

func TestServiceValidate_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, p := range []int{0, -1, 65536, 99999} {
		if err := (Service{Port: p}).Validate(); err == nil {
			t.Errorf("port %d should fail validation", p)
		}
	}
}

func TestResolvedInstanceName_FallsBackToHostname(t *testing.T) {
	t.Parallel()
	s := Service{Port: 8119}
	got := s.resolvedInstanceName()
	if got == "" || got == "openccu-loom" {
		// "openccu-loom" is the last-ditch fallback; reaching it
		// would mean os.Hostname failed, which is rare on CI/dev hosts.
		host, err := os.Hostname()
		if err == nil && host != "" {
			t.Fatalf("expected hostname fallback, got %q (host = %q)", got, host)
		}
	}
	if strings.HasSuffix(got, ".local") {
		t.Fatalf(".local suffix must be stripped, got %q", got)
	}
}

func TestResolvedInstanceName_RespectsOverride(t *testing.T) {
	t.Parallel()
	s := Service{Port: 8119, InstanceName: "my-hm"}
	if got := s.resolvedInstanceName(); got != "my-hm" {
		t.Fatalf("override ignored, got %q", got)
	}
}

func TestNoop_StartStopRoundTrip(t *testing.T) {
	t.Parallel()
	n := NewNoop(Service{Port: 8119})
	if n.Active() {
		t.Fatal("Active before Start should be false")
	}
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !n.Active() {
		t.Fatal("Active after Start should be true")
	}
	if err := n.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n.Active() {
		t.Fatal("Active after Stop should be false")
	}
}

func TestNoop_StartTwiceReturnsAlreadyStarted(t *testing.T) {
	t.Parallel()
	n := NewNoop(Service{Port: 8119})
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer n.Stop()
	if err := n.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start: got %v, want ErrAlreadyStarted", err)
	}
}

func TestNoop_StopIdempotent(t *testing.T) {
	t.Parallel()
	n := NewNoop(Service{Port: 8119})
	_ = n.Start(context.Background())
	if err := n.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := n.Stop(); err != nil {
		t.Fatalf("second Stop must be no-op, got %v", err)
	}
}

func TestNoop_StartRejectsInvalidService(t *testing.T) {
	t.Parallel()
	n := NewNoop(Service{Port: -1})
	if err := n.Start(context.Background()); err == nil {
		t.Fatal("expected validation error on negative port")
	}
}

func TestServiceTypeIsCanonical(t *testing.T) {
	t.Parallel()
	if ServiceType != "_openccu-loom._tcp" {
		t.Fatalf("ServiceType drift: %q", ServiceType)
	}
	if Domain != "local." {
		t.Fatalf("Domain drift: %q", Domain)
	}
}

// TestMulticastUpdateTXTBeforeStart pins the not-started contract of the
// multicast advertiser: republishing a record that was never registered must
// report [ErrNotStarted] rather than touching a nil server. The published path
// needs a live multicast responder and is exercised on the wire, not here.
func TestMulticastUpdateTXTBeforeStart(t *testing.T) {
	t.Parallel()
	m := NewMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1"}})
	if err := m.UpdateTXT([]string{"a=2"}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("UpdateTXT before Start: err = %v, want %v", err, ErrNotStarted)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop on a never-started advertiser: %v", err)
	}
}

// fakeResponder stands in for a live zeroconf server: it only has to be
// shut down, and it records that it was.
type fakeResponder struct {
	name string
	down bool
}

func (f *fakeResponder) Shutdown() { f.down = true }

// scriptedRegistrar publishes a fakeResponder per call and fails on the
// call numbers named in failOn (1-based), so a test can pick exactly which
// register attempt breaks.
type scriptedRegistrar struct {
	calls  int
	failOn map[int]bool
	txt    [][]string
	live   []*fakeResponder
}

func (s *scriptedRegistrar) register(svc Service) (responder, error) {
	s.calls++
	s.txt = append(s.txt, append([]string(nil), svc.TXT...))
	if s.failOn[s.calls] {
		return nil, errors.New("bind race on udp/5353")
	}
	r := &fakeResponder{name: svc.resolvedInstanceName()}
	s.live = append(s.live, r)
	return r, nil
}

// newScriptedMulticast wires a Multicast onto a scripted registrar and
// silences its logger so a deliberate failure does not pollute test output.
func newScriptedMulticast(svc Service, failOn ...int) (*Multicast, *scriptedRegistrar) {
	reg := &scriptedRegistrar{failOn: map[int]bool{}}
	for _, n := range failOn {
		reg.failOn[n] = true
	}
	m := NewMulticast(svc)
	m.register = reg.register
	m.logger = slog.New(slog.DiscardHandler)
	return m, reg
}

// TestMulticastUpdateTXTRepublishesRecord pins the happy path: the old
// record is withdrawn and a new one carrying the new TXT bundle is
// published in its place.
func TestMulticastUpdateTXTRepublishesRecord(t *testing.T) {
	t.Parallel()
	m, reg := newScriptedMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1"}})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.UpdateTXT([]string{"a=2"}); err != nil {
		t.Fatalf("UpdateTXT: %v", err)
	}
	if reg.calls != 2 {
		t.Fatalf("register calls = %d, want 2", reg.calls)
	}
	if got := reg.txt[1]; len(got) != 1 || got[0] != "a=2" {
		t.Fatalf("second register TXT = %v, want [a=2]", got)
	}
	if !reg.live[0].down {
		t.Error("the previous record must be withdrawn before the new one is published")
	}
	if reg.live[1].down {
		t.Error("the fresh record must stay live")
	}
}

// TestMulticastUpdateTXTKeepsAdvertisingWhenReRegisterFails is the
// regression pin: a re-register that fails after the old record was
// withdrawn must not take the daemon off the network permanently. The
// previous bundle is re-published, and the advertiser stays startable
// through a later refresh instead of reporting ErrNotStarted forever.
func TestMulticastUpdateTXTKeepsAdvertisingWhenReRegisterFails(t *testing.T) {
	t.Parallel()
	// Call 1 = Start, call 2 = the failing refresh, call 3 = the restore.
	m, reg := newScriptedMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1"}}, 2)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.UpdateTXT([]string{"a=2"}); err == nil {
		t.Fatal("UpdateTXT must report the failed re-register")
	}
	if reg.calls != 3 {
		t.Fatalf("register calls = %d, want 3 (start, failed refresh, restore)", reg.calls)
	}
	if got := reg.txt[2]; len(got) != 1 || got[0] != "a=1" {
		t.Fatalf("restore register TXT = %v, want the previous bundle [a=1]", got)
	}
	m.mu.Lock()
	live := m.server
	m.mu.Unlock()
	if live == nil {
		t.Fatal("no record on the wire after a failed refresh — discovery is dead")
	}
	// A later refresh must publish again rather than report ErrNotStarted.
	if err := m.UpdateTXT([]string{"a=3"}); err != nil {
		t.Fatalf("second UpdateTXT: %v", err)
	}
	if got := reg.txt[len(reg.txt)-1]; len(got) != 1 || got[0] != "a=3" {
		t.Fatalf("last register TXT = %v, want [a=3]", got)
	}
}

// TestMulticastUpdateTXTRetriesAfterBothRegistersFail covers the worst
// case: neither the refresh nor the restore gets a record onto the wire.
// The advertiser must remember that it is meant to be advertising so the
// next refresh republishes it.
func TestMulticastUpdateTXTRetriesAfterBothRegistersFail(t *testing.T) {
	t.Parallel()
	m, reg := newScriptedMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1"}}, 2, 3)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.UpdateTXT([]string{"a=2"}); err == nil {
		t.Fatal("UpdateTXT must report the failed re-register")
	}
	if err := m.UpdateTXT([]string{"a=3"}); err != nil {
		t.Fatalf("recovery UpdateTXT: %v", err)
	}
	if reg.calls != 4 {
		t.Fatalf("register calls = %d, want 4", reg.calls)
	}
	m.mu.Lock()
	live := m.server
	m.mu.Unlock()
	if live == nil {
		t.Fatal("the recovering refresh must republish the record")
	}
}

// TestMulticastStopEndsTheRecoveryPath pins that an operator-driven Stop
// really stops: a refresh afterwards reports ErrNotStarted instead of
// silently re-publishing a record nobody asked for.
func TestMulticastStopEndsTheRecoveryPath(t *testing.T) {
	t.Parallel()
	m, reg := newScriptedMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1"}})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.UpdateTXT([]string{"a=2"}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("UpdateTXT after Stop: err = %v, want %v", err, ErrNotStarted)
	}
	if reg.calls != 1 {
		t.Fatalf("register calls = %d, want 1 (Start only)", reg.calls)
	}
}

// TestMulticastUpdateTXTSkipsAnUnchangedBundle pins the churn guard.
// Republishing sends a goodbye packet first, which takes the service out
// of every browser's cache until the new announcement lands. The refresh
// fires on every hub-ready and every reconnect-ready event, so a bundle
// that did not change must leave the live record alone.
func TestMulticastUpdateTXTSkipsAnUnchangedBundle(t *testing.T) {
	t.Parallel()
	m, reg := newScriptedMulticast(Service{InstanceName: "loom", Port: 8080, TXT: []string{"a=1", "b=2"}})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.UpdateTXT([]string{"a=1", "b=2"}); err != nil {
		t.Fatalf("UpdateTXT: %v", err)
	}
	if reg.calls != 1 {
		t.Fatalf("register calls = %d, want 1 — an unchanged bundle withdrew and re-announced the record", reg.calls)
	}
	if reg.live[0].down {
		t.Error("the live record was withdrawn for an unchanged TXT bundle")
	}
	// A changed bundle still republishes.
	if err := m.UpdateTXT([]string{"a=1", "b=3"}); err != nil {
		t.Fatalf("UpdateTXT with a changed bundle: %v", err)
	}
	if reg.calls != 2 {
		t.Fatalf("register calls = %d, want 2", reg.calls)
	}
}
