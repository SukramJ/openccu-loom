// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestOpenAPISpecIsValid runs the full kin-openapi validator on
// assets/openapi.yaml, verifying the spec is structurally valid.
func TestOpenAPISpecIsValid(t *testing.T) {
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
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%s", doc.OpenAPI)
	}
	if len(doc.Paths.Map()) < 20 {
		t.Fatalf("expected ≥20 paths, got %d", len(doc.Paths.Map()))
	}
}
