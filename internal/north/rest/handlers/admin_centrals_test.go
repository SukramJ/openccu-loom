// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeCentralAdminService is an in-memory CentralAdminService for tests.
type fakeCentralAdminService struct {
	centrals map[string]sqlite.CentralRow
	putErr   error
	delErr   error
}

func newFakeCentralSvc() *fakeCentralAdminService {
	now := time.Now().UTC()
	return &fakeCentralAdminService{
		centrals: map[string]sqlite.CentralRow{
			"home": {
				Name:      "home",
				Host:      "192.168.1.10",
				Enabled:   true,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
}

func (f *fakeCentralAdminService) Put(_ context.Context, row sqlite.CentralRow) error {
	if f.putErr != nil {
		return f.putErr
	}
	if f.centrals == nil {
		f.centrals = map[string]sqlite.CentralRow{}
	}
	now := time.Now().UTC()
	if existing, ok := f.centrals[row.Name]; ok {
		row.CreatedAt = existing.CreatedAt
	} else {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	f.centrals[row.Name] = row
	return nil
}

func (f *fakeCentralAdminService) Get(_ context.Context, name string) (sqlite.CentralRow, error) {
	row, ok := f.centrals[name]
	if !ok {
		return sqlite.CentralRow{}, sqlite.ErrCentralNotFound
	}
	return row, nil
}

func (f *fakeCentralAdminService) Delete(_ context.Context, name string) error {
	if f.delErr != nil {
		return f.delErr
	}
	if _, ok := f.centrals[name]; !ok {
		return sqlite.ErrCentralNotFound
	}
	delete(f.centrals, name)
	return nil
}

func (f *fakeCentralAdminService) List(_ context.Context) ([]sqlite.CentralRow, error) {
	out := make([]sqlite.CentralRow, 0, len(f.centrals))
	for k := range f.centrals {
		row := f.centrals[k]
		out = append(out, row)
	}
	return out, nil
}

// --- ListCentrals ---

func TestListCentrals_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals", http.NoBody)
	w := httptest.NewRecorder()
	ListCentrals(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 central, got %d", len(rows))
	}
}

func TestListCentrals_Empty(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{}}
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals", http.NoBody)
	w := httptest.NewRecorder()
	ListCentrals(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []sqlite.CentralRow
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Errorf("expected empty list, got %d", len(rows))
	}
}

// --- GetCentral ---

func TestGetCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.Name != "home" {
		t.Errorf("expected name=home, got %q", row.Name)
	}
}

func TestGetCentral_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/unknown", http.NoBody)
	req = withChiParam(req, "name", "unknown")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- CreateCentral ---

func TestCreateCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Name":"office","Host":"10.0.0.5","Enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.Name != "office" {
		t.Errorf("expected name=office, got %q", row.Name)
	}
}

func TestCreateCentral_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateCentral_MissingName_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Host":"10.0.0.5"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when name missing, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateCentral_MissingHost_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Name":"office"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when host missing, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- UpdateCentral ---

func TestUpdateCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	body := strings.NewReader(`{"Host":"192.168.1.99","Enabled":false,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	// Verify the update was applied.
	if updated := svc.centrals["home"]; updated.Host != "192.168.1.99" {
		t.Errorf("expected host=192.168.1.99 after update, got %q", updated.Host)
	}
}

func TestUpdateCentral_MissingHost_Returns400(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	body := strings.NewReader(`{"Enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestUpdateCentral_MissingEnabled_Returns400AndDoesNotDisable pins the
// full-replace hazard: `enabled` has no `omitempty`, so a body that omits it
// would otherwise decode to false and silently take a working central
// offline. The request must be rejected instead, and the stored row must be
// untouched.
func TestUpdateCentral_MissingEnabled_Returns400AndDoesNotDisable(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc() // "home" starts Enabled: true
	body := strings.NewReader(`{"Host":"192.168.1.10","Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !svc.centrals["home"].Enabled {
		t.Error("a rejected update must not disable the central")
	}
}

// TestUpdateCentral_MissingInterfaces_Returns400AndKeepsThem pins the same
// hazard for `interfaces`: a body that omits the key would otherwise decode
// to nil and drop every configured interface on an unconditional upsert.
func TestUpdateCentral_MissingInterfaces_Returns400AndKeepsThem(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	svc.centrals["home"] = sqlite.CentralRow{
		Name:       "home",
		Host:       "192.168.1.10",
		Enabled:    true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}
	body := strings.NewReader(`{"Host":"192.168.1.10","Enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if len(svc.centrals["home"].Interfaces) != 1 {
		t.Errorf("a rejected update must not drop configured interfaces, got %+v", svc.centrals["home"].Interfaces)
	}
}

// TestUpdateCentral_PartialBody_PreservesOmittedFields pins the general form
// of the full-replace hazard: `enabled`, `interfaces` and `password_plain`
// already have presence tracking, but every other optional field in
// [sqlite.CentralRow] does not have `omitempty`'s safety net either once it
// decodes — a body that only changes username must not silently zero the
// port, TLS posture, serial or every other field it never mentioned.
func TestUpdateCentral_PartialBody_PreservesOmittedFields(t *testing.T) {
	t.Parallel()
	stored := sqlite.CentralRow{
		Name:                  "home",
		Host:                  "192.168.1.10",
		Serial:                "SER123",
		Port:                  2001,
		JSONRPCPort:           2010,
		Username:              "olduser",
		PasswordEnv:           "CCU_PW",
		TLS:                   true,
		TLSInsecureSkipVerify: true,
		PrimaryInterface:      "HmIP-RF",
		Interfaces:            []config.InterfaceSpec{{Name: "HmIP-RF"}},
		Ports:                 map[string]int{"HmIP-RF": 2010},
		Visibility:            config.VisibilityConfig{UnIgnore: []string{"*:*:LEVEL"}},
		Enabled:               true,
	}
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{"home": stored}}

	// The body carries only the required trio plus a single changed field
	// (username); every other optional field is omitted.
	body := strings.NewReader(`{"Host":"192.168.1.10","Enabled":true,"Interfaces":[{"name":"HmIP-RF"}],"username":"newuser"}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	got := svc.centrals["home"]
	if got.Username != "newuser" {
		t.Errorf("expected username to update to newuser, got %q", got.Username)
	}
	var zeroed []string
	if got.Serial != stored.Serial {
		zeroed = append(zeroed, "serial")
	}
	if got.Port != stored.Port {
		zeroed = append(zeroed, "port")
	}
	if got.JSONRPCPort != stored.JSONRPCPort {
		zeroed = append(zeroed, "json_rpc_port")
	}
	if got.PasswordEnv != stored.PasswordEnv {
		zeroed = append(zeroed, "password_env")
	}
	if got.TLS != stored.TLS {
		zeroed = append(zeroed, "tls")
	}
	if got.TLSInsecureSkipVerify != stored.TLSInsecureSkipVerify {
		zeroed = append(zeroed, "tls_insecure_skip_verify")
	}
	if got.PrimaryInterface != stored.PrimaryInterface {
		zeroed = append(zeroed, "primary_interface")
	}
	if len(got.Ports) != len(stored.Ports) {
		zeroed = append(zeroed, "ports")
	}
	if len(got.Visibility.UnIgnore) != len(stored.Visibility.UnIgnore) {
		zeroed = append(zeroed, "visibility")
	}
	if len(zeroed) > 0 {
		t.Errorf("partial update zeroed fields it never mentioned: %v", zeroed)
	}
}

// --- Password masking ---

func TestListCentrals_MasksPassword(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals", http.NoBody)
	w := httptest.NewRecorder()
	ListCentrals(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 central, got %d", len(rows))
	}
	if rows[0].PasswordPlain != maskSentinel {
		t.Errorf("expected masked password %q, got %q", maskSentinel, rows[0].PasswordPlain)
	}
	// Masking the response must not mutate the stored row.
	if svc.centrals["home"].PasswordPlain != "s3cret" {
		t.Error("masking must not mutate the stored central row")
	}
}

func TestGetCentral_MasksPassword(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.PasswordPlain != maskSentinel {
		t.Errorf("expected masked password %q, got %q", maskSentinel, row.PasswordPlain)
	}
}

func TestGetCentral_EmptyPasswordStaysEmpty(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc() // password_plain is empty on the fixture row
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.PasswordPlain != "" {
		t.Errorf("expected empty password to stay empty (not masked), got %q", row.PasswordPlain)
	}
}

func TestGetCentral_PasswordEnvStaysVisible(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordEnv: "CCU_HOME_PASSWORD"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)

	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.PasswordEnv != "CCU_HOME_PASSWORD" {
		t.Errorf("expected password_env to stay visible, got %q", row.PasswordEnv)
	}
}

// --- UpdateCentral password restore ---

func TestUpdateCentral_MaskedPassword_RestoresStoredCredential(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	body := strings.NewReader(`{"Host":"192.168.1.99","password_plain":"***","Enabled":true,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	// The masked round-trip must restore the ORIGINAL stored credential,
	// not persist the literal sentinel.
	if got := svc.centrals["home"].PasswordPlain; got != "s3cret" {
		t.Errorf("expected stored password s3cret to be restored, got %q", got)
	}
}

func TestUpdateCentral_RealPassword_PersistsAsIs(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	body := strings.NewReader(`{"Host":"192.168.1.99","password_plain":"newpass","Enabled":true,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	// A genuinely changed (non-sentinel) password must persist as sent.
	if got := svc.centrals["home"].PasswordPlain; got != "newpass" {
		t.Errorf("expected password to persist as newpass, got %q", got)
	}
}

func TestUpdateCentral_AbsentPasswordKey_KeepsStoredCredential(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	// password_plain is optional in the published schema and GET never
	// returns it in the clear, so a script that only flips `enabled` omits
	// it. Put is an unconditional upsert — the omission must not wipe the
	// CCU credential.
	body := strings.NewReader(`{"Host":"192.168.1.10","Enabled":true,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := svc.centrals["home"].PasswordPlain; got != "s3cret" {
		t.Errorf("expected stored password s3cret to survive a partial replace, got %q", got)
	}
	if !svc.centrals["home"].Enabled {
		t.Error("expected the submitted enabled flag to persist")
	}
}

func TestUpdateCentral_NullPassword_KeepsStoredCredential(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	body := strings.NewReader(`{"Host":"192.168.1.10","password_plain":null,"Enabled":true,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := svc.centrals["home"].PasswordPlain; got != "s3cret" {
		t.Errorf("expected stored password s3cret to survive a null password, got %q", got)
	}
}

func TestUpdateCentral_ExplicitEmptyPassword_ClearsStoredCredential(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"home": {Name: "home", Host: "192.168.1.10", PasswordPlain: "s3cret"},
	}}
	// An operator switching the central to password_env sends the key
	// explicitly empty; that is the one payload that clears it.
	body := strings.NewReader(`{"Host":"192.168.1.10","password_plain":"","password_env":"CCU_PW","Enabled":true,"Interfaces":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/centrals/home", body)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := svc.centrals["home"].PasswordPlain; got != "" {
		t.Errorf("expected an explicit empty password to clear the credential, got %q", got)
	}
}

// --- CreateCentral password sentinel clearing ---

func TestCreateCentral_MaskedPassword_ClearsToEmpty(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"Name":"office","Host":"10.0.0.5","password_plain":"***"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/centrals", body)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	// A fresh central has nothing stored to restore; the sentinel must be
	// cleared to empty rather than persisted as the literal "***".
	if got := svc.centrals["office"].PasswordPlain; got != "" {
		t.Errorf("expected sentinel password cleared to empty on create, got %q", got)
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.PasswordPlain != "" {
		t.Errorf("expected empty password in create response, got %q", row.PasswordPlain)
	}
}

// --- DeleteCentral ---

func TestDeleteCentral_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/centrals/home", http.NoBody)
	req = withChiParam(req, "name", "home")
	w := httptest.NewRecorder()
	DeleteCentral(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := svc.centrals["home"]; ok {
		t.Error("expected home to be deleted")
	}
}

func TestDeleteCentral_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/centrals/ghost", http.NoBody)
	req = withChiParam(req, "name", "ghost")
	w := httptest.NewRecorder()
	DeleteCentral(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestCentralWriteRefusedForCleartextPasswordReturns400 pins the operator-
// facing half of security.allow_plaintext_secrets: the store refuses to
// persist a CCU password it cannot encrypt, and the refusal is the
// operator's own doing (no master key, no opt-in), not a server fault. A 500
// would tell them to retry; a 400 tells them what to change.
func TestCentralWriteRefusedForCleartextPasswordReturns400(t *testing.T) {
	t.Parallel()
	body := `{"Name":"office","Host":"10.0.0.5","password_plain":"secret","Enabled":true,"Interfaces":[]}`
	tests := []struct {
		name    string
		handler func(svc CentralAdminService) http.HandlerFunc
	}{
		{
			name: "create",
			handler: func(svc CentralAdminService) http.HandlerFunc {
				return CreateCentral(svc, nil)
			},
		},
		{
			name: "update",
			handler: func(svc CentralAdminService) http.HandlerFunc {
				return UpdateCentral(svc, nil)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCentralAdminService{putErr: sqlite.ErrPlaintextSecretNotAllowed}
			req := httptest.NewRequest(http.MethodPost, "/admin/centrals/office", strings.NewReader(body))
			req = withChiParam(req, "name", "office")
			w := httptest.NewRecorder()
			tc.handler(svc).ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "allow_plaintext_secrets") {
				t.Errorf("response must name the knob the operator has to change: %s", w.Body.String())
			}
		})
	}
}

// centralRowRequest issues a GET against h with the given identity attached,
// or none at all when id is nil (which is what an auth-disabled daemon
// produces).
func centralRowRequest(t *testing.T, h http.HandlerFunc, target string, id *auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	if name := strings.TrimPrefix(target, "/api/v1/centrals/"); name != target {
		req = withChiParam(req, "name", name)
	}
	if id != nil {
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), *id))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", target, w.Code, w.Body.String())
	}
	return w
}

// TestCentralRowsAreNarrowedForNonAdmins pins the reconnaissance boundary on
// the two read routes: they are not admin-gated because the energy, backup
// and rooms/functions views need the central list, so the row itself has to
// stop naming the CCU's address, account and TLS posture for anyone below
// admin. The identity-less case is the auth-disabled deployment, where there
// is no viewer to narrow the row for.
func TestCentralRowsAreNarrowedForNonAdmins(t *testing.T) {
	t.Parallel()
	svc := newFakeCentralSvc()
	svc.centrals["home"] = sqlite.CentralRow{
		Name:                  "home",
		Host:                  "192.168.1.10",
		Serial:                "3014F711A0001F58A99",
		Port:                  2010,
		JSONRPCPort:           80,
		Username:              "Admin",
		PasswordPlain:         "s3cret",
		PasswordEnv:           "CCU_PW",
		TLS:                   true,
		TLSInsecureSkipVerify: true,
		Enabled:               true,
		Interfaces:            []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}

	viewer := auth.Identity{Subject: "vera", Role: auth.RoleViewer}
	admin := auth.Identity{Subject: "ada", Role: auth.RoleAdmin}

	t.Run("viewer sees only what the open views read", func(t *testing.T) {
		t.Parallel()
		w := centralRowRequest(t, GetCentral(svc), "/api/v1/centrals/home", &viewer)
		var row sqlite.CentralRow
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if row.Name != "home" || !row.Enabled || len(row.Interfaces) != 1 {
			t.Errorf("the fields the non-admin views read must survive: %+v", row)
		}
		if row.Host != "" || row.Username != "" || row.Port != 0 || row.JSONRPCPort != 0 || row.Serial != "" {
			t.Errorf("viewer must not learn where the CCU lives: %+v", row)
		}
		if row.TLS || row.TLSInsecureSkipVerify || row.PasswordEnv != "" {
			t.Errorf("viewer must not learn how the CCU is reached: %+v", row)
		}
		if strings.Contains(w.Body.String(), "s3cret") {
			t.Error("the CCU password must never reach a response body")
		}
	})

	t.Run("admin sees the full row with the password masked", func(t *testing.T) {
		t.Parallel()
		w := centralRowRequest(t, GetCentral(svc), "/api/v1/centrals/home", &admin)
		var row sqlite.CentralRow
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if row.Host != "192.168.1.10" || row.Username != "Admin" || row.Port != 2010 || row.Serial == "" {
			t.Errorf("admin must keep the full row: %+v", row)
		}
		if row.PasswordPlain != maskSentinel {
			t.Errorf("password_plain = %q, want the mask sentinel", row.PasswordPlain)
		}
	})

	t.Run("list is narrowed for a viewer too", func(t *testing.T) {
		t.Parallel()
		w := centralRowRequest(t, ListCentrals(svc), "/api/v1/centrals", &viewer)
		var rows []sqlite.CentralRow
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("want 1 row, got %d", len(rows))
		}
		if rows[0].Host != "" || rows[0].Username != "" {
			t.Errorf("the list route must narrow the same fields as the single-row route: %+v", rows[0])
		}
	})

	t.Run("no identity means auth is off and the row stays whole", func(t *testing.T) {
		t.Parallel()
		w := centralRowRequest(t, GetCentral(svc), "/api/v1/centrals/home", nil)
		var row sqlite.CentralRow
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if row.Host != "192.168.1.10" {
			t.Errorf("an auth-disabled daemon has no viewer to narrow for: %+v", row)
		}
		if row.PasswordPlain != maskSentinel {
			t.Errorf("the password mask is unconditional; got %q", row.PasswordPlain)
		}
	})
}
