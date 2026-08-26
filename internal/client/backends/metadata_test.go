// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for GetMetadata / SetMetadata on all backends.

package backends

import (
	"context"
	"errors"
	"testing"
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
// CcuBackend.AcknowledgeMessage via JSON-RPC
// ---------------------------------------------------------------------------

func TestCcuBackendAcknowledgeMessageRequiresJSON(t *testing.T) {
	t.Parallel()
	b := NewCcuBackend(&fakeCaller{}, nil, nil) // no JSON caller
	_, err := b.AcknowledgeMessage(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("CcuBackend.AcknowledgeMessage without JSON: want ErrUnsupported, got %v", err)
	}
}

func TestCcuBackendAcknowledgeMessageCallsJSON(t *testing.T) {
	t.Parallel()
	j := &fakeCaller{}
	b := NewCcuBackend(nil, j, nil)
	ok, err := b.AcknowledgeMessage(context.Background(), "42")
	if err != nil {
		t.Fatalf("AcknowledgeMessage: %v", err)
	}
	if !ok {
		t.Error("expected ok=true on success")
	}
	if j.called.Load() != 1 {
		t.Fatalf("json.Call not invoked (calls=%d)", j.called.Load())
	}
}
