// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Add-on auto-onboarding (ADR 0077). When the daemon runs as the add-on
// on an openccu-lite box, the box mints an API token for it at every
// occulited start (the manifest's runtime.api_scopes) and writes it to a
// file only this add-on's user can read. A first boot with no centrals
// configured therefore needs no wizard step: the local box is adopted by
// itself, authenticated by that file.
//
// A box that moved from OpenCCU to openccu-lite brings its central along
// instead: a CCU central pointing at the box itself, waiting for a CCU that
// no longer exists. That one central is moved to openccu-lite in place,
// keeping its name, so everything keyed by the name stays with it.
const (
	// liteAddonCentralsHealthComponent is the /health component naming the
	// CCU centrals that point at this openccu-lite box and could not be
	// moved over by the daemon.
	liteAddonCentralsHealthComponent = "config.lite_addon"
	// liteAddonTokenPath is where occulited writes the add-on's minted
	// API token (0600, the add-on's user; the secret plus a newline).
	liteAddonTokenPath = "/run/occulite/addon-tokens/openccu-loom.api"
	// liteAddonBaseURL is the box's own web server as seen from the
	// add-on: plain HTTP on the loopback.
	liteAddonBaseURL = "http://127.0.0.1"
	// liteAddonFallbackName names the auto-onboarded central when the
	// box's hostname is empty or no valid central name remains of it.
	liteAddonFallbackName = "local"
)

// Environment overrides for the three host facts, so the e2e harness can
// point the onboarding at a fake box and a scratch token file. Production
// never sets them.
const (
	liteAddonTokenPathEnv   = "OPENCCU_LOOM_LITE_ADDON_TOKEN_FILE" //nolint:gosec // an env var NAME, not a credential
	liteAddonVersionPathEnv = "OPENCCU_LOOM_LITE_ADDON_VERSION_FILE"
	liteAddonBaseURLEnv     = "OPENCCU_LOOM_LITE_ADDON_BASE_URL"
)

// liteAddonOnboarder adopts the local openccu-lite box as a central on a
// first boot. It is constructed by the composition root only.
type liteAddonOnboarder struct {
	admin   handlers.CentralAdminService
	tracker *health.Tracker
	logger  *slog.Logger
	// ifaceAddrs lists this machine's interface addresses (net.InterfaceAddrs
	// in production), against which a central's host is compared.
	ifaceAddrs  func() ([]net.Addr, error)
	tokenPath   string
	versionPath string
	baseURL     string
	// retryEvery paces the wait for a box that is still starting;
	// window bounds it (a box that never comes up leaves the daemon
	// exactly as configured).
	retryEvery time.Duration
	window     time.Duration
}

// maybeStartLiteAddonOnboarding starts the onboarding goroutine when the
// host is an openccu-lite box that minted a token for this add-on and
// either no central is configured yet or the one CCU central pointing at
// the box itself is still to be moved over. On every other host it is a
// cheap no-op. The goroutine ends on success, at the window's deadline, or
// when ctx is cancelled — whichever comes first.
func maybeStartLiteAddonOnboarding(ctx context.Context, admin handlers.CentralAdminService, tracker *health.Tracker, logger *slog.Logger) {
	o := &liteAddonOnboarder{
		admin:       admin,
		tracker:     tracker,
		logger:      logger,
		ifaceAddrs:  net.InterfaceAddrs,
		tokenPath:   envOr(liteAddonTokenPathEnv, liteAddonTokenPath),
		versionPath: envOr(liteAddonVersionPathEnv, addonupdate.HostVersionPath),
		baseURL:     envOr(liteAddonBaseURLEnv, liteAddonBaseURL),
		retryEvery:  5 * time.Second,
		window:      5 * time.Minute,
	}
	if admin == nil {
		return
	}
	if !addonupdate.HostIsLiteVariant(o.versionPath) {
		return
	}
	if _, err := os.Stat(o.tokenPath); err != nil {
		// A lite box without the file means the manifest's api_scopes
		// were not applied (an install predating them): pairing remains
		// the way in, so say so once instead of failing silently.
		logger.Info("lite_addon.onboard.no_token_file", slog.String("path", o.tokenPath))
		go o.reportStrandedCentrals(ctx)
		return
	}
	go o.run(ctx)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// run waits for the box to answer, then adopts it. Every exit path logs,
// so a boot that did not onboard says why.
func (o *liteAddonOnboarder) run(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, o.window)
	defer cancel()

	rows, err := o.admin.List(ctx)
	if err != nil {
		o.logger.Warn("lite_addon.onboard.list_failed", slog.String("err", err.Error()))
		return
	}
	convert := ""
	if len(rows) > 0 {
		// Not a first boot: the operator has configured centrals (or
		// deleted the auto-onboarded one — deliberately, so it stays
		// deleted). The one thing left to do is the central a move from
		// OpenCCU brought along.
		local := o.localCCUCentrals(ctx, rows)
		if len(local) != 1 {
			o.recordStranded(local, "more than one central points at this system")
			return
		}
		convert = local[0]
	}

	client, err := occulited.New(occulited.Config{
		BaseURL:     o.baseURL,
		TokenSource: occulited.FileToken(o.tokenPath),
		Logger:      o.logger,
	})
	if err != nil {
		o.logger.Warn("lite_addon.onboard.client", slog.String("err", err.Error()))
		return
	}

	for {
		var done, retry bool
		if convert != "" {
			done, retry = o.convert(ctx, client, convert)
		} else {
			done, retry = o.attempt(ctx, client)
		}
		if done || !retry {
			return
		}
		select {
		case <-ctx.Done():
			o.logger.Info("lite_addon.onboard.gave_up",
				slog.Duration("window", o.window), slog.String("base_url", o.baseURL))
			return
		case <-time.After(o.retryEvery):
		}
	}
}

// attempt probes the box once. done reports success; retry asks for
// another round (the box is still starting or unreachable).
func (o *liteAddonOnboarder) attempt(ctx context.Context, client *occulited.Client) (done, retry bool) {
	det, err := client.Detect(ctx)
	if err != nil || det.Kind != occulited.DetectLite {
		return false, true
	}
	status, err := client.Status(ctx)
	if err != nil {
		return false, true
	}
	ifaces, err := client.Interfaces(ctx)
	if err != nil || len(ifaces) == 0 {
		return false, true
	}

	specs := make([]config.InterfaceSpec, 0, len(ifaces))
	names := make([]string, 0, len(ifaces))
	for _, in := range ifaces {
		specs = append(specs, config.InterfaceSpec{Name: in.Name})
		names = append(names, in.Name)
	}

	host, port := o.hostPort()
	row := sqlite.CentralRow{
		Name:         centralNameFromHostname(status.Hostname),
		SystemType:   "openccu-lite",
		Host:         host,
		JSONRPCPort:  port,
		APITokenFile: o.tokenPath,
		Interfaces:   specs,
		Enabled:      true,
	}

	// Re-check emptiness right before the write: an operator who raced
	// the wizard during the wait wins.
	rows, err := o.admin.List(ctx)
	if err != nil || len(rows) > 0 {
		return false, false
	}
	if err := o.admin.Put(ctx, row); err != nil {
		o.logger.Warn("lite_addon.onboard.put_failed",
			slog.String("central", row.Name), slog.String("err", err.Error()))
		return false, false
	}
	o.logger.Info("lite_addon.onboard.adopted",
		slog.String("central", row.Name),
		slog.String("host", host),
		slog.String("interfaces", strings.Join(names, ",")),
		slog.String("token_file", o.tokenPath))
	return true, false
}

// centralIsCCU reports whether a stored central is a CCU central: the one
// system type that cannot carry an API token (`auto` can, and resolves
// itself).
func centralIsCCU(row sqlite.CentralRow) bool {
	return !config.CentralFieldsOf(hmenum.SystemType(row.SystemType)).APIToken
}

// localCCUCentrals names the CCU centrals whose host is this machine. On an
// openccu-lite box such a central can never come up — there is no ReGa to
// become ready. It answers nothing once a central already uses the add-on
// token: the box is configured then, and a leftover CCU central is the
// operator's to delete, not a second copy of the box to create.
func (o *liteAddonOnboarder) localCCUCentrals(ctx context.Context, rows []sqlite.CentralRow) []string {
	var local []string
	for i := range rows {
		if rows[i].APITokenFile == o.tokenPath {
			return nil
		}
		if centralIsCCU(rows[i]) && o.hostIsThisMachine(ctx, rows[i].Host) {
			local = append(local, rows[i].Name)
		}
	}
	return local
}

// hostIsThisMachine reports whether host names this machine: a loopback
// address, or an address one of its interfaces carries — a central created
// with the box's LAN address is as local as one created with 127.0.0.1. A
// host that does not resolve is not local.
func (o *liteAddonOnboarder) hostIsThisMachine(ctx context.Context, host string) bool {
	var addrs []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		addrs = append(addrs, addr.Unmap())
	} else {
		ips, lerr := net.DefaultResolver.LookupIPAddr(ctx, host)
		if lerr != nil {
			return false
		}
		for _, ip := range ips {
			if addr, ok := netip.AddrFromSlice(ip.IP); ok {
				addrs = append(addrs, addr.Unmap())
			}
		}
	}
	var own []netip.Addr
	if o.ifaceAddrs != nil {
		ifaces, _ := o.ifaceAddrs()
		for _, a := range ifaces {
			if ipn, ok := a.(*net.IPNet); ok {
				if addr, ok := netip.AddrFromSlice(ipn.IP); ok {
					own = append(own, addr.Unmap())
				}
			}
		}
	}
	return slices.ContainsFunc(addrs, func(a netip.Addr) bool {
		return a.IsLoopback() || slices.Contains(own, a)
	})
}

// convert moves the CCU central `name` to openccu-lite once the box
// answers. done reports success; retry asks for another round.
//
// The central keeps its name, behaviour and visibility; the account, the
// RPC ports and the interface list are the CCU's and go — the interfaces
// are read from the box, as at a first boot. It is one save of the new row:
// the admin service restarts a running central on an edit that changes how
// it is reached, and keeps everything stored under its name. A disabled save
// in between would be a removal, which deletes that state.
func (o *liteAddonOnboarder) convert(ctx context.Context, client *occulited.Client, name string) (done, retry bool) {
	det, err := client.Detect(ctx)
	if err != nil || det.Kind != occulited.DetectLite {
		return false, true
	}
	ifaces, err := client.Interfaces(ctx)
	if err != nil || len(ifaces) == 0 {
		return false, true
	}
	// Re-read right before the write: an operator who switched or deleted
	// the central during the wait wins.
	row, err := o.admin.Get(ctx, name)
	if err != nil || !centralIsCCU(row) {
		return false, false
	}

	specs := make([]config.InterfaceSpec, 0, len(ifaces))
	names := make([]string, 0, len(ifaces))
	for _, in := range ifaces {
		specs = append(specs, config.InterfaceSpec{Name: in.Name})
		names = append(names, in.Name)
	}
	next := row
	next.SystemType = "openccu-lite"
	next.Host, next.JSONRPCPort = o.hostPort()
	next.APITokenFile = o.tokenPath
	next.Interfaces = specs
	next.TLS, next.TLSInsecureSkipVerify = false, false
	next.Username, next.PasswordPlain, next.PasswordEnv = "", "", ""
	next.Port, next.Ports = 0, nil
	if !slices.Contains(names, next.PrimaryInterface) {
		next.PrimaryInterface = ""
	}

	if err := o.admin.Put(ctx, next); err != nil {
		o.logger.Warn("lite_addon.convert.put_failed",
			slog.String("central", name), slog.String("err", err.Error()))
		return false, false
	}
	o.logger.Info("lite_addon.convert.done",
		slog.String("central", name),
		slog.String("interfaces", strings.Join(names, ",")),
		slog.String("token_file", o.tokenPath))
	return true, false
}

// reportStrandedCentrals names, on a box without the add-on token, the CCU
// centrals that point at the box: they wait for a CCU forever, and without
// the token the daemon cannot move them over.
func (o *liteAddonOnboarder) reportStrandedCentrals(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := o.admin.List(ctx)
	if err != nil {
		return
	}
	o.recordStranded(o.localCCUCentrals(ctx, rows), "this add-on has no API token of the system")
}

// recordStranded puts the centrals the daemon left as CCU centrals on
// /health, with the reason and the way out. Non-critical, and recorded once
// (Sticky): it states a boot-time fact, not a heartbeat.
func (o *liteAddonOnboarder) recordStranded(names []string, reason string) {
	if len(names) == 0 {
		return
	}
	o.logger.Warn("lite_addon.convert.skipped",
		slog.String("centrals", strings.Join(names, ",")), slog.String("reason", reason))
	if o.tracker == nil {
		return
	}
	o.tracker.Record(liteAddonCentralsHealthComponent, health.Sample{
		Healthy: false,
		Sticky:  true,
		Note: "configured as a CCU, but this system runs openccu-lite: " + strings.Join(names, ", ") +
			" (" + reason + ") — switch the central to openccu-lite in its settings, " +
			"or install the add-on from the system's add-on page and restart it",
	})
}

// hostPort splits the base URL into the row's host and json_rpc_port.
// The production default yields 127.0.0.1 with port 0 — the lite
// profile's own default, the box web server's port 80.
func (o *liteAddonOnboarder) hostPort() (host string, port int) {
	u, err := url.Parse(o.baseURL)
	if err != nil || u.Hostname() == "" {
		return "127.0.0.1", 0
	}
	if p := u.Port(); p != "" {
		if n, aerr := strconv.Atoi(p); aerr == nil {
			port = n
		}
	}
	return u.Hostname(), port
}

// centralNameFromHostname derives the central's name from the box's
// hostname, taken once at onboarding — central names are sticky by
// design, so a later rename of the box deliberately does not follow.
// Characters outside the callback router's allowlist become "-", and an
// empty or unusable result falls back to a fixed name.
func centralNameFromHostname(hostname string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(hostname))
	mapped = strings.Trim(mapped, "-")
	if mapped == "" || hmtypes.ValidateCentralName(mapped) != nil {
		return liteAddonFallbackName
	}
	return mapped
}
