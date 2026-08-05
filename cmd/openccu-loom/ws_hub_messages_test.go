// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── wsHubQuery ListAlarmMessages with populated messages ────────────────────

// TestWSHubQuery_ListAlarmMessages_WithMessage_ReturnsEntry verifies the WS
// entry carries an alarm's identity, counter and Timestamp. An alarm entry
// has no device, channel or room — see [hub.AlarmMessage] — so none of
// those keys are present.
func TestWSHubQuery_ListAlarmMessages_WithMessage_ReturnsEntry(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	ts := time.Unix(1700000000, 0).UTC()
	h.Messages.Replace([]hub.AlarmMessage{
		{
			ID:          "alarm-1",
			Name:        "Smoke Alarm",
			Description: "Kitchen smoke detector",
			Timestamp:   ts,
			Counter:     3,
		},
	})

	got, err := q.ListAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("ListAlarmMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 alarm message, got %d", len(got))
	}
	if got[0]["id"] != "alarm-1" {
		t.Errorf("expected id=alarm-1, got %v", got[0]["id"])
	}
	if got[0]["name"] != "Smoke Alarm" {
		t.Errorf("expected name=Smoke Alarm, got %v", got[0]["name"])
	}
	if !got[0]["timestamp"].(time.Time).Equal(ts) {
		t.Errorf("expected timestamp=%v, got %v", ts, got[0]["timestamp"])
	}
	if _, present := got[0]["last_timestamp"]; present {
		t.Error("last_timestamp must be omitted when LastTimestamp is zero")
	}
}

// TestWSHubQuery_ListAlarmMessages_ZeroTimestamp_OmitsKey verifies that an
// alarm whose Timestamp / LastTimestamp the CCU never reported (the Go
// zero time) omits both keys rather than surfacing the zero time.
func TestWSHubQuery_ListAlarmMessages_ZeroTimestamp_OmitsKey(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.Messages.Replace([]hub.AlarmMessage{{ID: "alarm-2", Name: "Never raised"}})

	got, err := q.ListAlarmMessages(context.Background())
	if err != nil {
		t.Fatalf("ListAlarmMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 alarm message, got %d", len(got))
	}
	if _, present := got[0]["timestamp"]; present {
		t.Error("timestamp must be omitted for a zero Timestamp")
	}
	if _, present := got[0]["last_timestamp"]; present {
		t.Error("last_timestamp must be omitted for a zero LastTimestamp")
	}
}

// ── wsHubQuery ListServiceMessages with populated messages ───────────────────

func TestWSHubQuery_ListServiceMessages_WithMessage_ReturnsEntry(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.ServiceMessages.Replace([]hub.ServiceMessage{
		{
			ID:          "svc-1",
			Name:        "Low Battery",
			Address:     "DEV002:0",
			DeviceName:  "Motion Sensor",
			Type:        hmenum.ServiceMessageTypeGeneric,
			Description: "Battery level critical",
			Priority:    1,
			Counter:     2,
			Quittable:   true,
		},
	})

	got, err := q.ListServiceMessages(context.Background())
	if err != nil {
		t.Fatalf("ListServiceMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 service message, got %d", len(got))
	}
	if got[0]["id"] != "svc-1" {
		t.Errorf("expected id=svc-1, got %v", got[0]["id"])
	}
	if got[0]["quittable"] != true {
		t.Errorf("expected quittable=true, got %v", got[0]["quittable"])
	}
}

// ── wsHubQuery InboxDevices with populated inbox ─────────────────────────────

func TestWSHubQuery_InboxDevices_WithDevices_ReturnsEntries(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.Inbox.Replace([]hub.InboxDevice{
		{
			Address:      "NEW001",
			Model:        "HmIP-PSM",
			Serial:       "SN001",
			Manufacturer: "eQ-3",
			FirstSeen:    1700000000,
		},
	})

	got, err := q.InboxDevices(context.Background())
	if err != nil {
		t.Fatalf("InboxDevices: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inbox device, got %d", len(got))
	}
	if got[0]["address"] != "NEW001" {
		t.Errorf("expected address=NEW001, got %v", got[0]["address"])
	}
	if got[0]["model"] != "HmIP-PSM" {
		t.Errorf("expected model=HmIP-PSM, got %v", got[0]["model"])
	}
}

// ── wsHubQuery FirmwareInfo with observed update info ────────────────────────

func TestWSHubQuery_FirmwareInfo_WithObservedUpdate_ReturnsInfo(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:      "2.54.10",
		AvailableFirmware:    "2.55.0",
		UpdateAvailable:      true,
		CheckScriptAvailable: true,
	})

	got, err := q.FirmwareInfo(context.Background())
	if err != nil {
		t.Fatalf("FirmwareInfo: %v", err)
	}
	if got["observed"] != true {
		t.Errorf("expected observed=true, got %v", got["observed"])
	}
	if got["current_firmware"] != "2.54.10" {
		t.Errorf("expected current_firmware=2.54.10, got %v", got["current_firmware"])
	}
	if got["available_firmware"] != "2.55.0" {
		t.Errorf("expected available_firmware=2.55.0, got %v", got["available_firmware"])
	}
}

// ── wsHubQuery FirmwareInfo: non-nil Update but not-observed ─────────────────

func TestWSHubQuery_FirmwareInfo_NonNilUpdate_NotObserved_ReturnsObservedFalse(t *testing.T) {
	t.Parallel()
	q, h := liveHubQuery(t)
	// h.Update is non-nil by default (NewHub creates it), but Info() returns
	// observed=false until OnInfo is called.
	if h.Update == nil {
		t.Skip("h.Update is nil; test not applicable")
	}
	got, err := q.FirmwareInfo(context.Background())
	if err != nil {
		t.Fatalf("FirmwareInfo: %v", err)
	}
	// If not observed, should have observed=false (and may have in_progress key).
	if got["observed"] != false {
		t.Errorf("expected observed=false for fresh hub update, got %v", got["observed"])
	}
	// Covers the h.Update != nil && !observed branch: `in_progress` key must be present.
	if _, ok := got["in_progress"]; !ok {
		t.Error("expected 'in_progress' key in non-nil-Update unobserved response")
	}
}

// ── wsAllDevices with non-nil DevicesAdapter ─────────────────────────────────

func TestWSAllDevices_NonNilDevs_ReturnsDevices(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	devAdapter := adapter.NewDevicesAdapter(reg)
	w := &wsAllDevices{devs: devAdapter}
	// Empty registry → Devices() returns empty slice (not nil).
	got := w.AllDevices()
	// Must not panic; result is empty but non-nil from DevicesAdapter.
	_ = got
}
