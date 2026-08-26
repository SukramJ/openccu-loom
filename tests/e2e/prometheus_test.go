// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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

	// An empty body used to be accepted here "for a fresh registry",
	// which made the whole format check conditional on data the test does
	// not control — and a pass on an empty body is a pass that measured
	// nothing. The daemon registers its metrics during bring-up, so a
	// body arrives; if one ever does not, that is the finding, not an
	// excuse to skip the parse.
	if len(body) == 0 {
		t.Fatal("/metrics returned an empty body — the registry exposed nothing at all, " +
			"which a scraper cannot distinguish from a daemon with no traffic")
	}
	if err := parsePrometheusExposition(string(body)); err != nil {
		t.Errorf("/metrics is not valid Prometheus text exposition: %v\n\n%s",
			err, truncate(string(body), 600))
	}
}

// TestPrometheusExpositionParserRejectsMalformedBodies is the negative
// control for the check above, and the reason it exists.
//
// The previous validation counted line kinds and required a sample line to
// contain whitespace. `%%%bad{{{ 1` satisfied that, so the check returned
// the same verdict for a valid exposition and for one Prometheus would
// reject outright — it could not fail for the reason it was written. A
// parser is only worth its assertion once it is shown to disagree.
func TestPrometheusExpositionParserRejectsMalformedBodies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"invalid metric name", "%%%bad{{{ 1\n"},
		{"metric name starting with a digit", "9lives 1\n"},
		{"invalid label name", "m{1bad=\"x\"} 1\n"},
		{"unterminated label block", "m{a=\"x\" 1\n"},
		{"unquoted label value", "m{a=x} 1\n"},
		{"escape Prometheus does not define", "m{a=\"x\\ty\"} 1\n"},
		{"value is not a number", "m 1.2.3\n"},
		{"sample with no value", "m\n"},
		{"TYPE naming a kind that does not exist", "# TYPE m sparkline\nm 1\n"},
		{"two TYPE lines for one family", "# TYPE m gauge\n# TYPE m counter\nm 1\n"},
		{"TYPE after the family's samples", "m 1\n# TYPE m gauge\n"},
		{"duplicate series", "m{a=\"x\"} 1\nm{a=\"x\"} 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := parsePrometheusExposition(tc.body); err == nil {
				t.Errorf("parser accepted %q — it cannot fail for the reason it exists", tc.body)
			}
		})
	}

	// The positive half: a body exercising every shape the daemon emits
	// must pass, or the parser is merely strict rather than correct.
	good := "# HELP m_total how many\n" +
		"# TYPE m_total counter\n" +
		"m_total 12\n" +
		"# HELP g_thing a gauge\n" +
		"# TYPE g_thing gauge\n" +
		"g_thing{central=\"ccu-eg\"} 1.5\n" +
		"g_thing{central=\"ccu_og\"} -0.25\n" +
		"# TYPE e_scaped gauge\n" +
		"e_scaped{note=\"a \\\"quote\\\", a \\\\backslash and a \\nnewline\"} 0\n"
	if err := parsePrometheusExposition(good); err != nil {
		t.Errorf("parser rejected a valid exposition: %v", err)
	}
}

// parsePrometheusExposition validates a body the way a scraper does, and
// returns the first thing Prometheus would reject.
//
// It is written out rather than taken from `prometheus/common/expfmt`
// because adding a dependency for one test trips ADR scrutiny — the same
// call the previous version of this file made. What changed is the
// standard: "the canonical line shape is stable enough to validate by
// hand" was used to justify checking that a sample line contains a space,
// and that is not validation. The rules below are the text-format 0.0.4
// ones the daemon's hand-written renderer in internal/metrics/registry.go
// has to satisfy.
func parsePrometheusExposition(text string) error {
	types := map[string]string{}
	sampled := map[string]bool{}
	series := map[string]bool{}

	for i, line := range strings.Split(text, "\n") {
		ln := i + 1
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue // a bare comment
			}
			switch fields[1] {
			case "HELP":
				if len(fields) < 3 {
					return fmt.Errorf("line %d: HELP without a metric name: %q", ln, line)
				}
				if err := validMetricName(fields[2]); err != nil {
					return fmt.Errorf("line %d: %w", ln, err)
				}
				if sampled[fields[2]] {
					return fmt.Errorf("line %d: HELP for %q after its samples", ln, fields[2])
				}
			case "TYPE":
				if len(fields) < 4 {
					return fmt.Errorf("line %d: TYPE needs a name and a kind: %q", ln, line)
				}
				name, kind := fields[2], fields[3]
				if err := validMetricName(name); err != nil {
					return fmt.Errorf("line %d: %w", ln, err)
				}
				switch kind {
				case "counter", "gauge", "histogram", "summary", "untyped":
				default:
					return fmt.Errorf("line %d: %q is not a metric kind", ln, kind)
				}
				if _, dup := types[name]; dup {
					return fmt.Errorf("line %d: a second TYPE for %q", ln, name)
				}
				if sampled[name] {
					return fmt.Errorf("line %d: TYPE for %q after its samples", ln, name)
				}
				types[name] = kind
			}
			continue
		}

		name, labels, value, err := splitSample(line)
		if err != nil {
			return fmt.Errorf("line %d: %w", ln, err)
		}
		if err := validMetricName(name); err != nil {
			return fmt.Errorf("line %d: %w", ln, err)
		}
		if err := validLabels(labels); err != nil {
			return fmt.Errorf("line %d: %w", ln, err)
		}
		switch value {
		case "NaN", "+Inf", "-Inf":
		default:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return fmt.Errorf("line %d: %q is not a sample value", ln, value)
			}
		}
		key := name + labels
		if series[key] {
			return fmt.Errorf("line %d: %s is exposed twice — a scraper keeps one and drops the other", ln, key)
		}
		series[key] = true
		sampled[name] = true
	}
	return nil
}

// splitSample takes `name{a="1",b="2"} 3` apart. The label block is
// returned verbatim, braces included, so an unterminated one is an error
// here rather than a silently truncated name.
func splitSample(line string) (name, labels, value string, err error) {
	rest := line
	if open := strings.IndexByte(rest, '{'); open >= 0 {
		shut := strings.LastIndexByte(rest, '}')
		if shut < open {
			return "", "", "", fmt.Errorf("label block is not closed: %q", line)
		}
		name = rest[:open]
		labels = rest[open : shut+1]
		rest = rest[shut+1:]
	} else {
		sp := strings.IndexAny(rest, " \t")
		if sp < 0 {
			return "", "", "", fmt.Errorf("sample has no value: %q", line)
		}
		name = rest[:sp]
		rest = rest[sp:]
	}
	value = strings.TrimSpace(rest)
	if value == "" {
		return "", "", "", fmt.Errorf("sample has no value: %q", line)
	}
	// A timestamp may follow the value; the daemon emits none, but the
	// format allows it and rejecting it would be stricter than a scraper.
	if sp := strings.IndexAny(value, " \t"); sp >= 0 {
		value = value[:sp]
	}
	return name, labels, value, nil
}

var (
	metricNameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRE  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

func validMetricName(name string) error {
	if !metricNameRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid metric name", name)
	}
	return nil
}

// validLabels walks `{a="1",b="2"}` and rejects both an invalid label name
// and an escape sequence Prometheus does not define. The escape half is
// the one that matters most here: internal/metrics renders label values
// with Go's %q, which also emits \t, \r and \xNN — none of which the text
// format recognises. Today every label value is a central name, and those
// are restricted to [A-Za-z0-9_-], so nothing escapes; the check is what
// stands between that and the first label sourced from a device name.
func validLabels(block string) error {
	if block == "" {
		return nil
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(block, "{"), "}")
	if inner == "" {
		return nil
	}
	for _, pair := range splitLabelPairs(inner) {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			return fmt.Errorf("label %q has no value", pair)
		}
		name := strings.TrimSpace(pair[:eq])
		if !labelNameRE.MatchString(name) {
			return fmt.Errorf("%q is not a valid label name", name)
		}
		val := strings.TrimSpace(pair[eq+1:])
		if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
			return fmt.Errorf("label %s: value %s is not a quoted string", name, val)
		}
		body := val[1 : len(val)-1]
		for i := 0; i < len(body); i++ {
			if body[i] != '\\' {
				continue
			}
			if i+1 >= len(body) {
				return fmt.Errorf("label %s: trailing backslash", name)
			}
			switch body[i+1] {
			case '\\', '"', 'n':
				i++
			default:
				return fmt.Errorf("label %s: \\%c is not an escape the text format defines", name, body[i+1])
			}
		}
	}
	return nil
}

// splitLabelPairs splits on commas that are outside a quoted value, so a
// label value containing a comma does not split the pair.
func splitLabelPairs(inner string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case c == '\\' && inQuotes && i+1 < len(inner):
			cur.WriteByte(c)
			cur.WriteByte(inner[i+1])
			i++
			continue
		case c == '"':
			inQuotes = !inQuotes
		case c == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
