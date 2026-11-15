// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

const testLiteToken = "olt_0123456789abcdef0123456789abcdef"

func liteRowSvc() *fakeCentralAdminService {
	return &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"box": {
			Name: "box", Host: "box.local", SystemType: "openccu-lite",
			APITokenPlain: testLiteToken, Enabled: true,
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
		},
	}}
}

// TestGetCentral_MasksAPIToken pins that the API token never leaves the
// daemon in the clear, like the CCU password.
func TestGetCentral_MasksAPIToken(t *testing.T) {
	t.Parallel()
	svc := liteRowSvc()
	req := withChiParam(httptest.NewRequest(http.MethodGet, "/admin/centrals/box", http.NoBody), "name", "box")
	w := httptest.NewRecorder()
	GetCentral(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), testLiteToken) {
		t.Fatal("the API token appeared in the clear")
	}
	var row sqlite.CentralRow
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.APITokenPlain != maskSentinel {
		t.Errorf("api_token_plain = %q, want the mask", row.APITokenPlain)
	}
	if svc.centrals["box"].APITokenPlain != testLiteToken {
		t.Error("masking mutated the stored row")
	}
}

// TestUpdateCentral_MaskedAPIToken_RestoresStoredToken pins the round
// trip: a save that echoes the mask (or omits the key, or sends null) keeps
// the stored token instead of persisting "***" or wiping it.
func TestUpdateCentral_MaskedAPIToken_RestoresStoredToken(t *testing.T) {
	t.Parallel()
	for name, tokenField := range map[string]string{
		"mask":   `,"api_token_plain":"***"`,
		"absent": ``,
		"null":   `,"api_token_plain":null`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := liteRowSvc()
			body := strings.NewReader(`{"host":"box.local","system_type":"openccu-lite","enabled":true,"interfaces":[{"name":"HmIP-RF"}]` + tokenField + `}`)
			req := withChiParam(httptest.NewRequest(http.MethodPut, "/admin/centrals/box", body), "name", "box")
			w := httptest.NewRecorder()
			UpdateCentral(svc, nil, nil).ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
			}
			if got := svc.centrals["box"].APITokenPlain; got != testLiteToken {
				t.Errorf("stored token = %q, want the original restored", got)
			}
		})
	}
}

// TestUpdateCentral_ExplicitEmptyAPIToken_ClearsIt pins the one payload
// that clears the inline token: an explicit empty value, here while
// switching the central to a token named by an environment variable.
func TestUpdateCentral_ExplicitEmptyAPIToken_ClearsIt(t *testing.T) {
	t.Parallel()
	svc := liteRowSvc()
	body := strings.NewReader(`{"host":"box.local","system_type":"openccu-lite","enabled":true,"interfaces":[{"name":"HmIP-RF"}],"api_token_plain":"","api_token_env":"BOX_TOKEN"}`)
	req := withChiParam(httptest.NewRequest(http.MethodPut, "/admin/centrals/box", body), "name", "box")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	if got := svc.centrals["box"]; got.APITokenPlain != "" || got.APITokenEnv != "BOX_TOKEN" {
		t.Errorf("stored = token %q env %q, want cleared token and BOX_TOKEN", got.APITokenPlain, got.APITokenEnv)
	}
}

// TestCreateCentral_MaskedAPIToken_StoresEmpty pins that a fresh central
// never persists the literal mask.
func TestCreateCentral_MaskedAPIToken_StoresEmpty(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{}
	body := strings.NewReader(`{"name":"box","host":"box.local","system_type":"auto","interfaces":[{"name":"HmIP-RF"}],"api_token_plain":"***"}`)
	w := httptest.NewRecorder()
	CreateCentral(svc, nil, nil).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/centrals", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", w.Code, w.Body.String())
	}
	if got := svc.centrals["box"].APITokenPlain; got != "" {
		t.Errorf("stored token = %q, want empty", got)
	}
}

// TestCentralWriteValidatesTheSystemType pins that both write paths apply
// the system-type rules and refuse with 400 naming the field, and that a
// well-formed lite central is accepted.
func TestCentralWriteValidatesTheSystemType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, want string
		code             int
	}{
		{"lite ok", `{"name":"box","host":"box.local","system_type":"openccu-lite","api_token_plain":"` + testLiteToken + `","interfaces":[{"name":"HmIP-RF"}]}`, "", http.StatusCreated},
		{"lite without token", `{"name":"box","host":"box.local","system_type":"openccu-lite","interfaces":[{"name":"HmIP-RF"}]}`, "api_token: required", http.StatusBadRequest},
		{"lite token via env", `{"name":"box","host":"box.local","system_type":"openccu-lite","api_token_env":"BOX_TOKEN","interfaces":[{"name":"HmIP-RF"}]}`, "", http.StatusCreated},
		{"lite with username", `{"name":"box","host":"box.local","system_type":"openccu-lite","api_token_plain":"` + testLiteToken + `","username":"Admin","interfaces":[{"name":"HmIP-RF"}]}`, "authenticates with api_token", http.StatusBadRequest},
		{"lite with CUxD", `{"name":"box","host":"box.local","system_type":"openccu-lite","api_token_plain":"` + testLiteToken + `","interfaces":[{"name":"CUxD"}]}`, "CUxD is not available", http.StatusBadRequest},
		{"ccu with token", `{"name":"ccu","host":"ccu.local","api_token_plain":"` + testLiteToken + `","interfaces":[{"name":"HmIP-RF"}]}`, "only an openccu-lite central", http.StatusBadRequest},
		{"ccu unchanged", `{"name":"ccu","host":"ccu.local","username":"Admin","password_plain":"x","interfaces":[{"name":"HmIP-RF"},{"name":"CUxD"}]}`, "", http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCentralAdminService{}
			w := httptest.NewRecorder()
			CreateCentral(svc, nil, nil).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/centrals", strings.NewReader(tc.body)))
			if w.Code != tc.code {
				t.Fatalf("POST = %d %s, want %d", w.Code, w.Body.String(), tc.code)
			}
			if tc.want != "" && !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("body %s does not name %q", w.Body.String(), tc.want)
			}
			if tc.code != http.StatusCreated && len(svc.centrals) != 0 {
				t.Error("a refused central was persisted")
			}
		})
	}
}

// TestUpdateCentral_SwitchToLiteDropsTheCCUCredentials pins the move of a
// stored CCU central to openccu-lite: the editor's lite payload carries no
// username, password or RPC ports, and the stored ones must not come back
// through the omitted-field overlay — the lite rules refuse every one of
// them, so the switch would never save.
func TestUpdateCentral_SwitchToLiteDropsTheCCUCredentials(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"box": {
			Name: "box", Host: "127.0.0.1", Enabled: true,
			Username: "Admin", PasswordPlain: "secret", PasswordEnv: "CCU_PASSWORD",
			Port: 2010, Ports: map[string]int{"HmIP-RF": 2010},
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
		},
	}}
	body := strings.NewReader(`{"host":"127.0.0.1","system_type":"openccu-lite","enabled":true,"api_token_plain":"` + testLiteToken + `","interfaces":[{"name":"HmIP-RF"}]}`)
	req := withChiParam(httptest.NewRequest(http.MethodPut, "/admin/centrals/box", body), "name", "box")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	got := svc.centrals["box"]
	if got.SystemType != "openccu-lite" || got.APITokenPlain != testLiteToken {
		t.Errorf("stored = type %q token %q, want openccu-lite with the token", got.SystemType, got.APITokenPlain)
	}
	if got.Username != "" || got.PasswordPlain != "" || got.PasswordEnv != "" || got.Port != 0 || len(got.Ports) != 0 {
		t.Errorf("stored = user %q password %q env %q port %d ports %v, want the CCU fields gone",
			got.Username, got.PasswordPlain, got.PasswordEnv, got.Port, got.Ports)
	}
}

// TestUpdateCentral_SwitchToCCUDropsTheLiteCredentials is the way back: a
// stored lite central saved as a CCU does not keep the token, its
// environment source or the certificate pin, which the CCU rules refuse.
func TestUpdateCentral_SwitchToCCUDropsTheLiteCredentials(t *testing.T) {
	t.Parallel()
	svc := liteRowSvc()
	row := svc.centrals["box"]
	row.APITokenEnv = "BOX_TOKEN"
	row.APITokenPlain = ""
	svc.centrals["box"] = row
	body := strings.NewReader(`{"host":"box.local","system_type":"ccu","enabled":true,"username":"Admin","password_plain":"secret","interfaces":[{"name":"HmIP-RF"}]}`)
	req := withChiParam(httptest.NewRequest(http.MethodPut, "/admin/centrals/box", body), "name", "box")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	got := svc.centrals["box"]
	if got.APITokenPlain != "" || got.APITokenEnv != "" || got.TLSFingerprint != "" {
		t.Errorf("stored = token %q env %q pin %q, want the lite fields gone", got.APITokenPlain, got.APITokenEnv, got.TLSFingerprint)
	}
	if got.Username != "Admin" || got.PasswordPlain != "secret" {
		t.Errorf("stored = user %q password %q, want the CCU credentials", got.Username, got.PasswordPlain)
	}
}

// pairedOnboarding hands out one approved pairing's token.
type pairedOnboarding struct{ countingOnboarding }

func (*pairedOnboarding) PairingToken(id string) (token, fingerprint string, err error) {
	if id != "p1" {
		return "", "", errors.New("unknown pairing")
	}
	return testLiteToken, "", nil
}

// TestUpdateCentral_GuidedLiteMoveBodySaves pins the body the SPA's guided
// move sends for a stored CCU central on the same machine: the approved
// pairing supplies the token, and the explicitly cleared JSON-RPC port, TLS
// settings and dropped CUxD interface pass the lite rules instead of coming
// back through the omitted-field overlay.
func TestUpdateCentral_GuidedLiteMoveBodySaves(t *testing.T) {
	t.Parallel()
	svc := &fakeCentralAdminService{centrals: map[string]sqlite.CentralRow{
		"box": {
			Name: "box", Host: "127.0.0.1", Enabled: true,
			Username: "Admin", PasswordPlain: "secret",
			Port: 2010, Ports: map[string]int{"HmIP-RF": 2010},
			JSONRPCPort: 8181, TLS: true, TLSInsecureSkipVerify: true,
			PrimaryInterface: "CUxD",
			Interfaces:       []config.InterfaceSpec{{Name: "HmIP-RF"}, {Name: "CUxD"}},
		},
	}}
	body := strings.NewReader(`{"name":"box","host":"127.0.0.1","enabled":true,"system_type":"openccu-lite","pairing_id":"p1",` +
		`"json_rpc_port":0,"tls":false,"tls_insecure_skip_verify":false,"primary_interface":"","interfaces":[{"name":"HmIP-RF"}]}`)
	req := withChiParam(httptest.NewRequest(http.MethodPut, "/admin/centrals/box", body), "name", "box")
	w := httptest.NewRecorder()
	UpdateCentral(svc, nil, &pairedOnboarding{}).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	got := svc.centrals["box"]
	if got.SystemType != "openccu-lite" || got.APITokenPlain != testLiteToken {
		t.Errorf("stored = type %q token %q, want openccu-lite with the paired token", got.SystemType, got.APITokenPlain)
	}
	if got.JSONRPCPort != 0 || got.TLS || got.TLSInsecureSkipVerify || got.PrimaryInterface != "" || len(got.Interfaces) != 1 {
		t.Errorf("stored = jsonrpc %d tls %v insecure %v primary %q interfaces %v, want the CCU settings gone",
			got.JSONRPCPort, got.TLS, got.TLSInsecureSkipVerify, got.PrimaryInterface, got.Interfaces)
	}
}
