// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// --------------------------------------------------------------------------
// fakes
// --------------------------------------------------------------------------

// fullCentral implements CentralHandle + ValueReader + ValueWriter +
// UpdateSubscriber for testing .
type fullCentral struct {
	name   string
	values map[string]any
}

func newFullCentral(name string) *fullCentral {
	return &fullCentral{name: name, values: make(map[string]any)}
}

func (c *fullCentral) Name() string                       { return c.name }
func (c *fullCentral) Connect(_ context.Context) error    { return nil }
func (c *fullCentral) Disconnect(_ context.Context) error { return nil }
func (c *fullCentral) ReadValue(_ context.Context, ch, param string) (any, error) {
	key := ch + ":" + param
	v, ok := c.values[key]
	if !ok {
		return nil, errors.New("not found: " + key)
	}
	return v, nil
}

func (c *fullCentral) WriteValue(_ context.Context, ch, param string, value any) error {
	c.values[ch+":"+param] = value
	return nil
}

func (c *fullCentral) SubscribeToDataPointUpdates(handler func(string, string, string, any)) func() {
	// Immediately invoke with a test event.
	handler(c.name, "ABC:1", "TEMPERATURE", 22.5)
	return func() {}
}

// basicCentral does NOT implement ValueReader/ValueWriter/UpdateSubscriber.
type basicCentral struct{ name string }

func (b *basicCentral) Name() string                       { return b.name }
func (b *basicCentral) Connect(_ context.Context) error    { return nil }
func (b *basicCentral) Disconnect(_ context.Context) error { return nil }

// --------------------------------------------------------------------------
// ReadValue
// --------------------------------------------------------------------------

func TestReadValue_Success(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	c := newFullCentral("myhome")
	_ = api.Register(c)
	_ = api.Connect(context.Background())

	_ = api.WriteValue(context.Background(), "myhome", "ABC:1", "TEMPERATURE", 21.5)
	val, err := api.ReadValue(context.Background(), "myhome", "ABC:1", "TEMPERATURE")
	if err != nil {
		t.Fatalf("ReadValue error: %v", err)
	}
	if val != 21.5 {
		t.Errorf("ReadValue = %v, want 21.5", val)
	}
}

func TestReadValue_NotConnected(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	c := newFullCentral("myhome")
	_ = api.Register(c)
	// NOT calling Connect.
	_, err := api.ReadValue(context.Background(), "myhome", "ABC:1", "TEMPERATURE")
	if !errors.Is(err, hmapi.ErrNotConnected) {
		t.Errorf("ReadValue not connected: got %v, want ErrNotConnected", err)
	}
}

func TestReadValue_UnknownCentral(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Connect(context.Background())
	_, err := api.ReadValue(context.Background(), "ghost", "A:0", "X")
	if err == nil {
		t.Error("ReadValue unknown central: want error, got nil")
	}
}

func TestReadValue_NotSupported(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Register(&basicCentral{name: "basic"})
	_ = api.Connect(context.Background())
	_, err := api.ReadValue(context.Background(), "basic", "A:0", "X")
	if !errors.Is(err, hmapi.ErrNotSupported) {
		t.Errorf("ReadValue basic central: got %v, want ErrNotSupported", err)
	}
}

// --------------------------------------------------------------------------
// WriteValue
// --------------------------------------------------------------------------

func TestWriteValue_Success(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	c := newFullCentral("myhome")
	_ = api.Register(c)
	_ = api.Connect(context.Background())
	if err := api.WriteValue(context.Background(), "myhome", "ABC:1", "STATE", true); err != nil {
		t.Fatalf("WriteValue error: %v", err)
	}
}

func TestWriteValue_NotSupported(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Register(&basicCentral{name: "basic"})
	_ = api.Connect(context.Background())
	err := api.WriteValue(context.Background(), "basic", "A:0", "X", 1)
	if !errors.Is(err, hmapi.ErrNotSupported) {
		t.Errorf("WriteValue basic central: got %v, want ErrNotSupported", err)
	}
}

// --------------------------------------------------------------------------
// SubscribeToUpdates
// --------------------------------------------------------------------------

func TestSubscribeToUpdates_HandlerCalled(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Register(newFullCentral("myhome"))
	_ = api.Connect(context.Background())

	var called bool
	unsub := api.SubscribeToUpdates(func(cn, ch, param string, val any) {
		if cn == "myhome" && ch == "ABC:1" && param == "TEMPERATURE" {
			called = true
		}
	})
	defer unsub()

	if !called {
		t.Error("SubscribeToUpdates: handler not called immediately by fake central")
	}
}

func TestSubscribeToUpdates_NoSubscribers_NoPanic(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Register(&basicCentral{name: "basic"})
	_ = api.Connect(context.Background())

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SubscribeToUpdates panicked: %v", r)
		}
	}()
	unsub := api.SubscribeToUpdates(func(string, string, string, any) {})
	unsub()
}

func TestSubscribeToUpdates_UnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()
	api := hmapi.New()
	_ = api.Register(newFullCentral("myhome"))
	_ = api.Connect(context.Background())

	unsub := api.SubscribeToUpdates(func(string, string, string, any) {})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("double unsub panicked: %v", r)
		}
	}()
	unsub()
	unsub() // must not panic
}
