// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/codes"
	"github.com/SukramJ/openccu-loom/internal/audit"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// newAlarmCodesFixture opens a fresh migrated temp-file SQLite database
// and wires the real AlarmCodeStore + AlarmCodeStoreAdmin over it —
// the same adapter the daemon composition root wires in production —
// so these tests exercise the real argon2id-hashing path (internal/alarm/codes.HashPIN)
// rather than a hand-rolled stand-in.
func newAlarmCodesFixture(t *testing.T) (*sqlitestore.AlarmCodeStore, *AlarmCodeStoreAdmin) {
	t.Helper()
	db := openMigratedTestDB(t, "alarm-codes.db")
	store := sqlitestore.NewAlarmCodeStore(db)
	return store, NewAlarmCodeStoreAdmin(store)
}

func alarmCodeRequestBody(t *testing.T, req hmapi.AlarmCodeRequest) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal alarm code request: %v", err)
	}
	return bytes.NewReader(b)
}

// --- 503 when the codes subsystem is not wired ---

// TestAlarmCodeHandlers_NilAdmin_Returns503 covers every /alarm/codes
// route with a nil AlarmCodeAdmin: the daemon leaves the dependency nil
// when the alarm subsystem is disabled, and every route must degrade to
// 503 rather than panicking.
func TestAlarmCodeHandlers_NilAdmin_Returns503(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func(w http.ResponseWriter, r *http.Request)
	}{
		{"list", func(w http.ResponseWriter, r *http.Request) { ListAlarmCodes(nil).ServeHTTP(w, r) }},
		{"get", func(w http.ResponseWriter, r *http.Request) { GetAlarmCode(nil).ServeHTTP(w, r) }},
		{"create", func(w http.ResponseWriter, r *http.Request) { CreateAlarmCode(nil, nil).ServeHTTP(w, r) }},
		{"put", func(w http.ResponseWriter, r *http.Request) { PutAlarmCode(nil, nil).ServeHTTP(w, r) }},
		{"delete", func(w http.ResponseWriter, r *http.Request) { DeleteAlarmCode(nil, nil).ServeHTTP(w, r) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/codes/c1", http.NoBody)
			req = withChiParam(req, "id", "c1")
			w := httptest.NewRecorder()
			tc.call(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503, body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// --- ListAlarmCodes / GetAlarmCode ---

func TestListAlarmCodes_Empty_ReturnsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/codes", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmCodes(admin).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want the empty JSON array, not null", got)
	}
}

func TestGetAlarmCode_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/codes/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	GetAlarmCode(admin).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// --- CreateAlarmCode ---

// TestCreateAlarmCode_PINHashedAndNeverReturned is the central §11/§16
// contract for the codes CRUD surface: the response projection carries
// no hash and no cleartext PIN field at all (the wire type has none to
// serialize), yet the stored row's hash argon2id-verifies against the
// cleartext PIN that was submitted.
func TestCreateAlarmCode_PINHashedAndNeverReturned(t *testing.T) {
	t.Parallel()
	store, admin := newAlarmCodesFixture(t)
	rec := &captureRecorder{}

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{
		Name: "Markus", Kind: "pin", PIN: "1234",
		Perms:   hmapi.AlarmCodePerms{Disarm: true},
		Enabled: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", body)
	w := httptest.NewRecorder()
	CreateAlarmCode(admin, rec).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "1234") {
		t.Fatalf("response body leaks the cleartext PIN: %s", w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, has := raw["hash"]; has {
		t.Error("response body carries a hash field")
	}
	if _, has := raw["pin"]; has {
		t.Error("response body echoes the pin field")
	}

	var created hmapi.AlarmCode
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created code has no server-generated id")
	}
	if created.Kind != "pin" || !created.Perms.Disarm {
		t.Errorf("created = %+v, want kind=pin perms.disarm=true", created)
	}

	row, ok, err := store.Get(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("store.Get(%q): ok=%v err=%v", created.ID, ok, err)
	}
	if row.Hash == "" || row.Hash == "1234" {
		t.Fatalf("stored hash = %q, want a non-empty argon2id hash", row.Hash)
	}
	if !codes.VerifyPIN(row.Hash, "1234") {
		t.Error("stored hash does not argon2id-verify against the submitted PIN")
	}

	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmCodeChange {
		t.Fatalf("audit entries = %+v, want one alarm_code_change", rec.entries)
	}
}

// TestCreateAlarmCode_KeypadSlotBindingRoundTrips covers the hardware
// code kinds: no PIN/hash involved, the binding document round-trips
// verbatim through create.
func TestCreateAlarmCode_KeypadSlotBindingRoundTrips(t *testing.T) {
	t.Parallel()
	store, admin := newAlarmCodesFixture(t)

	binding := json.RawMessage(`{"central":"ccu1","device_address":"0001ABCD","slot":1,"arm_mode":"full","zone_id":"eg"}`)
	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{
		Name: "Front Door Slot 1", Kind: "keypad_slot",
		Perms: hmapi.AlarmCodePerms{Arm: true, Disarm: true},
		Zones: []string{"eg"}, Binding: binding, Enabled: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", body)
	w := httptest.NewRecorder()
	CreateAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created hmapi.AlarmCode
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Kind != "keypad_slot" || len(created.Zones) != 1 || created.Zones[0] != "eg" {
		t.Errorf("created = %+v, want kind=keypad_slot zones=[eg]", created)
	}
	if string(created.Binding) != string(binding) {
		t.Errorf("binding = %s, want %s", created.Binding, binding)
	}

	row, ok, err := store.Get(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("store.Get: ok=%v err=%v", ok, err)
	}
	if row.Hash != "" {
		t.Errorf("keypad_slot row carries a hash: %q, want empty", row.Hash)
	}
}

func TestCreateAlarmCode_MissingName_Returns422(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{Kind: "pin", PIN: "1234"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", body)
	w := httptest.NewRecorder()
	CreateAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateAlarmCode_InvalidKind_Returns422(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{Name: "x", Kind: "bogus"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", body)
	w := httptest.NewRecorder()
	CreateAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateAlarmCode_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", strings.NewReader("{not-json"))
	w := httptest.NewRecorder()
	CreateAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// --- PutAlarmCode ---

func TestPutAlarmCode_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{Name: "x", Kind: "pin", PIN: "1234"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/codes/missing", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmCode_EmptyPINKeepsExistingHash pins the update-without-PIN
// contract: an operator editing a code's metadata (name/perms/zones)
// must not have to re-enter the PIN, and the stored hash must survive
// the round trip unchanged.
func TestPutAlarmCode_EmptyPINKeepsExistingHash(t *testing.T) {
	t.Parallel()
	store, admin := newAlarmCodesFixture(t)
	ctx := context.Background()

	created, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{
		Name: "Markus", Kind: "pin", PIN: "1234", Perms: hmapi.AlarmCodePerms{Disarm: true}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed CreateCode: %v", err)
	}
	before, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("store.Get before update: ok=%v err=%v", ok, err)
	}

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{
		Name: "Markus Renamed", Kind: "pin", // PIN omitted
		Perms: hmapi.AlarmCodePerms{Disarm: true, Silence: true}, Enabled: true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/codes/"+created.ID, body)
	req = withChiParam(req, "id", created.ID)
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	PutAlarmCode(admin, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	after, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("store.Get after update: ok=%v err=%v", ok, err)
	}
	if after.Hash != before.Hash {
		t.Errorf("hash changed on a PIN-less update: before=%q after=%q", before.Hash, after.Hash)
	}
	if after.Name != "Markus Renamed" {
		t.Errorf("name = %q, want the updated name", after.Name)
	}
	if !codes.VerifyPIN(after.Hash, "1234") {
		t.Error("stored hash no longer verifies against the original PIN")
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmCodeChange {
		t.Fatalf("audit entries = %+v, want one alarm_code_change", rec.entries)
	}
}

// TestPutAlarmCode_NewPINRehashes covers the opposite path: supplying a
// fresh PIN on update replaces the stored hash.
func TestPutAlarmCode_NewPINRehashes(t *testing.T) {
	t.Parallel()
	store, admin := newAlarmCodesFixture(t)
	ctx := context.Background()

	created, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{
		Name: "Markus", Kind: "pin", PIN: "1234", Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed CreateCode: %v", err)
	}

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{Name: "Markus", Kind: "pin", PIN: "9999", Enabled: true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/codes/"+created.ID, body)
	req = withChiParam(req, "id", created.ID)
	w := httptest.NewRecorder()
	PutAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	row, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("store.Get: ok=%v err=%v", ok, err)
	}
	if codes.VerifyPIN(row.Hash, "1234") {
		t.Error("stale PIN still verifies after rehash")
	}
	if !codes.VerifyPIN(row.Hash, "9999") {
		t.Error("new PIN does not verify after rehash")
	}
}

// --- DeleteAlarmCode ---

func TestDeleteAlarmCode_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/codes/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	DeleteAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteAlarmCode_HappyPath_Returns204AndRemovesRow(t *testing.T) {
	t.Parallel()
	store, admin := newAlarmCodesFixture(t)
	ctx := context.Background()

	created, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "Guest", Kind: "pin", PIN: "5555", Enabled: true})
	if err != nil {
		t.Fatalf("seed CreateCode: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/codes/"+created.ID, http.NoBody)
	req = withChiParam(req, "id", created.ID)
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	DeleteAlarmCode(admin, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if _, ok, err := store.Get(ctx, created.ID); err != nil || ok {
		t.Fatalf("code still present after delete: ok=%v err=%v", ok, err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmCodeChange {
		t.Fatalf("audit entries = %+v, want one alarm_code_change", rec.entries)
	}
}

// TestListAlarmCodes_ReturnsSeeded covers the list surface end to end
// through the real store, ordered by name (AlarmCodeStore.GetAll).
// --- Write-time validation (S7 fail-visible: docs/alarm-concept.md §11) ---

// TestAlarmCodeRowFromReq_Validation is a table of every accepted and
// rejected shape validateAlarmCodeWrite enforces: a pin code must carry
// a PIN on creation (an update may omit it and keep the existing hash),
// and a hardware binding must decode strictly (no unknown fields, no
// malformed/missing JSON, no trailing data) into the fields and edges
// the intent router (internal/alarm/intents.go) and codes facade
// (internal/alarm/codes) actually consume.
func TestAlarmCodeRowFromReq_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		req          hmapi.AlarmCodeRequest
		existingHash string
		wantErr      bool
	}{
		{
			name: "pin create with PIN is valid",
			req:  hmapi.AlarmCodeRequest{Name: "x", Kind: "pin", PIN: "1234"},
		},
		{
			name:    "pin create without PIN is rejected",
			req:     hmapi.AlarmCodeRequest{Name: "x", Kind: "pin"},
			wantErr: true,
		},
		{
			name:         "pin update with empty PIN keeps existing hash",
			req:          hmapi.AlarmCodeRequest{Name: "x", Kind: "pin"},
			existingHash: "argon2id$stub",
		},
		{
			name: "pin kind ignores an arbitrary/garbage binding",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "pin", PIN: "1234",
				Binding: json.RawMessage(`{"anything":"goes","not-even":123}`),
			},
		},
		{
			name: "keypad_slot valid binding",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":1,"zone_id":"eg","arm_mode":"full"}`),
			},
		},
		{
			name:    "keypad_slot missing binding entirely is rejected",
			req:     hmapi.AlarmCodeRequest{Name: "x", Kind: "keypad_slot"},
			wantErr: true,
		},
		{
			name: "keypad_slot malformed JSON is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{not-json`),
			},
			wantErr: true,
		},
		{
			name: "keypad_slot unknown binding field is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":1,"zone_id":"eg","typo_field":"x"}`),
			},
			wantErr: true,
		},
		{
			name: "keypad_slot missing device_address is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"slot":1,"zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "keypad_slot slot 0 is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":0,"zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "keypad_slot slot 9 is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":9,"zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "keypad_slot missing zone_id is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":1}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key valid binding with disarm action",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"disarm","zone_id":"eg"}`),
			},
		},
		{
			name: "remote_key valid binding with arm:<mode> action",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_LONG","action":"arm:full","zone_id":"eg"}`),
			},
		},
		{
			name: "remote_key valid binding with bare arm: action",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"arm:","zone_id":"eg"}`),
			},
		},
		{
			name: "remote_key unsupported parameter PRESS is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS","action":"disarm","zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key missing channel_address is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"parameter":"PRESS_SHORT","action":"disarm","zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key missing zone_id is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"disarm"}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key unknown action is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"bogus","zone_id":"eg"}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key unknown binding field is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"disarm","zone_id":"eg","typo":"x"}`),
			},
			wantErr: true,
		},
		{
			name: "remote_key malformed JSON is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{not-json`),
			},
			wantErr: true,
		},
		{
			name: "remote_key trailing data after binding document is rejected",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS_SHORT","action":"disarm","zone_id":"eg"}{}`),
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := alarmCodeRowFromReq("id1", tc.req, tc.existingHash, 1, 1)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want a validation error, got nil")
				}
				if !errors.Is(err, ErrInvalidAlarmCode) {
					t.Errorf("err = %v, want it to wrap ErrInvalidAlarmCode", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestCreateAlarmCode_InvalidBinding_Returns422 exercises the REST 422
// mapping end to end: a create whose binding is malformed / carries an
// unknown field / targets an unsupported remote parameter must never
// reach the store — it answers 422 with a problem+json body naming the
// specific defect, mirroring decodeAlarmCodeRequest's existing 422s.
func TestCreateAlarmCode_InvalidBinding_Returns422(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		req         hmapi.AlarmCodeRequest
		errContains string
	}{
		{
			name:        "pin without PIN",
			req:         hmapi.AlarmCodeRequest{Name: "x", Kind: "pin"},
			errContains: "pin is required",
		},
		{
			name: "remote_key unsupported parameter",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "remote_key",
				Binding: json.RawMessage(`{"channel_address":"0002ABCD:1","parameter":"PRESS","action":"disarm","zone_id":"eg"}`),
			},
			errContains: "PRESS_SHORT or PRESS_LONG",
		},
		{
			name: "keypad_slot unknown field",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":1,"zone_id":"eg","typo_field":"x"}`),
			},
			errContains: "keypad_slot binding",
		},
		{
			// Syntactically valid JSON (a bare string), so it survives
			// the outer DecodeJSON step, but the wrong shape for
			// codes.Binding — the decode-time failure our stricter
			// decoder (not just the outer envelope decode) must catch.
			name: "keypad_slot binding is not a JSON object",
			req: hmapi.AlarmCodeRequest{
				Name: "x", Kind: "keypad_slot",
				Binding: json.RawMessage(`"not-an-object"`),
			},
			errContains: "keypad_slot binding",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, admin := newAlarmCodesFixture(t)

			body := alarmCodeRequestBody(t, tc.req)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/codes", body)
			w := httptest.NewRecorder()
			CreateAlarmCode(admin, nil).ServeHTTP(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
			}
			var problemBody map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &problemBody); err != nil {
				t.Fatalf("unmarshal problem: %v", err)
			}
			if problemBody["code"] != "validation" {
				t.Errorf("code = %v, want validation", problemBody["code"])
			}
			detail, _ := problemBody["detail"].(string)
			if !strings.Contains(detail, tc.errContains) {
				t.Errorf("detail = %q, want it to contain %q", detail, tc.errContains)
			}
		})
	}
}

// TestPutAlarmCode_InvalidBinding_Returns422 covers the same 422
// mapping through the update path, where an existing pin hash is
// present but the new binding is still validated against the kind
// actually submitted.
func TestPutAlarmCode_InvalidBinding_Returns422(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)
	ctx := context.Background()

	created, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "Front Door", Kind: "pin", PIN: "1234", Enabled: true})
	if err != nil {
		t.Fatalf("seed CreateCode: %v", err)
	}

	body := alarmCodeRequestBody(t, hmapi.AlarmCodeRequest{
		Name: "Front Door", Kind: "keypad_slot",
		Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":11,"zone_id":"eg"}`),
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/codes/"+created.ID, body)
	req = withChiParam(req, "id", created.ID)
	w := httptest.NewRecorder()
	PutAlarmCode(admin, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

// --- OnChange hook ---

// TestAlarmCodeStoreAdmin_OnChangeHook_FiresOnceOnSuccessNotOnValidationFailure
// pins the mutation-notification contract: the hook installed via
// OnChange fires exactly once per successful create/update/delete (so
// dependent surfaces like MQTT discovery's code-requirement flags
// re-derive), and does NOT fire when a write is rejected by write-time
// validation before it ever reaches the store.
func TestAlarmCodeStoreAdmin_OnChangeHook_FiresOnceOnSuccessNotOnValidationFailure(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)
	ctx := context.Background()

	var fired int
	admin.OnChange(func() { fired++ })

	// Rejected create: no hook fire.
	_, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "x", Kind: "pin"})
	if err == nil || !errors.Is(err, ErrInvalidAlarmCode) {
		t.Fatalf("CreateCode err = %v, want ErrInvalidAlarmCode", err)
	}
	if fired != 0 {
		t.Fatalf("fired = %d after a rejected create, want 0", fired)
	}

	// Successful create: hook fires once.
	created, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "x", Kind: "pin", PIN: "1234", Enabled: true})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired = %d after a successful create, want 1", fired)
	}

	// Rejected update: no additional hook fire.
	_, _, err = admin.UpdateCode(ctx, created.ID, hmapi.AlarmCodeRequest{
		Name: "x", Kind: "keypad_slot",
		Binding: json.RawMessage(`{"device_address":"0001ABCD","slot":99,"zone_id":"eg"}`),
	})
	if err == nil || !errors.Is(err, ErrInvalidAlarmCode) {
		t.Fatalf("UpdateCode err = %v, want ErrInvalidAlarmCode", err)
	}
	if fired != 1 {
		t.Fatalf("fired = %d after a rejected update, want unchanged 1", fired)
	}

	// Successful update: hook fires again.
	if _, _, err := admin.UpdateCode(ctx, created.ID, hmapi.AlarmCodeRequest{Name: "x renamed", Kind: "pin", Enabled: true}); err != nil {
		t.Fatalf("UpdateCode: %v", err)
	}
	if fired != 2 {
		t.Fatalf("fired = %d after a successful update, want 2", fired)
	}

	// Successful delete: hook fires again.
	if ok, err := admin.DeleteCode(ctx, created.ID); err != nil || !ok {
		t.Fatalf("DeleteCode: ok=%v err=%v", ok, err)
	}
	if fired != 3 {
		t.Fatalf("fired = %d after a successful delete, want 3", fired)
	}

	// Delete of an already-gone id: no additional hook fire.
	if ok, err := admin.DeleteCode(ctx, created.ID); err != nil || ok {
		t.Fatalf("DeleteCode (already gone): ok=%v err=%v", ok, err)
	}
	if fired != 3 {
		t.Fatalf("fired = %d after a no-op delete, want unchanged 3", fired)
	}
}

func TestListAlarmCodes_ReturnsSeeded(t *testing.T) {
	t.Parallel()
	_, admin := newAlarmCodesFixture(t)
	ctx := context.Background()
	if _, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "Bea", Kind: "pin", PIN: "1111", Enabled: true}); err != nil {
		t.Fatalf("seed Bea: %v", err)
	}
	if _, err := admin.CreateCode(ctx, hmapi.AlarmCodeRequest{Name: "Alex", Kind: "pin", PIN: "2222", Enabled: true}); err != nil {
		t.Fatalf("seed Alex: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/codes", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmCodes(admin).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmCode
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 || body[0].Name != "Alex" || body[1].Name != "Bea" {
		t.Fatalf("codes = %+v, want [Alex Bea] (name order)", body)
	}
}
