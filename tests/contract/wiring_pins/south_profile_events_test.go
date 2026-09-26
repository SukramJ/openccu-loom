// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestCCUProfileAttachRegistersCallbackRoute pins that the composition entry
// point attaches a CCU central's inbound events through its south profile:
// the per-central route on the shared XML-RPC callback server answers as soon
// as the central is wired (before the CCU is even ready — the route is
// permanent and precedes every announcement), and it is gone after the
// manager tears the central down.
//
// A bring-up that stops calling the profile's event ingress leaves the route
// unregistered: the CCU's first callback after `init` 404s and no push event
// ever arrives, while everything else looks healthy.
func TestCCUProfileAttachRegistersCallbackRoute(t *testing.T) {
	srv, err := rpcserver.NewXMLRPCServer(rpcserver.XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	const name = "route-pin"
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name: name,
		// Nothing listens here: the bring-up keeps waiting for readiness,
		// which is exactly the window in which the route must already exist.
		Host:        "127.0.0.1",
		JSONRPCPort: 1,
		Interfaces:  []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{
		CallbackServer:  srv,
		CallbackPort:    port,
		CallbackHostFor: func(*config.CentralConfig) string { return host },
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}

	callbackURL := "http://" + net.JoinHostPort(host, portStr) + "/RPC2/" + name
	body := `<?xml version="1.0"?><methodCall><methodName>listDevices</methodName>` +
		`<params><param><value><string>` + name + `-HmIP-RF</string></value></param></params></methodCall>`
	post := func() int {
		t.Helper()
		resp, err := http.Post(callbackURL, "text/xml", strings.NewReader(body)) //nolint:noctx // short-lived local round trip
		if err != nil {
			t.Fatalf("POST %s: %v", callbackURL, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(); code != http.StatusOK {
		t.Fatalf("callback route for %q answered %d; the profile's event ingress was not attached", name, code)
	}
	mgr.Teardown()
	if code := post(); code == http.StatusOK {
		t.Fatalf("callback route for %q still answers after teardown; the ingress detach was not registered", name)
	}
}
