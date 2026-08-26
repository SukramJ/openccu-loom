// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// extended_events_test.go covers additional event types:
// DataPointValueReceivedEvent,
// ConnectionHealthChangedEvent, CacheInvalidatedEvent,
// RecoveryAttemptedEvent,
// IntegrationIssue.

package hmevent_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func TestDataPointValueReceivedEventType(t *testing.T) {
	t.Parallel()
	e := hmevent.DataPointValueReceivedEvent{
		Base:           hmevent.NewBase(),
		CentralName:    "ccu1",
		InterfaceID:    "ccu1-HmIP-RF",
		ChannelAddress: "VCU0000001:1",
		Parameter:      "STATE",
		Value:          true,
	}
	if e.Type() != hmevent.EventTypeDataPointValueReceived {
		t.Fatalf("wrong event type: %s", e.Type())
	}
}

func TestConnectionHealthChangedEventType(t *testing.T) {
	t.Parallel()
	e := hmevent.ConnectionHealthChangedEvent{
		Base:                hmevent.NewBase(),
		CentralName:         "ccu1",
		InterfaceID:         "ccu1-HmIP-RF",
		IsHealthy:           false,
		FailureReason:       hmenum.FailureReasonNetwork,
		ConsecutiveFailures: 3,
	}
	if e.Type() != hmevent.EventTypeConnectionHealthChanged {
		t.Fatalf("wrong event type: %s", e.Type())
	}
}

func TestCacheInvalidatedEventType(t *testing.T) {
	t.Parallel()
	e := hmevent.CacheInvalidatedEvent{
		Base:            hmevent.NewBase(),
		CentralName:     "ccu1",
		CacheType:       hmenum.CacheTypeData,
		Reason:          hmenum.CacheInvalidationReasonDeviceRemoved,
		Scope:           "VCU0000001",
		EntriesAffected: 5,
	}
	if e.Type() != hmevent.EventTypeCacheInvalidated {
		t.Fatalf("wrong event type: %s", e.Type())
	}
}

func TestRecoveryAttemptedEventType(t *testing.T) {
	t.Parallel()
	e := hmevent.RecoveryAttemptedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   "ccu1",
		InterfaceID:   "ccu1-HmIP-RF",
		AttemptNumber: 2,
		MaxAttempts:   10,
		StageReached:  hmenum.RecoveryStageRecovered,
		Success:       true,
		ErrorMessage:  "",
	}
	if e.Type() != hmevent.EventTypeRecoveryAttempted {
		t.Fatalf("wrong event type: %s", e.Type())
	}
	if e.AttemptNumber != 2 {
		t.Fatalf("wrong attempt number: %d", e.AttemptNumber)
	}
}

func TestIntegrationIssueIssueID(t *testing.T) {
	t.Parallel()
	issue := hmevent.IntegrationIssue{
		IssueType:   hmenum.IntegrationIssueTypePingPongMismatch,
		Severity:    hmenum.IntegrationIssueSeverityWarning,
		InterfaceID: "ccu1-HmIP-RF",
	}
	want := "ping_pong_mismatch_ccu1-HmIP-RF"
	if got := issue.IssueID(); got != want {
		t.Fatalf("IssueID: want %q, got %q", want, got)
	}
}

func TestIntegrationIssueTranslationKey(t *testing.T) {
	t.Parallel()
	issue := hmevent.IntegrationIssue{
		IssueType:   hmenum.IntegrationIssueTypeFetchDataFailed,
		Severity:    hmenum.IntegrationIssueSeverityWarning,
		InterfaceID: "ccu1-BidCos-RF",
	}
	if got := issue.TranslationKey(); got != "fetch_data_failed" {
		t.Fatalf("TranslationKey: want %q, got %q", "fetch_data_failed", got)
	}
}

func TestIntegrationIssueWithDeviceAddresses(t *testing.T) {
	t.Parallel()
	issue := hmevent.IntegrationIssue{
		IssueType:       hmenum.IntegrationIssueTypeIncompleteDeviceData,
		Severity:        hmenum.IntegrationIssueSeverityError,
		InterfaceID:     "ccu1-HmIP-RF",
		DeviceAddresses: []string{"VCU0000001", "VCU0000002"},
	}
	if len(issue.DeviceAddresses) != 2 {
		t.Fatalf("want 2 device addresses, got %d", len(issue.DeviceAddresses))
	}
}
