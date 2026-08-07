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

// TestPutConfigSection_RestartRequiredPerField proves finding #2: the PUT
// response restart_required is computed per changed field, so a hot-appliable
// change (CORS only) reports false while a restart-required change (public_url,
// or any webhook field) reports true — consistent with GET /system/config-changes.
func TestPutConfigSection_RestartRequiredPerField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		section string
		body    string
		want    bool
	}{
		{
			name:    "cors only is hot-appliable",
			section: "north.rest",
			body:    `{"cors":["https://ui.example"]}`,
			want:    false,
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
