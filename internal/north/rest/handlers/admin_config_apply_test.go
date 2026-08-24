// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// recordingApplier records the sections a save asked to apply live.
type recordingApplier struct {
	sections []configstore.Section
	applied  bool
	err      error
}

func (r *recordingApplier) ApplySection(_ context.Context, section configstore.Section) (bool, error) {
	r.sections = append(r.sections, section)
	return r.applied, r.err
}

// mqttAdminSvc returns a config-admin fake whose effective config carries
// a usable north.mqtt block, so a section PUT validates.
func mqttAdminSvc() *fakeConfigAdminSvc {
	return &fakeConfigAdminSvc{
		effectiveResult: &configstore.EffectiveResult{
			Config: &config.Config{
				North: config.NorthConfig{
					MQTT: config.NorthMQTT{
						Enabled:   true,
						BrokerURL: "tcp://broker:1883",
						TopicBase: "openccu-loom",
					},
				},
			},
		},
	}
}

// putSectionWithApplier drives PutConfigSection with a live-apply seam.
func putSectionWithApplier(svc ConfigAdminService, applier SectionApplier, section, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/sections/"+section, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	PutConfigSection(svc, nil, applier).ServeHTTP(w, req)
	return w
}

// TestPutConfigSectionAppliesTheSectionToTheRunningDaemon pins that a
// saved section reaches the subsystem it configures.
//
// `north.mqtt` carries no restart-required field, so both the published
// schema and the PUT response tell the operator the change takes effect
// now. It did not: the live Bridge bakes the topic base and the two
// plane toggles into an immutable BridgeConfig at construction, the DB
// overlay these values live in is only re-read by applyDefaults at boot,
// and the file-watcher hot-reload path never fires for a section the SPA
// writes straight into the database. An operator who renamed the topic
// base got a success toast, no restart hint, and a daemon that kept
// publishing under the old base until the next restart.
func TestPutConfigSectionAppliesTheSectionToTheRunningDaemon(t *testing.T) {
	t.Parallel()

	applier := &recordingApplier{applied: true}
	w := putSectionWithApplier(mqttAdminSvc(), applier, "north.mqtt", `{"topic_base":"loomtest"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(applier.sections) != 1 {
		t.Fatalf("the save asked to apply %d section(s), want 1 — the change is persisted and "+
			"the operator is told it needs no restart, while the running bridge keeps the old "+
			"configuration", len(applier.sections))
	}
	if applier.sections[0] != configstore.Section("north.mqtt") {
		t.Errorf("applied section %q, want north.mqtt", applier.sections[0])
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if body["applied"] != true {
		t.Errorf(`response applied = %v, want true — a caller cannot otherwise tell "took effect now" `+
			`from "stored for the next restart"`, body["applied"])
	}
}

// TestPutConfigSectionReportsAFailedApplyWithoutFailingTheSave pins the
// split between the two outcomes: the section is stored either way, and
// the operator is told when the running daemon did not take it.
//
// Failing the request would be wrong — the value IS persisted and WILL
// apply at the next restart — but answering a bare 200 would repeat the
// original defect in a new place.
func TestPutConfigSectionReportsAFailedApplyWithoutFailingTheSave(t *testing.T) {
	t.Parallel()

	applier := &recordingApplier{err: errors.New("broker refused the connection")}
	w := putSectionWithApplier(mqttAdminSvc(), applier, "north.mqtt", `{"topic_base":"loomtest"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("a failed live apply must not fail the save; got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if body["applied"] != false {
		t.Errorf("response applied = %v, want false", body["applied"])
	}
	if s, _ := body["apply_error"].(string); !strings.Contains(s, "broker refused") {
		t.Errorf("response apply_error = %q, want the reason the apply failed", s)
	}
}

// TestPutConfigSectionWithoutAnApplierReportsNotApplied pins the honest
// answer for a section no subsystem can take live: applied is false and
// there is no error, because nothing failed — the value simply waits for
// a restart.
func TestPutConfigSectionWithoutAnApplierReportsNotApplied(t *testing.T) {
	t.Parallel()

	w := putSectionWithApplier(mqttAdminSvc(), nil, "north.mqtt", `{"topic_base":"loomtest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if body["applied"] != false {
		t.Errorf("response applied = %v, want false", body["applied"])
	}
	if _, present := body["apply_error"]; present {
		t.Errorf("no apply was attempted, so there is no error to report: %v", body["apply_error"])
	}
}
