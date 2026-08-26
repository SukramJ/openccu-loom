// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests that AlarmMessages, ServiceMessages, and Inbox embed BaseDataPointFields
// and expose the CallbackDataPoint lifecycle surface (UniqueID, IsRegistered,
// MarkRegistered, ModifiedAt, RefreshedAt).
package hub

import (
	"testing"
)

// TestHubAlarmMessagesIsCallbackDataPoint verifies that AlarmMessages
// exposes UniqueID, IsRegistered, MarkRegistered from the embedded
// BaseDataPointFields — the canonical CallbackDataPoint lifecycle surface.
func TestHubAlarmMessagesIsCallbackDataPoint(t *testing.T) {
	t.Parallel()

	a := NewAlarmMessagesWithCentral("ccu1", nil)

	// UniqueID must be non-empty and contain the central name.
	uid := a.UniqueID()
	if uid == "" {
		t.Fatal("AlarmMessages.UniqueID() must not be empty")
	}
	want := "ccu1::alarm_messages"
	if uid != want {
		t.Errorf("AlarmMessages.UniqueID() = %q, want %q", uid, want)
	}

	// Registration lifecycle: starts unregistered.
	if a.IsRegistered() {
		t.Fatal("fresh AlarmMessages must not be registered")
	}
	a.MarkRegistered()
	if !a.IsRegistered() {
		t.Fatal("AlarmMessages must be registered after MarkRegistered()")
	}

	// Timestamps are initially zero.
	if !a.ModifiedAt().IsZero() {
		t.Fatal("fresh AlarmMessages: ModifiedAt() must be zero")
	}
	if !a.RefreshedAt().IsZero() {
		t.Fatal("fresh AlarmMessages: RefreshedAt() must be zero")
	}
}

// TestHubServiceMessagesIsCallbackDataPoint mirrors the alarm-messages
// test for the service-messages aggregate.
func TestHubServiceMessagesIsCallbackDataPoint(t *testing.T) {
	t.Parallel()

	s := NewServiceMessagesWithCentral("ccu2", nil)

	uid := s.UniqueID()
	want := "ccu2::service_messages"
	if uid != want {
		t.Errorf("ServiceMessages.UniqueID() = %q, want %q", uid, want)
	}

	if s.IsRegistered() {
		t.Fatal("fresh ServiceMessages must not be registered")
	}
	s.MarkRegistered()
	if !s.IsRegistered() {
		t.Fatal("ServiceMessages must be registered after MarkRegistered()")
	}
}

// TestHubInboxIsCallbackDataPoint verifies that Inbox exposes the same
// BaseDataPointFields lifecycle surface as
// (hub/inbox.py:31).
func TestHubInboxIsCallbackDataPoint(t *testing.T) {
	t.Parallel()

	i := NewInboxWithCentral("ccu3")

	uid := i.UniqueID()
	want := "ccu3::inbox"
	if uid != want {
		t.Errorf("Inbox.UniqueID() = %q, want %q", uid, want)
	}

	if i.IsRegistered() {
		t.Fatal("fresh Inbox must not be registered")
	}
	i.MarkRegistered()
	if !i.IsRegistered() {
		t.Fatal("Inbox must be registered after MarkRegistered()")
	}

	if !i.ModifiedAt().IsZero() {
		t.Fatal("fresh Inbox: ModifiedAt() must be zero")
	}
	if !i.RefreshedAt().IsZero() {
		t.Fatal("fresh Inbox: RefreshedAt() must be zero")
	}
}
