// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	gosql "database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/secret"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// auditOverlay bundles the SQLite-backed audit / config stores opened in
// the early "audit DB + config overlay" phase of the composition root.
// Every field is read further down daemonServeWithDeps; the per-field
// call-site aliases keep the downstream wiring unchanged. Fields stay nil
// when the audit DB could not be opened (in-memory-only audit), preserving
// the original nil-DB degraded behaviour.
type auditOverlay struct {
	buf          *audit.Buffer
	rec          audit.Recorder
	db           *gosql.DB
	durableStats *audit.DurableSinkStats

	sqUsers     *sqlitestore.UserStore
	sqTokens    *sqlitestore.TokenStore
	sqCentrals  *sqlitestore.CentralsStore
	sqSections  *sqlitestore.ConfigSectionStore
	configStore *configstore.Store
}

// wireAuditOverlay opens the SQLite-backed audit / config store BEFORE the
// central registry is built. The DB carries SPA-side live edits (centrals,
// MQTT section, …) that have to land in cfg before bootstrap.Build snapshots
// cfg.Centrals — otherwise a CCU the operator added via the SPA only takes
// effect on the NEXT restart.
//
// The seed-from-YAML logic + auth-chain rewire stay further down where the
// in-memory user/token stores are constructed; only the DB open + the
// section/central overlay run here.
//
// The audit/config DB handle is released by the returned teardown, which the
// caller defers early so it runs late (LIFO) — after the health probe and the
// stores that read it. A leaked handle blocks temp-dir cleanup on Windows.
// teardown is always non-nil (a no-op when the DB could not be opened).
func wireAuditOverlay(ctx context.Context, cfg *config.Config, logger *slog.Logger) (ov *auditOverlay, teardown func()) {
	ov = &auditOverlay{}
	teardown = func() {}

	ov.buf = audit.NewBuffer(500)
	ov.rec, ov.db, ov.durableStats = wireAuditPersistenceWithDB(cfg, ov.buf, logger) //nolint:contextcheck // wireAuditPersistenceWithDB has no ctx parameter; it creates its own internal context
	if ov.db != nil {
		db := ov.db
		teardown = func() { _ = db.Close() }
	}
	if ov.db != nil {
		ov.sqUsers = sqlitestore.NewUserStore(ov.db)
		ov.sqTokens = sqlitestore.NewTokenStore(ov.db)
		ov.sqCentrals = sqlitestore.NewCentralsStore(ov.db)
		ov.sqSections = sqlitestore.NewConfigSectionStore(ov.db)
		bootstrapCfg := &config.BootstrapConfig{
			DataDir: cfg.DataDir,
			Logging: cfg.Logging,
			Listen:  config.BootstrapListen{REST: cfg.North.REST.Listen, UI: cfg.North.UI.Listen},
		}
		// Resolve the at-rest secret cipher (ADR 0027) and wire it into the
		// section + centrals stores so secret-classed fields are sealed
		// transparently on write and opened on read.
		cipher, cerr := secret.Load(cfg.DataDir, os.Getenv, logger)
		if cerr != nil {
			logger.Warn("secret.load", slog.String("err", cerr.Error()),
				slog.String("effect", "config secrets stored in plaintext"))
			cipher = &secret.Cipher{}
		}
		ov.sqCentrals.SetCipher(cipher)
		ov.sqSections.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
			return configstore.TransformSectionJSON(cipher, configstore.Section(section), value, seal)
		})
		ov.configStore = configstore.New(bootstrapCfg, ov.sqSections, ov.sqCentrals,
			configstore.WithEnvLookup(os.Getenv))
		// First-run seed: copy the YAML-loaded config sections into the DB
		// once (no-op when any section row exists). Secrets are sealed by the
		// section store's transform hook. Runs before OverlayInto so the DB is
		// populated for this and every subsequent boot.
		if n, err := ov.configStore.SeedSectionsFromConfig(ctx, cfg, "yaml-bootstrap"); err != nil {
			logger.Warn("configstore.seed", slog.String("err", err.Error()))
		} else if n > 0 {
			logger.Info("configstore.seed", slog.Int("sections", n))
		}
		if _, err := ov.configStore.OverlayInto(ctx, cfg); err != nil {
			logger.Warn("configstore.overlay", slog.String("err", err.Error()))
		} else if err := cfg.Validate(); err != nil {
			logger.Warn("configstore.overlay.validate", slog.String("err", err.Error()))
		}
	}
	return ov, teardown
}

// ccuArchive bundles the optional CCU translation / easymode / profile
// archives loaded in the "CCU translation archive" phase. Each field is read
// downstream (parameter-label adapters, UI-schema adapter, …).
type ccuArchive struct {
	translations *ccudata.Translations
	easymode     *ccudata.Easymode
	profiles     *ccudata.ProfileStore
}

// loadCCUArchive loads the optional CCU translation archive plus the embedded
// easymode + profile stores. Operators can drop a translations file into
// cfg.CCUData.TranslationsPath so the UI shows localised device/parameter
// labels; a missing/empty path falls back to the raw CCU strings, and parse
// errors are logged and degraded to empty. Loaded BEFORE the EventBridge so
// the bridge can hand the labeler down into the MQTT discovery `name` field —
// without it HA shows raw uppercase parameter ids.
func loadCCUArchive(cfg *config.Config, logger *slog.Logger) *ccuArchive {
	return &ccuArchive{
		translations: loadTranslations(cfg, logger),
		easymode:     loadEasymode(logger),
		profiles:     loadProfiles(logger),
	}
}

// xmlrpcCallback bundles the XML-RPC callback listener and the cancellable
// context the listener (and the BIN-RPC listener that follows) is served on.
// The ctx field is consumed by the BIN-RPC phase, which serves its own
// listener on the same cancellable context so both shut down together.
type xmlrpcCallback struct {
	ctx     context.Context
	srv     *rpcserver.XMLRPCServer
	baseURL string
}

// wireXMLRPCCallback stands up the shared XML-RPC callback server (routes by
// `/RPC2/<central_name>`). A binding failure is logged but not fatal — the
// daemon would otherwise silently lose every CCU value-change event, so the
// caller continues with a nil server. The returned teardown cancels the
// callback context (folded from the original `defer cancelCallback()`); the
// caller defers it.
func wireXMLRPCCallback(ctx context.Context, cfg *config.Config, logger *slog.Logger) (cb *xmlrpcCallback, teardown func()) {
	callbackCtx, cancelCallback := context.WithCancel(ctx)
	cb = &xmlrpcCallback{ctx: callbackCtx}
	srv, baseURL, err := startCallbackServer(callbackCtx, cfg, logger)
	if err != nil {
		logger.Warn("callback.start.failed", slog.String("err", err.Error()))
	}
	cb.srv = srv
	cb.baseURL = baseURL
	if srv != nil {
		logger.Info("callback.listen",
			slog.String("addr", srv.Addr().String()),
			slog.String("base_url", baseURL))
	}
	return cb, cancelCallback
}

// binrpcCallback bundles the shared BIN-RPC callback listener (CUxD) and the
// public callback address advertised to CUxD. A nil server is a valid degraded
// state — WireCentrals skips CUxD registration when BINRPCCallbackServer is nil.
type binrpcCallback struct {
	srv  *rpcserver.BINRPCServer
	addr string
}

// wireBINRPCCallback stands up the shared BIN-RPC callback listener for CUxD
// interfaces. Routing uses the interface_id carried inside every BIN-RPC
// envelope. The listener serves on callbackCtx so it shuts down together with
// the XML-RPC callback server. A binding failure is logged and degraded to a
// nil server.
func wireBINRPCCallback(callbackCtx context.Context, cfg *config.Config, logger *slog.Logger) *binrpcCallback {
	cb := &binrpcCallback{}
	binHost := cfg.Callback.Host
	if binHost == "" {
		binHost = "0.0.0.0"
	}
	binAddr := fmt.Sprintf("%s:%d", binHost, cfg.Callback.BinPort)
	binCfg := rpcserver.BINRPCConfig{
		Addr:   binAddr,
		Logger: logger.With(slog.String("component", "callback.binrpc")),
	}
	srv, binErr := rpcserver.NewBINRPCServer(binCfg) //nolint:contextcheck // NewBINRPCServer/bindAddr has no ctx parameter; bind is instantaneous
	if binErr != nil {
		logger.Warn("callback.binrpc.start.failed", slog.String("err", binErr.Error()))
		return cb
	}
	cb.srv = srv
	go func() {
		if serveErr := srv.Serve(callbackCtx); serveErr != nil {
			logger.Warn("callback.binrpc.serve", slog.String("err", serveErr.Error()))
		}
	}()
	publicHost := cfg.Callback.PublicHost
	if publicHost == "" {
		publicHost = autodetectCallbackHost(cfg) //nolint:contextcheck // test callers outside owned set prevent threading ctx; UDP bind is instantaneous
	}
	if tcpAddr, ok := srv.Addr().(*net.TCPAddr); ok && publicHost != "" {
		cb.addr = fmt.Sprintf("%s:%d", publicHost, tcpAddr.Port)
	}
	logger.Info("callback.binrpc.listen",
		slog.String("addr", srv.Addr().String()),
		slog.String("public_addr", cb.addr))
	return cb
}
