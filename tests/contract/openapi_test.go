// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

type openAPIDoc struct {
	OpenAPI string               `yaml:"openapi"`
	Paths   map[string]yaml.Node `yaml:"paths"`
}

func loadOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	b, err := os.ReadFile(filepath.Join(repoRoot, "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return doc
}

func TestOpenAPIVersion(t *testing.T) {
	doc := loadOpenAPI(t)
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%s, want 3.1.0", doc.OpenAPI)
	}
}

func TestOpenAPIDeclaresMVPEndpoints(t *testing.T) {
	doc := loadOpenAPI(t)
	// Every path wired in rest.NewRouter must also exist in the
	// OpenAPI spec. Drift is a release blocker — the runtime
	// validator middleware enforces this in production too.
	want := []string{
		"/info",
		"/health",
		"/config",
		"/devices",
		"/devices/{addr}",
		"/devices/{addr}/channels",
		"/devices/{addr}/channels/{no}",
		"/devices/{addr}/channels/{no}/event-groups",
		"/devices/{addr}/channels/{no}/data-points",
		"/devices/{addr}/channels/{no}/data-points/{param}",
		"/devices/{addr}/channels/{no}/data-points/{param}/value",
		"/devices/{addr}/paramsets/{key}",
		"/programs",
		"/programs/{id}/execute",
		"/sysvars",
		"/sysvars/{name}",
		"/alarm-messages",
		"/service-messages",
		"/hub/data-points",
		"/install-mode/interfaces",
		"/interfaces",
		"/interfaces/{id}",
		"/interfaces/{id}/reconnect",
		"/events",
		// additions — previously missing from OpenAPI.
		"/auth/oidc/start",
		"/auth/oidc/callback",
		"/functions",
		"/sessions/edit/take-over",
		// backfilled in the operational-maturity sweep so the REST
		// router can run with `OpenAPIValidate=true` by default.
		"/devices/{addr}/channels/{no}/week_profile",
		"/system/status",
	}
	missing := make([]string, 0)
	for _, p := range want {
		if _, ok := doc.Paths[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("openapi.yaml missing paths: %v", missing)
	}
}

// TestOpenAPIManagementPathsPresent pins the management and live-edit
// paths in the spec so a future router rename or refactor cannot
// silently drift away from the production OpenAPI validator middleware.
//
// Production daemons run with `OpenAPIValidate: true` by default —
// any path missing here would 404 every management request as
// "route not found in spec".
func TestOpenAPIManagementPathsPresent(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	specPath := filepath.Join(repoRoot, "assets", "openapi.yaml")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate spec: %v", err)
	}

	want := []struct {
		Path   string
		Method string
	}{
		{"/config/schema", "GET"},
		{"/config/effective", "GET"},
		{"/config/sections/{section}", "GET"},
		{"/config/sections/{section}", "PUT"},
		{"/config/sections/{section}", "DELETE"},
		{"/users", "GET"},
		{"/users", "POST"},
		{"/users/{subject}", "PATCH"},
		{"/users/{subject}", "DELETE"},
		{"/auth/tokens/v2", "GET"},
		{"/auth/tokens/v2", "POST"},
		{"/auth/tokens/v2/{fingerprint}", "DELETE"},
		{"/centrals", "GET"},
		{"/centrals", "POST"},
		{"/centrals/{name}", "GET"},
		{"/centrals/{name}", "PUT"},
		{"/centrals/{name}", "DELETE"},
	}

	for _, w := range want {
		item := doc.Paths.Find(w.Path)
		if item == nil {
			t.Errorf("missing path: %s", w.Path)
			continue
		}
		op := item.GetOperation(w.Method)
		if op == nil {
			t.Errorf("missing operation: %s %s", w.Method, w.Path)
		}
	}
}
