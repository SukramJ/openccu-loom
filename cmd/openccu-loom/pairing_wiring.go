// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/pairing"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

// pairedTokenMinter turns an approved pairing into a durable API token
// and leaves the same audit trail a manual token creation leaves — the
// operator's ledger must not care which door a credential came through.
type pairedTokenMinter struct {
	tokens *sqlitestore.TokenStore
	rec    audit.Recorder
}

func (m pairedTokenMinter) MintPairedToken(ctx context.Context, subject string, role auth.Role) (token, fingerprint string, err error) {
	res, err := m.tokens.Create(ctx, sqlitestore.CreateInput{Subject: subject, Role: role})
	if err != nil {
		return "", "", err
	}
	if m.rec != nil {
		m.rec.Record(audit.Entry{
			User:   "pairing",
			Action: audit.ActionTokenCreate,
			Note:   "paired subject=" + subject + " role=" + string(role) + " fingerprint=" + res.Fingerprint,
		})
	}
	return res.Token, res.Fingerprint, nil
}

// buildPairingService wires the client token pairing (ADR 0076). It
// needs the durable token store — a daemon without one cannot mint, so
// the surface answers pairing_off. The manager binds its code to the
// certificate the daemon serves (nil behind a TLS-terminating proxy or
// over plain HTTP), announces list changes on the WS hub, and feeds the
// operator warnings so a waiting request surfaces on Diagnostics.
func buildPairingService(
	cfg *config.Config,
	tokens *sqlitestore.TokenStore,
	rec audit.Recorder,
	tlsReloader *rest.CertReloader,
	hub *ws.Hub,
	warningsSvc *warnings.Aggregator,
	logger *slog.Logger,
) handlers.PairingService {
	if tokens == nil {
		return nil
	}
	fingerprint := func() []byte { return nil }
	if tlsReloader != nil {
		fingerprint = tlsReloader.LeafFingerprint
	}
	mgr := &pairing.Manager{
		Minter:      pairedTokenMinter{tokens: tokens, rec: rec},
		Enabled:     cfg.North.REST.Auth.Pairing.IsEnabled,
		Local:       pairing.LocalHost,
		Fingerprint: fingerprint,
		Log:         logger,
	}
	if hub != nil {
		mgr.OnChange = func() { hub.PublishPairingRequestsChanged(mgr.PendingCount(), time.Now()) }
	}
	if warningsSvc != nil {
		warningsSvc.WithPairing(mgr)
	}
	return mgr
}
