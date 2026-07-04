// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/config"
)

func loadTranslations(cfg *config.Config, logger *slog.Logger) *ccudata.Translations {
	if path := cfg.CCUData.TranslationsPath; path != "" {
		t, err := ccudata.LoadTranslations(path)
		if err != nil {
			logger.Warn("ccudata.translations.load",
				slog.String("path", path),
				slog.String("err", err.Error()))
		} else {
			logger.Info("ccudata.translations.ok",
				slog.String("source", "file"),
				slog.String("path", path),
				slog.Int("locales", len(t.Locales())))
			return t
		}
	}
	t, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		logger.Warn("ccudata.translations.embedded",
			slog.String("err", err.Error()))
		return ccudata.Empty()
	}
	logger.Info("ccudata.translations.ok",
		slog.String("source", "embedded"),
		slog.Int("locales", len(t.Locales())))
	return t
}

// loadEasymode decodes the easymode archive: a configured
// ccu_data.easymode_path wins (same override contract as the
// translations archive, see ADR 0003), otherwise the embedded archive
// is used. Errors are logged and return an empty struct so the UI
// schema adapter sees a non-nil value and falls through to "no groups".
func loadEasymode(cfg *config.Config, logger *slog.Logger) *ccudata.Easymode {
	if path := cfg.CCUData.EasymodePath; path != "" {
		em, err := ccudata.LoadEasymode(path)
		if err != nil {
			logger.Warn("ccudata.easymode.load",
				slog.String("path", path),
				slog.String("err", err.Error()))
		} else {
			logger.Info("ccudata.easymode.ok",
				slog.String("source", "file"),
				slog.String("path", path),
				slog.Int("channels", len(em.ChannelMetadata)),
				slog.Int("presets", len(em.OptionPresets)),
				slog.Int("cross_rules", len(em.CrossValidations.Rules)))
			return em
		}
	}
	em, err := ccudata.LoadEasymodeEmbedded()
	if err != nil {
		logger.Warn("ccudata.easymode.embedded", slog.String("err", err.Error()))
		return ccudata.EmptyEasymode()
	}
	logger.Info("ccudata.easymode.ok",
		slog.String("source", "embedded"),
		slog.Int("channels", len(em.ChannelMetadata)),
		slog.Int("presets", len(em.OptionPresets)),
		slog.Int("cross_rules", len(em.CrossValidations.Rules)))
	return em
}

// loadProfiles decodes the embedded receiver-profile catalogue.
func loadProfiles(logger *slog.Logger) *ccudata.ProfileStore {
	ps, err := ccudata.LoadProfilesEmbedded()
	if err != nil {
		logger.Warn("ccudata.profiles.embedded", slog.String("err", err.Error()))
	}
	if ps == nil {
		return nil
	}
	logger.Info("ccudata.profiles.ok",
		slog.String("source", "embedded"),
		slog.Int("receivers", len(ps.Receivers)),
		slog.Int("aliases", len(ps.Aliases)))
	return ps
}
