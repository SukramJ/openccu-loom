// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
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
