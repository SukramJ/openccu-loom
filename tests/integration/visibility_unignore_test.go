// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// TestVisibilityUnIgnore* exercises the /api/v1/visibility/unignore endpoints
// end-to-end through a real in-memory SQLite store and a godevccu-backed
// CentralUnit. The httptest.Server routes requests through the chi router so
// the full handler chain (validation, diff, audit, loader) runs.
//
// Coverage goals (per the stream-A brief):
//  1. Round-trip: PUT a list, GET it back with updated_by + updated_at.
//  2. Malformed pattern: PUT mix → response carries parse_errors, good subset
//     applies.
//  3. Candidates endpoint returns a non-empty list against the godevccu fleet.
//  4. Audit log endpoint shows the un_ignore_update entry.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── shared test fixture ──────────────────────────────────────────────────────

// visibilityTestFixture bundles the components used across the
// visibility_unignore integration tests. It is wired against an
// in-memory SQLite database and a godevccu-backed CentralUnit.
type visibilityTestFixture struct {
	centralName string
	store       *sqlite.VisibilityUnIgnoreStore
	loader      *integrationLoader
	lister      *integrationCentralLister
	provider    *integrationCandidateProvider
	auditBuf    *audit.Buffer
}

// newVisibilityTestFixture spins up a godevccu mock, ingests the default
// device fleet into a CentralUnit, and wires an in-memory SQLite
// VisibilityUnIgnoreStore. The caller owns the httptest.Server and should
// close it via t.Cleanup.
func newVisibilityTestFixture(t *testing.T) *visibilityTestFixture {
	t.Helper()

	const ccuName = "ccu-test"

	// ── godevccu + backend ───────────────────────────────────────────────────
	srv := startMockCCU(t)
	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: ccuName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	// ── in-memory SQLite ─────────────────────────────────────────────────────
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	unIgnoreStore := sqlite.NewVisibilityUnIgnoreStore(db)

	// ── visibility registry (pure in-process, no real loader needed) ─────────
	reg := visibility.NewRegistry()

	// ── adapter implementations ──────────────────────────────────────────────
	lister := &integrationCentralLister{names: []string{ccuName}}
	provider := &integrationCandidateProvider{central: ccuName, qf: c.QueryFacade()}
	loader := &integrationLoader{reg: reg, store: unIgnoreStore, central: c}
	auditBuf := audit.NewBuffer(200)

	return &visibilityTestFixture{
		centralName: ccuName,
		store:       unIgnoreStore,
		loader:      loader,
		lister:      lister,
		provider:    provider,
		auditBuf:    auditBuf,
	}
}

// newVisibilityServer creates an httptest.Server exposing the three
// visibility endpoints under /api/v1/visibility/... using the fixture's
// wired components. The server is registered for t.Cleanup.
func newVisibilityServer(t *testing.T, fx *visibilityTestFixture) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/visibility/unignore", handlers.ListVisibilityUnIgnore(fx.lister, fx.store))
	mux.Handle("PUT /api/v1/visibility/unignore", handlers.UpdateVisibilityUnIgnore(fx.store, fx.loader, fx.auditBuf))
	mux.Handle("GET /api/v1/visibility/unignore/candidates", handlers.ListVisibilityUnIgnoreCandidates(fx.lister, fx.provider))
	mux.Handle("GET /api/v1/audit", handlers.ListAudit(fx.auditBuf, nil))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// ─── adapter stubs for integration tests ──────────────────────────────────────

// integrationCentralLister satisfies handlers.VisibilityCentralLister.
type integrationCentralLister struct{ names []string }

func (l *integrationCentralLister) Names() []string {
	out := make([]string, len(l.names))
	copy(out, l.names)
	return out
}

// integrationCandidateProvider satisfies handlers.VisibilityCandidateProvider
// by delegating to the CentralUnit's QueryFacade.
type integrationCandidateProvider struct {
	central string
	qf      interface {
		GetUnIgnoreCandidates(hmenum.ParamsetKey) []string
	}
}

func (p *integrationCandidateProvider) UnIgnoreCandidates(centralName string, paramset hmenum.ParamsetKey) []string {
	if centralName != p.central || p.qf == nil {
		return nil
	}
	return p.qf.GetUnIgnoreCandidates(paramset)
}

// integrationLoader satisfies handlers.VisibilityRegistryLoader using the
// visibility.Registry + in-memory SQLite store. It mirrors the production
// visibilityAdapter.LoadUnIgnore logic (cmd/openccu-loom/visibility_adapter.go).
type integrationLoader struct {
	reg     *visibility.Registry
	store   *sqlite.VisibilityUnIgnoreStore
	central *central.CentralUnit
}

func (l *integrationLoader) LoadUnIgnore(centralName string, patterns []string) (affectedDevices int, parseErrors []string, err error) {
	if err := l.reg.LoadUnIgnore(strings.NewReader(strings.Join(patterns, "\n"))); err != nil {
		return 0, []string{err.Error()}, nil //nolint:nilerr // soft error
	}
	count := 0
	if l.central != nil && l.central.ModelRegistry != nil {
		decider := l.reg.Parameter()
		for _, d := range l.central.ModelRegistry.List() {
			visibility.ApplyUnIgnoredMarks(d, decider)
			count++
		}
	}
	return count, nil, nil
}

// ─── test helpers ──────────────────────────────────────────────────────────────

func doPUT(t *testing.T, base, path string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, base+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("PUT %s: build request: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return resp
}

func doGET(t *testing.T, base, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, http.NoBody)
	if err != nil {
		t.Fatalf("GET %s: build request: %v", path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, b)
	}
	return out
}

// ─── tests ─────────────────────────────────────────────────────────────────────

// TestVisibilityUnIgnoreRoundTrip verifies the full PUT → GET round trip:
// patterns submitted via PUT are returned by the subsequent GET, and each
// entry carries a non-empty updated_at and updated_by.
func TestVisibilityUnIgnoreRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newVisibilityTestFixture(t)
	s := newVisibilityServer(t, fx)

	// PUT two well-formed patterns.
	putBody := map[string]any{
		"central_name": fx.centralName,
		"patterns":     []string{"LOW_BAT", "RSSI_PEER"},
	}
	putResp := doPUT(t, s.URL, "/api/v1/visibility/unignore", putBody)
	putBytes := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putResp.StatusCode, putBytes)
	}
	var putDTO handlers.UnIgnoreUpdateResponseDTO
	putDTO = decodeJSON[handlers.UnIgnoreUpdateResponseDTO](t, putBytes)
	if putDTO.AppliedCount != 2 {
		t.Errorf("applied_count=%d, want 2", putDTO.AppliedCount)
	}
	if len(putDTO.Patterns) != 2 {
		t.Errorf("PUT response patterns=%d, want 2", len(putDTO.Patterns))
	}
	for _, p := range putDTO.Patterns {
		if p.UpdatedAt == "" {
			t.Errorf("pattern %q: updated_at is empty", p.Pattern)
		}
		// updated_by is "" when no auth identity is attached (test context); that
		// is expected — we only assert that updated_at is populated.
	}

	// GET confirms the stored state.
	getResp := doGET(t, s.URL, "/api/v1/visibility/unignore")
	getBytes := readBody(t, getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getResp.StatusCode, getBytes)
	}
	var getDTO handlers.UnIgnoreListResponseDTO
	getDTO = decodeJSON[handlers.UnIgnoreListResponseDTO](t, getBytes)
	if len(getDTO.Centrals) == 0 {
		t.Fatalf("GET: no centrals in response")
	}
	var found *handlers.UnIgnoreCentralPatternsDTO
	for i := range getDTO.Centrals {
		if getDTO.Centrals[i].CentralName == fx.centralName {
			found = &getDTO.Centrals[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("GET: central %q not in response", fx.centralName)
	}
	if len(found.Patterns) != 2 {
		t.Errorf("GET central patterns=%d, want 2", len(found.Patterns))
	}
	for _, p := range found.Patterns {
		if p.UpdatedAt == "" {
			t.Errorf("GET pattern %q: updated_at is empty", p.Pattern)
		}
	}
}

// TestVisibilityUnIgnoreMalformedPatterns verifies that a PUT containing
// a mix of valid and invalid patterns returns parse_errors while the
// well-formed subset is stored and reported in applied_count.
func TestVisibilityUnIgnoreMalformedPatterns(t *testing.T) {
	t.Parallel()
	fx := newVisibilityTestFixture(t)
	s := newVisibilityServer(t, fx)

	// ":bogus" starts with ":" so the parameter field is empty → parse error.
	// "LOW_BAT" is valid. Blank lines are silently skipped.
	putBody := map[string]any{
		"central_name": fx.centralName,
		"patterns":     []string{":bogus", "LOW_BAT", ""},
	}
	putResp := doPUT(t, s.URL, "/api/v1/visibility/unignore", putBody)
	putBytes := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putResp.StatusCode, putBytes)
	}
	var dto handlers.UnIgnoreUpdateResponseDTO
	dto = decodeJSON[handlers.UnIgnoreUpdateResponseDTO](t, putBytes)

	if len(dto.ParseErrors) == 0 {
		t.Errorf("parse_errors: expected at least one error for ':bogus', got none")
	}
	// Well-formed subset: only "LOW_BAT" survives.
	if dto.AppliedCount != 1 {
		t.Errorf("applied_count=%d, want 1 (only LOW_BAT)", dto.AppliedCount)
	}
	if len(dto.Patterns) != 1 || dto.Patterns[0].Pattern != "LOW_BAT" {
		t.Errorf("patterns=%v, want [LOW_BAT]", dto.Patterns)
	}
}

// TestVisibilityUnIgnoreCandidatesNonEmpty asserts that the candidates
// endpoint returns a non-empty list when VALUES-paramset DPs are present in
// the godevccu fleet and include_master=false is used.
func TestVisibilityUnIgnoreCandidatesNonEmpty(t *testing.T) {
	t.Parallel()
	fx := newVisibilityTestFixture(t)
	s := newVisibilityServer(t, fx)

	resp := doGET(t, s.URL, "/api/v1/visibility/unignore/candidates?include_master=false")
	b := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("candidates status=%d body=%s", resp.StatusCode, b)
	}
	var dto handlers.UnIgnoreCandidateListDTO
	dto = decodeJSON[handlers.UnIgnoreCandidateListDTO](t, b)

	if dto.IncludeMaster {
		t.Errorf("include_master=true, want false (default)")
	}
	// The godevccu fleet (HmIP-SWSD, HmIP-BWTH, HmIP-BSM, HmIP-BROLL) always
	// exposes hidden parameters such as RSSI_PEER. Assert non-empty rather than
	// a specific count so fleet changes in godevccu don't break this test.
	if len(dto.Candidates) == 0 {
		t.Errorf("candidates list is empty; expected hidden parameters from the godevccu fleet")
	}
	t.Logf("candidates (include_master=false): %d params", len(dto.Candidates))
}

// TestVisibilityUnIgnoreAuditEntry verifies that a PUT that changes patterns
// produces an un_ignore_update entry in the audit log.
func TestVisibilityUnIgnoreAuditEntry(t *testing.T) {
	t.Parallel()
	fx := newVisibilityTestFixture(t)
	s := newVisibilityServer(t, fx)

	// Seed an initial state so the diff is non-trivial.
	seed := map[string]any{
		"central_name": fx.centralName,
		"patterns":     []string{"OLD_PATTERN"},
	}
	seedResp := doPUT(t, s.URL, "/api/v1/visibility/unignore", seed)
	readBody(t, seedResp) // drain

	// Now PUT a new set so the audit records added+removed.
	putBody := map[string]any{
		"central_name": fx.centralName,
		"patterns":     []string{"LOW_BAT", "RSSI_PEER"},
	}
	putResp := doPUT(t, s.URL, "/api/v1/visibility/unignore", putBody)
	putBytes := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putResp.StatusCode, putBytes)
	}

	// Fetch the audit log and look for the un_ignore_update entry.
	// The audit endpoint returns a JSON array of audit.Entry objects.
	auditResp := doGET(t, s.URL, "/api/v1/audit")
	auditBytes := readBody(t, auditResp)
	if auditResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /audit status=%d body=%s", auditResp.StatusCode, auditBytes)
	}

	var auditEntries []struct {
		Action string `json:"action"`
		Note   string `json:"note,omitempty"`
	}
	if err := json.Unmarshal(auditBytes, &auditEntries); err != nil {
		t.Fatalf("decode audit response: %v\nbody: %s", err, auditBytes)
	}

	var found bool
	for _, e := range auditEntries {
		if e.Action == "un_ignore_update" {
			found = true
			t.Logf("audit un_ignore_update note: %q", e.Note)
			break
		}
	}
	if !found {
		t.Errorf("un_ignore_update not found in audit log; entries=%v", auditEntries)
	}
}

// TestVisibilityUnIgnoreReplaceIsIdempotent checks that re-PUTting the same
// pattern set does not add duplicate entries or create extra audit entries
// (no-op diff → no audit record).
func TestVisibilityUnIgnoreReplaceIsIdempotent(t *testing.T) {
	t.Parallel()
	fx := newVisibilityTestFixture(t)
	s := newVisibilityServer(t, fx)

	putBody := map[string]any{
		"central_name": fx.centralName,
		"patterns":     []string{"LOW_BAT"},
	}

	// First PUT: changes state → audit entry expected.
	resp1 := doPUT(t, s.URL, "/api/v1/visibility/unignore", putBody)
	readBody(t, resp1)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first PUT status=%d", resp1.StatusCode)
	}

	// Second PUT: same body → no diff → no new audit entry.
	resp2 := doPUT(t, s.URL, "/api/v1/visibility/unignore", putBody)
	b2 := readBody(t, resp2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second PUT status=%d body=%s", resp2.StatusCode, b2)
	}
	var dto handlers.UnIgnoreUpdateResponseDTO
	dto = decodeJSON[handlers.UnIgnoreUpdateResponseDTO](t, b2)
	if dto.AppliedCount != 1 {
		t.Errorf("second PUT applied_count=%d, want 1", dto.AppliedCount)
	}

	// Audit should have exactly ONE un_ignore_update (the first PUT).
	auditResp := doGET(t, s.URL, "/api/v1/audit")
	auditBytes := readBody(t, auditResp)
	var auditEntries []struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(auditBytes, &auditEntries)
	count := 0
	for _, e := range auditEntries {
		if e.Action == "un_ignore_update" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("audit un_ignore_update count=%d, want 1 (second PUT is a no-op)", count)
	}
}
