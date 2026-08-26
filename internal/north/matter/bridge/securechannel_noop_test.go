// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box tests for noopPaseHandler, noopCaseHandler, peekBytes, and
// resolveCaseHandler. Lives in package bridge to access unexported types.

import (
	"errors"
	"testing"
)

// ─── noopPaseHandler ──────────────────────────────────────────────────────────

func TestNoopPaseHandler_ProcessPBKDFParamRequest(t *testing.T) {
	t.Parallel()
	h := noopPaseHandler{}
	_, _, err := h.ProcessPBKDFParamRequest(nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("expected ErrPaseHandlerMissing, got %v", err)
	}
}

func TestNoopPaseHandler_ProcessPake1(t *testing.T) {
	t.Parallel()
	h := noopPaseHandler{}
	_, _, err := h.ProcessPake1(nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("expected ErrPaseHandlerMissing, got %v", err)
	}
}

func TestNoopPaseHandler_ProcessPake3(t *testing.T) {
	t.Parallel()
	h := noopPaseHandler{}
	_, _, err := h.ProcessPake3(nil)
	if !errors.Is(err, ErrPaseHandlerMissing) {
		t.Errorf("expected ErrPaseHandlerMissing, got %v", err)
	}
}

// ─── noopCaseHandler ──────────────────────────────────────────────────────────

func TestNoopCaseHandler_ProcessSigma1(t *testing.T) {
	t.Parallel()
	h := noopCaseHandler{}
	_, _, err := h.ProcessSigma1(nil)
	if !errors.Is(err, ErrCaseHandlerMissing) {
		t.Errorf("expected ErrCaseHandlerMissing, got %v", err)
	}
}

func TestNoopCaseHandler_ProcessSigma3(t *testing.T) {
	t.Parallel()
	h := noopCaseHandler{}
	_, _, err := h.ProcessSigma3(nil)
	if !errors.Is(err, ErrCaseHandlerMissing) {
		t.Errorf("expected ErrCaseHandlerMissing, got %v", err)
	}
}

func TestNoopCaseHandler_ProcessSigma2Resume(t *testing.T) {
	t.Parallel()
	h := noopCaseHandler{}
	_, _, err := h.ProcessSigma2Resume(nil)
	if !errors.Is(err, ErrCaseHandlerMissing) {
		t.Errorf("expected ErrCaseHandlerMissing, got %v", err)
	}
}

// ─── peekBytes ────────────────────────────────────────────────────────────────

func TestPeekBytes_ShorterThanN(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02}
	got := peekBytes(b, 10)
	if len(got) != 2 || got[0] != 0x01 {
		t.Errorf("shorter than n: expected full slice, got %v", got)
	}
}

func TestPeekBytes_ExactlyN(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02, 0x03}
	got := peekBytes(b, 3)
	if len(got) != 3 {
		t.Errorf("exactly n: expected 3 bytes, got %d", len(got))
	}
}

func TestPeekBytes_LongerThanN(t *testing.T) {
	t.Parallel()
	b := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	got := peekBytes(b, 3)
	if len(got) != 3 {
		t.Errorf("longer than n: expected 3 bytes, got %d", len(got))
	}
	if got[2] != 0x03 {
		t.Errorf("unexpected last byte: want 0x03, got 0x%02X", got[2])
	}
}

// ─── resolveCaseHandler ───────────────────────────────────────────────────────

func TestResolveCaseHandler_NoProvider_ReturnsSingleton(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingCaseHandler{}
	b.AttachCaseHandler(singleton)
	h := b.resolveCaseHandler(1)
	if h != singleton {
		t.Errorf("expected singleton, got %T", h)
	}
}

func TestResolveCaseHandler_ProviderOverridesSingleton(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingCaseHandler{}
	b.AttachCaseHandler(singleton)
	fromProvider := &recordingCaseHandler{}
	b.AttachCaseHandlerProvider(func(_ uint16) CaseHandler { return fromProvider })
	h := b.resolveCaseHandler(1)
	if h != fromProvider {
		t.Errorf("expected provider result, got %T", h)
	}
}

func TestResolveCaseHandler_ProviderReturningNilFallsBack(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	singleton := &recordingCaseHandler{}
	b.AttachCaseHandler(singleton)
	b.AttachCaseHandlerProvider(func(_ uint16) CaseHandler { return nil })
	h := b.resolveCaseHandler(1)
	if h != singleton {
		t.Errorf("nil provider result: expected singleton fallback, got %T", h)
	}
}
