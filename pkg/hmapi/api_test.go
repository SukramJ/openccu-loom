// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi

import (
	"context"
	"errors"
	"testing"
)

// stubCentral satisfies [CentralHandle] for unit tests.
type stubCentral struct {
	name       string
	connectErr error
	disconnErr error
	connected  bool
}

func (s *stubCentral) Name() string { return s.name }
func (s *stubCentral) Connect(_ context.Context) error {
	if s.connectErr != nil {
		return s.connectErr
	}
	s.connected = true
	return nil
}

func (s *stubCentral) Disconnect(_ context.Context) error {
	s.connected = false
	return s.disconnErr
}

func TestNewIsEmpty(t *testing.T) {
	a := New()
	if len(a.Centrals()) != 0 {
		t.Error("new API should have no centrals")
	}
	if a.IsConnected() {
		t.Error("new API should not be connected")
	}
}

func TestRegisterAndRetrieve(t *testing.T) {
	a := New()
	c := &stubCentral{name: "ccu1"}
	if err := a.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := a.Central("ccu1")
	if !ok || got.Name() != "ccu1" {
		t.Errorf("Central(%q): got (%v, %v)", "ccu1", got, ok)
	}
}

func TestRegisterDuplicateErrors(t *testing.T) {
	a := New()
	c := &stubCentral{name: "ccu1"}
	_ = a.Register(c)
	if err := a.Register(c); err == nil {
		t.Error("second Register with same name should error")
	}
}

func TestRegisterNilErrors(t *testing.T) {
	a := New()
	if err := a.Register(nil); err == nil {
		t.Error("Register(nil) should error")
	}
}

func TestConnectStartsCentrals(t *testing.T) {
	a := New()
	c := &stubCentral{name: "ccu1"}
	_ = a.Register(c)
	if err := a.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !a.IsConnected() {
		t.Error("should be connected after Connect")
	}
	if !c.connected {
		t.Error("stub central should be connected")
	}
}

func TestConnectTwiceErrors(t *testing.T) {
	a := New()
	_ = a.Register(&stubCentral{name: "c"})
	_ = a.Connect(context.Background())
	if err := a.Connect(context.Background()); !errors.Is(err, ErrAlreadyConnected) {
		t.Errorf("second Connect: got %v, want ErrAlreadyConnected", err)
	}
}

func TestConnectEmptyAPIIsNoop(t *testing.T) {
	a := New()
	if err := a.Connect(context.Background()); err != nil {
		t.Fatalf("Connect empty: %v", err)
	}
}

func TestConnectPropagatesErrors(t *testing.T) {
	a := New()
	sentinel := errors.New("dial failed")
	_ = a.Register(&stubCentral{name: "c", connectErr: sentinel})
	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error from Connect")
	}
}

func TestDisconnectStopsCentrals(t *testing.T) {
	a := New()
	c := &stubCentral{name: "ccu1"}
	_ = a.Register(c)
	_ = a.Connect(context.Background())
	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if a.IsConnected() {
		t.Error("should not be connected after Disconnect")
	}
	if c.connected {
		t.Error("stub central should be disconnected")
	}
}
