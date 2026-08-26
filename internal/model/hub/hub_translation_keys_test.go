// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for Update.VersionBeforeUpdate and MonitorProgress; translation keys
// and data types on AlarmMessages, ServiceMessages, Inbox, InstallMode, and
// Connectivity; TranslationKeyForMetric; and Sysvar.PathData.
package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── VersionBeforeUpdate ──────────────────────────────────────────────

// TestUpdateVersionBeforeUpdateAbsentInitially verifies that no version
// snapshot is present on a fresh Update.
func TestUpdateVersionBeforeUpdateAbsentInitially(t *testing.T) {
	u := NewUpdate()
	_, ok := u.VersionBeforeUpdate()
	if ok {
		t.Fatal("VersionBeforeUpdate must be absent on fresh Update")
	}
}

// TestUpdateSetVersionBeforeUpdate verifies that SetVersionBeforeUpdate
// stores the version and makes it readable.
func TestUpdateSetVersionBeforeUpdate(t *testing.T) {
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.55.5")
	v, ok := u.VersionBeforeUpdate()
	if !ok {
		t.Fatal("VersionBeforeUpdate must be present after Set")
	}
	if v != "3.55.5" {
		t.Fatalf("VersionBeforeUpdate()=%q, want %q", v, "3.55.5")
	}
}

// ─── MonitorProgress ────────────────────────────────────────────

// TestUpdateMonitorProgressClearsInProgressOnVersionChange verifies that
// MonitorProgress clears the in-progress flag when the firmware version
// changes.
func TestUpdateMonitorProgressClearsInProgressOnVersionChange(t *testing.T) {
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.55")
	u.SetInProgress(true)

	calls := 0
	pollFn := func(_ context.Context) (string, error) {
		calls++
		if calls >= 2 {
			return "3.57", nil // version changed
		}
		return "3.55", nil
	}

	u.MonitorProgress(context.Background(), pollFn, 0, 10)

	if u.InProgress() {
		t.Fatal("InProgress must be false after version change")
	}
	info, ok := u.UpdateInfo()
	if !ok {
		t.Fatal("Info must be observed after MonitorProgress")
	}
	if info.CurrentFirmware != "3.57" {
		t.Fatalf("CurrentFirmware=%q, want %q", info.CurrentFirmware, "3.57")
	}
}

// TestUpdateMonitorProgressClearsAfterMaxPoll verifies that MonitorProgress
// clears the in-progress flag when max polls are exhausted.
func TestUpdateMonitorProgressClearsAfterMaxPoll(t *testing.T) {
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.55")
	u.SetInProgress(true)

	// always return same version → no change detected
	pollFn := func(_ context.Context) (string, error) { return "3.55", nil }
	u.MonitorProgress(context.Background(), pollFn, 0, 3)

	if u.InProgress() {
		t.Fatal("InProgress must be false after maxPoll exhausted")
	}
}

// TestUpdateMonitorProgressCancelledByContext verifies that MonitorProgress
// respects ctx cancellation.
func TestUpdateMonitorProgressCancelledByContext(t *testing.T) {
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.55")
	u.SetInProgress(true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled immediately

	pollFn := func(_ context.Context) (string, error) {
		t.Error("pollFn must not be called after context cancel")
		return "", errors.New("unexpected call")
	}
	u.MonitorProgress(ctx, pollFn, 0, 5)
	// The key assertion is that pollFn is never called (a cancelled ctx
	// returns before the poll). MonitorProgress's defer clears InProgress on
	// every exit (mirrors the Python finally), so it is not asserted here.
}

// ─── AlarmMessages surface ───────────────────

// TestAlarmMessagesAvailable verifies that Available returns false initially
// and true after first Replace.
func TestAlarmMessagesAvailable(t *testing.T) {
	a := NewAlarmMessages(nil)
	if a.Available() {
		t.Fatal("Available must be false before any Replace")
	}
	a.Replace([]AlarmMessage{})
	if !a.Available() {
		t.Fatal("Available must be true after first Replace")
	}
}

// TestAlarmMessagesDataType verifies the DataType constant.
func TestAlarmMessagesDataType(t *testing.T) {
	a := NewAlarmMessages(nil)
	if got := a.DataType(); got != "INTEGER" {
		t.Fatalf("DataType()=%q, want INTEGER", got)
	}
}

// TestAlarmMessagesTranslationKey verifies the translation key.
func TestAlarmMessagesTranslationKey(t *testing.T) {
	a := NewAlarmMessages(nil)
	if got := a.TranslationKey(); got != "alarm_messages" {
		t.Fatalf("TranslationKey()=%q, want alarm_messages", got)
	}
}

// ─── ServiceMessages.TranslationKey ───────────────────────────────────

// TestServiceMessagesTranslationKey verifies the translation key.
func TestServiceMessagesTranslationKey(t *testing.T) {
	s := NewServiceMessages(nil)
	if got := s.TranslationKey(); got != "service_messages" {
		t.Fatalf("TranslationKey()=%q, want service_messages", got)
	}
}

// ─── Inbox.TranslationKey + DataType ─────────────────────────

// TestInboxTranslationKey verifies the translation key.
func TestInboxTranslationKey(t *testing.T) {
	i := NewInbox()
	if got := i.TranslationKey(); got != "inbox" {
		t.Fatalf("TranslationKey()=%q, want inbox", got)
	}
}

// TestInboxDataType verifies the data type constant.
func TestInboxDataType(t *testing.T) {
	i := NewInbox()
	if got := i.DataType(); got != "INTEGER" {
		t.Fatalf("DataType()=%q, want INTEGER", got)
	}
}

// ─── InstallMode.TranslationKey ───────────────────────────────────────

// TestInstallModeTranslationKey verifies the translation key.
func TestInstallModeTranslationKey(t *testing.T) {
	m := NewInstallMode("HmIP-RF", nil)
	if got := m.TranslationKey(); got != "install_mode" {
		t.Fatalf("TranslationKey()=%q, want install_mode", got)
	}
}

// ─── InstallMode.Press ────────────────────────────────────────────────

// TestInstallModePressEnablesFor60Seconds verifies that Press enables
// install mode for the default 60-second duration.
func TestInstallModePressEnablesFor60Seconds(t *testing.T) {
	w := &stubInstall{}
	m := NewInstallMode("HmIP-RF", w)
	if err := m.Press(context.Background()); err != nil {
		t.Fatalf("Press() unexpected error: %v", err)
	}
	enabled, remain, ok := m.InstallState()
	if !enabled || !ok {
		t.Fatalf("Press() must enable install mode; enabled=%v ok=%v", enabled, ok)
	}
	// duration should be 60 s (defaultInstallModeDuration)
	if remain > 60*time.Second || remain <= 0 {
		t.Fatalf("Press() remaining=%v, want ≤60s and >0", remain)
	}
	// Verify the writer received the correct args
	stored := w.last.Load().([3]any)
	if stored[1] != true {
		t.Errorf("writer: enabled=%v, want true", stored[1])
	}
	if stored[2].(time.Duration) != 60*time.Second {
		t.Errorf("writer: duration=%v, want 60s", stored[2])
	}
}

// TestInstallModePressWithoutWriterErr verifies that Press returns an
// error when no writer is configured.
func TestInstallModePressWithoutWriterErr(t *testing.T) {
	m := NewInstallMode("HmIP-RF", nil)
	if err := m.Press(context.Background()); err == nil {
		t.Fatal("Press() without writer must return error")
	}
}

// ─── InstallMode.EnableForDevice ──────────────────────────────────────

// TestInstallModeEnableForDeviceDelegatesToEnable verifies that
// EnableForDevice delegates to Enable (device_address not yet propagated).
func TestInstallModeEnableForDeviceDelegatesToEnable(t *testing.T) {
	w := &stubInstall{}
	m := NewInstallMode("HmIP-RF", w)
	if err := m.EnableForDevice(context.Background(), 30*time.Second, "00012A:1"); err != nil {
		t.Fatalf("EnableForDevice() unexpected error: %v", err)
	}
	enabled, _, ok := m.InstallState()
	if !enabled || !ok {
		t.Fatal("EnableForDevice must enable install mode")
	}
}

// ─── TranslationKeyForMetric ─────────────────────────────────────────

// TestTranslationKeyForMetricKnownKinds verifies canonical translation
// keys for the three built-in metric kinds.
func TestTranslationKeyForMetricKnownKinds(t *testing.T) {
	cases := []struct {
		kind MetricKind
		want string
	}{
		{MetricSystemHealth, "system_health"},
		{MetricConnectionLatMs, "connection_latency_ms"},
		{MetricLastEventAgeSecs, "last_event_age_seconds"},
	}
	for _, tc := range cases {
		if got := TranslationKeyForMetric(tc.kind); got != tc.want {
			t.Errorf("TranslationKeyForMetric(%v)=%q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestTranslationKeyForMetricUnknown verifies fallback for unknown metric kind.
func TestTranslationKeyForMetricUnknown(t *testing.T) {
	kind := MetricKind("custom_metric")
	if got := TranslationKeyForMetric(kind); got != "custom_metric" {
		t.Errorf("TranslationKeyForMetric(unknown)=%q, want %q", got, "custom_metric")
	}
}

// ─── Connectivity.TranslationKey + Available ─────────────────

// TestConnectivityTranslationKey verifies the translation key.
func TestConnectivityTranslationKey(t *testing.T) {
	c := NewConnectivity()
	if got := c.TranslationKey(); got != "interface_connectivity" {
		t.Fatalf("TranslationKey()=%q, want interface_connectivity", got)
	}
}

// TestConnectivityAvailableAfterFirstState verifies that Available returns
// false initially and true after first OnState call.
func TestConnectivityAvailableAfterFirstState(t *testing.T) {
	c := NewConnectivity()
	if c.Available() {
		t.Fatal("Available must be false before any OnState")
	}
	c.OnState("HmIP-RF", true)
	if !c.Available() {
		t.Fatal("Available must be true after first OnState")
	}
}

// ─── Sysvar.PathData ──────────────────────────────────────────────────

// TestSysvarPathDataWithVid verifies that PathData returns correct
// sysvar/set and sysvar/status paths when Vid is set.
func TestSysvarPathDataWithVid(t *testing.T) {
	sv := NewSysvar("c1", "myvar", "", hmenum.HubValueTypeString, nil)
	sv.Vid = 42
	pd := sv.PathData()
	if pd.IsZero() {
		t.Fatal("PathData must not be zero when Vid is set")
	}
	wantSet := "sysvar/set/42"
	wantState := "sysvar/status/42"
	if pd.SetPath != wantSet {
		t.Errorf("SetPath=%q, want %q", pd.SetPath, wantSet)
	}
	if pd.StatePath != wantState {
		t.Errorf("StatePath=%q, want %q", pd.StatePath, wantState)
	}
}

// TestSysvarPathDataWithoutVid verifies that PathData returns empty paths
// when Vid is zero.
func TestSysvarPathDataWithoutVid(t *testing.T) {
	sv := NewSysvar("c1", "myvar", "", hmenum.HubValueTypeString, nil)
	// Vid is 0 (default)
	pd := sv.PathData()
	if !pd.IsZero() {
		t.Fatalf("PathData must be zero when Vid=0, got set=%q state=%q", pd.SetPath, pd.StatePath)
	}
}
