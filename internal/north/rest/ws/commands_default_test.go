// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// --- system.health ---

type stubHealth struct {
	overall    health.Status
	score      float64
	components []health.Component
}

func (s *stubHealth) Snapshot() []health.Component { return s.components }
func (s *stubHealth) Overall() health.Status       { return s.overall }
func (s *stubHealth) Score() float64               { return s.score }

func TestRegisterDefaultCommandsSystemHealth(t *testing.T) {
	r := NewRouter()
	hp := &stubHealth{
		overall:    health.StatusDegraded,
		score:      0.75,
		components: []health.Component{{Name: "xml", Status: health.StatusHealthy}},
	}
	RegisterDefaultCommands(r, DefaultCommandsConfig{Health: hp})

	res := r.Dispatch(context.Background(), "system.health", nil)
	if res.Error != nil {
		t.Fatalf("dispatch err: %+v", res.Error)
	}
	m := res.Data.(map[string]any)
	if m["overall"] != "degraded" {
		t.Fatalf("overall=%v want degraded", m["overall"])
	}
	if m["score"] != 0.75 {
		t.Fatalf("score=%v want 0.75", m["score"])
	}
}

func TestRegisterDefaultCommandsSystemCommandsLists(t *testing.T) {
	r := NewRouter()
	r.Register("custom.x", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	RegisterDefaultCommands(r, DefaultCommandsConfig{})

	res := r.Dispatch(context.Background(), "system.commands", nil)
	if res.Error != nil {
		t.Fatalf("dispatch err: %+v", res.Error)
	}
	m := res.Data.(map[string]any)
	cmds := m["commands"].([]string)
	hasSelf := false
	hasCustom := false
	for _, c := range cmds {
		if c == "system.commands" {
			hasSelf = true
		}
		if c == "custom.x" {
			hasCustom = true
		}
	}
	if !hasSelf || !hasCustom {
		t.Fatalf("commands list incomplete: %v", cmds)
	}
}

// --- session.* ---

type stubBackend struct {
	openInitial      map[string]any
	openDescriptions map[string]any
	openErr          error
	saved            map[string]any
	saveErr          error
}

//nolint:gocritic // matches the production interface signature exactly
func (b *stubBackend) Open(_ context.Context, _ configui.SessionKey) (map[string]any, map[string]any, error) {
	if b.openErr != nil {
		return nil, nil, b.openErr
	}
	out := make(map[string]any, len(b.openInitial))
	maps.Copy(out, b.openInitial)
	var descs map[string]any
	if b.openDescriptions != nil {
		descs = make(map[string]any, len(b.openDescriptions))
		maps.Copy(descs, b.openDescriptions)
	}
	return descs, out, nil
}

func (b *stubBackend) PutParamset(_ context.Context, _ configui.SessionKey, changes map[string]any) error {
	if b.saveErr != nil {
		return b.saveErr
	}
	b.saved = changes
	return nil
}

func newSessionRouter(t *testing.T, backend *stubBackend) (*Router, *configui.SessionStore) {
	t.Helper()
	store := configui.NewSessionStore()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Sessions: store, SessionBackend: backend})
	return r, store
}

func sessionArgs(channel, ps string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": channel,
		"paramset_key":    ps,
	})
	return b
}

func TestSessionOpenStoresInitialValues(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	r, store := newSessionRouter(t, backend)

	res := r.Dispatch(context.Background(), "config.session.open", sessionArgs("0001ABCD:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("open err: %+v", res.Error)
	}
	if store.Len() != 1 {
		t.Fatalf("store.Len=%d want 1", store.Len())
	}
}

func TestSessionSetThenChangesReportsDirty(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	r, _ := newSessionRouter(t, backend)
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))

	setArgs, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": "addr:1",
		"paramset_key":    "MASTER",
		"parameter":       "BOOST_MODE",
		"value":           true,
	})
	res := r.Dispatch(context.Background(), "config.session.set", setArgs)
	if res.Error != nil {
		t.Fatalf("set err: %+v", res.Error)
	}
	state := res.Data.(map[string]any)
	if state["dirty"] != true || state["can_undo"] != true {
		t.Fatalf("after set: %+v want dirty + can_undo", state)
	}

	res = r.Dispatch(context.Background(), "config.session.changes", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("changes err: %+v", res.Error)
	}
	c := res.Data.(map[string]any)
	if c["dirty"] != true {
		t.Fatal("changes must report dirty")
	}
	delta := c["changes"].(map[string]any)
	if delta["BOOST_MODE"] != true {
		t.Fatalf("delta=%+v", delta)
	}
}

func TestSessionUndoRedo(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	r, _ := newSessionRouter(t, backend)
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))

	setArgs, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": "addr:1",
		"paramset_key":    "MASTER",
		"parameter":       "BOOST_MODE",
		"value":           true,
	})
	r.Dispatch(context.Background(), "config.session.set", setArgs)

	res := r.Dispatch(context.Background(), "config.session.undo", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("undo err: %+v", res.Error)
	}
	if res.Data.(map[string]any)["performed"] != true {
		t.Fatal("undo must report performed")
	}

	res = r.Dispatch(context.Background(), "config.session.redo", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("redo err: %+v", res.Error)
	}
	if res.Data.(map[string]any)["performed"] != true {
		t.Fatal("redo must report performed")
	}
}

func TestSessionSaveCommitsAndDeletes(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	r, store := newSessionRouter(t, backend)
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	setArgs, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": "addr:1",
		"paramset_key":    "MASTER",
		"parameter":       "BOOST_MODE",
		"value":           true,
	})
	r.Dispatch(context.Background(), "config.session.set", setArgs)

	res := r.Dispatch(context.Background(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("save err: %+v", res.Error)
	}
	out := res.Data.(map[string]any)
	if out["saved"] != true {
		t.Fatalf("save out=%+v", out)
	}
	if backend.saved["BOOST_MODE"] != true {
		t.Fatalf("backend not invoked: %+v", backend.saved)
	}
	if store.Len() != 0 {
		t.Fatalf("store should be drained, len=%d", store.Len())
	}
}

func TestSessionSaveWithNoChangesIsNoOp(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	r, store := newSessionRouter(t, backend)
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	res := r.Dispatch(context.Background(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("save err: %+v", res.Error)
	}
	out := res.Data.(map[string]any)
	if out["saved"] != false {
		t.Fatalf("empty save out=%+v", out)
	}
	// Session is *not* dropped on no-op save — caller can keep editing.
	if store.Len() != 1 {
		t.Fatalf("session should remain, len=%d", store.Len())
	}
}

func TestSessionDiscard(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"X": 1}}
	r, store := newSessionRouter(t, backend)
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	res := r.Dispatch(context.Background(), "config.session.discard", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("discard err: %+v", res.Error)
	}
	if store.Len() != 0 {
		t.Fatalf("discard must drop session, len=%d", store.Len())
	}
}

func TestSessionCommandsSurfaceBackendErrors(t *testing.T) {
	backend := &stubBackend{openErr: errors.New("connection lost")}
	r, _ := newSessionRouter(t, backend)
	res := r.Dispatch(context.Background(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	if res.Error == nil {
		t.Fatal("backend Open error must propagate")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Fatalf("code=%q want internal_error", res.Error.Code)
	}
}

// TestSessionOpenIncludesDescriptions verifies that session.open forwards the
// descriptions returned by the backend into the response frame (H-025).
func TestSessionOpenIncludesDescriptions(t *testing.T) {
	backend := &stubBackend{
		openInitial: map[string]any{"TEMPERATURE_OFFSET": 0.0},
		openDescriptions: map[string]any{
			"TEMPERATURE_OFFSET": map[string]any{
				"TYPE": "FLOAT",
				"MIN":  -10.0,
				"MAX":  10.0,
				"UNIT": "°C",
			},
		},
	}
	r, _ := newSessionRouter(t, backend)
	res := r.Dispatch(context.Background(), "config.session.open", sessionArgs("0001ABCD:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("open err: %+v", res.Error)
	}
	m := res.Data.(map[string]any)
	// current_values must be present as before.
	if m["current_values"] == nil {
		t.Fatal("current_values must be present in session.open response")
	}
	// descriptions must be forwarded from the backend (H-025).
	rawDescs, ok := m["descriptions"]
	if !ok {
		t.Fatal("descriptions key must be present in session.open response")
	}
	descs, ok := rawDescs.(map[string]any)
	if !ok {
		t.Fatalf("descriptions must be map[string]any, got %T", rawDescs)
	}
	if _, ok := descs["TEMPERATURE_OFFSET"]; !ok {
		t.Fatalf("descriptions must contain TEMPERATURE_OFFSET; got keys: %v", descs)
	}
}

// TestSessionOpenDescriptionsNilIsForwarded verifies that when the backend
// returns nil descriptions (e.g. lightweight stub or not-yet-implemented
// backend), the response still includes the descriptions key (as null or
// empty map) — the frontend must not panic on a missing key.
func TestSessionOpenDescriptionsNilIsForwarded(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	// openDescriptions is nil → backend returns nil
	r, _ := newSessionRouter(t, backend)
	res := r.Dispatch(context.Background(), "config.session.open", sessionArgs("0001ABCD:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("open err: %+v", res.Error)
	}
	m := res.Data.(map[string]any)
	// The key must be present even when descriptions are nil.
	if _, ok := m["descriptions"]; !ok {
		t.Fatal("descriptions key must always be present in session.open response, even when nil")
	}
}

// --- devices.* / paramset.* ---

type stubDeviceQuery struct {
	devices   []map[string]any
	device    map[string]any
	deviceErr error
	descs     map[string]any
	descsErr  error
	values    map[string]any
	valuesErr error
	listErr   error
}

func (q *stubDeviceQuery) ListDevices(context.Context) ([]map[string]any, error) {
	return q.devices, q.listErr
}

func (q *stubDeviceQuery) GetDevice(_ context.Context, _ string) (map[string]any, error) {
	return q.device, q.deviceErr
}

func (q *stubDeviceQuery) GetParamsetDescription(context.Context, configui.SessionKey) (map[string]any, error) {
	return q.descs, q.descsErr
}

func (q *stubDeviceQuery) GetParamset(context.Context, configui.SessionKey) (map[string]any, error) {
	return q.values, q.valuesErr
}

func TestDevicesListReturnsRegisteredDevices(t *testing.T) {
	q := &stubDeviceQuery{devices: []map[string]any{
		{"address": "0001ABCD", "name": "x"},
		{"address": "0002BCDE", "name": "y"},
	}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Devices: q})
	res := r.Dispatch(context.Background(), "devices.list", nil)
	if res.Error != nil {
		t.Fatalf("err: %+v", res.Error)
	}
	got := res.Data.(map[string]any)["devices"].([]map[string]any)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestDevicesGetReportsNotFoundOnNilDevice(t *testing.T) {
	q := &stubDeviceQuery{device: nil}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Devices: q})
	args, _ := json.Marshal(map[string]any{"address": "0001GHOST"})
	res := r.Dispatch(context.Background(), "devices.get", args)
	if res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("got %+v want not_found", res.Error)
	}
}

func TestDevicesGetRequiresAddress(t *testing.T) {
	q := &stubDeviceQuery{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Devices: q})
	res := r.Dispatch(context.Background(), "devices.get", json.RawMessage(`{}`))
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("got %+v want bad_request", res.Error)
	}
}

func TestParamsetDescriptionAndGet(t *testing.T) {
	q := &stubDeviceQuery{
		descs:  map[string]any{"BOOST_MODE": map[string]any{"TYPE": "BOOL"}},
		values: map[string]any{"BOOST_MODE": false},
	}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Devices: q})

	args, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": "0001ABCD:1",
		"paramset_key":    "MASTER",
	})
	res := r.Dispatch(context.Background(), "paramset.description", args)
	if res.Error != nil {
		t.Fatalf("description err: %+v", res.Error)
	}
	if _, ok := res.Data.(map[string]any)["descriptions"]; !ok {
		t.Fatalf("missing descriptions key in %+v", res.Data)
	}

	res = r.Dispatch(context.Background(), "paramset.get", args)
	if res.Error != nil {
		t.Fatalf("get err: %+v", res.Error)
	}
	got := res.Data.(map[string]any)["values"].(map[string]any)
	if got["BOOST_MODE"] != false {
		t.Fatalf("values=%+v", got)
	}
}

func TestParamsetGetSurfacesBackendError(t *testing.T) {
	q := &stubDeviceQuery{valuesErr: errors.New("connection lost")}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Devices: q})
	args, _ := json.Marshal(map[string]any{
		"central_name":    "test",
		"channel_address": "0001ABCD:1",
		"paramset_key":    "MASTER",
	})
	res := r.Dispatch(context.Background(), "paramset.get", args)
	if res.Error == nil || res.Error.Code != CommandErrorInternal {
		t.Fatalf("got %+v want internal_error", res.Error)
	}
}

// --- programs.* / sysvars.* ---

type stubHub struct {
	programs   []map[string]any
	executedID string
	executeErr error
	sysvars    []map[string]any
	listErr    error
	setName    string
	setValue   any
	setErr     error

	fetchCentral string
	fetchErr     error

	alarmMessages   []map[string]any
	serviceMessages []map[string]any
	ackedAlarmID    string
	ackedServiceID  string
	ackErr          error

	installStatus       map[string]any
	installEnabledID    string
	installDurationSecs int
	installDisabledID   string
	installErr          error

	backupTriggered   bool
	backupStatus      map[string]any
	backupErr         error
	firmwareInfo      map[string]any
	firmwareTriggered bool
	firmwareErr       error
	inboxDevices      []map[string]any
	inboxAccepted     string
	inboxErr          error
}

func (h *stubHub) ListPrograms(context.Context) ([]map[string]any, error) {
	return h.programs, h.listErr
}

func (h *stubHub) ExecuteProgram(_ context.Context, id string) error {
	h.executedID = id
	return h.executeErr
}

func (h *stubHub) ListSysvars(context.Context) ([]map[string]any, error) {
	return h.sysvars, h.listErr
}

func (h *stubHub) SetSysvar(_ context.Context, name string, value any) error {
	h.setName = name
	h.setValue = value
	return h.setErr
}

func (h *stubHub) FetchSystemVariables(_ context.Context, centralName string) error {
	h.fetchCentral = centralName
	return h.fetchErr
}

func (h *stubHub) ListAlarmMessages(context.Context) ([]map[string]any, error) {
	return h.alarmMessages, h.listErr
}

func (h *stubHub) AcknowledgeAlarmMessage(_ context.Context, id string) error {
	h.ackedAlarmID = id
	return h.ackErr
}

func (h *stubHub) ListServiceMessages(context.Context) ([]map[string]any, error) {
	return h.serviceMessages, h.listErr
}

func (h *stubHub) AcknowledgeServiceMessage(_ context.Context, id string) error {
	h.ackedServiceID = id
	return h.ackErr
}

func (h *stubHub) InstallModeStatus(context.Context) (map[string]any, error) {
	return h.installStatus, h.installErr
}

func (h *stubHub) EnableInstallMode(_ context.Context, ifaceID string, durationSecs int) error {
	h.installEnabledID = ifaceID
	h.installDurationSecs = durationSecs
	return h.installErr
}

func (h *stubHub) DisableInstallMode(_ context.Context, ifaceID string) error {
	h.installDisabledID = ifaceID
	return h.installErr
}

func (h *stubHub) TriggerBackup(context.Context) error { h.backupTriggered = true; return h.backupErr }

func (h *stubHub) BackupStatus(context.Context) (map[string]any, error) {
	return h.backupStatus, h.backupErr
}

func (h *stubHub) FirmwareInfo(context.Context) (map[string]any, error) {
	return h.firmwareInfo, h.firmwareErr
}

func (h *stubHub) TriggerFirmwareUpdate(context.Context) error {
	h.firmwareTriggered = true
	return h.firmwareErr
}

func (h *stubHub) InboxDevices(context.Context) ([]map[string]any, error) {
	return h.inboxDevices, h.inboxErr
}

func (h *stubHub) AcceptInboxDevice(_ context.Context, address string) error {
	h.inboxAccepted = address
	return h.inboxErr
}

func TestProgramsListAndExecute(t *testing.T) {
	hub := &stubHub{programs: []map[string]any{
		{"id": "P1", "name": "Wake-Up"},
		{"id": "P2", "name": "Bedtime"},
	}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "programs.list", nil)
	if res.Error != nil {
		t.Fatalf("list err: %+v", res.Error)
	}
	progs := res.Data.(map[string]any)["programs"].([]map[string]any)
	if len(progs) != 2 {
		t.Fatalf("len=%d want 2", len(progs))
	}

	args, _ := json.Marshal(map[string]any{"id": "P1"})
	res = r.Dispatch(context.Background(), "programs.execute", args)
	if res.Error != nil {
		t.Fatalf("execute err: %+v", res.Error)
	}
	if hub.executedID != "P1" {
		t.Fatalf("executed=%q want P1", hub.executedID)
	}
}

func TestProgramsExecuteRequiresID(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: &stubHub{}})
	res := r.Dispatch(context.Background(), "programs.execute", json.RawMessage(`{}`))
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestSysvarsListAndSet(t *testing.T) {
	hub := &stubHub{sysvars: []map[string]any{
		{"name": "PartyMode", "type": "LOGIC", "value": false},
	}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "sysvars.list", nil)
	if res.Error != nil {
		t.Fatalf("list err: %+v", res.Error)
	}
	if len(res.Data.(map[string]any)["sysvars"].([]map[string]any)) != 1 {
		t.Fatalf("sysvars=%+v", res.Data)
	}

	args, _ := json.Marshal(map[string]any{"name": "PartyMode", "value": true})
	res = r.Dispatch(context.Background(), "sysvars.set", args)
	if res.Error != nil {
		t.Fatalf("set err: %+v", res.Error)
	}
	if hub.setName != "PartyMode" || hub.setValue != true {
		t.Fatalf("set=(%q,%v)", hub.setName, hub.setValue)
	}
}

func TestSysvarsSetRequiresName(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: &stubHub{}})
	args, _ := json.Marshal(map[string]any{"value": 42})
	res := r.Dispatch(context.Background(), "sysvars.set", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestAlarmAndServiceMessagesListAndAck(t *testing.T) {
	hub := &stubHub{
		alarmMessages:   []map[string]any{{"id": "A1", "name": "Smoke"}},
		serviceMessages: []map[string]any{{"id": "S1", "name": "Battery low"}},
	}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "alarm_messages.list", nil)
	if res.Error != nil {
		t.Fatalf("alarm list err: %+v", res.Error)
	}
	if len(res.Data.(map[string]any)["messages"].([]map[string]any)) != 1 {
		t.Fatalf("alarms=%+v", res.Data)
	}

	args, _ := json.Marshal(map[string]any{"id": "A1"})
	res = r.Dispatch(context.Background(), "alarm_messages.ack", args)
	if res.Error != nil || hub.ackedAlarmID != "A1" {
		t.Fatalf("ack alarm: err=%+v hub.ackedAlarmID=%q", res.Error, hub.ackedAlarmID)
	}

	res = r.Dispatch(context.Background(), "service_messages.list", nil)
	if res.Error != nil {
		t.Fatalf("service list err: %+v", res.Error)
	}

	args2, _ := json.Marshal(map[string]any{"id": "S1"})
	res = r.Dispatch(context.Background(), "service_messages.ack", args2)
	if res.Error != nil || hub.ackedServiceID != "S1" {
		t.Fatalf("ack service: err=%+v hub.ackedServiceID=%q", res.Error, hub.ackedServiceID)
	}
}

func TestAlarmAckRequiresID(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: &stubHub{}})
	res := r.Dispatch(context.Background(), "alarm_messages.ack", json.RawMessage(`{}`))
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestInstallModeFullCycle(t *testing.T) {
	hub := &stubHub{installStatus: map[string]any{"HmIP-RF": map[string]any{"enabled": false}}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "install_mode.status", nil)
	if res.Error != nil {
		t.Fatalf("status err: %+v", res.Error)
	}

	enableArgs, _ := json.Marshal(map[string]any{"interface_id": "HmIP-RF", "duration_seconds": 60})
	res = r.Dispatch(context.Background(), "install_mode.enable", enableArgs)
	if res.Error != nil {
		t.Fatalf("enable err: %+v", res.Error)
	}
	if hub.installEnabledID != "HmIP-RF" || hub.installDurationSecs != 60 {
		t.Fatalf("enable saw (%q, %d) want (HmIP-RF, 60)", hub.installEnabledID, hub.installDurationSecs)
	}

	disableArgs, _ := json.Marshal(map[string]any{"interface_id": "HmIP-RF"})
	res = r.Dispatch(context.Background(), "install_mode.disable", disableArgs)
	if res.Error != nil {
		t.Fatalf("disable err: %+v", res.Error)
	}
	if hub.installDisabledID != "HmIP-RF" {
		t.Fatalf("disabled=%q want HmIP-RF", hub.installDisabledID)
	}
}

func TestInstallModeEnableValidatesArgs(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: &stubHub{}})

	noIface, _ := json.Marshal(map[string]any{"duration_seconds": 60})
	res := r.Dispatch(context.Background(), "install_mode.enable", noIface)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("missing interface: %+v", res.Error)
	}

	zeroDur, _ := json.Marshal(map[string]any{"interface_id": "HmIP-RF", "duration_seconds": 0})
	res = r.Dispatch(context.Background(), "install_mode.enable", zeroDur)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("zero duration: %+v", res.Error)
	}
}

// --- links.* / schedules.* ---

type stubLinks struct {
	links            []map[string]any
	linkable         []map[string]any
	listErr          error
	addedSender      string
	addedReceiver    string
	addedName        string
	addedDescription string
	removedSender    string
	removedReceiver  string
	addErr           error
	removeErr        error

	// link paramset
	linkParamsetValues map[string]any
	linkParamsetErr    error
	putParamsetAddr    string
	putParamsetPeer    string
	putParamsetValues  map[string]any
	putParamsetErr     error
}

func (l *stubLinks) ListLinks(_ context.Context, _ string) ([]map[string]any, error) {
	return l.links, l.listErr
}

func (l *stubLinks) AddLink(_ context.Context, sender, receiver, name, description string) error {
	l.addedSender = sender
	l.addedReceiver = receiver
	l.addedName = name
	l.addedDescription = description
	return l.addErr
}

func (l *stubLinks) RemoveLink(_ context.Context, sender, receiver string) error {
	l.removedSender = sender
	l.removedReceiver = receiver
	return l.removeErr
}

func (l *stubLinks) LinkableChannels(_ context.Context, _ string) ([]map[string]any, error) {
	return l.linkable, l.listErr
}

func (l *stubLinks) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return l.linkParamsetValues, l.linkParamsetErr
}

func (l *stubLinks) PutLinkParamset(_ context.Context, addr, peer string, values map[string]any) error {
	l.putParamsetAddr = addr
	l.putParamsetPeer = peer
	l.putParamsetValues = values
	return l.putParamsetErr
}

type stubSchedules struct {
	schedule      map[string]any
	getErr        error
	setChannel    string
	setProfile    map[string]any
	setErr        error
	activeChannel string
	activeIndex   int
	activeErr     error

	// Device-level (P0-6) tracking.
	deviceGetCalled    string
	deviceGetErr       error
	deviceSet          string
	deviceSetProfile   map[string]any
	deviceSetErr       error
	deviceActiveDevice string
	deviceActiveID     string
	deviceActiveErr    error

	// Copy tracking.
	copySrcDevice  string
	copyDstDevice  string
	copyErr        error
	copyProfSrcCh  string
	copyProfSrcP   int
	copyProfDstCh  string
	copyProfDstP   int
	copyProfileErr error
}

func (s *stubSchedules) GetClimateSchedule(_ context.Context, _ string) (map[string]any, error) {
	return s.schedule, s.getErr
}

func (s *stubSchedules) SetClimateSchedule(_ context.Context, channelAddress string, profile map[string]any) error {
	s.setChannel = channelAddress
	s.setProfile = profile
	return s.setErr
}

func (s *stubSchedules) SetActiveProfile(_ context.Context, channelAddress string, profileIndex int) error {
	s.activeChannel = channelAddress
	s.activeIndex = profileIndex
	return s.activeErr
}

func (s *stubSchedules) GetDeviceSchedule(_ context.Context, deviceAddress string) (map[string]any, error) {
	s.deviceGetCalled = deviceAddress
	return s.schedule, s.deviceGetErr
}

func (s *stubSchedules) SetDeviceSchedule(_ context.Context, deviceAddress string, profile map[string]any) error {
	s.deviceSet = deviceAddress
	s.deviceSetProfile = profile
	return s.deviceSetErr
}

func (s *stubSchedules) SetDeviceActiveProfile(_ context.Context, deviceAddress, profile string) error {
	s.deviceActiveDevice = deviceAddress
	s.deviceActiveID = profile
	return s.deviceActiveErr
}

func (s *stubSchedules) CopySchedule(_ context.Context, srcDeviceAddress, dstDeviceAddress string) error {
	s.copySrcDevice = srcDeviceAddress
	s.copyDstDevice = dstDeviceAddress
	return s.copyErr
}

func (s *stubSchedules) CopyClimateProfile(_ context.Context, srcChannelAddress string, srcProfile int, dstChannelAddress string, dstProfile int) error {
	s.copyProfSrcCh = srcChannelAddress
	s.copyProfSrcP = srcProfile
	s.copyProfDstCh = dstChannelAddress
	s.copyProfDstP = dstProfile
	return s.copyProfileErr
}

func TestLinksListAddRemove(t *testing.T) {
	links := &stubLinks{links: []map[string]any{{"sender": "S:1", "receiver": "R:1"}}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: links})

	listArgs, _ := json.Marshal(map[string]any{"device_address": "0001ABCD"})
	res := r.Dispatch(context.Background(), "links.list", listArgs)
	if res.Error != nil {
		t.Fatalf("list err: %+v", res.Error)
	}
	if len(res.Data.(map[string]any)["links"].([]map[string]any)) != 1 {
		t.Fatalf("links=%+v", res.Data)
	}

	addArgs, _ := json.Marshal(map[string]any{"sender": "S:1", "receiver": "R:1", "name": "test"})
	res = r.Dispatch(context.Background(), "links.add", addArgs)
	if res.Error != nil {
		t.Fatalf("add err: %+v", res.Error)
	}
	if links.addedSender != "S:1" || links.addedReceiver != "R:1" || links.addedName != "test" {
		t.Fatalf("add stub: %+v", links)
	}

	removeArgs, _ := json.Marshal(map[string]any{"sender": "S:1", "receiver": "R:1"})
	res = r.Dispatch(context.Background(), "links.remove", removeArgs)
	if res.Error != nil {
		t.Fatalf("remove err: %+v", res.Error)
	}
}

func TestLinksAddRequiresSenderAndReceiver(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: &stubLinks{}})
	args, _ := json.Marshal(map[string]any{"sender": "S:1"})
	res := r.Dispatch(context.Background(), "links.add", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestLinkableChannels(t *testing.T) {
	links := &stubLinks{linkable: []map[string]any{{"channel": "C:1"}, {"channel": "C:2"}}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: links})
	args, _ := json.Marshal(map[string]any{"device_address": "0001ABCD"})
	res := r.Dispatch(context.Background(), "links.linkable_channels", args)
	if res.Error != nil {
		t.Fatalf("err: %+v", res.Error)
	}
	if len(res.Data.(map[string]any)["channels"].([]map[string]any)) != 2 {
		t.Fatalf("channels=%+v", res.Data)
	}
}

func TestWSLinksGetParamset(t *testing.T) {
	t.Parallel()
	links := &stubLinks{linkParamsetValues: map[string]any{"SHORT_ACTION_TYPE": int32(0)}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: links})

	args, _ := json.Marshal(map[string]any{"address": "0001ABCD:1", "peer_address": "0002EFGH:1"})
	res := r.Dispatch(context.Background(), "links.get_paramset", args)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if _, ok := data["values"]; !ok {
		t.Fatal("expected 'values' key in result")
	}
}

func TestWSLinksGetParamset_MissingAddress(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: &stubLinks{}})

	args, _ := json.Marshal(map[string]any{"address": "0001ABCD:1"}) // missing peer_address
	res := r.Dispatch(context.Background(), "links.get_paramset", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestWSLinksPutParamset(t *testing.T) {
	t.Parallel()
	links := &stubLinks{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: links})

	params := map[string]any{"SHORT_ACTION_TYPE": float64(1)}
	args, _ := json.Marshal(map[string]any{
		"address":      "0001ABCD:1",
		"peer_address": "0002EFGH:1",
		"parameters":   params,
	})
	res := r.Dispatch(context.Background(), "links.put_paramset", args)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["success"] != true {
		t.Fatalf("expected success=true, got %+v", res.Data)
	}
	if links.putParamsetAddr != "0001ABCD:1" {
		t.Fatalf("addr: got %q want %q", links.putParamsetAddr, "0001ABCD:1")
	}
	if links.putParamsetPeer != "0002EFGH:1" {
		t.Fatalf("peer: got %q want %q", links.putParamsetPeer, "0002EFGH:1")
	}
}

func TestWSLinksPutParamset_MissingPeer(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Links: &stubLinks{}})

	args, _ := json.Marshal(map[string]any{"address": "0001ABCD:1"}) // missing peer_address
	res := r.Dispatch(context.Background(), "links.put_paramset", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestSchedulesClimateGetSet(t *testing.T) {
	sched := &stubSchedules{schedule: map[string]any{"P1": map[string]any{}}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: sched})

	getArgs, _ := json.Marshal(map[string]any{"channel_address": "0001ABCD:1"})
	res := r.Dispatch(context.Background(), "schedules.climate.get", getArgs)
	if res.Error != nil {
		t.Fatalf("get err: %+v", res.Error)
	}
	if _, ok := res.Data.(map[string]any)["schedule"]; !ok {
		t.Fatalf("missing schedule key")
	}

	setArgs, _ := json.Marshal(map[string]any{
		"channel_address": "0001ABCD:1",
		"profile":         map[string]any{"P1": "data"},
	})
	res = r.Dispatch(context.Background(), "schedules.climate.set", setArgs)
	if res.Error != nil {
		t.Fatalf("set err: %+v", res.Error)
	}
	if sched.setChannel != "0001ABCD:1" || sched.setProfile["P1"] != "data" {
		t.Fatalf("set stub: %+v", sched)
	}
}

// P0-6: Generic device-level schedules.

func TestSchedulesDeviceGetSet(t *testing.T) {
	sched := &stubSchedules{schedule: map[string]any{"kind": "simple"}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: sched})

	getArgs, _ := json.Marshal(map[string]any{"device_address": "0001ABCD"})
	res := r.Dispatch(context.Background(), "schedules.device.get", getArgs)
	if res.Error != nil {
		t.Fatalf("get err: %+v", res.Error)
	}
	if sched.deviceGetCalled != "0001ABCD" {
		t.Fatalf("device get not invoked: %+v", sched.deviceGetCalled)
	}
	if _, ok := res.Data.(map[string]any)["schedule"]; !ok {
		t.Fatalf("missing schedule key")
	}

	setArgs, _ := json.Marshal(map[string]any{
		"device_address": "0001ABCD",
		"profile":        map[string]any{"kind": "simple", "x": 1},
	})
	res = r.Dispatch(context.Background(), "schedules.device.set", setArgs)
	if res.Error != nil {
		t.Fatalf("set err: %+v", res.Error)
	}
	if sched.deviceSet != "0001ABCD" || sched.deviceSetProfile["kind"] != "simple" {
		t.Fatalf("device set stub: %+v", sched)
	}
}

func TestSchedulesDeviceGetRejectsMissingAddress(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})
	args, _ := json.Marshal(map[string]any{})
	res := r.Dispatch(context.Background(), "schedules.device.get", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestSchedulesDeviceSetRejectsMissingProfile(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})
	args, _ := json.Marshal(map[string]any{"device_address": "X"})
	res := r.Dispatch(context.Background(), "schedules.device.set", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestSchedulesDeviceActiveProfileSet(t *testing.T) {
	sched := &stubSchedules{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: sched})

	args, _ := json.Marshal(map[string]any{"device_address": "0001ABCD", "profile": "P2"})
	res := r.Dispatch(context.Background(), "schedules.device.active_profile.set", args)
	if res.Error != nil {
		t.Fatalf("err: %+v", res.Error)
	}
	if sched.deviceActiveDevice != "0001ABCD" || sched.deviceActiveID != "P2" {
		t.Fatalf("active profile stub: %+v", sched)
	}
}

func TestSchedulesDeviceActiveProfileRejectsEmptyProfile(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})
	args, _ := json.Marshal(map[string]any{"device_address": "X"})
	res := r.Dispatch(context.Background(), "schedules.device.active_profile.set", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

func TestBackupTriggerAndStatus(t *testing.T) {
	hub := &stubHub{backupStatus: map[string]any{"status": "running"}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "backup.trigger", nil)
	if res.Error != nil {
		t.Fatalf("trigger err: %+v", res.Error)
	}
	if !hub.backupTriggered {
		t.Fatal("backup not triggered on stub")
	}

	res = r.Dispatch(context.Background(), "backup.status", nil)
	if res.Error != nil {
		t.Fatalf("status err: %+v", res.Error)
	}
	if res.Data.(map[string]any)["status"] != "running" {
		t.Fatalf("status data=%+v", res.Data)
	}
}

func TestFirmwareInfoAndUpdate(t *testing.T) {
	hub := &stubHub{firmwareInfo: map[string]any{"current": "1.0", "available": "1.1"}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "firmware.info", nil)
	if res.Error != nil {
		t.Fatalf("info err: %+v", res.Error)
	}
	if res.Data.(map[string]any)["current"] != "1.0" {
		t.Fatalf("info=%+v", res.Data)
	}

	res = r.Dispatch(context.Background(), "firmware.update", nil)
	if res.Error != nil {
		t.Fatalf("update err: %+v", res.Error)
	}
	if !hub.firmwareTriggered {
		t.Fatal("update not triggered on stub")
	}
}

func TestInboxListAndAccept(t *testing.T) {
	hub := &stubHub{inboxDevices: []map[string]any{
		{"address": "0009ABCD", "type": "HmIP-STH"},
	}}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "inbox.list", nil)
	if res.Error != nil {
		t.Fatalf("list err: %+v", res.Error)
	}
	if len(res.Data.(map[string]any)["devices"].([]map[string]any)) != 1 {
		t.Fatalf("inbox=%+v", res.Data)
	}

	args, _ := json.Marshal(map[string]any{"device_address": "0009ABCD"})
	res = r.Dispatch(context.Background(), "inbox.accept", args)
	if res.Error != nil {
		t.Fatalf("accept err: %+v", res.Error)
	}
	if hub.inboxAccepted != "0009ABCD" {
		t.Fatalf("accepted=%q want 0009ABCD", hub.inboxAccepted)
	}
}

func TestInboxAcceptRequiresAddress(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: &stubHub{}})
	res := r.Dispatch(context.Background(), "inbox.accept", json.RawMessage(`{}`))
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("missing address: %+v", res.Error)
	}
}

func TestSchedulesActiveProfileBoundsCheck(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})
	for _, idx := range []int{0, 7, -1, 99} {
		args, _ := json.Marshal(map[string]any{"channel_address": "0001ABCD:1", "profile_index": idx})
		res := r.Dispatch(context.Background(), "schedules.active_profile.set", args)
		if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
			t.Fatalf("idx=%d: got %+v want bad_request", idx, res.Error)
		}
	}
	// In-bounds index passes.
	args, _ := json.Marshal(map[string]any{"channel_address": "0001ABCD:1", "profile_index": 2})
	res := r.Dispatch(context.Background(), "schedules.active_profile.set", args)
	if res.Error != nil {
		t.Fatalf("valid idx: %+v", res.Error)
	}
}

func TestSessionMutationsRequireOpenSession(t *testing.T) {
	r, _ := newSessionRouter(t, &stubBackend{})
	for _, cmd := range []string{
		"config.session.set",
		"config.session.undo",
		"config.session.redo",
		"config.session.discard",
		"config.session.changes",
		"config.session.save",
	} {
		res := r.Dispatch(context.Background(), cmd, sessionArgs("ghost:1", "MASTER"))
		if res.Error == nil {
			t.Fatalf("%s without open must error", cmd)
		}
	}
}

// TestSessionSaveRecordsToChangeLog verifies L-A6-01: a successful
// config.session.save appends a ChangeEntry to the wired ChangeLog with the
// correct before/after diff.
func TestSessionSaveRecordsToChangeLog(t *testing.T) {
	t.Parallel()

	backend := &stubBackend{openInitial: map[string]any{"BOOST_MODE": false}}
	store := configui.NewSessionStore()
	cl := audit.NewChangeLog()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{
		Sessions:       store,
		SessionBackend: backend,
		ChangeLog:      cl,
	})

	// Open a session then change a parameter.
	r.Dispatch(context.Background(), "config.session.open", sessionArgs("ch:1", "MASTER"))
	setArgs, _ := json.Marshal(map[string]any{
		"central_name": "test", "channel_address": "ch:1",
		"paramset_key": "MASTER", "parameter": "BOOST_MODE", "value": true,
	})
	r.Dispatch(context.Background(), "config.session.set", setArgs)

	res := r.Dispatch(context.Background(), "config.session.save", sessionArgs("ch:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("save err: %+v", res.Error)
	}

	// Verify the changelog was populated.
	sessionID := "test/ch:1/MASTER"
	entries, total, found := cl.GetEntries(sessionID, "", 10)
	if !found {
		t.Fatal("ChangeLog has no sessions after save")
	}
	if total != 1 {
		t.Fatalf("expected 1 change entry, got %d", total)
	}
	entry := entries[0]
	if entry.ChannelAddress != "ch:1" {
		t.Fatalf("entry.ChannelAddress=%q want ch:1", entry.ChannelAddress)
	}
	if entry.ParamsetKey != "MASTER" {
		t.Fatalf("entry.ParamsetKey=%q want MASTER", entry.ParamsetKey)
	}
	ch, ok := entry.Changes["BOOST_MODE"]
	if !ok {
		t.Fatal("entry.Changes must contain BOOST_MODE")
	}
	if ch.Old != false {
		t.Fatalf("change.Old=%v want false", ch.Old)
	}
	if ch.New != true {
		t.Fatalf("change.New=%v want true", ch.New)
	}
}

// ---------------------------------------------------------------------------
// schedules.copy
// ---------------------------------------------------------------------------

// TestSchedulesCopy_HappyPath verifies that schedules.copy dispatches both
// device addresses to the stub's CopySchedule method and reports success.
func TestSchedulesCopy_HappyPath(t *testing.T) {
	t.Parallel()
	sched := &stubSchedules{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: sched})

	args, _ := json.Marshal(map[string]any{
		"source_device_address": "DEV1",
		"target_device_address": "DEV2",
	})
	res := r.Dispatch(context.Background(), "schedules.copy", args)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if sched.copySrcDevice != "DEV1" {
		t.Errorf("copySrcDevice=%q want DEV1", sched.copySrcDevice)
	}
	if sched.copyDstDevice != "DEV2" {
		t.Errorf("copyDstDevice=%q want DEV2", sched.copyDstDevice)
	}
	if res.Data.(map[string]any)["copied"] != true {
		t.Errorf("result does not carry copied=true: %+v", res.Data)
	}
}

// TestSchedulesCopy_MissingTarget verifies that an absent target_device_address
// yields a bad_request error.
func TestSchedulesCopy_MissingTarget(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})

	args, _ := json.Marshal(map[string]any{"source_device_address": "DEV1"})
	res := r.Dispatch(context.Background(), "schedules.copy", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %+v", res.Error)
	}
}

// ---------------------------------------------------------------------------
// schedules.climate.copy_profile
// ---------------------------------------------------------------------------

// TestSchedulesClimateCopyProfile_HappyPath verifies that the handler
// forwards all four arguments to the stub unchanged.
func TestSchedulesClimateCopyProfile_HappyPath(t *testing.T) {
	t.Parallel()
	sched := &stubSchedules{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: sched})

	args, _ := json.Marshal(map[string]any{
		"source_channel_address": "A:1",
		"source_profile":         1,
		"target_channel_address": "B:2",
		"target_profile":         2,
	})
	res := r.Dispatch(context.Background(), "schedules.climate.copy_profile", args)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if sched.copyProfSrcCh != "A:1" {
		t.Errorf("copyProfSrcCh=%q want A:1", sched.copyProfSrcCh)
	}
	if sched.copyProfDstP != 2 {
		t.Errorf("copyProfDstP=%d want 2", sched.copyProfDstP)
	}
	if res.Data.(map[string]any)["copied"] != true {
		t.Errorf("result does not carry copied=true: %+v", res.Data)
	}
}

// TestSchedulesClimateCopyProfile_InvalidProfile verifies that a source_profile
// of 0 (below the 1..6 range) yields a bad_request error.
func TestSchedulesClimateCopyProfile_InvalidProfile(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Schedules: &stubSchedules{}})

	args, _ := json.Marshal(map[string]any{
		"source_channel_address": "A:1",
		"source_profile":         0, // out of range
		"target_channel_address": "B:2",
		"target_profile":         2,
	})
	res := r.Dispatch(context.Background(), "schedules.climate.copy_profile", args)
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request for profile 0, got %+v", res.Error)
	}
}

// ---------------------------------------------------------------------------
// sysvars.fetch
// ---------------------------------------------------------------------------

// TestSysvarsFetch_WithCentralName verifies that the central_name arg is
// forwarded to the stub's FetchSystemVariables method.
func TestSysvarsFetch_WithCentralName(t *testing.T) {
	t.Parallel()
	hub := &stubHub{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	args, _ := json.Marshal(map[string]any{"central_name": "ccu-01"})
	res := r.Dispatch(context.Background(), "sysvars.fetch", args)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if hub.fetchCentral != "ccu-01" {
		t.Errorf("fetchCentral=%q want ccu-01", hub.fetchCentral)
	}
	if res.Data.(map[string]any)["fetched"] != true {
		t.Errorf("result does not carry fetched=true: %+v", res.Data)
	}
}

// TestSysvarsFetch_EmptyBody verifies that an empty body is valid (refreshes
// all centrals) and that the stub receives an empty central_name.
func TestSysvarsFetch_EmptyBody(t *testing.T) {
	t.Parallel()
	hub := &stubHub{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{Hub: hub})

	res := r.Dispatch(context.Background(), "sysvars.fetch", nil)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if hub.fetchCentral != "" {
		t.Errorf("fetchCentral=%q want empty (refresh-all)", hub.fetchCentral)
	}
}
