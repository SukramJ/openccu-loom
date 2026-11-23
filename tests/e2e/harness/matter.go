// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric-ui/fabricui"
)

// The node API (/api/v1/node/*) is go-fabric-ui's. It admits a request
// only with its client header and an admin principal, so the helpers
// below send both themselves instead of relying on the session a test
// may or may not have opened on h.REST().

// NodeEndpoint is one bridged endpoint of GET /api/v1/node/endpoints as
// the e2e suite reads it: the endpoint number and the stable source key
// the host built it from (empty for an endpoint no source stands behind).
type NodeEndpoint struct {
	EndpointID       uint16            `json:"endpoint_id"`
	ParentEndpointID uint16            `json:"parent_endpoint_id"`
	DeviceTypeName   string            `json:"device_type_name"`
	Source           map[string]string `json:"source"`
}

// SourceKey is the key of the source the endpoint was built from.
func (e NodeEndpoint) SourceKey() string { return e.Source["key"] }

// NodeEndpoints reads the bridged endpoints of GET
// /api/v1/node/endpoints (its "endpoints" list; the root and the
// Aggregator are in "structural" and carry no source).
func (h *Harness) NodeEndpoints(t *testing.T) []NodeEndpoint {
	t.Helper()
	var out struct {
		Endpoints []NodeEndpoint `json:"endpoints"`
	}
	if status, body := h.nodeDo(t, http.MethodGet, "/api/v1/node/endpoints", nil, &out); status != http.StatusOK {
		t.Fatalf("GET /api/v1/node/endpoints: status %d: %s", status, body)
	}
	return out.Endpoints
}

// EnableAllExposures switches every source the node offers on, except
// the ones its verdict calls unmappable (the provider refuses those):
// GET /api/v1/node/exposure, then one PUT with the batch. It returns the
// number of sources it switched on; the apply reassembles the bridge.
func (h *Harness) EnableAllExposures(t *testing.T) int {
	t.Helper()
	var list struct {
		Items []struct {
			Key     string `json:"key"`
			Enabled bool   `json:"enabled"`
			Verdict struct {
				State string `json:"state"`
			} `json:"verdict"`
		} `json:"items"`
	}
	if status, body := h.nodeDo(t, http.MethodGet, "/api/v1/node/exposure", nil, &list); status != http.StatusOK {
		t.Fatalf("GET /api/v1/node/exposure: status %d: %s", status, body)
	}
	type update struct {
		Key     string `json:"key"`
		Enabled bool   `json:"enabled"`
	}
	var updates []update
	for _, it := range list.Items {
		if it.Enabled || it.Verdict.State == "unmappable" {
			continue
		}
		updates = append(updates, update{Key: it.Key, Enabled: true})
	}
	if len(updates) == 0 {
		return 0
	}
	payload, err := json.Marshal(map[string]any{"items": updates})
	if err != nil {
		t.Fatalf("marshal exposure batch: %v", err)
	}
	if status, body := h.nodeDo(t, http.MethodPut, "/api/v1/node/exposure", payload, nil); status < 200 || status >= 300 {
		t.Fatalf("PUT /api/v1/node/exposure: status %d: %s", status, body)
	}
	return len(updates)
}

// nodeDo sends one node API request with the client header and admin
// credentials, decodes a 200 body into `into` when it is non-nil, and
// returns the status with the raw body.
func (h *Harness) nodeDo(t *testing.T, method, path string, payload []byte, into any) (status int, body string) {
	t.Helper()
	req, err := h.nodeRequest(context.Background(), method, path, payload)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if into != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("decode %s %s: %v (%s)", method, path, err, raw)
		}
	}
	return resp.StatusCode, string(raw)
}

func (h *Harness) nodeRequest(ctx context.Context, method, path string, payload []byte) (*http.Request, error) {
	var body io.Reader = http.NoBody
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.RESTBase()+path, body)
	if err != nil {
		return nil, err
	}
	auth, err := adminAuthorization(h.opts.AuthMode)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set(fabricui.RequestHeader, "1")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// adminAuthorization is the Authorization header that authenticates the
// harness's admin under the given mode. Session mode enables Basic as
// well (buildConfigYAML), so Basic serves both.
func adminAuthorization(mode AuthMode) (string, error) {
	switch mode {
	case AuthBasic, AuthSession:
		return "Basic " + BasicAuthHeader(AdminUser, AdminPass), nil
	case AuthToken:
		return "Bearer " + AdminToken, nil
	case AuthOIDC:
		return "", errors.New("the node API helpers have no OIDC credentials; run the Matter harness with Basic, session or token auth")
	}
	return "", fmt.Errorf("unknown auth mode %d", mode)
}

// waitForMatterListening polls GET /api/v1/node/status until the node
// reports listening, the deadline passes or the daemon exits. The node
// starts after /api/v1/health turns green, so health alone does not
// mean the bridge is up.
func (h *Harness) waitForMatterListening(deadline time.Duration) error {
	until := time.Now().Add(deadline)
	hc := &http.Client{Timeout: 2 * time.Second}
	var last string
	for {
		req, err := h.nodeRequest(context.Background(), http.MethodGet, "/api/v1/node/status", nil)
		if err != nil {
			return err
		}
		if resp, err := hc.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			last = fmt.Sprintf("status %d: %s", resp.StatusCode, body)
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"listening":true`) {
				return nil
			}
		}
		select {
		case <-h.cmdDone:
			return errors.New("daemon exited before the Matter node came up")
		default:
		}
		if time.Now().After(until) {
			return fmt.Errorf("node not listening; last GET /api/v1/node/status: %s", last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
