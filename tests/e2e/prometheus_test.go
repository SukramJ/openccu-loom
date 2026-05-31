// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestPrometheusMetricsExposed asserts that the daemon's
// /api/v1/metrics endpoint serves a well-formed Prometheus
// text-exposition payload. The daemon's metrics registry
// (`internal/metrics`) populates lazily on event-bus traffic, so a
// fresh-from-bring-up snapshot is allowed to be empty — what we
// really pin is:
//
//   - HTTP 200 with a Content-Type beginning with `text/plain`
//     (Prometheus convention; the `version=0.0.4` suffix is allowed
//     to evolve).
//   - Authentication is enforced — anonymous /metrics gets 401.
//   - Body, if non-empty, parses cleanly: every non-blank,
//     non-comment line carries a `<name> <value>` separated by
//     whitespace; HELP and TYPE comment lines balance against
//     sample lines.
//
// Pulling in `prometheus/common/expfmt` for a stricter parser is
// rejected — adding a dep just for one test trips ADR scrutiny.
// The canonical line shape is stable enough to validate by hand.
func TestPrometheusMetricsExposed(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})

	// 1. Anonymous request must be rejected.
	resp401, err := h.REST().HTTPClient().Get(h.RESTBase() + "/api/v1/metrics")
	if err != nil {
		t.Fatalf("GET /metrics anon: %v", err)
	}
	resp401.Body.Close()
	if resp401.StatusCode != http.StatusUnauthorized {
		t.Errorf("anon /metrics: status=%d, want 401", resp401.StatusCode)
	}

	// 2. Authenticated request must succeed and respect the wire shape.
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/metrics: status=%d body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type=%q, want text/plain prefix", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Empty body is acceptable for a fresh registry. When samples
	// ARE present, the line categorisation must match the
	// Prometheus exposition format invariants.
	if len(body) == 0 {
		t.Logf("/metrics: empty body (registry has not yet seen events)")
		return
	}
	text := string(body)
	helpLines, typeLines, sampleLines := categoriseMetricLines(t, text)
	if sampleLines == 0 {
		t.Errorf("/metrics body non-empty but no sample lines:\n%s", truncate(text, 400))
	}
	// HELP and TYPE are per-metric headers; they should never
	// outnumber the samples below them.
	if typeLines > sampleLines {
		t.Errorf("/metrics: %d TYPE lines vs %d samples — orphaned metadata?", typeLines, sampleLines)
	}
	if helpLines > sampleLines {
		t.Errorf("/metrics: %d HELP lines vs %d samples — orphaned metadata?", helpLines, sampleLines)
	}
}

// categoriseMetricLines walks the text-exposition body once and
// counts the three line kinds. Any line that is not blank, not a
// comment, and not a `# HELP` / `# TYPE` directive must look like a
// sample (a metric name followed by whitespace and a value).
func categoriseMetricLines(t *testing.T, text string) (helpLines, typeLines, sampleLines int) {
	t.Helper()
	for i, line := range strings.Split(text, "\n") {
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "# HELP"):
			helpLines++
		case strings.HasPrefix(line, "# TYPE"):
			typeLines++
		case strings.HasPrefix(line, "#"):
			// Other comments are allowed and ignored.
			continue
		default:
			// Sample line — must have at least one whitespace
			// separator (between the labelled metric and its value).
			if !strings.ContainsAny(line, " \t") {
				t.Errorf("line %d: malformed sample %q", i+1, line)
				continue
			}
			sampleLines++
		}
	}
	return helpLines, typeLines, sampleLines
}
