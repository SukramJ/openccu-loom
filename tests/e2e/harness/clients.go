// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"time"
)

// RESTClient is a thin wrapper around net/http aimed at E2E tests:
// it carries the harness's base URL, an optional auth header, and a
// default timeout. The walker drives this directly; per-test code
// uses the higher-level helpers below.
type RESTClient struct {
	base string
	hc   *http.Client
	auth string // "Basic <b64>" / "Bearer <token>" / ""
}

// newRESTClient returns a client targeting base (e.g.
// "http://127.0.0.1:8123"). The cookie jar is enabled by default so
// session-based tests get login cookies for free.
func newRESTClient(base string) *RESTClient {
	jar, _ := cookiejar.New(nil)
	return &RESTClient{
		base: base,
		hc: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
	}
}

// Base returns the base URL. Tests that need to construct unusual
// URLs (e.g. multipart uploads) read this directly.
func (c *RESTClient) Base() string { return c.base }

// HTTPClient exposes the underlying *http.Client so tests can drive
// uncommon flows (streaming, custom transports). Use sparingly —
// prefer the higher-level helpers.
func (c *RESTClient) HTTPClient() *http.Client { return c.hc }

// SetAuthHeader pins an `Authorization` header (e.g. for AuthBasic
// or AuthToken modes). Empty string clears it.
func (c *RESTClient) SetAuthHeader(value string) { c.auth = value }

// LoginSession POSTs to /api/v1/auth/login with the harness's admin
// credentials. The session cookie is captured by the cookie jar and
// applied automatically to subsequent requests. Returns an error if
// the daemon does not respond with 200.
func (c *RESTClient) LoginSession(user, pass string) error {
	body, _ := json.Marshal(map[string]string{
		"username": user,
		"password": pass,
	})
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login: status=%d body=%s", resp.StatusCode, raw)
	}
	return nil
}

// Do executes a request against the daemon. The base URL is
// prepended only if path starts with "/"; absolute URLs are passed
// through. The `Authorization` header pinned via SetAuthHeader is
// applied here so callers do not have to remember it.
func (c *RESTClient) Do(req *http.Request) (*http.Response, error) {
	if c.auth != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", c.auth)
	}
	return c.hc.Do(req)
}

// NewRequest builds a request rooted at the daemon's base URL.
// `path` is taken as-is (must start with "/"). For absolute URLs use
// http.NewRequest directly.
func (c *RESTClient) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	return http.NewRequest(method, c.base+path, body)
}

// BasicAuthHeader returns the base64-encoded credentials string that
// follows "Basic " in an Authorization header. Convenience helper for
// tests that want to pin Basic auth without going through LoginSession.
func BasicAuthHeader(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// WSURL converts the REST base URL into the matching WebSocket URL.
// E.g. http://127.0.0.1:8123 → ws://127.0.0.1:8123/api/ws.
func WSURL(restBase string) string {
	if len(restBase) >= 7 && restBase[:7] == "http://" {
		return "ws://" + restBase[7:] + "/api/ws"
	}
	if len(restBase) >= 8 && restBase[:8] == "https://" {
		return "wss://" + restBase[8:] + "/api/ws"
	}
	return restBase
}
