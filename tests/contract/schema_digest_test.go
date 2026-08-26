// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// contractAssets is the closed list of files that define the
// north-bound wire contract. Must match the list in
// script/generate_schema_digest.go and script/check_api_version_bump.sh.
var contractAssets = []string{
	"assets/openapi.yaml",
	"assets/schemas/enums.json",
	"assets/schemas/types.json",
	"assets/wsapi.json",
}

func contractRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestSchemaDigestFresh recomputes the contract digest from the repo
// assets and fails when the generated constant served by
// `GET /api/v1/info` has gone stale. Fix: `make export-schemas`
// (which regenerates internal/north/rest/handlers/schema_digest_gen.go).
func TestSchemaDigestFresh(t *testing.T) {
	root := contractRepoRoot(t)
	combined := sha256.New()
	for _, rel := range contractAssets {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read contract asset: %v", err)
		}
		fileSum := sha256.Sum256(data)
		fmt.Fprintf(combined, "%s\n%x\n", rel, fileSum)
	}
	want := fmt.Sprintf("sha256:%x", combined.Sum(nil))
	if handlers.SchemaDigest != want {
		t.Fatalf("generated SchemaDigest is stale:\n  have %s\n  want %s\n"+
			"a contract asset changed without regenerating — run `make export-schemas`",
			handlers.SchemaDigest, want)
	}
}

// TestOpenAPIInfoVersionMatchesAPIVersion pins the two declarations of
// the contract version to each other: the OpenAPI document's
// `info.version` and the [handlers.APIVersion] constant served by
// `GET /api/v1/info`. Diverging values would let a client pin against
// a version the daemon never reports.
func TestOpenAPIInfoVersionMatchesAPIVersion(t *testing.T) {
	root := contractRepoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if doc.Info.Version != handlers.APIVersion {
		t.Fatalf("openapi.yaml info.version=%q != handlers.APIVersion=%q — bump both together",
			doc.Info.Version, handlers.APIVersion)
	}
}
