// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ovTLogger returns a discard logger for tests that do not assert on log
// output.
func ovTLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ovTNewServer builds a multi-instance Server for the given names via
// New(). Each instance gets a syntactically valid but never-dialed
// upstream URL: the overview and status rendering under test only reads
// the poller's seeded snapshot, it never proxies a real request.
func ovTNewServer(t *testing.T, names ...string) *Server {
	t.Helper()
	var opts Options
	for i, name := range names {
		opts.Instances = append(opts.Instances, Instance{
			Name: name,
			URL:  fmt.Sprintf("http://127.0.0.1:%d", 10000+i),
		})
	}
	srv, err := New(opts, ovTLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// ovTSeedStatus overwrites one instance's poller status directly, so a
// test can pin the overview page's rendered tile without a live probe.
func ovTSeedStatus(srv *Server, name, status string) {
	srv.poller.store(InstanceStatus{Name: name, Status: status})
}

// ovTDotClass extracts the status dot's class attribute value (e.g.
// "dot healthy") from the tile rendered for the given instance name.
func ovTDotClass(t *testing.T, body, name string) string {
	t.Helper()
	marker := `id="inst-` + name + `"`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("tile for instance %q not found in body:\n%s", name, body)
	}
	rest := body[idx:]
	const needle = `class="dot `
	start := strings.Index(rest, needle)
	if start < 0 {
		t.Fatalf("dot span not found for instance %q:\n%s", name, rest)
	}
	rest = rest[start+len(`class="`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated class attribute for instance %q", name)
	}
	return rest[:end]
}

func TestOverviewPageRendersOneCardPerInstanceWithLiveStatus(t *testing.T) {
	srv := ovTNewServer(t, "alpha", "beta")
	ovTSeedStatus(srv, "alpha", "healthy")
	ovTSeedStatus(srv, "beta", "degraded")

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	body := rw.Body.String()

	if got := strings.Count(body, `<a class="card"`); got != 2 {
		t.Errorf("card count = %d, want 2:\n%s", got, body)
	}
	if dot := ovTDotClass(t, body, "alpha"); dot != "dot healthy" {
		t.Errorf("alpha dot class = %q, want %q", dot, "dot healthy")
	}
	if dot := ovTDotClass(t, body, "beta"); dot != "dot degraded" {
		t.Errorf("beta dot class = %q, want %q", dot, "dot degraded")
	}
	if !strings.Contains(body, `lang="en"`) {
		t.Errorf("body missing lang=en:\n%s", body)
	}
	if !strings.Contains(body, `fetch('./-/status'`) {
		t.Errorf("inline refresh script does not reference a relative ./-/status:\n%s", body)
	}
}

func TestOverviewPageGermanAcceptLanguage(t *testing.T) {
	srv := ovTNewServer(t, "alpha", "beta")

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	rw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	if body := rw.Body.String(); !strings.Contains(body, `lang="de"`) {
		t.Errorf("body missing lang=de:\n%s", body)
	}
}

func TestServeStatusJSONPreservesConfiguredOrder(t *testing.T) {
	names := []string{"c", "a", "b"}
	statuses := []string{"healthy", "degraded", "unreachable"}
	srv := ovTNewServer(t, names...)
	for i, name := range names {
		ovTSeedStatus(srv, name, statuses[i])
	}

	req := httptest.NewRequest(http.MethodGet, "/-/status", http.NoBody)
	rw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
	if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}

	var decoded struct {
		Instances []InstanceStatus `json:"instances"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(decoded.Instances) != len(names) {
		t.Fatalf("instances length = %d, want %d", len(decoded.Instances), len(names))
	}
	for i, name := range names {
		if decoded.Instances[i].Name != name {
			t.Errorf("instances[%d].Name = %q, want %q", i, decoded.Instances[i].Name, name)
		}
		if decoded.Instances[i].Status != statuses[i] {
			t.Errorf("instances[%d].Status = %q, want %q", i, decoded.Instances[i].Status, statuses[i])
		}
	}
}

func TestServeUnreachableRendersLocalized502(t *testing.T) {
	t.Run("english default locale", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/alpha/", http.NoBody)
		rw := httptest.NewRecorder()
		serveUnreachable(rw, req, "alpha")

		if rw.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusBadGateway)
		}
		if ct := rw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html prefix", ct)
		}
		body := rw.Body.String()
		if !strings.Contains(body, "alpha") {
			t.Errorf("body missing instance name:\n%s", body)
		}
		if !strings.Contains(body, "unreachable") {
			t.Errorf("body missing English wording:\n%s", body)
		}
	})

	t.Run("german locale", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/i/alpha/", http.NoBody)
		req.Header.Set("Accept-Language", "de")
		rw := httptest.NewRecorder()
		serveUnreachable(rw, req, "alpha")

		if rw.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want %d", rw.Code, http.StatusBadGateway)
		}
		body := rw.Body.String()
		if !strings.Contains(body, "alpha") {
			t.Errorf("body missing instance name:\n%s", body)
		}
		if !strings.Contains(body, "nicht erreichbar") {
			t.Errorf("body missing German wording:\n%s", body)
		}
	})
}
