// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// TestPutConfigSection_SemanticValidationRejectsInvalidMQTT proves finding #3:
// a well-typed but semantically-invalid section (mqtt enabled with an empty
// broker_url) is rejected with 400 and never persisted, instead of being saved
// with 200 and only warned about at the next boot.
func TestPutConfigSection_SemanticValidationRejectsInvalidMQTT(t *testing.T) {
	t.Parallel()

	current := config.Default()
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"enabled":true,"broker_url":""}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for mqtt enabled with empty broker_url, got %d: %s", w.Code, w.Body.String())
	}
	if fake.putCalled {
		t.Fatal("invalid section must never be persisted (PutSection was called)")
	}
}

// TestPutConfigSection_SemanticValidationRejectsBadCallbackPort proves an
// out-of-range callback port is rejected with 400 rather than persisted.
func TestPutConfigSection_SemanticValidationRejectsBadCallbackPort(t *testing.T) {
	t.Parallel()

	current := config.Default()
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"port":99999}`
	w := putSection(fake, "callback", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for callback.port out of range, got %d: %s", w.Code, w.Body.String())
	}
	if fake.putCalled {
		t.Fatal("invalid section must never be persisted (PutSection was called)")
	}
}

// TestPutConfigSection_SemanticValidationRejectsBadPublicURL proves a
// non-http(s) public_url is rejected with 400 rather than persisted.
func TestPutConfigSection_SemanticValidationRejectsBadPublicURL(t *testing.T) {
	t.Parallel()

	current := config.Default()
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"public_url":"ftp://example.com"}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for ftp:// public_url, got %d: %s", w.Code, w.Body.String())
	}
	if fake.putCalled {
		t.Fatal("invalid section must never be persisted (PutSection was called)")
	}
}

// TestPutConfigSection_ValidSemanticSectionPersists is the positive companion:
// a valid mqtt section (enabled with a real broker_url) saves with 200.
func TestPutConfigSection_ValidSemanticSectionPersists(t *testing.T) {
	t.Parallel()

	current := config.Default()
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"enabled":true,"broker_url":"tcp://broker.example:1883"}`
	w := putSection(fake, "north.mqtt", body)

	if w.Code != http.StatusOK {
		t.Fatalf("valid mqtt section should save, got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("valid section was not persisted")
	}
}

// TestPutConfigSection_RestartRequiredPerField pins that the PUT response's
// restart_required is computed per changed field: a hot-appliable change (the
// MQTT broker, which the reload path re-wires) reports false, while a change
// captured once at assembly time (public_url, the CORS origins, any webhook
// field) reports true — consistent with GET /system/config-changes.
func TestPutConfigSection_RestartRequiredPerField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		section string
		body    string
		want    bool
	}{
		{
			// The allowed-origin list is captured when the CORS middleware
			// is constructed at router assembly, and an empty list installs
			// no middleware at all — so no CORS edit can take effect in a
			// running daemon.
			name:    "cors change needs restart",
			section: "north.rest",
			body:    `{"cors":["https://ui.example"]}`,
			want:    true,
		},
		{
			name:    "public_url change needs restart",
			section: "north.rest",
			body:    `{"public_url":"https://loom.example"}`,
			want:    true,
		},
		{
			name:    "webhook change needs restart",
			section: "north.webhook",
			body:    `{"enabled":true,"url":"https://hook.example"}`,
			want:    true,
		},
		{
			name:    "mqtt change is hot-appliable",
			section: "north.mqtt",
			body:    `{"enabled":true,"broker_url":"tcp://broker.example:1883"}`,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A freshly defaulted config as the baseline so the diff reflects
			// only the section change under test.
			fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: config.Default()}}
			w := putSection(fake, tc.section, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("save should succeed, got %d: %s", w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response not JSON: %v", err)
			}
			got, ok := resp["restart_required"].(bool)
			if !ok {
				t.Fatalf("restart_required missing/not bool: %v", resp["restart_required"])
			}
			if got != tc.want {
				t.Errorf("restart_required=%v want %v", got, tc.want)
			}
		})
	}
}

// TestPutConfigSection_RestartRequiredForBootWiredSections covers the sections
// whose values are captured once, while the daemon wires itself, and are never
// re-read: the OIDC client, the rate limiter, the TLS listener, the locale the
// label/bridge wiring bakes in, the CCU metadata archives, the reliability
// stack of each interface client, and the persistence recorders.
//
// The response asserted here is the operator's only signal — the SPA renders
// the save result and the restart-pending banner from it. While these sections
// had no rule, rotating a leaked OIDC client_secret, enabling the rate limiter
// under a brute-force load or pointing the listener at a certificate all
// answered restart_required:false, so the operator believed a security control
// was live while the daemon kept running on the value it booted with.
func TestPutConfigSection_RestartRequiredForBootWiredSections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		section string
		body    string
	}{
		{
			name:    "oidc client secret rotation",
			section: "north.rest.auth.oidc",
			body:    `{"enabled":true,"issuer":"https://idp.example","client_id":"loom","client_secret":"rotated","redirect_url":"https://loom.example/auth/callback"}`,
		},
		{
			name:    "rate limiter enabled",
			section: "north.rest",
			body:    `{"rate_limit":{"enabled":true,"requests_per_second":5,"burst":10}}`,
		},
		{
			name:    "tls certificate configured",
			section: "north.rest",
			body:    `{"tls_cert_file":"/etc/loom/tls.crt","tls_key_file":"/etc/loom/tls.key"}`,
		},
		{
			name:    "locale changed",
			section: "locale",
			body:    `{"locale":"de"}`,
		},
		{
			name:    "ccu data archive path changed",
			section: "ccu_data",
			body:    `{"translations_path":"/srv/ccu/translations"}`,
		},
		{
			name:    "reliability retry delay tuned",
			section: "reliability",
			body:    `{"command_retry_initial_delay":3000000000}`,
		},
		{
			name:    "values cache disabled",
			section: "persistence",
			body:    `{"values_cache":{"enabled":false}}`,
		},
		{
			name:    "history recorder enabled",
			section: "persistence",
			body:    `{"history":{"enabled":true}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: config.Default()}}
			w := putSection(fake, tc.section, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("save should succeed, got %d: %s", w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response not JSON: %v", err)
			}
			if got, _ := resp["restart_required"].(bool); !got {
				t.Errorf("restart_required=false for %s — the SPA shows the save as applied "+
					"while the daemon keeps the value it booted with", tc.section)
			}
		})
	}
}

// TestPutConfigSection_PartialPutKeepsUntouchedFields reproduces the reported
// defect: PutSection REPLACES the row, so persisting the request fragment
// instead of the candidate that was validated silently drops every field the
// client did not resend. A PUT of {"enabled":true} on north.mqtt validated
// green against a merged candidate that still had broker_url, and left behind a
// row describing an enabled MQTT bridge with no broker.
func TestPutConfigSection_PartialPutKeepsUntouchedFields(t *testing.T) {
	t.Parallel()

	current := config.Default()
	current.North.MQTT.Enabled = false
	current.North.MQTT.BrokerURL = "tcp://broker.example:1883"
	current.North.MQTT.ClientID = "loom"
	current.North.MQTT.TopicBase = "homematic"
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	w := putSection(fake, "north.mqtt", `{"enabled":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called")
	}

	var saved config.NorthMQTT
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if !saved.Enabled {
		t.Error("the edited field was not persisted")
	}
	if saved.BrokerURL != "tcp://broker.example:1883" {
		t.Errorf("partial PUT dropped broker_url: %q", saved.BrokerURL)
	}
	if saved.ClientID != "loom" {
		t.Errorf("partial PUT dropped client_id: %q", saved.ClientID)
	}
	if saved.TopicBase != "homematic" {
		t.Errorf("partial PUT dropped topic_base: %q", saved.TopicBase)
	}
}

// TestPutConfigSection_PersistsTheValidatedCandidate states the invariant the
// previous test is one instance of: the row describes the whole section, so
// replaying it onto a bare config reproduces what the handler validated. The
// replay deliberately starts from config.Default() rather than the current
// config — that is the boot-time situation, where nothing but the row is left
// to carry the operator's settings.
func TestPutConfigSection_PersistsTheValidatedCandidate(t *testing.T) {
	t.Parallel()

	current := config.Default()
	current.North.Webhook.URL = "https://old.example"
	current.North.Webhook.ParameterGlob = "*"
	current.North.Webhook.TimeoutMs = 2500
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	w := putSection(fake, "north.webhook", `{"enabled":true,"url":"https://new.example"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}

	replayed := config.Default()
	if err := configstore.ApplySectionToConfig(configstore.SectionWebhook, fake.putJSON, replayed); err != nil {
		t.Fatalf("replaying the persisted row failed: %v", err)
	}
	if !replayed.North.Webhook.Enabled || replayed.North.Webhook.URL != "https://new.example" {
		t.Errorf("persisted row lost the edit: %+v", replayed.North.Webhook)
	}
	if replayed.North.Webhook.ParameterGlob != "*" {
		t.Errorf("persisted row lost the untouched parameter_glob: %q", replayed.North.Webhook.ParameterGlob)
	}
	if replayed.North.Webhook.TimeoutMs != 2500 {
		t.Errorf("persisted row lost the untouched timeout_ms: %d", replayed.North.Webhook.TimeoutMs)
	}
}

// TestPutConfigSection_RESTRowNeverCarriesNestedAuthSections guards the REST
// side of the section-layering rule: a client that PUTs the nested auth blocks
// into north.rest must not get them persisted there. A north.rest row carrying
// north.rest.auth.ha_ingress would shadow the nested row at the next boot and
// re-enable an auth passthrough an operator had just disabled.
func TestPutConfigSection_RESTRowNeverCarriesNestedAuthSections(t *testing.T) {
	t.Parallel()

	current := config.Default()
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"public_url":"https://loom.example","auth":{"ha_ingress":{"enabled":true},` +
		`"ccu":{"enabled":true},"oidc":{"issuer":"https://idp.example"}}}`
	w := putSection(fake, "north.rest", body)
	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	for _, key := range []string{`"ha_ingress"`, `"ccu"`, `"oidc"`} {
		if strings.Contains(string(fake.putJSON), key) {
			t.Errorf("north.rest row carries the nested %s block: %s", key, fake.putJSON)
		}
	}
	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if saved.PublicURL != "https://loom.example" {
		t.Errorf("north.rest's own field was not persisted: %q", saved.PublicURL)
	}
}

// TestPutConfigSection_MapEntryDeletionIsPersisted pins that removing one
// entry from a map-valued field actually lands. The candidate the handler
// validates and persists is a clone of the current config, and decoding a
// JSON object into an already-populated Go map keeps the entries the
// payload omits — so a role_mapping the operator trimmed came back as the
// union of old and new, while the response still said "saved".
func TestPutConfigSection_MapEntryDeletionIsPersisted(t *testing.T) {
	t.Parallel()

	current := config.Default()
	current.North.REST.Auth.CCU.RoleMapping = map[string]string{"8": "admin", "1": "admin"}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	w := putSection(fake, "north.rest.auth.ccu", `{"role_mapping":{"8":"admin"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.CCUAuthConfig
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON invalid: %v", err)
	}
	if _, still := saved.RoleMapping["1"]; still {
		t.Errorf("deleted role_mapping entry survived the save: %v", saved.RoleMapping)
	}
	if saved.RoleMapping["8"] != "admin" {
		t.Errorf("kept role_mapping entry lost: %v", saved.RoleMapping)
	}
}

// TestPutConfigSectionRefusesToSaveWhenTheEffectiveConfigIsUnavailable is
// the guard for a save that used to succeed by skipping everything that
// makes it safe.
//
// The effective config was fetched best-effort. When the lookup failed
// the handler dropped the masked-secret restore, the semantic validation
// and the restart-required answer, then persisted the raw request body
// anyway and reported 200 — so a section the SPA had re-sent with its
// "***" placeholders overwrote the operator's real credentials, and
// nothing in the response said so.
func TestPutConfigSectionRefusesToSaveWhenTheEffectiveConfigIsUnavailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fake *fakeConfigAdminSvc
	}{
		{
			name: "lookup fails",
			fake: &fakeConfigAdminSvc{effectiveErr: errors.New("database is locked")},
		},
		{
			name: "lookup returns nothing",
			fake: &fakeConfigAdminSvc{},
		},
		{
			name: "lookup returns an empty result",
			fake: &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := putSection(tc.fake, "north.mqtt", `{"enabled":true,"broker_url":"tcp://h:1883"}`)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("want 503 when the effective config is unavailable, got %d: %s", w.Code, w.Body.String())
			}
			if tc.fake.putCalled {
				t.Fatal("a section must not be persisted when it could not be validated")
			}
		})
	}
}

// TestPutConfigSectionStillSavesWhenTheEffectiveConfigIsAvailable keeps
// the refusal above from swallowing the ordinary path.
func TestPutConfigSectionStillSavesWhenTheEffectiveConfigIsAvailable(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{Config: config.Default()},
	}
	w := putSection(fake, "north.mqtt", `{"enabled":true,"broker_url":"tcp://h:1883"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("a valid section must be persisted")
	}
}
