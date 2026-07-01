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

// newDaemonClient constructs a daemonClient. baseURL trailing slashes are
// stripped so path joins produce clean URLs. The caller owns the timeout
// budget: a zero timeout means no deadline.
func newDaemonClient(baseURL, token, user, password string, timeout time.Duration) *daemonClient {
	return &daemonClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		user:     user,
		password: password,
		http:     &http.Client{Timeout: timeout},
	}
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
