// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Add-on auto-onboarding (ADR 0077). When the daemon runs as the add-on
// on an openccu-lite box, the box mints an API token for it at every
// occulited start (the manifest's runtime.api_scopes) and writes it to a
// file only this add-on's user can read. A first boot with no centrals
// configured therefore needs no wizard step: the local box is adopted by
// itself, authenticated by that file.
const (
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
	admin       handlers.CentralAdminService
	logger      *slog.Logger
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
// host is an openccu-lite box that minted a token for this add-on and no
// central is configured yet. On every other host it is a cheap no-op.
// The goroutine ends on success, at the window's deadline, or when ctx
// is cancelled — whichever comes first.
func maybeStartLiteAddonOnboarding(ctx context.Context, admin handlers.CentralAdminService, logger *slog.Logger) {
	o := &liteAddonOnboarder{
		admin:       admin,
		logger:      logger,
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
	if len(rows) > 0 {
		// Not a first boot: the operator has configured centrals (or
		// deleted the auto-onboarded one — deliberately, so it stays
		// deleted).
		return
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
		done, retry := o.attempt(ctx, client)
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
