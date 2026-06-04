// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

func bridgeHealthSupplier(cfg *config.Config, startedAt time.Time) func() map[string]any {
	centrals := make([]string, 0, len(cfg.Centrals))
	for i := range cfg.Centrals {
		centrals = append(centrals, cfg.Centrals[i].Name)
	}
	return func() map[string]any {
		return map[string]any{
			"version":    build.Version,
			"commit":     build.Commit,
			"build_date": build.BuildDate,
			"started_at": startedAt.Format(time.RFC3339),
			"centrals":   centrals,
		}
	}
}

// startCallbackServer binds the XML-RPC callback listener on
// `cfg.Callback.{Host,Port}` and computes the URL the CCU is told
// to push events to. The URL's host comes from PublicHost when
// configured; otherwise it is autodetected via a UDP probe against
// the first central's host (the kernel picks the egress interface,
// which is what the CCU will see as our peer address).
//
// When cfg.Callback.Port is 0 and cfg.Callback.PortRange is set (e.g.
// "30000-30099"), the server scans the range and binds on the first
// available port. The effective port is always read from srv.Addr()
// after construction, never from the configured value.
func startCallbackServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*rpcserver.XMLRPCServer, string, error) {
	host := cfg.Callback.Host
	if host == "" {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Callback.Port)

	xcfg := rpcserver.XMLRPCConfig{
		Addr:   addr,
		Logger: logger.With(slog.String("component", "callback.xmlrpc")),
	}
	if cfg.Callback.Port == 0 && cfg.Callback.PortRange != "" {
		lo, hi, err := config.ParsePortRange(cfg.Callback.PortRange)
		if err != nil {
			return nil, "", fmt.Errorf("callback: %w", err)
		}
		xcfg.PortRange = rpcserver.NewPortRange(lo, hi)
	}

	srv, err := rpcserver.NewXMLRPCServer(xcfg) //nolint:contextcheck // NewXMLRPCServer/bindAddr has no ctx parameter; bind is instantaneous
	if err != nil {
		return nil, "", fmt.Errorf("callback listen %s: %w", addr, err)
	}
	go func() {
		if err := srv.Serve(ctx); err != nil {
			logger.Warn("callback.serve", slog.String("err", err.Error()))
		}
	}()

	publicHost := cfg.Callback.PublicHost
	if publicHost == "" {
		publicHost = autodetectCallbackHost(cfg) //nolint:contextcheck // test callers outside owned set prevent threading ctx; UDP bind is instantaneous
	}
	if publicHost == "" {
		return srv, "", errors.New("callback: public host could not be determined — set callback.public_host")
	}
	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	port := cfg.Callback.Port
	if ok {
		port = tcpAddr.Port
	}
	baseURL := fmt.Sprintf("http://%s:%d", publicHost, port)
	return srv, baseURL, nil
}

// autodetectCallbackHost opens a throw-away UDP socket to the first
// configured central and reads back the local address. This is the
// standard "egress interface" trick — no packets are actually sent
// because UDP "Dial" only binds.
func autodetectCallbackHost(cfg *config.Config) string { //nolint:contextcheck // test callers outside owned set prevent ctx signature; UDP bind uses context.Background() below
	if len(cfg.Centrals) == 0 {
		return ""
	}
	target := cfg.Centrals[0].Host
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "udp", target+":80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

// loadTranslations resolves the translation archive in this order:
// 1. cfg.CCUData.TranslationsPath (explicit operator override)
// 2. Embedded extract (shipped with the binary, see
// internal/ccudata/embedded)
// 3. Empty fallback (raw CCU strings in the UI)
//
// Every transition is logged at INFO so operators can tell at a
// glance which source their daemon is running against.
func pickFirstCentral(cfg *config.Config) string {
	if len(cfg.Centrals) == 0 {
		return ""
	}
	return cfg.Centrals[0].Name
}

// buildOpenAPIValidator loads the OpenAPI spec from the configured
// path and returns a ready validator, or nil when the file is missing
// or fails to parse. Failures are logged but never abort the daemon —
// a missing spec must not take the REST surface offline; operators
// see the warning and can either supply the spec or flip
// OpenAPIValidate off.
func singleCentralName(reg *central.Registry) string {
	names := reg.Names()
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

// Compile-time check that the unused handlers import stays referenced
// (the router builds DTOs from it when Paramsets is nil, which is the
// case in the MVP composition).
var _ handlers.ConfigReader = (*adapter.ConfigAdapter)(nil)

// startMatterBridge constructs and starts the Matter bridge when
// matter.enabled is set. Returns the bridge and a stop function the
// caller defers; both are nil when the feature flag is off or the
// bridge cannot stand up. Errors are logged at warn level but never
// abort the daemon — the bridge is feature-flagged and
// failing to start it must not take REST / MQTT down with it.
//
// Defaults applied here mirror the [config.NorthMatter] doc strings:
// VendorID 0xFFF1 (test vendor block — never ship), ProductID 0x8000,
// NodeLabel "openccu-loom", Discriminator 0xF00, Listen ":5540".
func detectSupervisedRestart() bool {
	if os.Getenv("OPENCCU_LOOM_SUPERVISOR") == "1" {
		return true
	}
	ppid := os.Getppid()
	if ppid == 1 {
		if os.Getenv("JOURNAL_STREAM") != "" {
			return true
		}
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return true
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// startMDNSAdvertiser parses the REST listen address, builds the
// daemon-discovery TXT bundle, and starts a multicast advertiser.
// Returns (nil, nil) when the listen address has no usable port
// (Unix socket etc.) so the caller can skip without an error path.
// Returns (nil, err) when the port is malformed or zeroconf fails to
// register; the caller is expected to log and continue (mDNS is a
// convenience, not a hard dependency).
