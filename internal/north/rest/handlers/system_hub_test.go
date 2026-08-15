// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
)

func TestGetSystemUpdate(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:   "3.75.6",
		AvailableFirmware: "3.77.10",
		UpdateAvailable:   true,
	})
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetSystemUpdate(idx)(rr, httptest.NewRequest(http.MethodGet, "/system/update", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []SystemUpdateEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].CurrentFirmware != "3.75.6" || !out[0].UpdateAvailable || !out[0].Observed {
		t.Fatalf("unexpected entry: %+v", out)
	}
}

func TestGetHubMetrics(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Metrics.Observe(hub.MetricSystemHealth, 98.5)
	h.Metrics.Observe(hub.MetricConnectionLatMs, 12.3)
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetHubMetrics(idx)(rr, httptest.NewRequest(http.MethodGet, "/system/metrics", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []HubMetricsEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].SystemHealth == nil || *out[0].SystemHealth != 98.5 {
		t.Fatalf("system_health: %+v", out)
	}
	// last_event_age was never observed — must be omitted, not zero.
	if out[0].LastEventAgeSeconds != nil {
		t.Fatalf("last_event_age must be nil until observed, got %v", *out[0].LastEventAgeSeconds)
	}
}

func TestInstallModeInterfaces(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	dp := hub.NewInstallMode("HmIP-RF", nil)
	dp.OnState(false, 0)
	h.PutInstallMode(dp)
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	GetInstallModeInterfaces(idx)(rr, httptest.NewRequest(http.MethodGet, "/install-mode/interfaces", http.NoBody))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var out []InstallModeInterfaceEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Interface != "HmIP-RF" || out[0].Active || !out[0].Observed {
		t.Fatalf("unexpected entry: %+v", out)
	}

	// POST without interface → 422.
	rr = httptest.NewRecorder()
	PostInstallModeInterface(idx, nil)(rr, httptest.NewRequest(http.MethodPost, "/install-mode/interfaces",
		strings.NewReader(`{"active":true}`)))
	if rr.Code != 422 {
		t.Fatalf("missing interface: status = %d, want 422", rr.Code)
	}

	// POST for an unknown interface → 404.
	rr = httptest.NewRecorder()
	PostInstallModeInterface(idx, nil)(rr, httptest.NewRequest(http.MethodPost, "/install-mode/interfaces",
		strings.NewReader(`{"interface":"BidCos-RF","active":true}`)))
	if rr.Code != 404 {
		t.Fatalf("unknown interface: status = %d, want 404", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// PostInstallModeInterface — LOCAL teach-in (SGTIN + device key)
// ---------------------------------------------------------------------------

// fakeInstallWriter implements [hub.InstallModeWriter] for handler tests.
type fakeInstallWriter struct {
	gotEnabled  bool
	gotDuration time.Duration
	gotDevice   string
}

func (f *fakeInstallWriter) SetInstallMode(_ context.Context, _ string, enabled bool, duration time.Duration) error {
	f.gotEnabled = enabled
	f.gotDuration = duration
	return nil
}

// SetInstallModeForDevice satisfies [hub.DeviceInstallModeWriter] so the
// device_address dispatch path can be pinned without falling back to the
// plain broadcast SetInstallMode.
func (f *fakeInstallWriter) SetInstallModeForDevice(_ context.Context, _ string, duration time.Duration, deviceAddress string) error {
	f.gotEnabled = true
	f.gotDuration = duration
	f.gotDevice = deviceAddress
	return nil
}

// fakeLocalInstallWriter implements [hub.LocalInstallModeWriter] for
// handler tests exercising the LOCAL teach-in dispatch.
type fakeLocalInstallWriter struct {
	fakeInstallWriter
	err      error
	gotSGTIN string
	gotKey   string
}

func (f *fakeLocalInstallWriter) SetInstallModeLocal(_ context.Context, _ string, duration time.Duration, sgtin, keyHex string) error {
	f.gotDuration = duration
	f.gotSGTIN = sgtin
	f.gotKey = keyHex
	return f.err
}

// postInstallMode is a small helper that dispatches PostInstallModeInterface
// with the given body and returns the recorder.
func postInstallMode(idx HubIndex, rec audit.Recorder, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	PostInstallModeInterface(idx, rec)(rr, httptest.NewRequest(http.MethodPost, "/install-mode/interfaces", strings.NewReader(body)))
	return rr
}

// TestPostInstallModeInterface_ValidationMatrix pins the 422 validation
// matrix for the LOCAL teach-in fields: key without sgtin, sgtin+key
// combined with active=false, and sgtin+key combined with device_address.
func TestPostInstallModeInterface_ValidationMatrix(t *testing.T) {
	t.Parallel()
	idx := &testHubIndex{h: hub.NewHub("test-ccu")}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "key without sgtin",
			body: `{"interface":"HmIP-RF","active":true,"key":"0110C8531D0952D8D73E1194E95B5F19"}`,
		},
		{
			name: "sgtin+key with active false",
			body: `{"interface":"HmIP-RF","active":false,"sgtin":"3014F711A061A7D569892A67","key":"0110C8531D0952D8D73E1194E95B5F19"}`,
		},
		{
			name: "sgtin+key with device_address",
			body: `{"interface":"HmIP-RF","active":true,"sgtin":"3014F711A061A7D569892A67","key":"0110C8531D0952D8D73E1194E95B5F19","device_address":"AABBCCDD"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rr := postInstallMode(idx, nil, tc.body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestPostInstallModeInterface_CentralFilterSelectsNamedHub verifies that
// two centrals exposing the same interface name are disambiguated by the
// `central` request field: only the named central's install-mode DP is
// toggled, the other is left untouched.
func TestPostInstallModeInterface_CentralFilterSelectsNamedHub(t *testing.T) {
	t.Parallel()

	writerA := &fakeInstallWriter{}
	hA := hub.NewHub("ccu-a")
	hA.PutInstallMode(hub.NewInstallMode("HmIP-RF", writerA))

	writerB := &fakeInstallWriter{}
	hB := hub.NewHub("ccu-b")
	hB.PutInstallMode(hub.NewInstallMode("HmIP-RF", writerB))

	idx := &multiHubIndex{hubs: []NamedHub{
		{Central: "ccu-a", Hub: hA},
		{Central: "ccu-b", Hub: hB},
	}}

	rr := postInstallMode(idx, nil, `{"interface":"HmIP-RF","active":true,"seconds":60,"central":"ccu-b"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if !writerB.gotEnabled {
		t.Fatal("ccu-b's install-mode writer must have been toggled")
	}
	if writerA.gotEnabled {
		t.Fatal("ccu-a's install-mode writer must NOT have been toggled")
	}
}

// TestPostInstallModeInterface_LocalTeachInRecordsAudit verifies the happy
// LOCAL path: the audit entry carries ActionInstallModeLocal, DeviceAddress
// equal to the submitted SGTIN, and a Note that never contains the device
// key — the key is credential material and must never be logged.
func TestPostInstallModeInterface_LocalTeachInRecordsAudit(t *testing.T) {
	t.Parallel()
	writer := &fakeLocalInstallWriter{}
	h := hub.NewHub("test-ccu")
	h.PutInstallMode(hub.NewInstallMode("HmIP-RF", writer))
	idx := &testHubIndex{h: h}
	rec := &captureRecorder{}

	const (
		sgtin = "3014-F711-A061-A7D5-6989-2A67"
		key   = "0110C8531D0952D8D73E1194E95B5F19"
	)
	rr := postInstallMode(idx, rec, `{"interface":"HmIP-RF","active":true,"seconds":300,"sgtin":"`+sgtin+`","key":"`+key+`"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if writer.gotSGTIN != "3014F711A061A7D569892A67" {
		t.Fatalf("writer got sgtin=%q, want normalised 3014F711A061A7D569892A67", writer.gotSGTIN)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	entry := rec.entries[0]
	if entry.Action != audit.ActionInstallModeLocal {
		t.Fatalf("Action = %q, want %q", entry.Action, audit.ActionInstallModeLocal)
	}
	if entry.DeviceAddress != sgtin {
		t.Fatalf("DeviceAddress = %q, want the submitted SGTIN %q", entry.DeviceAddress, sgtin)
	}
	if strings.Contains(entry.Note, key) {
		t.Fatalf("audit Note must never contain the device key, got %q", entry.Note)
	}
	if strings.Contains(entry.Note, "0110C8531D0952D8D73E1194E95B5F19") {
		t.Fatal("audit Note must never contain the normalised device key either")
	}
}

// TestPostInstallModeInterface_PlainEnableRecordsAudit verifies that the
// plain broadcast enable path (no sgtin/key) records ActionInstallMode, not
// ActionInstallModeLocal.
func TestPostInstallModeInterface_PlainEnableRecordsAudit(t *testing.T) {
	t.Parallel()
	writer := &fakeInstallWriter{}
	h := hub.NewHub("test-ccu")
	h.PutInstallMode(hub.NewInstallMode("HmIP-RF", writer))
	idx := &testHubIndex{h: h}
	rec := &captureRecorder{}

	rr := postInstallMode(idx, rec, `{"interface":"HmIP-RF","active":true,"seconds":60}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if rec.entries[0].Action != audit.ActionInstallMode {
		t.Fatalf("Action = %q, want %q", rec.entries[0].Action, audit.ActionInstallMode)
	}
}

// TestPostInstallModeInterface_DeviceAddressBodyDecodes is a regression test:
// a request body naming device_address must decode successfully (not 400 —
// DecodeJSON's DisallowUnknownFields would reject it if the field were
// missing from InstallModeInterfaceRequest) and dispatch through
// EnableForDevice.
func TestPostInstallModeInterface_DeviceAddressBodyDecodes(t *testing.T) {
	t.Parallel()
	writer := &fakeInstallWriter{}
	h := hub.NewHub("test-ccu")
	h.PutInstallMode(hub.NewInstallMode("HmIP-RF", writer))
	idx := &testHubIndex{h: h}

	rr := postInstallMode(idx, nil, `{"interface":"HmIP-RF","active":true,"seconds":60,"device_address":"AABBCCDD:1"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (device_address must decode, not 400), body=%s", rr.Code, rr.Body.String())
	}
	if writer.gotDevice != "AABBCCDD:1" {
		t.Fatalf("writer got device=%q, want AABBCCDD:1 (EnableForDevice dispatch)", writer.gotDevice)
	}
}

// TestPostInstallModeInterface_LocalUnsupportedIs422 verifies that a
// hub.ErrLocalInstallModeUnsupported failure (writer without the LOCAL
// extension) maps to 422, not a generic 502 upstream error — it is an
// operator-fixable request shape (wrong interface/backend), not a CCU
// transport failure.
func TestPostInstallModeInterface_LocalUnsupportedIs422(t *testing.T) {
	t.Parallel()
	writer := &fakeInstallWriter{} // no LOCAL extension
	h := hub.NewHub("test-ccu")
	h.PutInstallMode(hub.NewInstallMode("HmIP-RF", writer))
	idx := &testHubIndex{h: h}

	rr := postInstallMode(idx, nil, `{"interface":"HmIP-RF","active":true,"seconds":60,"sgtin":"3014F711A061A7D569892A67","key":"0110C8531D0952D8D73E1194E95B5F19"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PostSystemUpdateInstall — optional pre-update backup (backup_first)
// ---------------------------------------------------------------------------

// orderedFirmwareUpdater implements hub.FirmwareUpdater and, when log is
// set, appends "install" to the shared log — letting a test pin the
// backup-then-install ordering directly rather than just checking both ran.
type orderedFirmwareUpdater struct {
	log   *[]string
	calls int
	err   error
}

func (o *orderedFirmwareUpdater) TriggerFirmwareUpdate(_ context.Context) error {
	o.calls++
	if o.log != nil {
		*o.log = append(*o.log, "install")
	}
	return o.err
}

// orderedBackupPort implements PreUpdateBackupPort for the pre-update
// backup tests. It records every central it was asked to back up and, when
// log is set, appends "backup" to the same shared log orderedFirmwareUpdater
// writes to.
type orderedBackupPort struct {
	log         *[]string
	calls       int
	lastCentral string
	id          string
	err         error
}

func (o *orderedBackupPort) CreateBackupForCentral(_ context.Context, centralName string) (string, error) {
	o.calls++
	o.lastCentral = centralName
	if o.log != nil {
		*o.log = append(*o.log, "backup")
	}
	if o.err != nil {
		return "", o.err
	}
	return o.id, nil
}

func systemUpdateInstallRequestBody(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/system/update/install", strings.NewReader(body))
}

// TestPostSystemUpdateInstall_BackupFirstFailure_Returns502AndSkipsInstall
// verifies that a failing pre-update backup aborts the whole request: the
// entire reason to ask for a backup before a firmware update is to have
// something to fall back to, so an update must never proceed without it.
func TestPostSystemUpdateInstall_BackupFirstFailure_Returns502AndSkipsInstall(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	fw := &orderedFirmwareUpdater{}
	h.Update.FirmwareUpdater = fw
	idx := &testHubIndex{h: h}
	backup := &orderedBackupPort{err: errors.New("ccu busy")}

	rr := httptest.NewRecorder()
	PostSystemUpdateInstall(idx, backup, nil)(rr, systemUpdateInstallRequestBody(`{"backup_first":true}`))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", rr.Code, rr.Body.String())
	}
	if backup.calls != 1 {
		t.Fatalf("expected the backup to be attempted once, got %d", backup.calls)
	}
	if fw.calls != 0 {
		t.Fatal("the update must not be installed when the pre-update backup fails")
	}
}

// TestPostSystemUpdateInstall_BackupFirstSuccess_BacksUpThenInstalls
// verifies the happy path runs the backup to completion before triggering
// the install, in that order, and records the backup_pre_update audit
// entry.
func TestPostSystemUpdateInstall_BackupFirstSuccess_BacksUpThenInstalls(t *testing.T) {
	t.Parallel()
	var order []string
	h := hub.NewHub("test-ccu")
	fw := &orderedFirmwareUpdater{log: &order}
	h.Update.FirmwareUpdater = fw
	idx := &testHubIndex{h: h}
	backup := &orderedBackupPort{log: &order, id: "b1"}
	rec := &captureRecorder{}

	rr := httptest.NewRecorder()
	PostSystemUpdateInstall(idx, backup, rec)(rr, systemUpdateInstallRequestBody(`{"backup_first":true}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if backup.calls != 1 || backup.lastCentral != "test-ccu" {
		t.Fatalf("expected 1 backup call for test-ccu, got calls=%d central=%q", backup.calls, backup.lastCentral)
	}
	if fw.calls != 1 {
		t.Fatalf("expected 1 install call, got %d", fw.calls)
	}
	if len(order) != 2 || order[0] != "backup" || order[1] != "install" {
		t.Fatalf("call order = %v, want [backup install]", order)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0]; got.Action != audit.ActionBackupPreUpdate {
		t.Fatalf("audit action = %q, want %q", got.Action, audit.ActionBackupPreUpdate)
	}
}

// TestPostSystemUpdateInstall_BackupFirstNilPort_Returns503 verifies that
// backup_first with no backup port wired is reported as unwired rather
// than silently skipping the safety net.
func TestPostSystemUpdateInstall_BackupFirstNilPort_Returns503(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Update.FirmwareUpdater = &orderedFirmwareUpdater{}
	idx := &testHubIndex{h: h}

	rr := httptest.NewRecorder()
	PostSystemUpdateInstall(idx, nil, nil)(rr, systemUpdateInstallRequestBody(`{"backup_first":true}`))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rr.Code, rr.Body.String())
	}
}

// ambiguousBackupHubIndex resolves a single hub via the back-compat Hub()
// getter (the same shape adapter.HubAdapter.Hub uses) while Hubs() reports
// none — the condition under which resolveHubForMutation can still find a
// hub for an empty `central` query, but the pre-update-backup target
// defaulting (which re-reads idx.Hubs()) cannot infer a single central and
// must demand an explicit one.
type ambiguousBackupHubIndex struct {
	h *hub.Hub
}

func (a *ambiguousBackupHubIndex) Hub() *hub.Hub                { return a.h }
func (a *ambiguousBackupHubIndex) Hubs() []NamedHub             { return nil }
func (a *ambiguousBackupHubIndex) HubFor(_ string) *hub.Hub     { return nil }
func (a *ambiguousBackupHubIndex) SerialSuffix(_ string) string { return "" }

// TestPostSystemUpdateInstall_BackupFirstAmbiguousCentral_Returns400
// verifies that backup_first refuses to guess a target central when it
// cannot resolve exactly one from the hub index, even though the update
// target itself resolved via the legacy Hub() fallback.
func TestPostSystemUpdateInstall_BackupFirstAmbiguousCentral_Returns400(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	h.Update.FirmwareUpdater = &orderedFirmwareUpdater{}
	idx := &ambiguousBackupHubIndex{h: h}
	backup := &orderedBackupPort{id: "b1"}

	rr := httptest.NewRecorder()
	PostSystemUpdateInstall(idx, backup, nil)(rr, systemUpdateInstallRequestBody(`{"backup_first":true}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if backup.calls != 0 {
		t.Fatal("backup must not be attempted when the target central is ambiguous")
	}
}

// slowBackupPort implements PreUpdateBackupPort with a backup that takes
// longer than the request deadline — the realistic case, since a CCU backup
// polls for minutes. It reports the context error it observed so a test can
// tell "the backup was cancelled" from "the backup ran to completion".
type slowBackupPort struct {
	duration time.Duration
	calls    int
	ctxErr   error
}

func (s *slowBackupPort) CreateBackupForCentral(ctx context.Context, _ string) (string, error) {
	s.calls++
	select {
	case <-ctx.Done():
		s.ctxErr = ctx.Err()
		return "", ctx.Err()
	case <-time.After(s.duration):
	}
	return "b1", nil
}

// ctxObservingFirmwareUpdater records whether the context it was handed was
// still live — the install runs after the backup, so a request-bound context
// is already expired by the time it is reached.
type ctxObservingFirmwareUpdater struct {
	calls  int
	ctxErr error
}

func (c *ctxObservingFirmwareUpdater) TriggerFirmwareUpdate(ctx context.Context) error {
	c.calls++
	c.ctxErr = ctx.Err()
	return ctx.Err()
}

// TestPostSystemUpdateInstall_BackupFirstOutlivesRequestDeadline verifies
// that the pre-update backup and the install that follows it are not bound
// to the router's request deadline. The router wraps /api/v1 in
// middleware.Timeout, so a backup that polls the CCU for minutes would be
// cancelled every time and the operator told the safety net failed — while
// the CCU-side backup keeps running.
func TestPostSystemUpdateInstall_BackupFirstOutlivesRequestDeadline(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	fw := &ctxObservingFirmwareUpdater{}
	h.Update.FirmwareUpdater = fw
	idx := &testHubIndex{h: h}
	backup := &slowBackupPort{duration: 80 * time.Millisecond}

	rr := httptest.NewRecorder()
	// The real router deadline, only shorter: the point is that the backup
	// outlasts it, not how long it is.
	handler := middleware.Timeout(20 * time.Millisecond)(PostSystemUpdateInstall(idx, backup, nil))
	handler.ServeHTTP(rr, systemUpdateInstallRequestBody(`{"backup_first":true}`))

	if backup.ctxErr != nil {
		t.Fatalf("pre-update backup was cancelled by the request deadline: %v", backup.ctxErr)
	}
	if fw.ctxErr != nil {
		t.Fatalf("install ran on an expired context: %v", fw.ctxErr)
	}
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if backup.calls != 1 || fw.calls != 1 {
		t.Fatalf("backup calls = %d, install calls = %d, want 1 and 1", backup.calls, fw.calls)
	}
}

// TestPostSystemUpdateInstall_WithoutBackupFirst_InstallOnlyBackupNeverCalled
// verifies the default (no flag) behaviour is unchanged: only the update
// runs, the backup port is never consulted.
func TestPostSystemUpdateInstall_WithoutBackupFirst_InstallOnlyBackupNeverCalled(t *testing.T) {
	t.Parallel()
	h := hub.NewHub("test-ccu")
	fw := &orderedFirmwareUpdater{}
	h.Update.FirmwareUpdater = fw
	idx := &testHubIndex{h: h}
	backup := &orderedBackupPort{}

	rr := httptest.NewRecorder()
	PostSystemUpdateInstall(idx, backup, nil)(rr, systemUpdateInstallRequestBody(`{}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rr.Code, rr.Body.String())
	}
	if backup.calls != 0 {
		t.Fatal("backup must never run when backup_first is not set")
	}
	if fw.calls != 1 {
		t.Fatalf("expected 1 install call, got %d", fw.calls)
	}
}
