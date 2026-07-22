// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

type fakeCentralLinks struct {
	createReport hmapi.CentralLinksReport
	createErr    error
	removeReport hmapi.CentralLinksReport
	removeErr    error
	status       hmapi.CentralLinksStatus
	statusErr    error

	// Captured targets of the last create/remove call so tests can
	// assert the channel argument is forwarded from the request.
	lastCreateAddr    string
	lastCreateChannel string
	lastRemoveAddr    string
	lastRemoveChannel string
}

func (f *fakeCentralLinks) CreateCentralLinks(_ context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error) {
	f.lastCreateAddr = deviceAddress
	f.lastCreateChannel = channelAddress
	return f.createReport, f.createErr
}

func (f *fakeCentralLinks) RemoveCentralLinks(_ context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error) {
	f.lastRemoveAddr = deviceAddress
	f.lastRemoveChannel = channelAddress
	return f.removeReport, f.removeErr
}

func (f *fakeCentralLinks) CentralLinksStatus(_ string) (hmapi.CentralLinksStatus, error) {
	return f.status, f.statusErr
}

type fakeSessionRecorder struct {
	active bool
}

func (f *fakeSessionRecorder) Start() bool {
	f.active = true
	return f.active
}

func (f *fakeSessionRecorder) Stop() bool {
	f.active = false
	return f.active
}

func (f *fakeSessionRecorder) IsActive() bool {
	return f.active
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// decodeReport JSON-round-trips res.Data into a CentralLinksReport.
func decodeReport(t *testing.T, data any) hmapi.CentralLinksReport {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var r hmapi.CentralLinksReport
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return r
}

// decodeStatus JSON-round-trips res.Data into a CentralLinksStatus.
func decodeStatus(t *testing.T, data any) hmapi.CentralLinksStatus {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var s hmapi.CentralLinksStatus
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	return s
}

func newLinksRouter(cl CentralLinksManager, rec SessionRecorder) *Router {
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		CentralLinks:    cl,
		SessionRecorder: rec,
	})
	return r
}

func marshalArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return b
}

// ─── central links tests ─────────────────────────────────────────────────────

func TestCentralCreateLinks_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{
		createReport: hmapi.CentralLinksReport{Touched: 3, Skipped: 1, Failed: 0},
	}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "central.create_links", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	got := decodeReport(t, res.Data)
	if got.Touched != 3 {
		t.Errorf("expected Touched=3, got %d", got.Touched)
	}
	if got.Skipped != 1 {
		t.Errorf("expected Skipped=1, got %d", got.Skipped)
	}
}

func TestCentralCreateLinks_MissingAddress(t *testing.T) {
	t.Parallel()
	r := newLinksRouter(&fakeCentralLinks{}, nil)

	res := r.Dispatch(opCtx(), "central.create_links", marshalArgs(t, map[string]any{}))
	if res.Error == nil {
		t.Fatal("expected error for missing address")
	}
	if res.Error.Code != CommandErrorBadRequest {
		t.Errorf("expected bad_request, got %q", res.Error.Code)
	}
}

func TestCentralCreateLinks_ManagerError(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{createErr: errors.New("boom")}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "central.create_links", raw)
	if res.Error == nil {
		t.Fatal("expected error from manager")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Errorf("expected internal_error, got %q", res.Error.Code)
	}
}

func TestCentralRemoveLinks_AddressAlias(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{
		removeReport: hmapi.CentralLinksReport{Touched: 2},
	}
	r := newLinksRouter(fake, nil)

	// Use the `address` alias instead of `device_address`.
	raw := marshalArgs(t, map[string]any{"address": "DEF0002"})
	res := r.Dispatch(opCtx(), "central.remove_links", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	got := decodeReport(t, res.Data)
	if got.Touched != 2 {
		t.Errorf("expected Touched=2, got %d", got.Touched)
	}
}

func TestCentralCreateLinks_ForwardsChannel(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{createReport: hmapi.CentralLinksReport{Touched: 1}}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001", "channel": "ABC0001:2"})
	res := r.Dispatch(opCtx(), "central.create_links", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if fake.lastCreateAddr != "ABC0001" {
		t.Errorf("device address = %q, want ABC0001", fake.lastCreateAddr)
	}
	if fake.lastCreateChannel != "ABC0001:2" {
		t.Errorf("channel = %q, want ABC0001:2", fake.lastCreateChannel)
	}
}

func TestCentralRemoveLinks_ForwardsChannel(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{removeReport: hmapi.CentralLinksReport{Touched: 1}}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001", "channel": "ABC0001:3"})
	res := r.Dispatch(opCtx(), "central.remove_links", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if fake.lastRemoveChannel != "ABC0001:3" {
		t.Errorf("channel = %q, want ABC0001:3", fake.lastRemoveChannel)
	}
}

func TestCentralCreateLinks_NoChannelForwardsEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{createReport: hmapi.CentralLinksReport{Touched: 2}}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "central.create_links", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if fake.lastCreateChannel != "" {
		t.Errorf("channel = %q, want empty", fake.lastCreateChannel)
	}
}

func TestCentralRemoveLinks_MissingAddress(t *testing.T) {
	t.Parallel()
	r := newLinksRouter(&fakeCentralLinks{}, nil)

	res := r.Dispatch(opCtx(), "central.remove_links", marshalArgs(t, map[string]any{}))
	if res.Error == nil {
		t.Fatal("expected error for missing address")
	}
	if res.Error.Code != CommandErrorBadRequest {
		t.Errorf("expected bad_request, got %q", res.Error.Code)
	}
}

func TestCentralRemoveLinks_ManagerError(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{removeErr: errors.New("boom")}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "central.remove_links", raw)
	if res.Error == nil {
		t.Fatal("expected error from manager")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Errorf("expected internal_error, got %q", res.Error.Code)
	}
}

func TestCentralLinksStatus_MissingAddress(t *testing.T) {
	t.Parallel()
	r := newLinksRouter(&fakeCentralLinks{}, nil)

	res := r.Dispatch(context.Background(), "central.links_status", marshalArgs(t, map[string]any{}))
	if res.Error == nil {
		t.Fatal("expected error for missing address")
	}
	if res.Error.Code != CommandErrorBadRequest {
		t.Errorf("expected bad_request, got %q", res.Error.Code)
	}
}

func TestCentralLinksStatus_ManagerError(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{statusErr: errors.New("lookup failed")}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(context.Background(), "central.links_status", raw)
	if res.Error == nil {
		t.Fatal("expected error from manager")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Errorf("expected internal_error, got %q", res.Error.Code)
	}
}

func TestCentralLinksStatus_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeCentralLinks{
		status: hmapi.CentralLinksStatus{
			Supported:        true,
			EligibleChannels: 2,
			Channels: []hmapi.CentralLinksChannelStatus{
				{Address: "ABC0001:1", Number: 1, Eligible: true},
				{Address: "ABC0001:2", Number: 2, Eligible: true},
			},
		},
	}
	r := newLinksRouter(fake, nil)

	raw := marshalArgs(t, map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(context.Background(), "central.links_status", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	got := decodeStatus(t, res.Data)
	if !got.Supported {
		t.Error("expected Supported=true")
	}
	if got.EligibleChannels != 2 {
		t.Errorf("expected EligibleChannels=2, got %d", got.EligibleChannels)
	}
	if len(got.Channels) != 2 {
		t.Fatalf("expected 2 per-channel entries, got %d", len(got.Channels))
	}
	if got.Channels[1].Address != "ABC0001:2" || !got.Channels[1].Eligible {
		t.Errorf("unexpected channel entry: %+v", got.Channels[1])
	}
}

// ─── recording tests ─────────────────────────────────────────────────────────

func recordingBool(t *testing.T, data any) bool {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m["recording"].(bool)
	if !ok {
		t.Fatalf("recording field missing or not bool: %v", m)
	}
	return v
}

func TestRecordingStart(t *testing.T) {
	t.Parallel()
	rec := &fakeSessionRecorder{}
	r := newLinksRouter(nil, rec)

	res := r.Dispatch(opCtx(), "recording.start", nil)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if !recordingBool(t, res.Data) {
		t.Error("expected recording=true after start")
	}
	if !rec.IsActive() {
		t.Error("expected fake.IsActive()==true after start")
	}
}

func TestRecordingStop_AfterStart(t *testing.T) {
	t.Parallel()
	rec := &fakeSessionRecorder{}
	r := newLinksRouter(nil, rec)

	// Start first, then stop.
	r.Dispatch(opCtx(), "recording.start", nil)
	res := r.Dispatch(opCtx(), "recording.stop", nil)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if recordingBool(t, res.Data) {
		t.Error("expected recording=false after stop")
	}
	if rec.IsActive() {
		t.Error("expected fake.IsActive()==false after stop")
	}
}

func TestRecordingStatus_ReflectsState(t *testing.T) {
	t.Parallel()
	rec := &fakeSessionRecorder{}
	r := newLinksRouter(nil, rec)

	// Activate and check status.
	r.Dispatch(opCtx(), "recording.start", nil)
	res := r.Dispatch(context.Background(), "recording.status", nil)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if !recordingBool(t, res.Data) {
		t.Error("expected recording=true when active")
	}

	// Deactivate and check status.
	r.Dispatch(opCtx(), "recording.stop", nil)
	res = r.Dispatch(context.Background(), "recording.status", nil)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if recordingBool(t, res.Data) {
		t.Error("expected recording=false after stop")
	}
}
