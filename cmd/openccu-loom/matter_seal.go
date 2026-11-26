// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"errors"
	"fmt"
	"log/slog"

	"thingfabric.dev/go-fabric/node"
	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/secret"
)

// matterSealLabel is the HKDF label of the key the Matter credential store
// is sealed under (ADR 0092). Changing it changes the key: every sealed
// database would stop opening.
const matterSealLabel = "openccu-loom matter-store seal v1"

// matterSealCanaryKey is the reserved matter_settings row go-fabric's
// NewSealed writes to mark a sealed database (go-fabric store/seal.go:427,
// settingSealCanary). go-fabric exports no probe for it; matterStoreSealed
// reads the row, and TestMatterStoreSealedFollowsTheCanary pins that the
// probe answers as NewSealed and Erase leave the database.
const matterSealCanaryKey = "store.seal_canary"

// The reasons a sealed store does not open; the bridge stays off.
var (
	// errMatterSealKeyMissing: the database is sealed and no master key
	// resolved.
	errMatterSealKeyMissing = errors.New("the Matter credential store is sealed and no master key is available")
	// errMatterSealKeyMismatch: the master key does not open the store.
	errMatterSealKeyMismatch = errors.New("the master key does not open the sealed Matter credential store")
)

// matterSealer is the sealer of the Matter credential store under cipher's
// master key, nil when no master key resolved.
func matterSealer(cipher *secret.Cipher) (matterstore.Sealer, error) {
	if !cipher.Available() {
		return nil, nil //nolint:nilnil // no key is a state, not an error: the store stays clear
	}
	key, err := cipher.DeriveKey(matterSealLabel)
	if err != nil {
		return nil, err
	}
	return node.NewAESGCMSealer(key)
}

// matterStoreSealed reports whether db carries go-fabric's seal canary.
func matterStoreSealed(ctx context.Context, db *gosql.DB) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM matter_settings WHERE key = ?`, matterSealCanaryKey).Scan(&one)
	switch {
	case errors.Is(err, gosql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("probe the seal canary: %w", err)
	}
	return true, nil
}

// openMatterStore opens the Matter credential store: sealed under the
// master key whenever one resolved — which seals a clear database on the
// way in — else clear, unless the database was sealed before. A sealed
// database that the key does not open, or that meets no key, is refused
// (errMatterSealKeyMismatch, errMatterSealKeyMissing): the bridge then stays
// off rather than start on credentials it cannot read. scrubOwed reports a
// sealing whose scrub of the file did not finish; go-fabric retries it on
// every open.
func openMatterStore(ctx context.Context, db *gosql.DB, sealer matterstore.Sealer) (st *matterstore.Store, scrubOwed bool, err error) {
	if sealer == nil {
		sealed, err := matterStoreSealed(ctx, db)
		if err != nil {
			return nil, false, err
		}
		if sealed {
			return nil, false, errMatterSealKeyMissing
		}
		return matterstore.New(db), false, nil
	}
	st, err = matterstore.NewSealed(ctx, db, sealer)
	if err != nil {
		if errors.Is(err, matterstore.ErrUnseal) {
			return nil, false, fmt.Errorf("%w: %w", errMatterSealKeyMismatch, err)
		}
		return nil, false, err
	}
	owed, err := st.ScrubOwed(ctx)
	if err != nil {
		return nil, false, err
	}
	return st, owed, nil
}

// matterSealRefused reports a sealed store the bridge cannot open: the log
// line and the health component name the reason and the way out.
func matterSealRefused(err error, tracker *health.Tracker, logger *slog.Logger) {
	hint := "restore the master key the store was sealed under (OPENCCU_LOOM_SECRET_KEY or <data_dir>/secret.key); " +
		"if it is lost, stop the daemon and run `openccu-loom matter reset` to start the bridge unpaired"
	logger.Error("matter.bridge.seal.refused", slog.String("err", err.Error()), slog.String("hint", hint))
	if tracker == nil {
		return
	}
	note := "bridge off: the Matter credential store is sealed and no master key is available"
	if errors.Is(err, errMatterSealKeyMismatch) {
		note = "bridge off: the master key does not open the sealed Matter credential store"
	}
	tracker.RecordUnhealthy(matterHealthComponentName, health.Sample{Healthy: false, Note: note, Sticky: true})
}
