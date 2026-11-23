// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/configstore"
)

// sectionApplyTimeout bounds one live apply. The MQTT swap connects the
// new stack before tearing the old one down, so the ceiling has to cover
// a broker handshake — but it runs inside the operator's PUT, so it must
// not be the 30 s the file-watcher path allows itself.
const sectionApplyTimeout = 15 * time.Second

// sectionReloader is what a section applier needs from a subsystem:
// rebuild yourself from the current effective config, and say whether
// there is anything to rebuild. *mqttReloadAdapter satisfies it.
//
// Available is not redundant with a nil check. The reload adapters are
// constructed unconditionally and report their own absence, so a nil
// test here would be false for a daemon that has no MQTT stack at all —
// and every north.mqtt save on such a daemon would answer the operator
// with an error instead of "stored, takes effect at the next restart".
type sectionReloader interface {
	Reload(ctx context.Context) (time.Duration, error)
	Available() bool
}

// sectionApplier hands a freshly saved config section to the subsystem
// that owns it, so an operator's edit takes effect without a restart.
//
// Only sections whose subsystem can genuinely rebuild itself appear in
// the table. Everything else answers "not applied", which is the honest
// report: the value is stored and takes effect at the next restart.
//
// The gap this closes: `north.mqtt` declares no restart-required field,
// so the schema and the save response both told an operator the change
// was live. It was not. The running Bridge bakes the topic base and the
// two plane toggles into an immutable BridgeConfig at construction, and
// the one path that rebuilds it — the file watcher's hot reload — never
// fires for a section the SPA writes straight into the database. The
// rebuild machinery existed and simply had no caller on this path.
type sectionApplier struct {
	mqtt   sectionReloader
	matter matterSectionApplier
	logger *slog.Logger
}

// matterSectionApplier applies the part of a north.matter save the running
// node can take live — today the node label (matterNodeLabeler); every
// other north.matter field is restart-required. It reports whether the
// node changed.
type matterSectionApplier interface {
	ApplyMatterSection(ctx context.Context) (bool, error)
}

// matterLabelSection reads the effective north.matter.node_label and hands
// it to the running node's labeler. The config save and the file watcher
// both go through it, so the two paths cannot disagree.
type matterLabelSection struct {
	deps   *reloadDeps
	logger *slog.Logger
}

// ApplyMatterSection implements matterSectionApplier. Only a freshly
// assembled config is applied: the fallback snapshot is the last one
// recorded (at boot, on a file reload or an MQTT reload), which a label
// saved in the SPA since then need not be part of — applying it could turn
// the node's label back.
func (m matterLabelSection) ApplyMatterSection(ctx context.Context) (bool, error) {
	l := m.deps.MatterNodeLabeler()
	if l == nil {
		return false, nil
	}
	cfg, fresh := m.deps.AssembleConfig(ctx)
	if cfg == nil || !fresh {
		if m.logger != nil {
			m.logger.Warn("matter.bridge.node_label_not_applied",
				slog.String("reason", "the effective config could not be assembled"),
				slog.String("effect", "the label applies at the next start"))
		}
		return false, nil
	}
	return l.Apply(ctx, cfg.North.Matter.WithDefaults().NodeLabel)
}

// newSectionApplier binds the per-section reloaders. A reloader that is
// nil, or that reports itself unavailable, leaves its section reporting
// "not applied" rather than claiming a rebuild that cannot happen.
func newSectionApplier(mqtt sectionReloader, matter matterSectionApplier, logger *slog.Logger) *sectionApplier {
	if logger == nil {
		logger = slog.Default()
	}
	return &sectionApplier{mqtt: mqtt, matter: matter, logger: logger}
}

// ApplySection implements handlers.SectionApplier.
func (a *sectionApplier) ApplySection(ctx context.Context, section configstore.Section) (bool, error) {
	if a == nil {
		return false, nil
	}
	switch section {
	case configstore.SectionMQTT:
		if a.mqtt == nil || !a.mqtt.Available() {
			return false, nil
		}
		applyCtx, cancel := context.WithTimeout(ctx, sectionApplyTimeout)
		defer cancel()
		took, err := a.mqtt.Reload(applyCtx)
		if err != nil {
			a.logger.Warn("config.section.apply_failed",
				slog.String("section", string(section)),
				slog.Duration("took", took),
				slog.String("error", err.Error()),
				slog.String("effect", "the value is stored and takes effect at the next restart"))
			return false, err
		}
		a.logger.Info("config.section.applied",
			slog.String("section", string(section)),
			slog.Duration("took", took))
		return true, nil
	case configstore.SectionMatter:
		if a.matter == nil {
			return false, nil
		}
		applyCtx, cancel := context.WithTimeout(ctx, sectionApplyTimeout)
		defer cancel()
		applied, err := a.matter.ApplyMatterSection(applyCtx)
		if err != nil {
			a.logger.Warn("config.section.apply_failed",
				slog.String("section", string(section)),
				slog.String("error", err.Error()),
				slog.String("effect", "the value is stored and takes effect at the next restart"))
			return false, err
		}
		if applied {
			a.logger.Info("config.section.applied", slog.String("section", string(section)))
		}
		return applied, nil
	default:
		return false, nil
	}
}
