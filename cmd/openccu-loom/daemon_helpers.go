// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// configuredCentralNames returns the names of every configured central,
// in configuration order. The list is the DB-overlaid one, so a CCU the
// operator adopted through the SPA is included.
func configuredCentralNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Centrals))
	for i := range cfg.Centrals {
		names = append(names, cfg.Centrals[i].Name)
	}
	return names
}

// liveCentralNames returns every central the daemon currently serves: the
// boot-config tier in configuration order, followed by any central registered
// since. Both tiers are needed — a runtime adopt writes the registry and the
// centrals table but never cfg.Centrals, while the config tier still names a
// central whose bring-up has not registered it yet.
func liveCentralNames(cfg *config.Config, reg *central.Registry) []string {
	names := configuredCentralNames(cfg)
	if reg == nil {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		seen[n] = struct{}{}
	}
	for _, n := range reg.Names() {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	return names
}

// bridgeHealthSupplier returns a closure the MQTT bridge invokes on
// every AnnounceOnline to compose the `<base>/bridge/health` payload.
// The body carries operator-visible metadata that is more useful than
// a bare "online" flag — build identity, daemon boot timestamp, and
// the centrals the daemon serves.
//
// centralNames is resolved per call, never captured: the payload is retained
// and republished on every broker reconnect, so a list snapshotted when the
// MQTT stack was built would keep announcing the boot fleet — a CCU adopted
// at runtime would be missing from it until a config reload or a restart.
func bridgeHealthSupplier(centralNames func() []string, startedAt time.Time) func() map[string]any {
	return func() map[string]any {
		names := []string{}
		if centralNames != nil {
			if resolved := centralNames(); resolved != nil {
				names = resolved
			}
		}
		return map[string]any{
			"version":    build.Version,
			"commit":     build.Commit,
			"build_date": build.BuildDate,
			"started_at": startedAt.Format(time.RFC3339),
			"centrals":   names,
		}
	}
}

// startCallbackServer binds the XML-RPC callback listener on
// `cfg.Callback.{Host,Port}` and returns the effective port. The host
// advertised to each CCU is NOT computed here — it is resolved
// per-central by the caller (see callbackHostFor) so a daemon serving a
// local and an external CCU advertises loopback to the former and its
// LAN IP to the latter. Baking one global host into the URL (the former
// behaviour) was wrong for any central reached over a different
// interface than central[0].
//
// When cfg.Callback.PortRange is set (e.g. "30000-30099"), the server
// scans the range and binds the first available port; the configured
// Port is then not used. The range has to win rather than defer to
// "Port == 0" because config.applyDefaults fills Port with 8120 on every
// load — under the old rule the range branch was unreachable in every
// installation, so an operator behind a narrow firewall got the default
// port and no indication that the setting had been ignored.
//
// The effective port is always read from srv.Addr() after construction,
// never from the configured value.
func startCallbackServer(ctx context.Context, cfg *config.Config, allowlist rpcserver.PeerAllowlist, obs rpcserver.CallbackObserver, logger *slog.Logger) (*rpcserver.XMLRPCServer, int, error) {
	host := cfg.Callback.Host
	if host == "" {
		host = "0.0.0.0"
	}
	// A port of 0 in the address is what makes bindAddr consult the range.
	bindPort := cfg.Callback.Port
	var portRange *rpcserver.PortRange
	if cfg.Callback.PortRange != "" {
		lo, hi, err := config.ParsePortRange(cfg.Callback.PortRange)
		if err != nil {
			return nil, 0, fmt.Errorf("callback: %w", err)
		}
		portRange = rpcserver.NewPortRange(lo, hi)
		bindPort = 0
	}
	addr := fmt.Sprintf("%s:%d", host, bindPort)

	xcfg := rpcserver.XMLRPCConfig{
		Addr:           addr,
		Logger:         logger.With(slog.String("component", "callback.xmlrpc")),
		MaxConnections: cfg.Callback.MaxConnections,
		Metrics:        obs,
		PeerAllowlist:  allowlist,
		PortRange:      portRange,
	}

	srv, err := rpcserver.NewXMLRPCServer(xcfg) //nolint:contextcheck // NewXMLRPCServer/bindAddr has no ctx parameter; bind is instantaneous
	if err != nil {
		return nil, 0, fmt.Errorf("callback listen %s: %w", addr, err)
	}
	go func() {
		if err := srv.Serve(ctx); err != nil {
			logger.Warn("callback.serve", slog.String("err", err.Error()))
		}
	}()

	port := cfg.Callback.Port
	if tcpAddr, ok := srv.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	}
	return srv, port, nil
}

// callbackAllowlistRefresh is how often the source-IP allowlist is
// re-derived. It bounds two windows: how long a CCU adopted through the
// admin surface is refused, and how long a CCU whose DHCP lease or DNS
// record moved keeps being refused.
const callbackAllowlistRefresh = 30 * time.Second

// newCallbackAllowlist returns the source-IP allowlist for both callback
// listeners, or nil (accept-all) unless cfg.Callback.RestrictSourceIPs is
// set — preserving the default open-LAN behaviour.
//
// The result is a resolver, not a snapshot. The CCU set is not fixed at boot:
// adding a central is an explicitly restart-free operation, and the
// orchestrator wires it live without touching cfg.Centrals. A listener frozen
// on the boot-time set therefore drops every callback from that CCU — with
// only a DEBUG line and no fallback poll, so it presents as a central that
// comes up, reports live, and then never updates a value. Re-resolving also
// covers an existing CCU whose address changed under it.
//
// Both tiers are consulted, the same union [hasConfiguredCentral] uses: the
// boot config (YAML plus the DB rows layered in at boot) and the SQLite
// centrals table a runtime adopt writes. Loopback is always included so a
// co-located CCU (pushing from 127.0.0.1) keeps working. A host that is an IP
// literal becomes a /32 or /128; a hostname is resolved to all of its A/AAAA
// records, and one that fails to resolve is skipped with a warning rather
// than silently blackholed — the operator opted in, so a visible log beats an
// invisible drop.
//
// Resolution runs on its own goroutine, ticking until ctx is done, so no DNS
// lookup ever blocks an Accept. The first list is resolved synchronously, so
// the listener is never briefly open.
func newCallbackAllowlist(
	ctx context.Context,
	cfg *config.Config,
	centrals *sqlitestore.CentralsStore,
	logger *slog.Logger,
) rpcserver.PeerAllowlist {
	if !cfg.Callback.RestrictSourceIPs {
		return nil
	}
	return newLiveCallbackAllowlist(ctx, cfg, centrals, logger, callbackAllowlistRefresh)
}

// newLiveCallbackAllowlist is [newCallbackAllowlist] past the opt-in gate,
// with the refresh cadence as a parameter so the loop can be exercised
// without waiting out the production interval.
func newLiveCallbackAllowlist(
	ctx context.Context,
	cfg *config.Config,
	centrals *sqlitestore.CentralsStore,
	logger *slog.Logger,
	refresh time.Duration,
) rpcserver.PeerAllowlist {
	live := &callbackAllowlist{cfg: cfg, centrals: centrals, logger: logger}
	live.store(live.resolve(ctx))
	go live.run(ctx, refresh)
	return live.prefixes
}

// callbackAllowlist holds the most recently resolved prefix set. Readers are
// the two listeners' accept paths, which must never block; the single writer
// is the refresh goroutine [newCallbackAllowlist] starts.
type callbackAllowlist struct {
	cfg      *config.Config
	centrals *sqlitestore.CentralsStore
	logger   *slog.Logger

	mu      sync.RWMutex
	current []netip.Prefix
}

// prefixes reports the current allowlist. It satisfies
// [rpcserver.PeerAllowlist].
func (a *callbackAllowlist) prefixes() []netip.Prefix {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current
}

func (a *callbackAllowlist) store(prefixes []netip.Prefix) {
	a.mu.Lock()
	a.current = prefixes
	a.mu.Unlock()
}

// run re-resolves the allowlist until ctx is cancelled — the daemon context,
// so the refresh outlives a callback-listener restart and stops at shutdown.
func (a *callbackAllowlist) run(ctx context.Context, refresh time.Duration) {
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.store(a.resolve(ctx))
		}
	}
}

func (a *callbackAllowlist) resolve(ctx context.Context) []netip.Prefix {
	out := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	seen := make(map[string]struct{}, len(a.cfg.Centrals))
	for i := range a.cfg.Centrals {
		out = a.appendHost(ctx, out, seen, a.cfg.Centrals[i].Name, a.cfg.Centrals[i].Host)
	}
	if a.centrals == nil {
		return out
	}
	rows, err := a.centrals.List(ctx)
	if err != nil {
		// The boot-config tier still holds; a transient store error must not
		// shrink the allowlist to loopback-plus-nothing.
		a.logger.Warn("callback.allowlist.centrals.list", slog.String("err", err.Error()))
		return out
	}
	for i := range rows {
		if !rows[i].Enabled {
			continue
		}
		out = a.appendHost(ctx, out, seen, rows[i].Name, rows[i].Host)
	}
	return out
}

// appendHost adds host's addresses to out, skipping hosts already covered so
// a central present in both tiers is resolved once.
func (a *callbackAllowlist) appendHost(ctx context.Context, out []netip.Prefix, seen map[string]struct{}, name, host string) []netip.Prefix {
	if host == "" {
		return out
	}
	if _, dup := seen[host]; dup {
		return out
	}
	seen[host] = struct{}{}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		return append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		a.logger.Warn("callback.allowlist.resolve.failed",
			slog.String("central", name),
			slog.String("host", host))
		return out
	}
	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip.IP); ok {
			addr = addr.Unmap()
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	return out
}

// callbackHostFor resolves the host the CCU `cc` should push callbacks
// to. An explicit cfg.Callback.PublicHost wins (the operator's NAT
// override, applied to every central); otherwise the host is detected
// per-central as the egress interface toward THAT central, so the
// advertised address is reachable from each CCU independently — loopback
// for a co-located CCU, the LAN IP for an external one. Returns "" when
// the host cannot be determined; the caller then skips callbacks for
// that central (it still works, just without push events).
func callbackHostFor(cfg *config.Config, cc *config.CentralConfig) string {
	if cfg.Callback.PublicHost != "" {
		return cfg.Callback.PublicHost
	}
	return egressHostToward(cc.Host)
}

// egressHostToward opens a throw-away UDP socket toward host and reads
// back the local address the kernel picked. This is the standard "egress
// interface" trick — no packets are actually sent because UDP "Dial"
// only binds. Returns "" when host is empty or the bind fails.
func egressHostToward(host string) string { //nolint:contextcheck // UDP bind uses context.Background(); it is instantaneous and has no cancellation point
	if host == "" {
		return ""
	}
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "udp", host+":80")
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

// pickFirstCentral returns the first configured central's name, or "" when
// none is configured. It is only for daemon-global surfaces that need one
// default label (the MQTT metrics collector, the discovery TXT bundle);
// anything that acts on a specific CCU must resolve the name it means.
func pickFirstCentral(cfg *config.Config) string {
	if len(cfg.Centrals) == 0 {
		return ""
	}
	return cfg.Centrals[0].Name
}

// singleCentralName returns the name of the registered central when
// the registry contains exactly one entry, otherwise the empty
// string. The REST router uses the result to pre-populate
// `central_name` in every request's [reqctx.RequestContext]. Multi-
// central deployments leave the request scope unset and rely on
// per-handler resolution.
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

// detectSupervisedRestart reports whether the daemon is running
// under a supervisor that will restart it after a clean shutdown.
// The check is cheap + heuristic — we do not try to verify the
// supervisor's restart policy; we look for tight markers that
// imply the daemon's IMMEDIATE parent (or runtime) is a
// supervisor, not just the terminal session.
//
// Signals (any one fires):
//
//   - OPENCCU_LOOM_SUPERVISOR=1 — explicit operator override.
//   - JOURNAL_STREAM set AND getppid()==1 — systemd attached the
//     daemon's stdout/stderr to journald and re-parented it to PID 1
//     (the unambiguous "I am a systemd service" signal; INVOCATION_ID
//     alone is too lax because gnome-terminal inherits it).
//   - getppid()==1 AND /run/systemd/system exists — fallback when
//     JOURNAL_STREAM was suppressed but systemd is still PID 1.
//   - KUBERNETES_SERVICE_HOST set — Kubernetes injects it into
//     every Pod and the kubelet always restarts dead containers.
//   - /.dockerenv exists — Docker / Podman containers; restart
//     policy is operator-chosen but presence is the usual signal.
//
// Missing all of these means the binary is running on bare metal
// from a shell. The SPA disables the Restart-Daemon button in
// that case so an operator does not accidentally take the daemon
// permanently offline.
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
