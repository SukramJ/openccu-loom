// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/ccuauth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// buildCCUAuthStore returns an [auth.UserStore] that validates logins
// against the CCU's own user database (ADR 0043), or nil when the
// feature is disabled or no central registry is available.
func buildCCUAuthStore(cfg *config.Config, reg *central.Registry, logger *slog.Logger) auth.UserStore {
	cc := cfg.North.REST.Auth.CCU
	if !cc.Enabled || reg == nil {
		return nil
	}
	domain := adapter.NewCCUAuthDomain(reg, cfg.Centrals, logger)
	store := ccuauth.New(domain, ccuauth.Config{
		Central:      cc.Central,
		MinUserLevel: cc.MinUserLevel,
		RoleMapping:  parseCCURoleMapping(cc.RoleMapping, logger),
	}, logger)
	logger.Info("auth.ccu.enabled",
		slog.String("central", cc.Central), slog.Int("min_user_level", cc.MinUserLevel))
	return store
}

// loginChainWithCCU builds the login chain in the ADR-0043 order: local
// stores first (SQLite, then the in-memory fallback), CCU last. With no
// CCU store it degrades to the plain local chain.
func loginChainWithCCU(sqUsers, memUsers, ccu auth.UserStore) auth.UserStore {
	if ccu == nil {
		return auth.ChainedUserStore{Primary: sqUsers, Secondary: memUsers}
	}
	return auth.ChainedUserStore{
		Primary:   sqUsers,
		Secondary: auth.ChainedUserStore{Primary: memUsers, Secondary: ccu},
	}
}

// parseCCURoleMapping converts the string-keyed config override
// ("8"→"admin", …) into the typed map the store consumes. Invalid keys
// or roles are skipped with a warning so a typo cannot silently grant
// the wrong role.
func parseCCURoleMapping(m map[string]string, logger *slog.Logger) map[int]auth.Role {
	if len(m) == 0 {
		return nil
	}
	out := make(map[int]auth.Role, len(m))
	for k, v := range m {
		level, err := strconv.Atoi(k)
		if err != nil {
			logger.Warn("auth.ccu.role_mapping.bad_level", slog.String("key", k))
			continue
		}
		role := auth.Role(v)
		switch role {
		case auth.RoleAdmin, auth.RoleOperator, auth.RoleViewer:
			out[level] = role
		default:
			logger.Warn("auth.ccu.role_mapping.bad_role", slog.String("role", v))
		}
	}
	return out
}
