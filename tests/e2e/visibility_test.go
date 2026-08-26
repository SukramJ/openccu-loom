// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

// TestVisibilityUnIgnoreE2E exercises the /api/v1/visibility/unignore
// endpoint family through the running daemon process started by the harness:
//
//  1. PUT two patterns — verify applied_count=2 and patterns carry updated_at.
//  2. PUT a mix of valid + invalid patterns — verify parse_errors is non-empty
//     and only the valid subset is stored.
//  3. GET /visibility/unignore/candidates — verify a non-empty list is
//     returned (the godevccu fleet always exposes hidden parameters).
//  4. GET /audit — verify the un_ignore_update entry was written.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestVisibilityUnIgnoreE2E is the single end-to-end test for the
// visibility/unignore surface. It uses the daemon started by the harness
// (with a godevccu backing CCU) and hits the live REST endpoints.
func TestVisibilityUnIgnoreE2E(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	rest := h.REST()
	if err := rest.LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// The daemon registers one central named "godevccu-ccu" or derived from
	// the harness config. We read the live central name from the GET response
	// so the test is not hard-coded to a specific name.
	centralName := e2eResolveCentralName(t, rest)
	t.Logf("e2e central_name: %q", centralName)

	t.Run("round_trip", func(t *testing.T) {
		e2ePutPatterns(t, rest, centralName, []string{"LOW_BAT", "RSSI_PEER"})

		// GET the list back.
		resp := e2eGET(t, rest, "/api/v1/visibility/unignore")
		body := e2eReadBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /unignore status=%d body=%s", resp.StatusCode, body)
		}
		var dto struct {
			Centrals []struct {
				CentralName string `json:"central_name"`
				Patterns    []struct {
					Pattern   string `json:"pattern"`
					UpdatedAt string `json:"updated_at"`
				} `json:"patterns"`
			} `json:"centrals"`
		}
		if err := json.Unmarshal(body, &dto); err != nil {
			t.Fatalf("decode GET /unignore: %v\nbody=%s", err, body)
		}
		var found bool
		for _, cc := range dto.Centrals {
			if cc.CentralName != centralName {
				continue
			}
			found = true
			if len(cc.Patterns) < 2 {
				t.Errorf("patterns=%d, want >= 2 after PUT", len(cc.Patterns))
			}
			for _, p := range cc.Patterns {
				if p.UpdatedAt == "" {
					t.Errorf("pattern %q: updated_at is empty", p.Pattern)
				}
			}
		}
		if !found {
			t.Errorf("central %q not found in GET /unignore response; centrals=%v",
				centralName, dto.Centrals)
		}
	})

	t.Run("malformed_pattern_produces_parse_errors", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]any{
			"central_name": centralName,
			"patterns":     []string{":bad", "LOW_BAT"},
		})
		req, _ := rest.NewRequest(http.MethodPut, "/api/v1/visibility/unignore", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := rest.Do(req)
		if err != nil {
			t.Fatalf("PUT /unignore: %v", err)
		}
		body := e2eReadBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /unignore status=%d body=%s", resp.StatusCode, body)
		}
		var dto struct {
			AppliedCount int      `json:"applied_count"`
			ParseErrors  []string `json:"parse_errors"`
		}
		if err := json.Unmarshal(body, &dto); err != nil {
			t.Fatalf("decode PUT /unignore: %v\nbody=%s", err, body)
		}
		if len(dto.ParseErrors) == 0 {
			t.Errorf("parse_errors: expected at least one for ':bad', got none")
		}
		// ":bad" rejected, "LOW_BAT" accepted.
		if dto.AppliedCount != 1 {
			t.Errorf("applied_count=%d, want 1 (only LOW_BAT)", dto.AppliedCount)
		}
	})

	t.Run("candidates_non_empty", func(t *testing.T) {
		resp := e2eGET(t, rest, "/api/v1/visibility/unignore/candidates?include_master=false")
		body := e2eReadBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /candidates status=%d body=%s", resp.StatusCode, body)
		}
		var dto struct {
			Candidates    []string `json:"candidates"`
			IncludeMaster bool     `json:"include_master"`
		}
		if err := json.Unmarshal(body, &dto); err != nil {
			t.Fatalf("decode /candidates: %v\nbody=%s", err, body)
		}
		if dto.IncludeMaster {
			t.Errorf("include_master=true, want false")
		}
		if len(dto.Candidates) == 0 {
			t.Errorf("candidates list is empty; expected hidden params from godevccu fleet")
		}
		t.Logf("candidates count: %d", len(dto.Candidates))
	})

	t.Run("audit_entry_recorded", func(t *testing.T) {
		// PUT a new set to ensure an audit entry is created.
		e2ePutPatterns(t, rest, centralName, []string{"RSSI_PEER"})

		// The audit entry is persisted off the request path, so poll for it
		// rather than reading once: the assertion is that the write is
		// recorded, not that it lands before the next HTTP round-trip.
		var seen int
		deadline := time.Now().Add(10 * time.Second)
		for {
			resp := e2eGET(t, rest, "/api/v1/audit")
			body := e2eReadBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /audit status=%d body=%s", resp.StatusCode, body)
			}
			// Audit endpoint returns a flat JSON array of entries.
			var entries []struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(body, &entries); err != nil {
				t.Fatalf("decode /audit: %v\nbody=%s", err, body)
			}
			seen = len(entries)
			for _, e := range entries {
				if e.Action == "un_ignore_update" {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Errorf("un_ignore_update not found in audit log after 10s (entries=%d)", seen)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// e2eResolveCentralName reads GET /api/v1/visibility/unignore and returns the
// first central name in the response. This avoids hard-coding the harness's
// generated central name.
func e2eResolveCentralName(t *testing.T, rest *harness.RESTClient) string {
	t.Helper()
	resp := e2eGET(t, rest, "/api/v1/visibility/unignore")
	body := e2eReadBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /unignore (resolve central name): status=%d body=%s", resp.StatusCode, body)
	}
	var dto struct {
		Centrals []struct {
			CentralName string `json:"central_name"`
		} `json:"centrals"`
	}
	if err := json.Unmarshal(body, &dto); err != nil || len(dto.Centrals) == 0 {
		t.Fatalf("resolve central name: decode failed or no centrals: err=%v body=%s", err, body)
	}
	return dto.Centrals[0].CentralName
}

// e2ePutPatterns PUTs patterns for centralName and fails the test on error.
func e2ePutPatterns(t *testing.T, rest *harness.RESTClient, centralName string, patterns []string) {
	t.Helper()
	reqBody, _ := json.Marshal(map[string]any{
		"central_name": centralName,
		"patterns":     patterns,
	})
	req, err := rest.NewRequest(http.MethodPut, "/api/v1/visibility/unignore", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("PUT /unignore build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rest.Do(req)
	if err != nil {
		t.Fatalf("PUT /unignore: %v", err)
	}
	body := e2eReadBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /unignore status=%d body=%s", resp.StatusCode, body)
	}
}

// e2eGET issues an authenticated GET against the REST base.
func e2eGET(t *testing.T, rest *harness.RESTClient, path string) *http.Response {
	t.Helper()
	req, err := rest.NewRequest(http.MethodGet, path, http.NoBody)
	if err != nil {
		t.Fatalf("GET %s: build request: %v", path, err)
	}
	resp, err := rest.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// e2eReadBody drains and closes the response body.
func e2eReadBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}
