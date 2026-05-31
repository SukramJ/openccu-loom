// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi

import (
	"context"
	"errors"
	"testing"
)

// multiErrorCentral returns the given error from both Connect and Disconnect.
type multiErrorCentral struct {
	name string
	err  error
}

func (m *multiErrorCentral) Name() string                       { return m.name }
func (m *multiErrorCentral) Connect(_ context.Context) error    { return m.err }
func (m *multiErrorCentral) Disconnect(_ context.Context) error { return m.err }

// TestMultiErrorErrorMethod exercises the Error() and Unwrap() methods on
// the internal multiError type via the public Connect path.
func TestMultiErrorErrorMethod(t *testing.T) {
	a := New()
	err1 := errors.New("first error")
	err2 := errors.New("second error")
	_ = a.Register(&multiErrorCentral{name: "c1", err: err1})
	_ = a.Register(&multiErrorCentral{name: "c2", err: err2})

	err := a.Connect(context.Background())
	if err == nil {
		t.Fatal("expected multi-error from Connect with two failing centrals")
	}

	// Error() must contain both messages.
	msg := err.Error()
	if msg == "" {
		t.Fatal("multiError.Error() returned empty string")
	}

	// Unwrap must surface both wrapped errors.
	var me *multiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *multiError, got %T", err)
	}
	unwrapped := me.Unwrap()
	if len(unwrapped) != 2 {
		t.Errorf("Unwrap() returned %d errors, want 2", len(unwrapped))
	}
}

// TestMultiErrorDisconnect exercises the multi-error path through Disconnect.
func TestMultiErrorDisconnect(t *testing.T) {
	a := New()
	err1 := errors.New("disc error 1")
	err2 := errors.New("disc error 2")

	// Connect succeeds (no error), Disconnect will fail.
	c1 := &stubbedDisconn{name: "d1", disconnErr: err1}
	c2 := &stubbedDisconn{name: "d2", disconnErr: err2}
	_ = a.Register(c1)
	_ = a.Register(c2)
	_ = a.Connect(context.Background())

	err := a.Disconnect(context.Background())
	if err == nil {
		t.Fatal("expected multi-error from Disconnect with two failing centrals")
	}
	var me *multiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *multiError, got %T", err)
	}
	if len(me.Unwrap()) != 2 {
		t.Errorf("Unwrap() returned %d errors, want 2", len(me.Unwrap()))
	}
}

// stubbedDisconn connects cleanly but disconnects with an error.
type stubbedDisconn struct {
	name       string
	disconnErr error
}

func (s *stubbedDisconn) Name() string                       { return s.name }
func (s *stubbedDisconn) Connect(_ context.Context) error    { return nil }
func (s *stubbedDisconn) Disconnect(_ context.Context) error { return s.disconnErr }
