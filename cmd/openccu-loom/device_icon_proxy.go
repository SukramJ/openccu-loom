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
	configs  map[string]config.CentralConfig
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
func newDeviceIconProxy(reg *central.Registry, centrals []config.CentralConfig) *deviceIconProxy {
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
	return newDeviceIconProxyWith(locate, centrals)
}

// newDeviceIconProxyWith is the testable constructor — it takes the
// address→(filename, central) resolver directly instead of a registry.
func newDeviceIconProxyWith(
	locate func(address string) (filename, centralName string, ok bool),
	centrals []config.CentralConfig,
) *deviceIconProxy {
	configs := make(map[string]config.CentralConfig, len(centrals))
	for i := range centrals {
		configs[centrals[i].Name] = centrals[i]
	}
	return &deviceIconProxy{
		locate:  locate,
		configs: configs,
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
func (p *deviceIconProxy) Icon(ctx context.Context, address string) (data []byte, contentType string, ok bool) {
	if p == nil || p.locate == nil || address == "" {
		return nil, "", false
	}
	p.mu.RLock()
	c, cached := p.cache[address]
	p.mu.RUnlock()
	if cached {
		return c.data, c.contentType, len(c.data) > 0
	}

	data, contentType = p.fetch(ctx, address)
	p.mu.Lock()
	p.cache[address] = cachedIcon{data: data, contentType: contentType}
	p.mu.Unlock()
	return data, contentType, len(data) > 0
}

// fetch resolves the device + central and pulls the icon from the CCU.
// Returns nil on any failure so Icon caches the miss.
func (p *deviceIconProxy) fetch(ctx context.Context, address string) (data []byte, contentType string) {
	filename, centralName, ok := p.locate(address)
	if !ok || filename == "" || strings.Contains(filename, "..") || !safeIconName.MatchString(filename) {
		return nil, ""
	}
	cc, ok := p.configs[centralName]
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
