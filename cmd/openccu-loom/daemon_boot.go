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
	"path/filepath"
	"time"

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

	sqUsers    *sqlitestore.UserStore
	sqTokens   *sqlitestore.TokenStore
	sqCentrals *sqlitestore.CentralsStore
	sqSections *sqlitestore.ConfigSectionStore

	sqDiscoveryIgnore *sqlitestore.DiscoveryIgnoreStore
	authSessions      *sqlitestore.AuthSessionStore
	configStore       *configstore.Store

	// secretsAvailable reports whether the at-rest secret cipher resolved a
	// master key. When false the daemon stores config secrets in plaintext
	// (ADR 0027 resilient fallback); the daemon surfaces this on /health and
	// as a metric. Only meaningful when db != nil.
	secretsAvailable bool

	// yamlBase is the config as loaded from YAML/env, captured before
	// OverlayInto mutates it with the DB-tier sections. Re-assembling the
	// effective config for a reload has to start here rather than from
	// defaults: a section that exists only in the YAML has no DB row (the
	// seed runs once, on a database with no sections at all), so an assembly
	// starting from defaults would silently drop it.
	yamlBase *config.Config
}

// openLoomDB opens the single shared <DataDir>/openccu-loom.db database used
// by every persistence-backed subsystem in the composition root (audit,
// session recorder, incident recorder, Matter bridge). Centralising the open
// here means those subsystems receive the already-open handle as a parameter
// instead of each independently resolving the dataDir fallback, building the
// DSN, and opening the file — four opens racing to migrate the same SQLite
// file at boot. Returns nil (logged as a warning) when the DB cannot be
// opened; callers must degrade gracefully on a nil handle.
//
// The open runs on its own [context.Background] timeout rather than the
// caller's ctx: this executes early in the composition root, before the
// central registry exists, and must not abort mid-migration just because the
// daemon's lifecycle ctx (which also carries shutdown) happens to already be
// cancelled — e.g. a signal arriving in the same instant as boot. Every
// original per-site open this replaces used the same independent timeout.
func openLoomDB(cfg *config.Config, logger *slog.Logger) *gosql.DB {
	if cfg == nil {
		return nil
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := sqlitestore.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
	openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sqlitestore.Open(openCtx, dsn)
	if err != nil {
		logger.Warn("loom_db.open_failed", slog.String("dsn", dsn), slog.String("err", err.Error()))
		return nil
	}
	return db
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
	// Open the single shared <DataDir>/openccu-loom.db handle here, before any
	// downstream wiring. wireAuditPersistenceWithDB, wireSessionRecorderPersistence,
	// wireIncidentRecorder and startMatterBridge all read/write the same file;
	// threading this one handle down (instead of each opening its own) avoids
	// four independent sqlite.Open calls racing to migrate the same database on
	// every boot. teardown closes it last (LIFO defer in daemonServeWithDeps),
	// after every downstream user has torn down.
	ov.db = openLoomDB(cfg, logger) //nolint:contextcheck // deliberately opens on its own timeout, independent of the lifecycle ctx — see openLoomDB doc comment
	var stopAuditSink func()
	ov.rec, ov.durableStats, stopAuditSink = wireAuditPersistenceWithDB(ov.db, ov.buf, logger) //nolint:contextcheck // the durable sink persists on its own per-write timeout, detached from the boot ctx by design
	if ov.db != nil {
		db := ov.db
		// Order is the whole point: the durable audit sink persists off its
		// own queue through this very handle, so the worker is joined — which
		// drains what is still queued — and only then is the handle closed.
		// Closing first ends a burst of mutations that arrived just before
		// shutdown with "database is closed", leaving the append-only trail
		// short of exactly the writes an operator is most likely to look for.
		// This runs as the last teardown in the LIFO chain, by which point
		// every producer is already down and cannot refill the queue.
		teardown = func() {
			stopAuditSink()
			_ = db.Close()
		}
	}
	if ov.db != nil {
		ov.sqUsers = sqlitestore.NewUserStore(ov.db)
		ov.sqTokens = sqlitestore.NewTokenStore(ov.db)
		ov.sqCentrals = sqlitestore.NewCentralsStore(ov.db)
		ov.sqSections = sqlitestore.NewConfigSectionStore(ov.db)
		ov.sqDiscoveryIgnore = sqlitestore.NewDiscoveryIgnoreStore(ov.db)
		ov.authSessions = sqlitestore.NewAuthSessionStore(ov.db)
		bootstrapCfg := &config.BootstrapConfig{
			DataDir: cfg.DataDir,
			Logging: cfg.Logging,
			Listen:  config.BootstrapListen{REST: cfg.North.REST.Listen},
			// The hardening toggle has to travel with the tier that owns it:
			// [configstore.Store] re-applies the bootstrap tier on every
			// re-assembly, so omitting it here would silently re-open the
			// unauthenticated onboarding surface on a reload.
			Bootstrap: cfg.Bootstrap,
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
		ov.secretsAvailable = cipher.Available()
		ov.sqCentrals.SetCipher(cipher)
		ov.sqSections.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
			return configstore.TransformSectionJSON(cipher, configstore.Section(section), value, seal)
		})
		// Capture the pre-overlay YAML/env tier so a later reload can replay
		// exactly this assembly against the then-current DB rows — and hand the
		// same base to the store, so Store.Effective assembles from where the
		// daemon started rather than from the built-in defaults.
		ov.yamlBase = config.Clone(cfg)
		ov.configStore = configstore.New(bootstrapCfg, ov.sqSections, ov.sqCentrals,
			configstore.WithEnvLookup(os.Getenv),
			configstore.WithBaseConfig(ov.yamlBase))
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
		// Replay the persisted audit history into the in-memory read
		// buffer so GET /api/v1/audit surfaces past changes instead of
		// only what happened since this boot (the recorder persists to
		// SQLite, but the read path is the buffer).
		hydrateAuditBuffer(ctx, ov.buf, sqlitestore.NewAuditStore(ov.db), logger)
	}
	return ov, teardown
}

// wireConfigAssembler teaches the reload path how to re-derive the effective
// config, replaying the boot assembly against the then-current database: the
// captured YAML/env base with the DB-tier sections overlaid.
//
// Without this a reload triggered from the SPA rebuilds its subsystem from the
// config snapshot the daemon booted with. A section the operator saved through
// the UI lands in the database, which no config-file event follows, so the
// snapshot never advances and the reload silently re-applies the old values.
//
// Starting from the YAML base rather than from defaults matters: the section
// seed only runs on a database with no sections at all, so a section that
// exists solely in the YAML has no row to overlay, and an assembly starting
// from defaults would drop it.
//
// A no-op when the config store or the captured base is unavailable (no
// database); reload consumers then fall back to the recorded snapshot.
func wireConfigAssembler(deps *reloadDeps, ov *auditOverlay) {
	if deps == nil || ov == nil || ov.configStore == nil || ov.yamlBase == nil {
		return
	}
	store, base := ov.configStore, ov.yamlBase
	deps.SetConfigAssembler(func(ctx context.Context) (*config.Config, error) {
		next := config.Clone(base)
		if _, err := store.OverlayInto(ctx, next); err != nil {
			return nil, err
		}
		if err := next.Validate(); err != nil {
			return nil, err
		}
		return next, nil
	})
}

// hydrateAuditBuffer loads the most recent persisted audit entries into
// the in-memory buffer on boot. The store lists newest-first; the buffer
// prepends on Record, so replay oldest-first to preserve order. Existing
// timestamps are kept (Buffer.Record only stamps zero-value times).
func hydrateAuditBuffer(ctx context.Context, buf *audit.Buffer, store *sqlitestore.AuditStore, logger *slog.Logger) {
	if buf == nil || store == nil {
		return
	}
	entries, err := store.List(ctx, "", 500)
	if err != nil {
		logger.Warn("audit.buffer.hydrate", slog.String("err", err.Error()))
		return
	}
	for i := len(entries) - 1; i >= 0; i-- {
		buf.Record(entries[i])
	}
	if len(entries) > 0 {
		logger.Info("audit.buffer.hydrated", slog.Int("entries", len(entries)))
	}
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
		easymode:     loadEasymode(cfg, logger),
		profiles:     loadProfiles(logger),
	}
}

// xmlrpcCallback bundles the XML-RPC callback listener and the cancellable
// context the listener (and the BIN-RPC listener that follows) is served on.
// The ctx field is consumed by the BIN-RPC phase, which serves its own
// listener on the same cancellable context so both shut down together.
type xmlrpcCallback struct {
	ctx  context.Context
	srv  *rpcserver.XMLRPCServer
	port int
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
	srv, port, err := startCallbackServer(callbackCtx, cfg, logger)
	if err != nil {
		logger.Warn("callback.start.failed", slog.String("err", err.Error()))
	}
	cb.srv = srv
	cb.port = port
	if srv != nil {
		logger.Info("callback.listen",
			slog.String("addr", srv.Addr().String()),
			slog.Int("port", port))
	}
	return cb, cancelCallback
}

// binrpcCallback bundles the shared BIN-RPC callback listener (CUxD) and
// its effective port. The host advertised to CUxD is resolved per-central
// by the caller (callbackHostFor), like the XML-RPC path. A nil server is
// a valid degraded state — WireCentrals skips CUxD registration when
// BINRPCCallbackServer is nil.
type binrpcCallback struct {
	srv  *rpcserver.BINRPCServer
	port int
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
		Addr:           binAddr,
		Logger:         logger.With(slog.String("component", "callback.binrpc")),
		MaxConnections: cfg.Callback.MaxConnections,
		PeerAllowlist:  buildCallbackAllowlist(callbackCtx, cfg, logger),
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
	cb.port = cfg.Callback.BinPort
	if tcpAddr, ok := srv.Addr().(*net.TCPAddr); ok {
		cb.port = tcpAddr.Port
	}
	logger.Info("callback.binrpc.listen",
		slog.String("addr", srv.Addr().String()),
		slog.Int("port", cb.port))
	return cb
}
