// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"testing"
	"time"
)

// TestCommandError exercises Error() and NewCommandError.
func TestCommandError(t *testing.T) {
	e := NewCommandError("bad_request", "missing field")
	got := e.Error()
	if got != "bad_request: missing field" {
		t.Fatalf("CommandError.Error() = %q", got)
	}
}

// TestHubClientCount exercises ClientCount on an empty hub.
func TestHubClientCount(t *testing.T) {
	h := NewHub()
	if n := h.ClientCount(); n != 0 {
		t.Fatalf("ClientCount() = %d, want 0", n)
	}
}

// TestHubPublishNoClients verifies Publish doesn't panic with an empty hub.
func TestHubPublishNoClients(t *testing.T) {
	h := NewHub()
	h.Publish(Event{Topic: "device.*", Type: "test", When: time.Now(), Payload: nil})
	// No panic = pass.
}

// TestSystemStatusTopicFormat verifies the topic format.
func TestSystemStatusTopicFormat(t *testing.T) {
	got := SystemStatusTopic("home")
	if got != "system.home.status" {
		t.Fatalf("SystemStatusTopic = %q", got)
	}
}

// TestPublishCentralStateChanged exercises PublishCentralStateChanged on a
// hub with no clients (smoke-test that it doesn't panic).
func TestPublishCentralStateChanged(t *testing.T) {
	h := NewHub()
	h.PublishCentralStateChanged("home", "CONNECTING", "READY", time.Now())
	// No panic = pass.
}

// TestPublishDataPointValueChanged smoke-tests the typed publisher.
func TestPublishDataPointValueChanged(t *testing.T) {
	h := NewHub()
	h.PublishDataPointValueChanged(ValueChange{
		Central: "home", Interface: "HmIP-RF", DeviceAddress: "VCU123", Channel: 1,
		Parameter: "LEVEL", ParamsetKey: "VALUES", Value: 1.0, Previous: 0.0, When: time.Now(),
		Available: true,
	})
	// No panic = pass.
}

// TestSystemStatusSubscriberNilSafe verifies Start/Stop with nil reg/hub.
func TestSystemStatusSubscriberNilSafe(t *testing.T) {
	s := NewSystemStatusSubscriber(nil, nil)
	s.Start() // must not panic
	s.Stop()  // must not panic
}
