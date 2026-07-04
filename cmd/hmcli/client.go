// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// daemonClient is a thin HTTP client for the openccu-loom REST API.
// It holds credentials and a base URL so callers never repeat auth setup.
type daemonClient struct {
	baseURL  string
	token    string
	user     string
	password string
	http     *http.Client
}

// clientConfig carries everything newDaemonClient needs to build a client:
// connection target, resolved credentials, TLS trust settings, and the request
// timeout budget.
type clientConfig struct {
	baseURL  string
	token    string
	user     string
	password string
	cacert   string        // optional PEM CA bundle to trust
	insecure bool          // skip TLS verification (explicit opt-out)
	timeout  time.Duration // zero means no deadline
}

// newDaemonClient constructs a daemonClient. baseURL trailing slashes are
// stripped so path joins produce clean URLs. The caller owns the timeout
// budget: a zero timeout means no deadline. It returns an error only when the
// configured CA bundle cannot be loaded.
func newDaemonClient(cfg clientConfig) (*daemonClient, error) {
	tlsCfg, err := buildTLSConfig(cfg.cacert, cfg.insecure)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: cfg.timeout}
	if tlsCfg != nil {
		httpClient.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	return &daemonClient{
		baseURL:  strings.TrimRight(cfg.baseURL, "/"),
		token:    cfg.token,
		user:     cfg.user,
		password: cfg.password,
		http:     httpClient,
	}, nil
}

// applyAuth attaches credentials to req. Bearer token wins over basic-auth
// when both are set, mirroring the export-def auth precedence.
func (c *daemonClient) applyAuth(req *http.Request) {
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case c.user != "":
		req.SetBasicAuth(c.user, c.password)
	}
}

// getJSON issues a GET to baseURL+path, applies auth, and JSON-decodes the
// response body into out. Non-2xx responses return an error that includes the
// HTTP status and up to 4 KiB of the response body for diagnostic context.
func (c *daemonClient) getJSON(ctx context.Context, path string, out any) error {
	target := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("GET %s: build request: %w", target, err)
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: HTTP %s: %s", target, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// sendJSON marshals body as JSON, issues method+path with auth, and optionally
// JSON-decodes the response into out (pass nil to discard the body). Non-2xx
// responses return an error including the HTTP status and up to 4 KiB of body.
func (c *daemonClient) sendJSON(ctx context.Context, method, path string, body, out any) error {
	return c.sendJSONHeaders(ctx, method, path, body, out, nil)
}

// sendJSONHeaders is sendJSON with extra request headers (e.g. the
// edit-lock token on a MASTER/LINK paramset write). Kept as the single
// implementation so the common sendJSON stays a thin wrapper.
func (c *daemonClient) sendJSONHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	target := c.baseURL + path
	var reqBody io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s %s: marshal body: %w", method, target, err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return fmt.Errorf("%s %s: build request: %w", method, target, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: HTTP %s: %s", method, target, resp.Status, strings.TrimSpace(string(errBody)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// editTokenHeader is the request header carrying the edit-lock token on
// a MASTER/LINK paramset write. Mirrors handlers.EditTokenHeader.
const editTokenHeader = "X-Edit-Token"

// openEditSession acquires the per-resource edit lock for key and
// returns the issued token. The daemon rejects a MASTER/LINK paramset
// write that does not present a token currently holding the lock, so
// non-interactive clients must open a session first.
func (c *daemonClient) openEditSession(ctx context.Context, key string) (string, error) {
	var resp struct {
		Token string `json:"token"`
	}
	body := map[string]string{"key": key, "subject": "hmcli"}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/sessions/edit", body, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("open edit session %q: empty token in response", key)
	}
	return resp.Token, nil
}

// closeEditSession releases the edit lock held by token. Best-effort:
// the daemon prunes abandoned locks by TTL, so a failed release is not
// fatal to the caller.
func (c *daemonClient) closeEditSession(ctx context.Context, key, token string) error {
	body := map[string]string{"key": key, "token": token}
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/sessions/edit", body, nil)
}
