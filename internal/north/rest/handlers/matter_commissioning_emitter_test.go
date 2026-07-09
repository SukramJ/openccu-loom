// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Both existing happy-path tests for these two handlers
// (TestMatterCommissioningWindow_HappyPath_Returns200 in matter_test.go,
// TestMatterCommissioningClose_HappyPath_Returns204 in
// matter_exposures_test.go) call the handler with a nil
// MatterEventPublisher, so neither ever exercises the publishMatterEvent
// call site. assets/wsapi.json documents "matter.commissioning_window_opened"
// and "matter.commissioning_progress" as broadcasts every WS client can
// subscribe to; without a passing publisher the two tests above are blind
// to a regression that drops the publishMatterEvent call (or its topic
// constant) from either handler. These tests wire a real
// fakeMatterEventPublisher and assert the emitted topic + payload shape.

// TestMatterCommissioningWindow_HappyPath_PublishesCommissioningWindowOpened
// asserts that a successful POST /matter/commissioning/window emits exactly
// one "matter.commissioning_window_opened" broadcast carrying the response
// body, mirroring MatterCommissioningWindow's documented publisher contract.
func TestMatterCommissioningWindow_HappyPath_PublishesCommissioningWindowOpened(t *testing.T) {
	t.Parallel()

	opener := &fakeCommissioningOpener{result: MatterCommissioningWindowResult{
		Discriminator: 0xABC,
		Passcode:      12345678,
		QRCode:        "MT:Y.GHY00-0007217580",
		ManualCode:    "12345678901",
	}}
	pub := &fakeMatterEventPublisher{}

	body := `{"duration_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	MatterCommissioningWindow(opener, pub).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := pub.count(); got != 1 {
		t.Fatalf("expected exactly 1 published event, got %d: %+v", got, pub.events)
	}
	ev := pub.events[0]
	if ev.Topic != MatterTopicCommissioningWindowOpened {
		t.Errorf("topic = %q, want %q", ev.Topic, MatterTopicCommissioningWindowOpened)
	}
	resp, ok := ev.Payload.(MatterCommissioningWindowResponse)
	if !ok {
		t.Fatalf("payload type %T, want MatterCommissioningWindowResponse", ev.Payload)
	}
	if resp.Discriminator != 0xABC || resp.Passcode != 12345678 || resp.DurationSeconds != 300 {
		t.Errorf("payload = %+v, want discriminator=0xABC passcode=12345678 duration_seconds=300", resp)
	}
}

// TestMatterCommissioningClose_HappyPath_PublishesCommissioningProgress
// asserts that a successful POST /matter/commissioning/window/close emits
// exactly one "matter.commissioning_progress" broadcast with stage "closed",
// mirroring MatterCommissioningClose's documented publisher contract.
func TestMatterCommissioningClose_HappyPath_PublishesCommissioningProgress(t *testing.T) {
	t.Parallel()

	pub := &fakeMatterEventPublisher{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/matter/commissioning/window/close", http.NoBody)
	w := httptest.NewRecorder()
	MatterCommissioningClose(&fakeCommissioningCloser{}, pub, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := pub.count(); got != 1 {
		t.Fatalf("expected exactly 1 published event, got %d: %+v", got, pub.events)
	}
	ev := pub.events[0]
	if ev.Topic != MatterTopicCommissioningProgress {
		t.Errorf("topic = %q, want %q", ev.Topic, MatterTopicCommissioningProgress)
	}
	stage, ok := ev.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type %T, want map[string]any", ev.Payload)
	}
	if stage["stage"] != "closed" {
		t.Errorf("payload[stage] = %v, want %q", stage["stage"], "closed")
	}
}
