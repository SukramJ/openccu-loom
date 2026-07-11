// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// ccuImagePath is the CCU web-server directory that serves the 250px
// device artwork.
const ccuImagePath = "/config/img/devices/250"

// maxIconBytes caps a single icon download so a misbehaving upstream
// cannot exhaust memory. Real device PNGs are a few KB.
const maxIconBytes = 1 << 20 // 1 MiB

// safeIconName guards the upstream path: flat PNG names, optionally a
// coupling subdirectory. Combined with an explicit ".." check this
// prevents path traversal into other CCU web-server directories.
var safeIconName = regexp.MustCompile(`^[\w\-./]+\.png$`)

// deviceIconProxy resolves a device address to its CCU and proxies the
// model-icon image, caching the bytes (icons are effectively static).
// The real eQ-3 images are not embedded locally — ccudata carries only
// the filename — so we fetch them from the CCU that owns the device,
// the same source the HA integration proxies.
type deviceIconProxy struct {
	// locate maps a device address to its icon filename and central name.
	locate   func(address string) (filename, centralName string, ok bool)
	resolve  adapter.CentralConfigResolver
	secure   *http.Client
	insecure *http.Client

	mu    sync.RWMutex
	cache map[string]cachedIcon // address → bytes (empty data = known-missing)
}

type cachedIcon struct {
	data        []byte
	contentType string
}

// newDeviceIconProxy wires the proxy against the live central registry.
// resolve looks up a central's connection config against the live
// central-config source (see [adapter.CentralConfigResolver]) so a
// central adopted at runtime is served without a daemon restart.
func newDeviceIconProxy(reg *central.Registry, resolve adapter.CentralConfigResolver) *deviceIconProxy {
	locate := func(address string) (string, string, bool) {
		if reg == nil {
			return "", "", false
		}
		for _, unit := range reg.List() {
			if unit == nil || unit.ModelRegistry == nil {
				continue
			}
			if dev, found := unit.ModelRegistry.Get(address); found && dev != nil {
				return dev.ModelIcon, unit.Name(), true
			}
		}
		return "", "", false
	}
	return newDeviceIconProxyWith(locate, resolve)
}

// newDeviceIconProxyWith is the testable constructor — it takes the
// address→(filename, central) resolver directly instead of a registry.
func newDeviceIconProxyWith(
	locate func(address string) (filename, centralName string, ok bool),
	resolve adapter.CentralConfigResolver,
) *deviceIconProxy {
	return &deviceIconProxy{
		locate:  locate,
		resolve: resolve,
		secure:  &http.Client{Timeout: 15 * time.Second},
		insecure: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // explicit per-central opt-in via TLSInsecureSkipVerify
			},
		},
		cache: map[string]cachedIcon{},
	}
}

// Icon implements handlers.DeviceIconProxy. Cached results — including a
// cached miss (empty data) — short-circuit the upstream fetch.
//
// The route is unauthenticated (an <img> tag cannot carry auth), so an
// unknown address must NEVER create a cache entry: otherwise a caller
// could grow the cache without bound — and probe which addresses exist —
// by requesting random addresses in a loop. Only addresses that resolve
// to a real device are cached, which naturally bounds the map to the
// device fleet.
func (p *deviceIconProxy) Icon(ctx context.Context, address string) (data []byte, contentType string, ok bool) {
	if p == nil || p.locate == nil || address == "" {
		return nil, "", false
	}
	filename, centralName, known := p.locate(address)
	if !known {
		return nil, "", false
	}
	p.mu.RLock()
	c, cached := p.cache[address]
	p.mu.RUnlock()
	if cached {
		return c.data, c.contentType, len(c.data) > 0
	}

	data, contentType = p.fetch(ctx, filename, centralName)
	p.mu.Lock()
	p.cache[address] = cachedIcon{data: data, contentType: contentType}
	p.mu.Unlock()
	return data, contentType, len(data) > 0
}

// fetch pulls the icon for an already-resolved device from its CCU.
// Returns nil on any failure so Icon caches the (known-device) miss and
// does not hammer an unreachable CCU on every request.
func (p *deviceIconProxy) fetch(ctx context.Context, filename, centralName string) (data []byte, contentType string) {
	if filename == "" || strings.Contains(filename, "..") || !safeIconName.MatchString(filename) {
		return nil, ""
	}
	if p.resolve == nil {
		return nil, ""
	}
	cc, ok := p.resolve(ctx, centralName)
	if !ok || cc.Host == "" {
		return nil, ""
	}
	url := ccuImageBaseURL(cc) + ccuImagePath + "/" + filename
	client := p.secure
	if cc.TLS && cc.TLSInsecureSkipVerify {
		client = p.insecure
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, ""
	}
	// A CCU with authentication enabled protects its web-server paths.
	// Sending the central's credentials mirrors the XML-RPC transport
	// (internal/client/transport/xmlrpc/client.go) so the icon still
	// resolves when the daemon runs off-box (e.g. as an HA add-on)
	// against such a CCU; a CCU that does not require auth ignores the
	// header.
	if cc.Username != "" {
		req.SetBasicAuth(cc.Username, cc.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIconBytes))
	if err != nil || len(body) == 0 {
		return nil, ""
	}
	contentType = resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}
	return body, contentType
}

// ccuImageBaseURL builds the CCU web-server base URL (scheme://host:port).
// Mirrors the JSON-RPC endpoint scheme (internal/central/adapter
// hub_wiring.go): 80/443 by default, overridable via JSONRPCPort.
func ccuImageBaseURL(cc config.CentralConfig) string {
	scheme, port := "http", 80
	if cc.TLS {
		scheme, port = "https", 443
	}
	if cc.JSONRPCPort > 0 {
		port = cc.JSONRPCPort
	}
	return fmt.Sprintf("%s://%s:%d", scheme, cc.Host, port)
}
