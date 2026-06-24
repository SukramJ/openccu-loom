// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// TestValidateSection_MCP confirms the north.mcp section is structurally
// validated (so PutConfigSection accepts it) and that unknown fields are
// rejected by the strict decoder.
func TestValidateSection_MCP(t *testing.T) {
	t.Parallel()

	valid := json.RawMessage(`{"enabled":true,"allow_writes":true,"path":"/mcp"}`)
	if err := validateSection(configstore.SectionMCP, valid); err != nil {
		t.Fatalf("valid north.mcp payload rejected: %v", err)
	}

	unknown := json.RawMessage(`{"enabled":true,"bogus":1}`)
	if err := validateSection(configstore.SectionMCP, unknown); err == nil {
		t.Fatal("expected unknown-field rejection for north.mcp payload")
	}
}

// TestSectionRestartRequired_MCP pins that MCP changes are restart-required:
// the MCP route is mounted once at boot, so toggling any field only takes
// effect after a restart.
func TestSectionRestartRequired_MCP(t *testing.T) {
	t.Parallel()
	if !sectionRestartRequired(configstore.SectionMCP) {
		t.Fatal("north.mcp must be restart-required (route mounted at boot only)")
	}
}

// TestGetConfigSchema_IncludesMCP verifies the schema endpoint surfaces
// north.mcp as a section (so the SPA renders a tab) and that its fields
// carry the restart-required flag plus the "/mcp" path default.
func TestGetConfigSchema_IncludesMCP(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/schema", http.NoBody)
	w := httptest.NewRecorder()
	GetConfigSchema().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var schema SchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	hasSection := false
	for _, s := range schema.Sections {
		if s == string(configstore.SectionMCP) {
			hasSection = true
			break
		}
	}
	if !hasSection {
		t.Fatalf("schema sections missing north.mcp: %v", schema.Sections)
	}

	var enabled, path *SchemaField
	for i := range schema.Fields {
		switch schema.Fields[i].Path {
		case "north.mcp.enabled":
			enabled = &schema.Fields[i]
		case "north.mcp.path":
			path = &schema.Fields[i]
		}
	}
	if enabled == nil {
		t.Fatal("schema fields missing north.mcp.enabled")
	}
	if !enabled.RestartRequired {
		t.Error("north.mcp.enabled should be flagged restart-required")
	}
	if path == nil {
		t.Fatal("schema fields missing north.mcp.path")
	}
	if path.Default != "/mcp" {
		t.Errorf("north.mcp.path default = %v, want /mcp", path.Default)
	}
}

// TestValidateSection_CCUAuth confirms the north.rest.auth.ccu section is
// structurally validated (so PutConfigSection accepts it) and that unknown
// fields are rejected by the strict decoder.
func TestValidateSection_CCUAuth(t *testing.T) {
	t.Parallel()

	valid := json.RawMessage(`{"enabled":true,"primary":false,"central":"ccu1","min_user_level":1,"role_mapping":{"8":"admin"}}`)
	if err := validateSection(configstore.SectionCCUAuth, valid); err != nil {
		t.Fatalf("valid north.rest.auth.ccu payload rejected: %v", err)
	}

	unknown := json.RawMessage(`{"enabled":true,"bogus":1}`)
	if err := validateSection(configstore.SectionCCUAuth, unknown); err == nil {
		t.Fatal("expected unknown-field rejection for north.rest.auth.ccu payload")
	}
}

// TestSectionRestartRequired_CCUAuth pins that CCU-auth changes are
// restart-required: the login chain (including the CCU auth provider) is
// wired once at boot, so any field change only takes effect after a restart.
func TestSectionRestartRequired_CCUAuth(t *testing.T) {
	t.Parallel()
	if !sectionRestartRequired(configstore.SectionCCUAuth) {
		t.Fatal("north.rest.auth.ccu must be restart-required (login chain wired once at boot)")
	}
}

// TestGetConfigSchema_IncludesCCUAuth verifies the schema endpoint surfaces
// north.rest.auth.ccu as a section (so the SPA renders a tab) and that
// the enabled field carries the restart-required flag.
func TestGetConfigSchema_IncludesCCUAuth(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/schema", http.NoBody)
	w := httptest.NewRecorder()
	GetConfigSchema().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var schema SchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	hasSection := false
	for _, s := range schema.Sections {
		if s == string(configstore.SectionCCUAuth) {
			hasSection = true
			break
		}
	}
	if !hasSection {
		t.Fatalf("schema sections missing north.rest.auth.ccu: %v", schema.Sections)
	}

	var enabled *SchemaField
	for i := range schema.Fields {
		if schema.Fields[i].Path == "north.rest.auth.ccu.enabled" {
			enabled = &schema.Fields[i]
			break
		}
	}
	if enabled == nil {
		t.Fatal("schema fields missing north.rest.auth.ccu.enabled")
	}
	if !enabled.RestartRequired {
		t.Error("north.rest.auth.ccu.enabled should be flagged restart-required")
	}
}

// TestValidateSectionCoversAllSections is an anti-regression guard that
// ensures every section in AllSections() has a corresponding case in
// validateSection. An empty object is a valid instance of every section
// struct and must never reach the "unknown section" default.
func TestValidateSectionCoversAllSections(t *testing.T) {
	t.Parallel()
	for _, sec := range configstore.AllSections() {
		t.Run(string(sec), func(t *testing.T) {
			t.Parallel()
			if err := validateSection(sec, json.RawMessage("{}")); err != nil {
				t.Errorf("validateSection(%q, {}) returned error (missing case?): %v", sec, err)
			}
		})
	}
}
