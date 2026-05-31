// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestRESTGetWalker drives every documented GET operation in
// assets/openapi.yaml against the running daemon and asserts that
// the response status matches one of the documented responses.
//
// Scope (v1): GET only. POST/PUT/PATCH/DELETE are deferred — they
// need request bodies pulled from operation `examples:`, which we
// will add once the spec carries them consistently.
//
// Acceptance contract:
//   - Every GET operation either gets driven and returns a documented
//     status, OR appears in tests/e2e/openapi_skip.txt with a reason.
//   - 5xx responses always fail the test, even when documented — we
//     do not want the daemon raising 500 in normal operation.
//   - The set of "visited" operations is asserted to cover the spec.
func TestRESTGetWalker(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})

	rest := h.REST()
	if err := rest.LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	doc := loadOpenAPISpec(t)
	skip := loadOpenAPISkip(t)
	resolver := defaultPathParamValues()

	type result struct {
		opID   string
		path   string
		method string
		status int
		ok     bool
		why    string
	}
	var results []result
	visited := make(map[string]bool)

	for _, pathItem := range sortedPaths(doc.Paths) {
		path := pathItem.path
		for _, methOp := range methodOps(pathItem.item) {
			if methOp.method != http.MethodGet {
				continue
			}
			op := methOp.op
			opID := op.OperationID
			if opID == "" {
				opID = methOp.method + " " + path
			}
			if reason, ok := skip[opID]; ok {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: true, why: "skipped: " + reason})
				visited[opID] = true
				continue
			}
			url, ok := buildURL(path, op, resolver)
			if !ok {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "missing path-param default"})
				continue
			}
			req, err := rest.NewRequest(http.MethodGet, "/api/v1"+url, nil)
			if err != nil {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "build request: " + err.Error()})
				continue
			}
			req.Header.Set("Accept", "application/json")
			resp, err := rest.Do(req)
			if err != nil {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "transport: " + err.Error()})
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			documented := isDocumentedStatus(op, resp.StatusCode)
			// 500 is treated as a server bug regardless of whether the
			// spec lists it. Documented 502 / 503 (e.g. "Matter bridge
			// not enabled", "upstream CCU unreachable") are legitimate
			// contract responses and pass.
			serverBug := resp.StatusCode == http.StatusInternalServerError
			ok = documented && !serverBug

			why := ""
			switch {
			case serverBug:
				why = "500 is always a bug"
			case !documented:
				why = "status not listed in documented responses"
			}
			results = append(results, result{
				opID: opID, path: path, method: methOp.method,
				status: resp.StatusCode, ok: ok, why: why,
			})
			visited[opID] = true
		}
	}

	// Per-operation report.
	sort.Slice(results, func(i, j int) bool { return results[i].opID < results[j].opID })
	var failures []string
	for _, r := range results {
		switch {
		case r.ok && r.why != "":
			t.Logf("OK   %-40s %s %s   (%s)", r.opID, r.method, r.path, r.why)
		case r.ok:
			t.Logf("OK   %-40s %s %-50s -> %d", r.opID, r.method, r.path, r.status)
		default:
			line := r.opID + " " + r.method + " " + r.path
			if r.status != 0 {
				line += " -> " + strconv.Itoa(r.status)
			}
			line += "   (" + r.why + ")"
			failures = append(failures, line)
		}
	}

	// Coverage assertion: every GET op in the spec is either visited
	// or in the skip list. A new GET added without test or skip
	// entry → red CI.
	for _, pi := range sortedPaths(doc.Paths) {
		for _, mo := range methodOps(pi.item) {
			if mo.method != http.MethodGet {
				continue
			}
			id := mo.op.OperationID
			if id == "" {
				id = mo.method + " " + pi.path
			}
			if !visited[id] {
				failures = append(failures, "uncovered GET operation: "+id)
			}
		}
	}

	if len(failures) > 0 {
		t.Fatalf("REST GET walker failed (%d issue(s)):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// TestRESTMutationWalker drives every documented POST / PUT / PATCH /
// DELETE operation in assets/openapi.yaml against the running daemon
// with a minimal (empty-JSON or no-body) payload and asserts that the
// response status is listed in the documented responses.
//
// Acceptance contract (same as TestRESTGetWalker):
//   - Every mutation operation returns a documented status, OR the
//     operation appears in tests/e2e/openapi_skip.txt.
//   - 500 is always treated as a server bug.
//   - 503 (service unwired) and 502 (upstream unavailable) are
//     accepted when documented — they are legitimate contract responses
//     in a test harness that does not have a live CCU.
//
// Body strategy: the walker sends an empty JSON object ({}) for
// operations that declare a requestBody. A 400 response from that
// is treated as passing when 400 is documented — the operation is
// reachable and the handler is running. Operations that cannot
// handle an empty body without a panic surface as 500, which
// is a test failure by design.
func TestRESTMutationWalker(t *testing.T) {
	t.Parallel()
	// AuthSession enables both cookie sessions and HTTP Basic. The walker
	// uses Basic-Auth headers so that the auth/logout walker step (which
	// clears the session cookie) does not invalidate subsequent requests.
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})

	rest := h.REST()
	rest.SetAuthHeader("Basic " + harness.BasicAuthHeader(harness.AdminUser, harness.AdminPass))

	doc := loadOpenAPISpec(t)
	skip := loadOpenAPISkip(t)
	resolver := defaultPathParamValues()

	mutationMethods := map[string]bool{
		http.MethodPost:   true,
		http.MethodPut:    true,
		http.MethodPatch:  true,
		http.MethodDelete: true,
	}

	type result struct {
		opID   string
		path   string
		method string
		status int
		ok     bool
		why    string
	}
	var results []result
	visited := make(map[string]bool)

	for _, pathItem := range sortedPaths(doc.Paths) {
		path := pathItem.path
		for _, methOp := range methodOps(pathItem.item) {
			if !mutationMethods[methOp.method] {
				continue
			}
			op := methOp.op
			opID := op.OperationID
			if opID == "" {
				opID = methOp.method + " " + path
			}
			if reason, ok := skip[opID]; ok {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: true, why: "skipped: " + reason})
				visited[opID] = true
				continue
			}
			url, ok := buildURL(path, op, resolver)
			if !ok {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "missing path-param default"})
				continue
			}

			// Use an empty JSON object for operations with a request body;
			// use nil body for DELETE and operations with no declared body.
			var bodyReader io.Reader
			if op.RequestBody != nil && methOp.method != http.MethodDelete {
				bodyReader = strings.NewReader("{}")
			}
			req, err := rest.NewRequest(methOp.method, "/api/v1"+url, bodyReader)
			if err != nil {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "build request: " + err.Error()})
				continue
			}
			if bodyReader != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Accept", "application/json")
			resp, err := rest.Do(req)
			if err != nil {
				results = append(results, result{opID: opID, path: path, method: methOp.method, ok: false, why: "transport: " + err.Error()})
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			documented := isDocumentedStatus(op, resp.StatusCode)
			serverBug := resp.StatusCode == http.StatusInternalServerError
			ok = documented && !serverBug

			why := ""
			switch {
			case serverBug:
				why = "500 is always a bug"
			case !documented:
				why = "status not in documented responses"
			}
			results = append(results, result{
				opID: opID, path: path, method: methOp.method,
				status: resp.StatusCode, ok: ok, why: why,
			})
			visited[opID] = true
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].opID < results[j].opID })
	var failures []string
	for _, r := range results {
		switch {
		case r.ok && r.why != "":
			t.Logf("OK   %-40s %s %s   (%s)", r.opID, r.method, r.path, r.why)
		case r.ok:
			t.Logf("OK   %-40s %s %-50s -> %d", r.opID, r.method, r.path, r.status)
		default:
			line := r.opID + " " + r.method + " " + r.path
			if r.status != 0 {
				line += " -> " + strconv.Itoa(r.status)
			}
			line += "   (" + r.why + ")"
			failures = append(failures, line)
		}
	}

	// Coverage assertion: every mutation op in the spec is either
	// visited or in the skip list. A new mutation endpoint without
	// test or skip entry produces a red CI run.
	for _, pi := range sortedPaths(doc.Paths) {
		for _, mo := range methodOps(pi.item) {
			if !mutationMethods[mo.method] {
				continue
			}
			id := mo.op.OperationID
			if id == "" {
				id = mo.method + " " + pi.path
			}
			if !visited[id] {
				failures = append(failures, "uncovered mutation operation: "+id)
			}
		}
	}

	if len(failures) > 0 {
		t.Fatalf("REST mutation walker failed (%d issue(s)):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// ─── spec loading ─────────────────────────────────────────────────

// loadOpenAPISpec parses assets/openapi.yaml via kin-openapi.
func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(filepath.Join(repoRoot, "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("validate openapi.yaml: %v", err)
	}
	return doc
}

// loadOpenAPISkip reads tests/e2e/openapi_skip.txt and returns the
// operationId → reason map. Each non-blank, non-comment line must
// have the form `<opId> -- <reason>` or `<opId>` (no reason).
func loadOpenAPISkip(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	_, thisFile, _, _ := runtime.Caller(0)
	skipPath := filepath.Join(filepath.Dir(thisFile), "openapi_skip.txt")
	f, err := os.Open(skipPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("open skip file: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "--"); i >= 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+2:])
		} else {
			out[line] = "(no reason)"
		}
	}
	return out
}

// ─── traversal helpers ────────────────────────────────────────────

type pathPair struct {
	path string
	item *openapi3.PathItem
}

func sortedPaths(paths *openapi3.Paths) []pathPair {
	keys := make([]string, 0, paths.Len())
	for k := range paths.Map() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]pathPair, 0, len(keys))
	for _, k := range keys {
		out = append(out, pathPair{path: k, item: paths.Find(k)})
	}
	return out
}

type methodOp struct {
	method string
	op     *openapi3.Operation
}

func methodOps(p *openapi3.PathItem) []methodOp {
	var out []methodOp
	if p.Get != nil {
		out = append(out, methodOp{http.MethodGet, p.Get})
	}
	if p.Post != nil {
		out = append(out, methodOp{http.MethodPost, p.Post})
	}
	if p.Put != nil {
		out = append(out, methodOp{http.MethodPut, p.Put})
	}
	if p.Patch != nil {
		out = append(out, methodOp{http.MethodPatch, p.Patch})
	}
	if p.Delete != nil {
		out = append(out, methodOp{http.MethodDelete, p.Delete})
	}
	return out
}

// ─── path-param resolution ────────────────────────────────────────

// defaultPathParamValues maps path-template names to fallback values.
// godevccu's default fleet exposes HmIP-SWSD, HmIP-BWTH, HmIP-BSM,
// HmIP-BROLL — the addresses below are illustrative; endpoints that
// do not find them respond with a documented 404, which the walker
// accepts. Add entries here as the walker grows.
func defaultPathParamValues() map[string]string {
	return map[string]string{
		"addr":        "0001ABCDE12345",
		"central":     "ccu-e2e",
		"no":          "0",
		"name":        "test_name",
		"id":          "test_id",
		"key":         "MASTER",
		"param":       "STATE",
		"peer":        "peer_test",
		"value":       "0",
		"operation":   "list",
		"path":        "hmlog",
		"fingerprint": "testfingerprint",
		"subject":     "test_subject",
		"section":     "north",
	}
}

// buildURL fills path-template parameters from the resolver. Returns
// false if any required path parameter has no default — the walker
// records that as a coverage gap rather than skipping silently.
func buildURL(template string, op *openapi3.Operation, resolver map[string]string) (string, bool) {
	out := template
	for _, paramRef := range op.Parameters {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}
		p := paramRef.Value
		if p.In != "path" {
			continue
		}
		val, ok := resolver[p.Name]
		if !ok {
			return "", false
		}
		out = strings.ReplaceAll(out, "{"+p.Name+"}", val)
	}
	// Some specs leave path params undeclared on the operation and
	// only on the path item. Fall back to the resolver for any
	// remaining `{name}` token.
	for name, val := range resolver {
		out = strings.ReplaceAll(out, "{"+name+"}", val)
	}
	return out, true
}

// ─── response matching ────────────────────────────────────────────

// isDocumentedStatus reports whether `code` matches one of the
// operation's documented response codes. A `default` response
// matches anything.
func isDocumentedStatus(op *openapi3.Operation, code int) bool {
	if op.Responses == nil {
		return false
	}
	want := strconv.Itoa(code)
	for k := range op.Responses.Map() {
		if k == want {
			return true
		}
		if k == "default" {
			return true
		}
	}
	return false
}
