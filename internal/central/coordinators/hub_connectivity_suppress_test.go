// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// -- L-A5-v13-31: HubCoordinator.ConnectivityDPs ----------------------------

// TestConnectivityDPsNilWhenNoModel verifies that ConnectivityDPs returns nil
// when no hub model is wired.
func TestConnectivityDPsNilWhenNoModel(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	if got := h.ConnectivityDPs(); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// TestConnectivityDPsNilWhenModelHasNoConnectivity verifies nil when a hub
// model is wired but SetConnectivity was never called.
func TestConnectivityDPsNilWhenModelHasNoConnectivity(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	m := hub.NewHub("main")
	h.SetHubModel(m)

	if got := h.ConnectivityDPs(); got != nil {
		t.Fatalf("expected nil before SetConnectivity, got %v", got)
	}
}

// TestConnectivityDPsReturnsWiredConnectivity verifies that ConnectivityDPs
// surfaces the *hub.Connectivity registered via hub.Hub.SetConnectivity.
func TestConnectivityDPsReturnsWiredConnectivity(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	m := hub.NewHub("main")
	conn := hub.NewConnectivity()
	m.SetConnectivity(conn)
	h.SetHubModel(m)

	got := h.ConnectivityDPs()
	if got == nil {
		t.Fatal("expected non-nil Connectivity, got nil")
	}
	if got != conn {
		t.Fatal("ConnectivityDPs must return the exact pointer registered with the hub model")
	}
}

// TestConnectivityDPsNilAfterDetach verifies that ConnectivityDPs returns nil
// after SetConnectivity(nil) detaches the aggregate from the hub model.
func TestConnectivityDPsNilAfterDetach(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	m := hub.NewHub("main")
	conn := hub.NewConnectivity()
	m.SetConnectivity(conn)
	h.SetHubModel(m)

	// Detach
	m.SetConnectivity(nil)

	if got := h.ConnectivityDPs(); got != nil {
		t.Fatalf("expected nil after detach, got %v", got)
	}
}

// -- L-A5-v13-37: SuppressServiceMessage signature --------------------------

type fullSignatureSuppressor struct {
	lastIface    string
	lastCh       string
	lastParam    string
	lastSuppress bool
	err          error
}

func (s *fullSignatureSuppressor) SuppressServiceMessage(
	_ context.Context, iface, ch, param string, suppress bool,
) error {
	s.lastIface = iface
	s.lastCh = ch
	s.lastParam = param
	s.lastSuppress = suppress
	return s.err
}

// TestSuppressServiceMessageFullSignature verifies that the coordinator
// forwards all four parameters (interfaceID, channelAddress, parameterID,
// suppress) to the wired suppressor.
func TestSuppressServiceMessageFullSignature(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	sup := &fullSignatureSuppressor{}
	h.SetServiceMessageSuppressor(sup)

	err := h.SuppressServiceMessage(context.Background(), "BidCos-RF", "JEQ0123456:1", "LOWBAT", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sup.lastIface != "BidCos-RF" {
		t.Errorf("interfaceID: got %q, want %q", sup.lastIface, "BidCos-RF")
	}
	if sup.lastCh != "JEQ0123456:1" {
		t.Errorf("channelAddress: got %q, want %q", sup.lastCh, "JEQ0123456:1")
	}
	if sup.lastParam != "LOWBAT" {
		t.Errorf("parameterID: got %q, want %q", sup.lastParam, "LOWBAT")
	}
	if !sup.lastSuppress {
		t.Error("suppress flag: got false, want true")
	}
}

// TestSuppressServiceMessageUnsuppress verifies suppress=false is forwarded.
func TestSuppressServiceMessageUnsuppress(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	sup := &fullSignatureSuppressor{}
	h.SetServiceMessageSuppressor(sup)

	_ = h.SuppressServiceMessage(context.Background(), "HmIP-RF", "ch", "param", false)
	if sup.lastSuppress {
		t.Error("suppress flag: got true, want false")
	}
}

// TestSuppressServiceMessageEmptyParamID verifies that an empty parameterID
// (suppress all) is forwarded without error.
func TestSuppressServiceMessageEmptyParamID(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	sup := &fullSignatureSuppressor{}
	h.SetServiceMessageSuppressor(sup)

	if err := h.SuppressServiceMessage(context.Background(), "HmIP-RF", "ch", "", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sup.lastParam != "" {
		t.Errorf("expected empty paramID to be forwarded as-is, got %q", sup.lastParam)
	}
}

// TestSuppressServiceMessageErrorPropagation verifies that errors from the
// suppressor are returned to the caller.
func TestSuppressServiceMessageErrorPropagation(t *testing.T) {
	h := NewHubCoordinator("main", events.NewBus())
	want := errors.New("service unavailable")
	sup := &fullSignatureSuppressor{err: want}
	h.SetServiceMessageSuppressor(sup)

	got := h.SuppressServiceMessage(context.Background(), "HmIP-RF", "ch", "param", true)
	if !errors.Is(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
