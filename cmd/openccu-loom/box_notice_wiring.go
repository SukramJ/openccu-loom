// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/boxnotice"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/warnings"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

const (
	// liteAddonControlTokenPath is where the box writes the add-on's
	// control token: the credential of the add-on's own notices, distinct
	// from the API token beside it. The box mints it anew whenever its
	// service starts.
	liteAddonControlTokenPath = "/run/occulite/addon-tokens/openccu-loom"
	// liteAddonControlTokenPathEnv overrides it for the tests. Production
	// never sets it.
	liteAddonControlTokenPathEnv = "OPENCCU_LOOM_LITE_ADDON_CONTROL_TOKEN_FILE" //nolint:gosec // an env var NAME, not a credential

	// boxNoticeKeyPrefix is where the server-side catalogue keeps the
	// notice sentences; a warning's message key maps onto it by its
	// suffix.
	boxNoticeKeyPrefix = "box_notice."
	warningKeyPrefix   = "warnings."
	// serviceMessageWarningPrefix marks the warning that counts a
	// central's service messages.
	serviceMessageWarningPrefix = "servicemsg:"
)

// boxNoticeDeps is what the notice mirror follows.
type boxNoticeDeps struct {
	warnings *warnings.Aggregator
	catalogs *i18n.Catalogs
	health   *health.Tracker
	reg      *central.Registry
	admin    handlers.CentralAdminService
}

// boxNotices is the running mirror as the composition root holds it.
type boxNotices struct {
	mirror *boxnotice.Mirror
	stop   func()
}

// Nudge tells the mirror that a warning source changed. Safe on nil: a
// daemon without a mirror has nothing to tell.
func (b *boxNotices) Nudge() {
	if b != nil && b.mirror != nil {
		b.mirror.Nudge()
	}
}

// Stop ends the mirror and detaches it from its sources. Safe on nil.
func (b *boxNotices) Stop() {
	if b != nil && b.stop != nil {
		b.stop()
	}
}

// boxNoticesEnabled resolves north.ui.box_notices: unset means on beside
// an openccu-lite box and off everywhere else; an explicit value
// overrides. Without a local box there is nothing to post to, whatever
// the switch says.
func boxNoticesEnabled(cfg *config.Config, logger *slog.Logger) bool {
	liteAddon := deploymentKind() == deployment.LiteAddon
	enabled := liteAddon
	if v := cfg.North.UI.BoxNotices; v != nil {
		enabled = *v
	}
	if enabled && !liteAddon {
		logger.Warn("north.ui.box_notices.enabled_but_no_local_box — notices inert (not the openccu-lite add-on)")
		return false
	}
	return enabled
}

// startBoxNotices mirrors the daemon's warnings onto the local box's
// Status page (ADR 0090) and returns the running mirror, or nil when the
// daemon is not the openccu-lite add-on or the switch is off.
//
// The mirror reconciles when a warning source changes: a health component
// of the daemon or of a central moves, an incident is recorded, or a
// central's service messages change. The pairing manager is built later,
// with the REST server, and nudges through the returned value.
func startBoxNotices(ctx context.Context, cfg *config.Config, d boxNoticeDeps, logger *slog.Logger) *boxNotices {
	if logger == nil {
		logger = slog.Default()
	}
	if d.warnings == nil || d.catalogs == nil || !boxNoticesEnabled(cfg, logger) {
		return nil
	}
	client, err := occulited.New(occulited.Config{
		BaseURL: envOr(liteAddonBaseURLEnv, liteAddonBaseURL),
		Logger:  logger,
	})
	if err != nil {
		logger.Warn("north.ui.box_notices.client_failed — notices inert", slog.String("err", err.Error()))
		return nil
	}
	return runBoxNotices(ctx, boxnotice.Config{
		Warnings: d.warnings,
		Box:      client,
		Texts:    boxNoticeTexts{catalogs: d.catalogs},
		Token:    controlTokenReader(envOr(liteAddonControlTokenPathEnv, liteAddonControlTokenPath)),
		Skip:     skipLocalBoxServiceMessages(ctx, d.admin, envOr(liteAddonTokenPathEnv, liteAddonTokenPath)),
		Logger:   logger,
	}, d)
}

// runBoxNotices starts a mirror and attaches it to the warning sources.
func runBoxNotices(ctx context.Context, mc boxnotice.Config, d boxNoticeDeps) *boxNotices {
	m := boxnotice.New(mc)
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { m.Run(runCtx) })

	var detach []func()
	if d.health != nil {
		detach = append(detach, d.health.OnStatusChange(m.Nudge))
	}
	if d.reg != nil {
		detach = append(detach, d.reg.OnRegisterDeclared(wiring.Seam{
			Name:         "box_notices.sources",
			Collaborator: "boxnotice.Mirror.Nudge",
			Phase:        wiring.PhasePerCentral,
			Why:          "a central's health, incidents and service messages change the warning list the box's Status page mirrors; without the subscription a warning reaches the box only at the next resynchronisation, minutes later",
		}, func(u *central.Unit) func() { return watchCentralWarnings(u, m.Nudge) }))
	}
	return &boxNotices{mirror: m, stop: func() {
		for _, f := range detach {
			f()
		}
		cancel()
		wg.Wait()
	}}
}

// watchCentralWarnings nudges when one central's contribution to the
// warning list may have changed, and returns what detaches it.
func watchCentralWarnings(u *central.Unit, nudge func()) func() {
	var detach []func()
	if u.Health != nil {
		detach = append(detach, u.Health.OnStatusChange(nudge))
	}
	if u.EventBus != nil {
		detach = append(detach, u.EventBus.Subscribe(func(hmevent.IncidentRecordedEvent) { nudge() }))
	}
	if u.HubModel != nil && u.HubModel.ServiceMessages != nil {
		detach = append(detach, u.HubModel.ServiceMessages.OnUpdate(func([]hubmodel.ServiceMessage) { nudge() }))
	}
	return func() {
		for _, f := range detach {
			f()
		}
	}
}

// controlTokenReader reads the add-on's control token from path at every
// call: the file is rewritten whenever the box's service starts.
func controlTokenReader(path string) func() (string, error) {
	return func() (string, error) {
		raw, err := os.ReadFile(path) //nolint:gosec // a fixed path of the box, or the test's override
		if err != nil {
			return "", fmt.Errorf("box notices: read the add-on control token: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", errors.New("box notices: the add-on control token file is empty")
		}
		return token, nil
	}
}

// skipLocalBoxServiceMessages leaves the service-message warning of the
// box the daemon runs on off the box: its Status page lists its own
// service messages already. The local box is the central that reads the
// add-on's API token file, which is how the add-on onboarding stores it.
func skipLocalBoxServiceMessages(ctx context.Context, admin handlers.CentralAdminService, apiTokenPath string) func(warnings.Warning) bool {
	return func(w warnings.Warning) bool {
		if admin == nil || w.Central == "" || !strings.HasPrefix(w.ID, serviceMessageWarningPrefix) {
			return false
		}
		row, err := admin.Get(ctx, w.Central)
		return err == nil && row.APITokenFile != "" && row.APITokenFile == apiTokenPath
	}
}

// boxNoticeTexts renders the notice sentences from the daemon's own
// catalogue, in the two languages the box requires.
type boxNoticeTexts struct {
	catalogs *i18n.Catalogs
}

func (t boxNoticeTexts) both(key string, args map[string]string) occulited.NoticeText {
	return occulited.NoticeText{DE: t.catalogs.TF("de", key, args), EN: t.catalogs.TF("en", key, args)}
}

// Warning implements [boxnotice.Texts].
func (t boxNoticeTexts) Warning(w warnings.Warning) occulited.NoticeText {
	return t.both(boxNoticeKeyPrefix+strings.TrimPrefix(w.MessageKey, warningKeyPrefix), w.Args)
}

// More implements [boxnotice.Texts].
func (t boxNoticeTexts) More(count int) occulited.NoticeText {
	return t.both(boxNoticeKeyPrefix+"more", map[string]string{"count": strconv.Itoa(count)})
}
