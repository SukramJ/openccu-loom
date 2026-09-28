// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// updateWireFixtures regenerates assets/wire-fixtures/ from the handlers.
var updateWireFixtures = flag.Bool("update-wire-fixtures", false,
	"rewrite assets/wire-fixtures/ from the live handler output")

// wireFixture names one recorded daemon response. The fixture file is the
// handler's own output — recorded, never hand-written — with the fields in
// volatile masked, because they change on every run (clocks, uptimes).
// status names the recorded response code; schemaPath/"" means the body is
// validated against that operation's response schema in openapi.yaml, and
// "Problem" validates against the shared problem+json component instead
// (error responses are not declared per-operation everywhere).
type wireFixture struct {
	file     string
	method   string
	path     string // openapi path, also the request path (no params in the corpus yet)
	status   int
	schema   string // "" = the operation's response schema; "Problem" = components/schemas/Problem
	auth     bool   // send a valid bearer token
	volatile []string
}

// wireFixtureCorpus is the recorded wire corpus external clients parse.
// assets/wire-fixtures/README.md carries the contract; the rule is
// occulited's: if one side changes the semantics without changing a
// fixture here, that is the bug.
var wireFixtureCorpus = []wireFixture{
	{
		file: "info.json", method: http.MethodGet, path: "/info", status: 200,
		volatile: []string{"uptime", "started_at"},
	},
	{file: "health.json", method: http.MethodGet, path: "/health", status: 200},
	{file: "warnings-empty.json", method: http.MethodGet, path: "/warnings", status: 200, auth: true},
	{file: "sbom.json", method: http.MethodGet, path: "/sbom", status: 200, auth: true},
	{file: "problem-unauthorized.json", method: http.MethodGet, path: "/warnings", status: 401, schema: "Problem"},
	{file: "problem-not-found.json", method: http.MethodGet, path: "/nowhere", status: 404, schema: "Problem"},
}

// corpusHealth backs /health for the corpus: one healthy component, so
// the recorded body carries the real row shape instead of an empty list.
type corpusHealth struct{}

func (corpusHealth) Overall() health.Status { return health.StatusHealthy }
func (corpusHealth) Snapshot() []health.Component {
	return []health.Component{{Name: "rest", Status: health.StatusHealthy}}
}

// wireCorpusRouter builds the same fully wired router the other walks use,
// with the production auth chain so the corpus records real 401 bodies.
func wireCorpusRouter() http.Handler {
	deps := fullyWiredRouterDeps()
	deps.Health = corpusHealth{}
	mw := auth.NewMiddleware(nil, auth.NewMemoryTokenStore(map[string]auth.Identity{
		"corpus-token": {Subject: "corpus", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin},
	}))
	deps.AuthResolve = mw.Resolve
	deps.AuthRequire = mw.Require
	deps.RequireOperator = func(next http.Handler) http.Handler {
		return mw.RequireRole(auth.RoleOperator, next)
	}
	deps.RequireAdmin = func(next http.Handler) http.Handler {
		return mw.RequireRole(auth.RoleAdmin, next)
	}
	return rest.NewRouter(deps)
}

// maskVolatile replaces each named top-level field with a stable
// placeholder so the corpus is byte-stable across runs. Only top-level
// fields are volatile in the current corpus; a nested need extends this.
func maskVolatile(body map[string]any, fields []string) {
	for _, f := range fields {
		if _, ok := body[f]; ok {
			body[f] = "<volatile>"
		}
	}
}

// TestWireFixtureCorpusMatchesHandlers records every corpus operation
// against the fully wired router and compares the (volatile-masked)
// response verbatim with the committed fixture, after validating the raw
// response against openapi.yaml. The fixtures are the cross-language
// contract corpus: openccu-loom-client parses these files in its own
// test suite, so a payload change fails visibly on both sides.
func TestWireFixtureCorpusMatchesHandlers(t *testing.T) {
	router := wireCorpusRouter()
	spec := loadOpenAPISpec(t)
	dir := filepath.Join(repoRoot(t), "assets", "wire-fixtures")

	for _, fx := range wireFixtureCorpus {
		t.Run(fx.file, func(t *testing.T) {
			req := httptest.NewRequest(fx.method, "/api/v1"+fx.path, http.NoBody)
			if fx.auth {
				req.Header.Set("Authorization", "Bearer corpus-token")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != fx.status {
				t.Fatalf("%s %s answered %d, corpus expects %d: %s", fx.method, fx.path, rec.Code, fx.status, rec.Body.String())
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not a JSON object: %v", err)
			}

			// Validate the RAW response before masking.
			switch fx.schema {
			case "Problem":
				problemSchema := spec.Components.Schemas["Problem"]
				if problemSchema == nil || problemSchema.Value == nil {
					t.Fatal("components/schemas/Problem missing from openapi.yaml")
				}
				if err := problemSchema.Value.VisitJSON(body); err != nil {
					t.Errorf("response does not match the Problem schema: %v", err)
				}
			case "":
				item := spec.Paths.Find(fx.path)
				if item == nil {
					t.Fatalf("%s not in openapi.yaml", fx.path)
				}
				op := item.GetOperation(fx.method)
				resp := op.Responses.Value(strconv.Itoa(fx.status))
				if resp == nil || resp.Value == nil {
					t.Fatalf("%s %s has no %d response in openapi.yaml", fx.method, fx.path, fx.status)
				}
				mt := resp.Value.Content.Get("application/json")
				if err := mt.Schema.Value.VisitJSON(body); err != nil {
					t.Errorf("response does not match the %s %s %d schema: %v", fx.method, fx.path, fx.status, err)
				}
			default:
				t.Fatalf("unknown schema selector %q", fx.schema)
			}

			maskVolatile(body, fx.volatile)
			got, err := json.MarshalIndent(body, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')

			path := filepath.Join(dir, fx.file)
			if *updateWireFixtures {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture (regenerate with -update-wire-fixtures): %v", err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("the wire payload changed — update assets/wire-fixtures/%s (run with "+
					"-update-wire-fixtures) AND tell openccu-loom-client, whose parser corpus this is.\ngot:\n%s\nwant:\n%s",
					fx.file, got, want)
			}
		})
	}

	// The corpus directory must not carry stray files the table does not
	// own — a fixture nobody records is a fixture nobody keeps true.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus dir: %v", err)
	}
	known := map[string]bool{"README.md": true}
	for _, fx := range wireFixtureCorpus {
		known[fx.file] = true
	}
	var stray []string
	for _, e := range entries {
		if !known[e.Name()] {
			stray = append(stray, e.Name())
		}
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		t.Errorf("assets/wire-fixtures/ carries files no corpus entry records: %v", stray)
	}
}
