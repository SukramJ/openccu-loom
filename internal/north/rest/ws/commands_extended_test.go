// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

type stubDevices struct {
	renamed         map[string]string
	renamedChannels map[string]string
	lastInclude     bool
	installModes    map[string]int
	failOnAddress   string
}

func (s *stubDevices) Rename(_ context.Context, address, name string, includeChannels bool) error {
	if address == s.failOnAddress {
		return errors.New("device offline")
	}
	if s.renamed == nil {
		s.renamed = map[string]string{}
	}
	s.renamed[address] = name
	s.lastInclude = includeChannels
	return nil
}

func (s *stubDevices) RenameChannel(_ context.Context, deviceAddr string, channelNo int, name string) error {
	if deviceAddr == s.failOnAddress {
		return errors.New("device offline")
	}
	if s.renamedChannels == nil {
		s.renamedChannels = map[string]string{}
	}
	s.renamedChannels[deviceAddr+":"+strconv.Itoa(channelNo)] = name
	return nil
}

func (s *stubDevices) SetInstallMode(_ context.Context, address string, dur int) error {
	if s.installModes == nil {
		s.installModes = map[string]int{}
	}
	s.installModes[address] = dur
	return nil
}

type stubParamsetWriter struct {
	calls        []paramsetCall
	hiddenParams map[string]struct{} // parameters that return ErrParameterHidden
}

type paramsetCall struct {
	key    configui.SessionKey
	values map[string]any
}

func (s *stubParamsetWriter) PutParamset(_ context.Context, key configui.SessionKey, values map[string]any) error {
	for name := range values {
		if _, hidden := s.hiddenParams[name]; hidden {
			return fmt.Errorf("parameter %q: %w", name, hmerr.ErrParameterHidden)
		}
	}
	s.calls = append(s.calls, paramsetCall{key: key, values: values})
	return nil
}

type stubChangeHistory struct {
	entries  []map[string]any
	forceErr error
}

func (s *stubChangeHistory) List(_ context.Context, _ int, _ string) ([]map[string]any, error) {
	if s.forceErr != nil {
		return nil, s.forceErr
	}
	return s.entries, nil
}

type stubCentral struct {
	reconciled      int
	connectivityErr error
	systemHealthErr error
	reconcileErr    error
}

func (s *stubCentral) Info(_ context.Context) (map[string]any, error) {
	return map[string]any{"name": "ccu1", "version": "v1.0.0"}, nil
}

func (s *stubCentral) Connectivity(_ context.Context) ([]map[string]any, error) {
	if s.connectivityErr != nil {
		return nil, s.connectivityErr
	}
	return []map[string]any{{"interface_id": "HmIP-RF", "reachable": true}}, nil
}

func (s *stubCentral) SystemHealth(_ context.Context) (map[string]any, error) {
	if s.systemHealthErr != nil {
		return nil, s.systemHealthErr
	}
	return map[string]any{"score": 95}, nil
}

func (s *stubCentral) Reconcile(_ context.Context) error {
	if s.reconcileErr != nil {
		return s.reconcileErr
	}
	s.reconciled++
	return nil
}

type stubExtendedHub struct {
	disabled     []string
	unsuppressed []string
	suppressed   []map[string]any

	listErr       error
	unsuppressErr error
}

func (s *stubExtendedHub) DisableServiceMessage(_ context.Context, id string) error {
	s.disabled = append(s.disabled, id)
	return nil
}

func (s *stubExtendedHub) ListSuppressedServiceMessages(_ context.Context) ([]map[string]any, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.suppressed, nil
}

func (s *stubExtendedHub) UnsuppressServiceMessage(_ context.Context, _, channel, _ string) error {
	if s.unsuppressErr != nil {
		return s.unsuppressErr
	}
	s.unsuppressed = append(s.unsuppressed, channel)
	return nil
}

//nolint:gocritic // test rig helper — every dependency surfaces as its own positional return so callers can wire mocks individually
func newRouterWithExtended() (*Router, *stubDevices, *stubParamsetWriter, *stubChangeHistory, *stubCentral, *stubExtendedHub) {
	r := NewRouter()
	devs := &stubDevices{}
	pw := &stubParamsetWriter{}
	hist := &stubChangeHistory{entries: []map[string]any{{"id": 1, "param": "TEMPERATURE"}}}
	central := &stubCentral{}
	ehub := &stubExtendedHub{}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Devices:        devs,
		Paramsets:      pw,
		ChangeHistory:  hist,
		Central:        central,
		ExtendedHub:    ehub,
		MasterProfiles: masterprofile.New(),
	})
	return r, devs, pw, hist, central, ehub
}

func dispatch(t *testing.T, r *Router, name string, params any) any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	res := r.Dispatch(ctxForCommand(name), name, raw)
	if res.Error != nil {
		t.Fatalf("%s: dispatch err: %v", name, res.Error.Message)
	}
	return res.Data
}

func dispatchExpectErr(t *testing.T, r *Router, name string, params any, contains string) {
	t.Helper()
	raw, _ := json.Marshal(params)
	res := r.Dispatch(ctxForCommand(name), name, raw)
	if res.Error == nil {
		t.Fatalf("%s: expected error", name)
	}
	if contains != "" && !strings.Contains(res.Error.Message, contains) {
		t.Fatalf("%s: error %q does not contain %q", name, res.Error.Message, contains)
	}
}

func TestExtendedDeviceRenameHandler(t *testing.T) {
	r, devs, _, _, _, _ := newRouterWithExtended()
	dispatch(t, r, "device.rename", map[string]any{"address": "ABC0001", "name": "Wohnzimmer", "include_channels": true})
	if devs.renamed["ABC0001"] != "Wohnzimmer" {
		t.Fatalf("rename not applied: %v", devs.renamed)
	}
	if !devs.lastInclude {
		t.Fatal("include_channels flag not forwarded")
	}
	dispatchExpectErr(t, r, "device.rename", map[string]any{"address": "", "name": "X"}, "address is required")
}

func TestExtendedDeviceRenameChannelHandler(t *testing.T) {
	r, devs, _, _, _, _ := newRouterWithExtended()
	dispatch(t, r, "device.rename_channel", map[string]any{"address": "ABC0001", "channel": 2, "name": "Licht"})
	if devs.renamedChannels["ABC0001:2"] != "Licht" {
		t.Fatalf("channel rename not applied: %v", devs.renamedChannels)
	}
	dispatchExpectErr(t, r, "device.rename_channel", map[string]any{"address": "", "channel": 1, "name": "X"}, "address is required")
	dispatchExpectErr(t, r, "device.rename_channel", map[string]any{"address": "ABC0001", "channel": 1, "name": ""}, "name is required")
}

func TestExtendedDeviceInstallMode(t *testing.T) {
	r, devs, _, _, _, _ := newRouterWithExtended()
	dispatch(t, r, "device.install_mode", map[string]any{"address": "ABC0001", "duration_seconds": 90})
	if devs.installModes["ABC0001"] != 90 {
		t.Fatalf("install_mode duration not stored: %v", devs.installModes)
	}
	// default duration
	dispatch(t, r, "device.install_mode", map[string]any{"address": "DEF0002"})
	if devs.installModes["DEF0002"] != 60 {
		t.Fatalf("default duration should be 60, got %d", devs.installModes["DEF0002"])
	}
}

func TestExtendedParamsetPut(t *testing.T) {
	r, _, pw, _, _, _ := newRouterWithExtended()
	dispatch(t, r, "paramset.put", map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "VALUES",
		"values":          map[string]any{"STATE": true},
	})
	if len(pw.calls) != 1 {
		t.Fatalf("expected 1 paramset write, got %d", len(pw.calls))
	}
	if pw.calls[0].values["STATE"] != true {
		t.Fatalf("paramset values not propagated")
	}
	dispatchExpectErr(t, r, "paramset.put", map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "VALUES",
		"values":          map[string]any{},
	}, "values must not be empty")
}

func TestExtendedMasterProfilesListAndGet(t *testing.T) {
	r, _, _, _, _, _ := newRouterWithExtended()
	out := dispatch(t, r, "master_profiles.list", map[string]any{}).(map[string]any)
	if dts, ok := out["device_types"].([]string); !ok || len(dts) == 0 {
		t.Fatalf("master_profiles.list (no args) should return device_types, got %v", out)
	}
	out = dispatch(t, r, "master_profiles.list", map[string]any{
		"device_type": "BLIND",
		"locale":      "de",
	}).(map[string]any)
	profiles := out["profiles"].([]map[string]any)
	if len(profiles) == 0 {
		t.Fatalf("BLIND should have profiles")
	}
	getOut := dispatch(t, r, "master_profiles.get", map[string]any{
		"device_type": "BLIND",
		"id":          0,
	}).(masterprofile.Profile)
	if getOut.ID != 0 {
		t.Fatalf("expected id=0, got %d", getOut.ID)
	}
}

func TestExtendedMasterProfilesApply(t *testing.T) {
	r, _, pw, _, _, _ := newRouterWithExtended()
	dispatch(t, r, "master_profiles.apply", map[string]any{
		"device_type":     "BLIND",
		"channel_address": "ABC0001:1",
		"id":              1,
	})
	if len(pw.calls) != 1 {
		t.Fatalf("apply should issue PutParamset")
	}
	if pw.calls[0].key.ChannelAddress != "ABC0001:1" {
		t.Fatalf("wrong channel: %s", pw.calls[0].key.ChannelAddress)
	}
	if string(pw.calls[0].key.ParamsetKey) != "MASTER" {
		t.Fatalf("expected MASTER paramset, got %s", pw.calls[0].key.ParamsetKey)
	}
	if len(pw.calls[0].values) == 0 {
		t.Fatalf("expected master profile params to be applied")
	}
}

func TestExtendedChangeHistoryList(t *testing.T) {
	r, _, _, _, _, _ := newRouterWithExtended()
	out := dispatch(t, r, "change_history.list", map[string]any{"limit": 50}).(map[string]any)
	if len(out["entries"].([]map[string]any)) != 1 {
		t.Fatalf("expected 1 entry, got %v", out)
	}
}

func TestExtendedCentralCommands(t *testing.T) {
	r, _, _, _, central, _ := newRouterWithExtended()
	if got := dispatch(t, r, "central.info", nil).(map[string]any); got["name"] != "ccu1" {
		t.Fatalf("central.info: %v", got)
	}
	got := dispatch(t, r, "central.connectivity", nil).(map[string]any)
	if len(got["interfaces"].([]map[string]any)) != 1 {
		t.Fatalf("connectivity payload: %v", got)
	}
	dispatch(t, r, "central.reconcile", nil)
	if central.reconciled != 1 {
		t.Fatalf("reconcile should fire once, got %d", central.reconciled)
	}
}

func TestExtendedServiceMessagesDisable(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()
	dispatch(t, r, "service_messages.disable", map[string]any{"id": "MSG-7"})
	if len(ehub.disabled) != 1 || ehub.disabled[0] != "MSG-7" {
		t.Fatalf("disable not recorded: %v", ehub.disabled)
	}
}

// TestExtendedServiceMessagesSuppressed verifies the `service_messages.suppressed`
// command wraps the domain listing in an {"items": [...]} envelope, and
// substitutes an empty array (never `null`) when nothing is suppressed.
func TestExtendedServiceMessagesSuppressed(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()
	ehub.suppressed = []map[string]any{{"channel": "ABC:1", "parameter": "LOWBAT"}}

	out := dispatch(t, r, "service_messages.suppressed", map[string]any{}).(map[string]any)
	items, ok := out["items"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["channel"] != "ABC:1" {
		t.Fatalf("service_messages.suppressed payload = %v", out)
	}
}

func TestExtendedServiceMessagesSuppressedEmpty(t *testing.T) {
	r, _, _, _, _, _ := newRouterWithExtended()

	out := dispatch(t, r, "service_messages.suppressed", map[string]any{}).(map[string]any)
	items, ok := out["items"].([]map[string]any)
	if !ok || items == nil || len(items) != 0 {
		t.Fatalf("expected a non-nil empty items array, got %v", out)
	}
}

func TestExtendedServiceMessagesSuppressedHandlerError(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()
	ehub.listErr = errors.New("rega timeout")

	dispatchExpectErr(t, r, "service_messages.suppressed", map[string]any{}, "rega timeout")
}

// TestExtendedServiceMessagesUnsuppress verifies the `service_messages.unsuppress`
// command forwards the channel/parameter/interface to the domain layer and
// returns an acknowledgement payload naming the cleared channel.
func TestExtendedServiceMessagesUnsuppress(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()

	out := dispatch(t, r, "service_messages.unsuppress", map[string]any{
		"interface": "HmIP-RF", "channel": "ABC:1", "parameter": "LOWBAT",
	}).(map[string]any)
	if out["unsuppressed"] != "ABC:1" {
		t.Fatalf("unsuppress payload = %v", out)
	}
	if len(ehub.unsuppressed) != 1 || ehub.unsuppressed[0] != "ABC:1" {
		t.Fatalf("unsuppress not recorded: %v", ehub.unsuppressed)
	}
}

// TestExtendedServiceMessagesUnsuppressMissingChannel asserts an empty
// channel is rejected before reaching the domain layer.
func TestExtendedServiceMessagesUnsuppressMissingChannel(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()

	dispatchExpectErr(t, r, "service_messages.unsuppress", map[string]any{
		"parameter": "LOWBAT",
	}, "channel is required")
	if len(ehub.unsuppressed) != 0 {
		t.Fatalf("domain layer must not be called on validation failure, got %v", ehub.unsuppressed)
	}
}

func TestExtendedServiceMessagesUnsuppressHandlerError(t *testing.T) {
	r, _, _, _, _, ehub := newRouterWithExtended()
	ehub.unsuppressErr = errors.New("ccu unreachable")

	dispatchExpectErr(t, r, "service_messages.unsuppress", map[string]any{
		"channel": "ABC:1",
	}, "ccu unreachable")
}

// TestExtendedParamsetPutHiddenParamReturnsForbidenError asserts that
// when the ParamsetWriter rejects a write with ErrParameterHidden, the
// WS Dispatch propagates a structured error with code "forbidden".
func TestExtendedParamsetPutHiddenParamReturnsForbidenError(t *testing.T) {
	r := NewRouter()
	pw := &stubParamsetWriter{
		hiddenParams: map[string]struct{}{
			"PARTY_MODE_SUBMIT": {},
		},
	}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Paramsets: pw})

	raw, _ := json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "VALUES",
		"values":          map[string]any{"PARTY_MODE_SUBMIT": "submit"},
	})
	res := r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error == nil {
		t.Fatal("expected error for hidden parameter, got nil")
	}
	if res.Error.Code != CommandErrorForbidden {
		t.Fatalf("expected error code %q, got %q (message: %s)",
			CommandErrorForbidden, res.Error.Code, res.Error.Message)
	}
	if !strings.Contains(res.Error.Message, "hidden") {
		t.Errorf("error message should mention 'hidden', got: %q", res.Error.Message)
	}
	// The write must NOT have been recorded.
	if len(pw.calls) != 0 {
		t.Fatalf("hidden param write must not be forwarded; got %d calls", len(pw.calls))
	}
}

// TestExtendedParamsetPutVisibleParamSucceeds guards that a normal
// write still works when the writer has a hidden-param list but the
// parameter written is not in it.
func TestExtendedParamsetPutVisibleParamSucceeds(t *testing.T) {
	r := NewRouter()
	pw := &stubParamsetWriter{
		hiddenParams: map[string]struct{}{
			"PARTY_MODE_SUBMIT": {},
		},
	}
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Paramsets: pw})

	raw, _ := json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "VALUES",
		"values":          map[string]any{"STATE": true},
	})
	res := r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error != nil {
		t.Fatalf("expected success for visible param, got error: %v", res.Error)
	}
	if len(pw.calls) != 1 {
		t.Fatalf("expected 1 paramset write recorded, got %d", len(pw.calls))
	}
}

func TestExtendedRouterCountsNewCommands(t *testing.T) {
	r, _, _, _, _, _ := newRouterWithExtended()
	names := r.Commands()
	want := []string{
		"device.rename", "device.rename_channel", "device.install_mode",
		"paramset.put",
		"master_profiles.list", "master_profiles.get", "master_profiles.apply",
		"master_profiles.match",
		"change_history.list",
		"central.info", "central.connectivity", "central.system_health", "central.reconcile",
		"service_messages.disable",
	}
	if len(want) != 14 {
		t.Fatalf("expected 14 new commands, got %d", len(want))
	}
	registered := make(map[string]bool, len(names))
	for _, n := range names {
		registered[n] = true
	}
	for _, w := range want {
		if !registered[w] {
			t.Fatalf("command %q not registered", w)
		}
	}
}

// stubUISchema implements [UISchemaQuery] for tests.
type stubUISchema struct {
	schema       map[string]any
	err          error
	lastAddress  string
	lastChannel  int
	lastParamset string
}

func (s *stubUISchema) FormSchema(_ context.Context, address string, channelNo int, paramset, _, _ string) (map[string]any, error) {
	s.lastAddress = address
	s.lastChannel = channelNo
	s.lastParamset = paramset
	if s.err != nil {
		return nil, s.err
	}
	if s.schema != nil {
		return s.schema, nil
	}
	return map[string]any{"groups": []any{}, "parameters": []any{}}, nil
}

// TestWSParamsetFormSchema verifies that `paramset.form_schema` forwards
// address/channel_no/paramset to UISchemaQuery.FormSchema and returns its
// result. Mirrors Python `ws_get_form_schema` (websocket_api.py:252).
func TestWSParamsetFormSchema(t *testing.T) {
	t.Parallel()
	uiSchema := &stubUISchema{schema: map[string]any{"groups": []any{}, "parameters": []any{}}}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{UISchema: uiSchema})

	raw, _ := json.Marshal(map[string]any{
		"address":    "0001ABCD:1",
		"channel_no": 1,
		"paramset":   "MASTER",
	})
	res := r.Dispatch(context.Background(), "paramset.form_schema", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if uiSchema.lastAddress != "0001ABCD:1" {
		t.Errorf("address: got %q want %q", uiSchema.lastAddress, "0001ABCD:1")
	}
	if uiSchema.lastParamset != "MASTER" {
		t.Errorf("paramset: got %q want %q", uiSchema.lastParamset, "MASTER")
	}
	result, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", res.Data)
	}
	if _, ok := result["parameters"]; !ok {
		t.Error("expected 'parameters' key in result")
	}
}

func TestWSParamsetFormSchema_DefaultsParamset(t *testing.T) {
	t.Parallel()
	uiSchema := &stubUISchema{}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{UISchema: uiSchema})

	raw, _ := json.Marshal(map[string]any{"address": "0001ABCD:1", "channel_no": 1})
	res := r.Dispatch(context.Background(), "paramset.form_schema", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	if uiSchema.lastParamset != "VALUES" {
		t.Errorf("default paramset: got %q want VALUES", uiSchema.lastParamset)
	}
}

func TestWSParamsetFormSchema_MissingAddressReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{UISchema: &stubUISchema{}})

	raw, _ := json.Marshal(map[string]any{"channel_no": 1, "paramset": "MASTER"})
	res := r.Dispatch(context.Background(), "paramset.form_schema", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing address")
	}
}

// TestExtendedMasterProfilesMatchDispatch exercises the master_profiles.match
// handler end-to-end through the Router. The store has BLIND profiles with
// known fixed-param values; we feed a current_values map that satisfies
// profile id=1 and verify active_id=1 is returned.
func TestExtendedMasterProfilesMatchDispatch(t *testing.T) {
	r, _, _, _, _, _ := newRouterWithExtended()

	// No match: empty current_values → active_id=0 (no match / Expert).
	out := dispatch(t, r, "master_profiles.match", map[string]any{
		"device_type":    "BLIND",
		"current_values": map[string]any{},
	}).(map[string]any)
	if _, ok := out["active_id"]; !ok {
		t.Fatalf("master_profiles.match: response must contain active_id, got %v", out)
	}

	// Missing device_type must return an error.
	dispatchExpectErr(t, r, "master_profiles.match", map[string]any{}, "device_type is required")
}

// ---------------------------------------------------------------------------
// L12 — incidents.list / incidents.get alias parity test
// ---------------------------------------------------------------------------

// stubIncidentLister implements [IncidentLister].
type stubIncidentLister struct {
	items []map[string]any
}

func (s *stubIncidentLister) ListIncidents(_ context.Context) ([]map[string]any, error) {
	return s.items, nil
}

// newRouterWithIncidentLister builds a minimal router that has only the
// IncidentLister wired — enough to test incidents.list and incidents.get.
func newRouterWithIncidentLister(lister IncidentLister) *Router {
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{IncidentLister: lister})
	return r
}

// TestIncidentsListAndGetAliasSameOutput asserts that incidents.list and
// incidents.get return identical JSON payloads — proving that incidents.get
// is a true alias and not a separate code-path. Closes parity-audit L12.
func TestIncidentsListAndGetAliasSameOutput(t *testing.T) {
	lister := &stubIncidentLister{
		items: []map[string]any{
			{"id": 1, "type": "connection_error", "severity": "warning", "message": "ping timeout"},
		},
	}
	r := newRouterWithIncidentLister(lister)

	outList := dispatch(t, r, "incidents.list", map[string]any{}).(map[string]any)
	outGet := dispatch(t, r, "incidents.get", map[string]any{}).(map[string]any)

	listItems, ok := outList["incidents"].([]map[string]any)
	if !ok {
		t.Fatalf("incidents.list: expected incidents key, got %T", outList["incidents"])
	}
	getItems, ok := outGet["incidents"].([]map[string]any)
	if !ok {
		t.Fatalf("incidents.get: expected incidents key, got %T", outGet["incidents"])
	}
	if len(listItems) != len(getItems) {
		t.Fatalf("incidents.list returned %d items, incidents.get returned %d — must be equal", len(listItems), len(getItems))
	}
	if len(listItems) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(listItems))
	}
}

// TestIncidentsListEmptyReturnsEmptyArray asserts that incidents.list
// returns an empty array (not null) when the store is empty.
func TestIncidentsListEmptyReturnsEmptyArray(t *testing.T) {
	r := newRouterWithIncidentLister(&stubIncidentLister{items: nil})
	out := dispatch(t, r, "incidents.list", map[string]any{}).(map[string]any)
	items, ok := out["incidents"].([]map[string]any)
	if !ok {
		t.Fatalf("incidents.list: expected []map[string]any, got %T", out["incidents"])
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 incidents, got %d", len(items))
	}
}

// fakeEditLocks is a minimal [EditLockVerifier] fake: it holds exactly
// one valid (key, token) pair and rejects everything else.
type fakeEditLocks struct{ key, token string }

func (f fakeEditLocks) Verify(key, token string) bool {
	return token != "" && key == f.key && token == f.token
}

// TestExtendedParamsetPut_EditLockEnforced asserts that paramset.put
// gates MASTER/LINK writes behind EditLockVerifier.Verify using the
// "channel:{channel_address}:{paramset_key}" lock key, while VALUES
// writes remain ungated.
func TestExtendedParamsetPut_EditLockEnforced(t *testing.T) {
	pw := &stubParamsetWriter{}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Paramsets: pw,
		EditLocks: fakeEditLocks{key: "channel:ABC0001:1:MASTER", token: "good-token"},
	})

	// 1. MASTER write with no edit_token: locked, no write recorded.
	raw, _ := json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "MASTER",
		"values":          map[string]any{"CTRL_MODE": 1},
	})
	res := r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error == nil || res.Error.Code != CommandErrorLocked {
		t.Fatalf("MASTER write without edit_token: expected code %q, got %+v", CommandErrorLocked, res.Error)
	}
	if len(pw.calls) != 0 {
		t.Fatalf("MASTER write without edit_token must not be forwarded; got %d calls", len(pw.calls))
	}

	// 2. MASTER write with a wrong edit_token: still locked, no write recorded.
	raw, _ = json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "MASTER",
		"values":          map[string]any{"CTRL_MODE": 1},
		"edit_token":      "wrong-token",
	})
	res = r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error == nil || res.Error.Code != CommandErrorLocked {
		t.Fatalf("MASTER write with wrong edit_token: expected code %q, got %+v", CommandErrorLocked, res.Error)
	}
	if len(pw.calls) != 0 {
		t.Fatalf("MASTER write with wrong edit_token must not be forwarded; got %d calls", len(pw.calls))
	}

	// 3. MASTER write with the correct edit_token: succeeds, write recorded.
	raw, _ = json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "MASTER",
		"values":          map[string]any{"CTRL_MODE": 1},
		"edit_token":      "good-token",
	})
	res = r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error != nil {
		t.Fatalf("MASTER write with correct edit_token: unexpected error: %+v", res.Error)
	}
	if len(pw.calls) != 1 {
		t.Fatalf("MASTER write with correct edit_token: expected 1 write, got %d", len(pw.calls))
	}

	// 4. VALUES write with EditLocks set but no token: never gated.
	raw, _ = json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "VALUES",
		"values":          map[string]any{"STATE": true},
	})
	res = r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error != nil {
		t.Fatalf("VALUES write: unexpected error: %+v", res.Error)
	}
	if len(pw.calls) != 2 {
		t.Fatalf("VALUES write: expected 2 total writes, got %d", len(pw.calls))
	}
}
