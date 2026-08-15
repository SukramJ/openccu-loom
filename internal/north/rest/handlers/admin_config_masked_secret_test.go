// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// getSection drives GetConfigSection for the given section.
func getSection(svc ConfigAdminService, section string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/"+section, http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	GetConfigSection(svc).ServeHTTP(w, req)
	return w
}

// TestGetConfigSection_MasksSecrets verifies the per-section GET masks secret
// values to "***" instead of handing the operator's cleartext credential to
// the browser. The section store opens (decrypts) secrets on read, so without
// masking the GET would leak them — unlike the snapshot endpoint, which masks.
func TestGetConfigSection_MasksSecrets(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.mqtt",
			ValueJSON: []byte(`{"enabled":true,"password":"hunter2"}`),
		},
	}
	w := getSection(fake, "north.mqtt")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("cleartext secret leaked to the GET response: %s", w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if got["password"] != "***" {
		t.Errorf("password should be masked to ***, got %v", got["password"])
	}
	if got["enabled"] != true {
		t.Errorf("non-secret field must pass through unchanged, got %v", got["enabled"])
	}
}

// putSection drives PutConfigSection with the given section + JSON body.
func putSection(svc ConfigAdminService, section, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/"+section, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	PutConfigSection(svc, nil).ServeHTTP(w, req)
	return w
}

// TestPutConfigSection_StripsAuthCredentials verifies the north.rest section
// PUT can neither carry nor wipe the basic-auth users / API tokens: those
// credentials live only in the SQLite user/token stores now (managed by the
// /api/v1/users and /auth/tokens CRUD). A REST PUT that echoes back the masked
// auth maps (or omits them) must still persist the edited public_url, and the
// persisted section JSON must contain no auth credentials at all — so a REST
// save can never overwrite an operator's logins.
func TestPutConfigSection_StripsAuthCredentials(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			REST: config.NorthREST{
				PublicURL: "https://old.example",
				Auth: config.AuthConfig{
					Users:  map[string]string{"admin": "$2a$10$realhashvalue"},
					Tokens: map[string]string{"tok": "admin"},
				},
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// The operator changed public_url; the SPA round-trips the masked sentinel
	// for the two secret maps it never received in cleartext.
	body := `{"public_url":"https://loom-rc.toonlan.de/","auth":{"users":"***","tokens":"***"}}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called — the save silently aborted")
	}

	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.PublicURL != "https://loom-rc.toonlan.de/" {
		t.Errorf("edited public_url not persisted: %q", saved.PublicURL)
	}
	// Auth credentials must never be carried by the section row.
	if len(saved.Auth.Users) != 0 {
		t.Errorf("auth.users must not be persisted in the REST section: %#v", saved.Auth.Users)
	}
	if len(saved.Auth.Tokens) != 0 {
		t.Errorf("auth.tokens must not be persisted in the REST section: %#v", saved.Auth.Tokens)
	}
	// The raw persisted JSON must carry no auth key at all.
	if strings.Contains(string(fake.putJSON), `"users"`) || strings.Contains(string(fake.putJSON), `"tokens"`) {
		t.Errorf("persisted REST section still carries auth credentials: %s", fake.putJSON)
	}
}

// TestGetConfigSection_MasksWebhookSecret verifies the outbound-webhook signing
// secret is masked on GET — a string secret (north.webhook.secret) that, if
// leaked, would let anyone forge signed webhook deliveries.
func TestGetConfigSection_MasksWebhookSecret(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.webhook",
			ValueJSON: []byte(`{"enabled":true,"url":"https://hook.example","secret":"s3cr3t-key"}`),
		},
	}
	w := getSection(fake, "north.webhook")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "s3cr3t-key") {
		t.Fatalf("cleartext webhook secret leaked to the GET response: %s", w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if got["secret"] != "***" {
		t.Errorf("secret should be masked to ***, got %v", got["secret"])
	}
	if got["url"] != "https://hook.example" {
		t.Errorf("non-secret field must pass through unchanged, got %v", got["url"])
	}
}

// TestPutConfigSection_RestoresWebhookSecret verifies the masked webhook secret
// is restored to the operator's stored value on save, so editing an unrelated
// webhook field (e.g. the URL) does not overwrite the real signing secret with
// the "***" sentinel.
func TestPutConfigSection_RestoresWebhookSecret(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			Webhook: config.NorthWebhook{
				Enabled: true,
				URL:     "https://hook.example",
				Secret:  "real-signing-key",
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// The operator changed the URL; the SPA echoes the masked secret unchanged.
	body := `{"enabled":true,"url":"https://hook.example/v2","secret":"***"}`
	w := putSection(fake, "north.webhook", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called — the save silently aborted")
	}

	var saved config.NorthWebhook
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.URL != "https://hook.example/v2" {
		t.Errorf("edited url not persisted: %q", saved.URL)
	}
	if saved.Secret != "real-signing-key" {
		t.Errorf("masked webhook secret not restored: %q", saved.Secret)
	}
}

// TestPutConfigSection_KeepsOperatorSuppliedSecret verifies a genuinely changed
// string secret (a non-sentinel value) is persisted verbatim — restoration
// only touches the "***" sentinel, never operator-supplied new secrets. Uses
// the outbound-webhook signing secret, a string secret that still lives in its
// section (unlike the auth credentials, which moved to the SQLite stores).
func TestPutConfigSection_KeepsOperatorSuppliedSecret(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			Webhook: config.NorthWebhook{Enabled: true, URL: "https://hook.example", Secret: "old-key"},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"enabled":true,"url":"https://hook.example","secret":"brand-new-key"}`
	w := putSection(fake, "north.webhook", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthWebhook
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.Secret != "brand-new-key" {
		t.Errorf("operator-supplied new secret was not persisted: %q", saved.Secret)
	}
}

// TestPutConfigSection_EmptyComplexSecretPlaceholder reproduces the operator's
// actual 400: when no HTTP-basic users are configured, the section load
// returns north.rest WITHOUT auth.users, so the SPA's parseValue yields an
// empty string "" for that map[string]string field and the editor echoes it
// back. Strict unmarshal of "" into a map fails with
// `cannot unmarshal string into ... AuthConfig.auth.users of type map[string]string`.
// The reconcile must replace the empty placeholder (no stored value) so the
// edited public_url still saves.
func TestPutConfigSection_EmptyComplexSecretPlaceholder(t *testing.T) {
	t.Parallel()

	// Current config has NO basic-auth users/tokens.
	current := &config.Config{North: config.NorthConfig{REST: config.NorthREST{}}}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// What the SPA sends: a changed public_url plus the empty-string
	// placeholders for the unset secret maps.
	body := `{"public_url":"https://loom-rc.toonlan.de/","auth":{"users":"","tokens":""}}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called")
	}
	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.PublicURL != "https://loom-rc.toonlan.de/" {
		t.Errorf("edited public_url not persisted: %q", saved.PublicURL)
	}
	if len(saved.Auth.Users) != 0 {
		t.Errorf("auth.users should stay empty, got: %#v", saved.Auth.Users)
	}
}

// TestGetConfigSection_MasksInboundWebhookToken verifies the nested secret
// north.webhook.inbound.token is masked on GET so the bearer token is never
// sent to the browser even though it lives one struct level deeper than the
// outbound webhook secret.
func TestGetConfigSection_MasksInboundWebhookToken(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.webhook",
			ValueJSON: []byte(`{"enabled":true,"inbound":{"enabled":true,"token":"s3cr3t"}}`),
		},
	}
	w := getSection(fake, "north.webhook")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "s3cr3t") {
		t.Fatalf("cleartext inbound token leaked to the GET response: %s", w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	inbound, ok := got["inbound"].(map[string]any)
	if !ok {
		t.Fatalf("inbound field missing or wrong type: %v", got["inbound"])
	}
	if inbound["token"] != "***" {
		t.Errorf("inbound.token should be masked to ***, got %v", inbound["token"])
	}
	if got["enabled"] != true {
		t.Errorf("non-secret field must pass through unchanged, got %v", got["enabled"])
	}
}

// TestPutConfigSection_RestoresInboundWebhookToken verifies the masked nested
// secret north.webhook.inbound.token is restored from the current config when
// the SPA echoes back "***", so editing an adjacent field does not overwrite
// the real token with the sentinel.
func TestPutConfigSection_RestoresInboundWebhookToken(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			Webhook: config.NorthWebhook{
				Enabled: true,
				Inbound: config.NorthWebhookInbound{
					Enabled: true,
					Token:   "real-inbound-key",
				},
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// Operator toggled outbound enabled; SPA echoes masked sentinel for Inbound.Token.
	body := `{"enabled":false,"inbound":{"enabled":true,"token":"***"}}`
	w := putSection(fake, "north.webhook", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called — the save silently aborted")
	}

	var saved config.NorthWebhook
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.Enabled {
		t.Errorf("edited enabled field not persisted: want false, got true")
	}
	if saved.Inbound.Token != "real-inbound-key" {
		t.Errorf("masked inbound token not restored: %q", saved.Inbound.Token)
	}
}

// TestMaskSecrets_MasksCentralsArrayPassword is the regression test for the
// slice-nested secret leak: centrals[].password is classified under the
// singular path "centrals.password", but maskPath used to only descend into
// map[string]any, so a []any of centrals was never walked and every central's
// password reached GET /api/v1/config/effective in cleartext. Two centrals
// are used to prove the fix applies per-element, not just to a first entry.
func TestMaskSecrets_MasksCentralsArrayPassword(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Centrals: []config.CentralConfig{
			{Name: "ccu-1", Host: "ccu-1.example", Username: "admin", Password: "cleartext-pw-1"},
			{Name: "ccu-2", Host: "ccu-2.example", Username: "admin", Password: "cleartext-pw-2"},
		},
	}

	out := maskSecrets(cfg)

	centrals, ok := out["centrals"].([]any)
	if !ok {
		t.Fatalf("centrals field missing or wrong type: %v", out["centrals"])
	}
	if len(centrals) != 2 {
		t.Fatalf("want 2 centrals in masked output, got %d", len(centrals))
	}

	wantNames := []string{"ccu-1", "ccu-2"}
	wantHosts := []string{"ccu-1.example", "ccu-2.example"}
	wantPasswords := []string{"cleartext-pw-1", "cleartext-pw-2"}
	for i, c := range centrals {
		central, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("centrals[%d] wrong type: %v", i, c)
		}
		if central["password"] != maskSentinel {
			t.Errorf("centrals[%d].password leaked cleartext: got %v, want %q", i, central["password"], maskSentinel)
		}
		if central["password"] == wantPasswords[i] {
			t.Errorf("centrals[%d].password still equals the cleartext value %q", i, wantPasswords[i])
		}
		if central["name"] != wantNames[i] {
			t.Errorf("centrals[%d].name must pass through unchanged, got %v", i, central["name"])
		}
		if central["host"] != wantHosts[i] {
			t.Errorf("centrals[%d].host must pass through unchanged, got %v", i, central["host"])
		}
	}
}

// TestMaskSecrets_MasksTopLevelMapSecret guards the map-path (non-array) leg
// of maskPath alongside the new array leg above: north.webhook.secret is a
// plain string secret reached purely through map[string]any descent, so this
// pins that the array-recursion change did not regress the existing path.
func TestMaskSecrets_MasksTopLevelMapSecret(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		North: config.NorthConfig{
			Webhook: config.NorthWebhook{Enabled: true, URL: "https://hook.example", Secret: "real-signing-key"},
		},
	}

	out := maskSecrets(cfg)

	north, ok := out["north"].(map[string]any)
	if !ok {
		t.Fatalf("north field missing or wrong type: %v", out["north"])
	}
	webhook, ok := north["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("north.webhook field missing or wrong type: %v", north["webhook"])
	}
	if webhook["secret"] != maskSentinel {
		t.Errorf("north.webhook.secret should be masked to %q, got %v", maskSentinel, webhook["secret"])
	}
	if webhook["url"] != "https://hook.example" {
		t.Errorf("non-secret field must pass through unchanged, got %v", webhook["url"])
	}
}

// mqttSectionConfig is the current config the placeholder tests reconcile a
// north.mqtt PUT against: a fully configured broker link with a password.
func mqttSectionConfig() *config.Config {
	return &config.Config{
		North: config.NorthConfig{
			MQTT: config.NorthMQTT{
				Enabled:   true,
				BrokerURL: "tcp://broker.example:1883",
				ClientID:  "loom",
				Username:  "loom",
				Password:  "real-broker-password",
				TopicBase: "openccu-loom",
			},
		},
	}
}

// TestPutConfigSection_NullSecretPlaceholderKeepsPassword reproduces the
// reported broker rejection: the operator edits an unrelated MQTT field, the
// editor serialises the untouched password as JSON null, and the save wipes
// the credential. The daemon then sends a CONNECT with no password flag and
// the broker answers "Not authorized (0x87)".
func TestPutConfigSection_NullSecretPlaceholderKeepsPassword(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: mqttSectionConfig()}}
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","password":null,"topic_base":"homematic"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.TopicBase != "homematic" {
		t.Errorf("edited topic_base not persisted: %q", saved.TopicBase)
	}
	if saved.Password != "real-broker-password" {
		t.Errorf("null placeholder wiped the broker password: %q", saved.Password)
	}
}

// TestPutConfigSection_AbsentSecretKeepsPassword covers the shape the editor
// sends after the fix and any REST client that simply omits the field: an
// absent key means "unchanged", never "clear it".
func TestPutConfigSection_AbsentSecretKeepsPassword(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: mqttSectionConfig()}}
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","topic_base":"homematic"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.Password != "real-broker-password" {
		t.Errorf("absent secret key wiped the broker password: %q", saved.Password)
	}
}

// TestPutConfigSection_EnvResolvedSecretStaysOutOfTheRow covers the secret an
// operator supplies only through OPENCCU_LOOM_MQTT_PASSWORD. Effective
// overlays it onto the assembled config as its last step and stamps it
// SourceEnv; both the masked-secret restore and the section marshal then carry
// it into the blob PutSection writes, so saving any unrelated MQTT field made
// the credential durable — in database backups and, in cleartext, in
// `config export`. The row must keep whatever it held (here: nothing).
func TestPutConfigSection_EnvResolvedSecretStaysOutOfTheRow(t *testing.T) {
	t.Parallel()

	cur := mqttSectionConfig()
	cur.North.MQTT.Password = "s3cr3t-from-env"
	fake := &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{
			Config:  cur,
			Sources: map[string]configstore.FieldSource{"north.mqtt.password": configstore.SourceEnv},
		},
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.mqtt",
			ValueJSON: []byte(`{"enabled":true,"broker_url":"tcp://broker.example:1883"}`),
		},
	}
	// The editor drops an untouched secret from the payload entirely.
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","topic_base":"homematic"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(string(fake.putJSON), "s3cr3t-from-env") {
		t.Fatalf("env-only secret was persisted into the section row: %s", fake.putJSON)
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.Password != "" {
		t.Errorf("persisted password = %q, want it absent", saved.Password)
	}
	if saved.TopicBase != "homematic" {
		t.Errorf("the unrelated edit was lost: topic_base = %q", saved.TopicBase)
	}
}

// TestPutConfigSection_EnvSecretDoesNotDeleteTheStoredOne pins the other half:
// an operator who saved a password through the editor and later added the env
// var keeps the stored value in the row. Dropping it would delete the
// credential the moment the env var goes away.
func TestPutConfigSection_EnvSecretDoesNotDeleteTheStoredOne(t *testing.T) {
	t.Parallel()

	cur := mqttSectionConfig()
	cur.North.MQTT.Password = "s3cr3t-from-env"
	fake := &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{
			Config:  cur,
			Sources: map[string]configstore.FieldSource{"north.mqtt.password": configstore.SourceEnv},
		},
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.mqtt",
			ValueJSON: []byte(`{"enabled":true,"password":"saved-in-db"}`),
		},
	}
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","topic_base":"homematic"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.Password != "saved-in-db" {
		t.Errorf("persisted password = %q, want the stored value kept", saved.Password)
	}
}

// TestPutConfigSection_TypedSecretPersistsDespiteEnvOverride verifies the
// env-secret guard does not swallow an operator's own edit: a password typed
// into the editor is theirs, and the row records it even while the env var
// takes precedence at runtime.
func TestPutConfigSection_TypedSecretPersistsDespiteEnvOverride(t *testing.T) {
	t.Parallel()

	cur := mqttSectionConfig()
	cur.North.MQTT.Password = "s3cr3t-from-env"
	fake := &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{
			Config:  cur,
			Sources: map[string]configstore.FieldSource{"north.mqtt.password": configstore.SourceEnv},
		},
	}
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","password":"typed-by-operator","topic_base":"homematic"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.Password != "typed-by-operator" {
		t.Errorf("persisted password = %q, want the operator's own value", saved.Password)
	}
}

// TestPutConfigSection_EmptyStringSecretClears verifies the operator can still
// remove a credential: emptying a string secret's input persists the empty
// value instead of being reconciled back to the stored one. Without this the
// restore would make a configured password impossible to delete.
func TestPutConfigSection_EmptyStringSecretClears(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: mqttSectionConfig()}}
	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883","client_id":"loom","username":"loom","password":"","topic_base":"openccu-loom"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.Password != "" {
		t.Errorf("deliberately cleared password was restored: %q", saved.Password)
	}
}

// TestMaskSecrets_LeavesUnsetSecretEmpty pins the diagnostic half of the
// contract: masking an unset secret to "***" makes a dropped credential look
// configured in the UI, which is what hid the wiped MQTT password. An empty
// secret must stay empty; a configured one must still be masked.
func TestMaskSecrets_LeavesUnsetSecretEmpty(t *testing.T) {
	t.Parallel()

	cfg := mqttSectionConfig()
	cfg.North.Webhook = config.NorthWebhook{Enabled: true, URL: "https://hook.example"} // secret unset
	masked := maskSecrets(cfg)

	north, _ := masked["north"].(map[string]any)
	mqttSec, _ := north["mqtt"].(map[string]any)
	if got := mqttSec["password"]; got != maskSentinel {
		t.Errorf("configured secret must be masked, got %#v", got)
	}
	hook, _ := north["webhook"].(map[string]any)
	if got := hook["secret"]; got != "" {
		t.Errorf("unset secret must stay empty so the UI can show \"not set\", got %#v", got)
	}
}

// TestGetConfigSection_LeavesUnsetSecretEmpty is the section-scoped
// counterpart: a stored section whose secret is empty must not come back as
// "***" either.
func TestGetConfigSection_LeavesUnsetSecretEmpty(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.mqtt",
			ValueJSON: []byte(`{"enabled":true,"broker_url":"tcp://broker.example:1883","password":""}`),
		},
	}
	w := getSection(fake, "north.mqtt")
	if w.Code != http.StatusOK {
		t.Fatalf("GET should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if v := got["password"]; v != "" {
		t.Errorf("unset section secret must stay empty, got %#v", v)
	}
}
