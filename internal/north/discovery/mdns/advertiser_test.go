// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"context"
	"errors"
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
