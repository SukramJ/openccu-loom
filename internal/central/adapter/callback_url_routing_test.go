// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// listDevicesCall is the XML-RPC body a CCU posts to the announced callback
// URL right after init(). Any routable request is enough to tell "the route
// matched" from "the mux answered 404".
const listDevicesCall = `<?xml version="1.0"?><methodCall><methodName>listDevices</methodName>` +
	`<params><param><value><string>loom-ccu-HmIP-RF</string></value></param></params></methodCall>`

// TestCallbackURLRoundTripsThroughTheCallbackRouter pairs the two halves of
// the callback contract that no layer used to reconcile: the URL the daemon
// announces to the CCU is built by interpolating the configured central name
// into `/RPC2/<name>`, while the callback router matches that segment — after
// net/http has percent-decoded it — against a strict allowlist. A name outside
// the allowlist produced a URL every callback 404s on: the daemon starts
// cleanly, REST and the SPA look healthy, and not one push event ever arrives.
//
// Two centrals are registered so the router's single-central bare-root
// fallback cannot mask a mismatch.
func TestCallbackURLRoundTripsThroughTheCallbackRouter(t *testing.T) {
	t.Parallel()

	srv, err := rpcserver.NewXMLRPCServer(rpcserver.XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	host, portStr, err := splitHostPort(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	deps := WireDeps{
		CallbackServer:  srv,
		CallbackPort:    port,
		CallbackHostFor: func(*config.CentralConfig) string { return host },
	}

	for _, name := range []string{"ccu-01", "CCU_Wohnzimmer", "ccuMain42"} {
		if err := hmtypes.ValidateCentralName(name); err != nil {
			t.Fatalf("fixture name %q is not a valid central name: %v", name, err)
		}
		cc := config.CentralConfig{Name: name, Host: "10.0.0.5"}
		unit := newTestCentralNamed(t, name)
		callbackURL, _, _, deregister := registerCentralCallbacks(deps, &cc, unit, slog.New(slog.DiscardHandler))
		if deregister != nil {
			t.Cleanup(deregister)
		}
		if callbackURL == "" {
			t.Fatalf("%s: no callback URL was announced", name)
		}

		resp, err := http.Post(callbackURL, "text/xml", strings.NewReader(listDevicesCall)) //nolint:noctx // short-lived local round trip
		if err != nil {
			t.Fatalf("%s: POST %s: %v", name, callbackURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: the announced callback URL %q does not route back to the registered handler: %d %s",
				name, callbackURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

// TestCentralNamesOutsideTheCallbackAllowlistAreRejectedAtConfigLoad is the
// other half: a name the callback router cannot match must never reach the
// wire in the first place. Rejecting it at config load points the operator at
// the name; rejecting it at callback time is indistinguishable from a dead
// CCU.
func TestCentralNamesOutsideTheCallbackAllowlistAreRejectedAtConfigLoad(t *testing.T) {
	t.Parallel()

	validate := func(name string) error {
		cfg := config.Default()
		cfg.Centrals = []config.CentralConfig{{
			Name:       name,
			Host:       "10.0.0.5",
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
		}}
		return cfg.Validate()
	}
	// Control: the same config with a routable name must be accepted, so a
	// rejection below can only come from the name.
	if err := validate("ccu-01"); err != nil {
		t.Fatalf("a routable central name must validate: %v", err)
	}
	for _, name := range []string{"CCU Wohnzimmer", "ccu.01", "ccu/01", "ccu:01", "../etc"} {
		err := validate(name)
		if err == nil {
			t.Errorf("central name %q must be rejected at config load — every callback for it 404s", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the rejection for %q must name the offending value, got: %v", name, err)
		}
	}
}

// splitHostPort splits an "http://host:port" test-server URL.
func splitHostPort(raw string) (host, port string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	return u.Hostname(), u.Port(), nil
}
