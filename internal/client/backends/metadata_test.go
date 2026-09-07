// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for GetMetadata / SetMetadata on all backends.

package backends

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// CcuBackend — delegates to XML-RPC getMetadata / setMetadata
// ---------------------------------------------------------------------------

func TestCcuBackendGetMetadataDelegates(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: "kitchen"}
	b := NewCcuBackend(x, nil, nil)
	val, err := b.GetMetadata(context.Background(), "ADDR0001", "NAME")
	if err != nil {
		t.Fatalf("CcuBackend.GetMetadata: %v", err)
	}
	if x.called.Load() != 1 {
		t.Fatalf("xml.Call not invoked (calls=%d)", x.called.Load())
	}
	if val != "kitchen" {
		t.Errorf("val = %v; want %q", val, "kitchen")
	}
}

func TestCcuBackendSetMetadataDelegates(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{}
	b := NewCcuBackend(x, nil, nil)
	if err := b.SetMetadata(context.Background(), "ADDR0001", "NAME", "office"); err != nil {
		t.Fatalf("CcuBackend.SetMetadata: %v", err)
	}
	if x.called.Load() != 1 {
		t.Fatalf("xml.Call not invoked (calls=%d)", x.called.Load())
	}
}

// ---------------------------------------------------------------------------
// CuxdBackend — ErrUnsupported
// ---------------------------------------------------------------------------

func TestCuxdBackendGetMetadataUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetMetadata(context.Background(), "ADDR0001", "NAME")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("CuxdBackend.GetMetadata: want ErrUnsupported, got %v", err)
	}
}

func TestCuxdBackendSetMetadataUnsupported(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SetMetadata(context.Background(), "ADDR0001", "NAME", "hello")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("CuxdBackend.SetMetadata: want ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// HomegearBackend — actual XML-RPC call
// ---------------------------------------------------------------------------

func TestHomegearBackendGetMetadataDelegates(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{reply: "living room"}
	b := NewHomegearBackend(x, nil)
	val, err := b.GetMetadata(context.Background(), "ADDR0001", "NAME")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if x.called.Load() != 1 {
		t.Fatalf("xml.Call not invoked (calls=%d)", x.called.Load())
	}
	if val != "living room" {
		t.Errorf("val = %v; want %q", val, "living room")
	}
}

func TestHomegearBackendSetMetadataDelegates(t *testing.T) {
	t.Parallel()
	x := &fakeCaller{}
	b := NewHomegearBackend(x, nil)
	if err := b.SetMetadata(context.Background(), "ADDR0001", "NAME", "bedroom"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if x.called.Load() != 1 {
		t.Fatalf("xml.Call not invoked (calls=%d)", x.called.Load())
	}
}

// ---------------------------------------------------------------------------
// CcuBackend.AcknowledgeMessage via the ReGa script engine
// ---------------------------------------------------------------------------

func TestCcuBackendAcknowledgeMessageRequiresScriptRunner(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, &fakeCaller{}, nil) // no ScriptRunner
	_, err := b.AcknowledgeMessage(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("CcuBackend.AcknowledgeMessage without a ScriptRunner: want ErrUnsupported, got %v", err)
	}
}

// TestCcuBackendAcknowledgeMessageRunsScript pins the ReGa route: the CCU's
// JSON-RPC method table has no acknowledge call, so the backend runs the
// acknowledge_message script with the message ISE-ID.
func TestCcuBackendAcknowledgeMessageRunsScript(t *testing.T) {
	t.Parallel()
	r := &fakeScriptRunner{rawJSON: `{"success":true}`}
	b := NewCcuBackend(nil, nil, nil)
	b.SetScriptRunner(r)
	ok, err := b.AcknowledgeMessage(context.Background(), "42")
	if err != nil {
		t.Fatalf("AcknowledgeMessage: %v", err)
	}
	if !ok {
		t.Error("expected ok=true on success")
	}
	if r.lastScript != hmenum.RegaScriptAcknowledgeMessage || r.lastParams["message_id"] != "42" {
		t.Fatalf("script=%s params=%v", r.lastScript, r.lastParams)
	}
}
