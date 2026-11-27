// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	gosql "database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/datalock"
	"github.com/SukramJ/openccu-loom/internal/secret"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// runSecretCLI dispatches `openccu-loom secret <subcommand>`.
func runSecretCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "rotate" {
		_, _ = fmt.Fprintln(stderr, "usage: openccu-loom secret rotate [--config path] [--new-key base64]")
		return errors.New("secret: missing or unknown subcommand")
	}
	fs := flag.NewFlagSet("secret rotate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml")
	newKey := fs.String("new-key", "", "the next master key (base64, 32 bytes); generated when empty")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	bc, err := loadBootstrapForCLI(*configPath, stderr)
	if err != nil {
		return err
	}
	return withOfflineDB(bc.DataDir, func(ctx context.Context, db *gosql.DB) error {
		return rotateMasterKey(ctx, db, bc.DataDir, os.Getenv, *newKey, stdout)
	})
}

// runMatterCLI dispatches `openccu-loom matter <subcommand>`.
func runMatterCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "reset" {
		_, _ = fmt.Fprintln(stderr, "usage: openccu-loom matter reset --yes [--config path]")
		return errors.New("matter: missing or unknown subcommand")
	}
	fs := flag.NewFlagSet("matter reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml")
	yes := fs.Bool("yes", false, "confirm: every paired controller has to commission the bridge again")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*yes {
		return errors.New("matter reset erases the bridge's fabrics, credentials and endpoint numbers; every paired controller has to commission it again — confirm with --yes")
	}
	bc, err := loadBootstrapForCLI(*configPath, stderr)
	if err != nil {
		return err
	}
	return withOfflineDB(bc.DataDir, func(ctx context.Context, db *gosql.DB) error {
		if err := resetMatterStore(ctx, db); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "matter reset: the bridge starts unpaired with a new identity; the device selection is kept")
		return nil
	})
}

// withOfflineDB runs fn on the daemon's database while it holds the data
// directory's lock, so it never writes under a running daemon (ADR 0092).
func withOfflineDB(dataDir string, fn func(context.Context, *gosql.DB) error) error {
	lock, err := datalock.Acquire(dataDir)
	if errors.Is(err, datalock.ErrHeld) {
		return fmt.Errorf("the daemon runs on %s — stop it first: %w", dataDir, err)
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbDSN(dataDir))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()
	return fn(ctx, db)
}

// resetMatterStore returns the Matter bridge to its factory state without a
// running node, the way out of a lost master key (ADR 0092): the endpoint
// records and the credential store erased — the seal canary with them, so
// the next start opens the store under whatever key it has — and a fresh
// random UniqueID pinned, so a controller's cache does not take the bridge
// that comes back for the one it knew. It needs no key: go-fabric's Erase
// deletes rows and opens none (store/erase.go:78-98). Mirrors the erase
// node.FactoryReset makes (go-fabric node/factory_reset.go:98-168); the
// daemon's own state — the exposure selection, the root serial — stays.
func resetMatterStore(ctx context.Context, db *gosql.DB) error {
	if err := matterendpoint.New(db).EraseEndpoints(ctx); err != nil {
		return fmt.Errorf("matter reset: erase endpoint records: %w", err)
	}
	st := matterstore.New(db)
	if err := st.Erase(ctx); err != nil && !errors.Is(err, matterstore.ErrScrubIncomplete) {
		return fmt.Errorf("matter reset: erase the credential store: %w", err)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Errorf("matter reset: %w", err)
	}
	if err := st.SetSetting(ctx, matterstore.SettingUniqueID, hex.EncodeToString(b[:])); err != nil {
		return fmt.Errorf("matter reset: pin a fresh UniqueID: %w", err)
	}
	return nil
}

// rotateMasterKey moves every value sealed under the master key to a new
// one (ADR 0092): the configuration secrets (config_sections leaves,
// centrals.password_plain) re-sealed in one transaction, then the Matter
// credential store with go-fabric's Reseal, then the key installed.
//
// It is re-runnable. With a key file the next key is written to
// secret.key.next before anything changes and replaces secret.key last;
// a run that finds secret.key.next resumes with it. Every step accepts a
// value already under the next key, so a rotation interrupted after any
// commit finishes on the next run. With OPENCCU_LOOM_SECRET_KEY the next
// key cannot be stored for the operator: it is printed first, and a resumed
// run passes it back with --new-key.
func rotateMasterKey(ctx context.Context, db *gosql.DB, dataDir string, getenv func(string) string, newKey string, out io.Writer) error {
	old, err := secret.LoadExisting(dataDir, getenv)
	if err != nil {
		return fmt.Errorf("secret rotate: %w", err)
	}
	envMode := getenv(secret.EnvKeyVar) != ""
	pending := filepath.Join(dataDir, secret.PendingKeyFileName)
	nextKey, err := rotationNextKey(pending, newKey, envMode)
	if err != nil {
		return err
	}
	if envMode {
		_, _ = fmt.Fprintf(out, "secret rotate: the next master key is %s\n", secret.EncodeKey(nextKey))
		_, _ = fmt.Fprintf(out, "  keep it: an interrupted rotation resumes with --new-key %s\n", secret.EncodeKey(nextKey))
	} else if err := secret.WriteKeyFile(pending, nextKey); err != nil {
		return err
	}
	next, err := secret.NewCipher(nextKey)
	if err != nil {
		return err
	}

	sections, centrals, err := resealConfigSecrets(ctx, db, old, next)
	if err != nil {
		return fmt.Errorf("secret rotate: config secrets: %w", err)
	}
	matter, err := resealMatterStore(ctx, db, old, next)
	if err != nil {
		return fmt.Errorf("secret rotate: Matter store: %w", err)
	}

	_, _ = fmt.Fprintf(out, "secret rotate: %d config section(s), %d central password(s) re-sealed; Matter store: %s\n", sections, centrals, matter)
	if envMode {
		_, _ = fmt.Fprintf(out, "secret rotate: set %s to the new key before the next start\n", secret.EnvKeyVar)
	} else if err := os.Rename(pending, filepath.Join(dataDir, secret.KeyFileName)); err != nil {
		return fmt.Errorf("secret rotate: install the new key: %w", err)
	}
	_, _ = fmt.Fprintln(out, "secret rotate: backup archives written before the rotation open only with the old key — keep it as long as you keep them")
	return nil
}

// rotationNextKey is the key a rotation moves to: the pending file a
// previous run left (key-file mode), else --new-key, else a fresh one. A
// --new-key that contradicts the pending file is refused: the stores may
// already hold values under the pending key.
func rotationNextKey(pending, newKey string, envMode bool) ([]byte, error) {
	var given []byte
	if newKey != "" {
		k, err := secret.DecodeKey(newKey)
		if err != nil {
			return nil, fmt.Errorf("secret rotate: --new-key: %w", err)
		}
		given = k
	}
	if !envMode {
		k, err := secret.ReadKeyFile(pending)
		switch {
		case err == nil:
			if given != nil && !bytes.Equal(given, k) {
				return nil, fmt.Errorf("secret rotate: %s holds another key than --new-key: an earlier rotation is unfinished; run without --new-key to finish it", pending)
			}
			return k, nil
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("secret rotate: %w", err)
		}
	}
	if given != nil {
		return given, nil
	}
	return secret.GenerateKey()
}

// resealConfigSecrets re-seals the configuration secrets under next in one
// transaction. A value that does not open under old but opens under next
// was moved by an interrupted run and is re-sealed as it is.
func resealConfigSecrets(ctx context.Context, db *gosql.DB, old, next *secret.Cipher) (sections, centrals int, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	type row struct{ key, value string }
	read := func(query string) ([]row, error) {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.key, &r.value); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	secs, err := read(`SELECT section, value_json FROM config_sections`)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range secs {
		sec := configstore.Section(r.key)
		plain, err := configstore.TransformSectionJSON(old, sec, []byte(r.value), false)
		if err != nil {
			if plain, err = configstore.TransformSectionJSON(next, sec, []byte(r.value), false); err != nil {
				return 0, 0, fmt.Errorf("section %s opens under neither key: %w", r.key, err)
			}
		}
		sealed, err := configstore.TransformSectionJSON(next, sec, plain, true)
		if err != nil {
			return 0, 0, fmt.Errorf("section %s: %w", r.key, err)
		}
		if string(sealed) == r.value {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE config_sections SET value_json = ? WHERE section = ?`, string(sealed), r.key); err != nil {
			return 0, 0, err
		}
		sections++
	}

	cents, err := read(`SELECT name, password_plain FROM centrals WHERE password_plain <> ''`)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range cents {
		plain, err := old.Open(r.value)
		if err != nil {
			if plain, err = next.Open(r.value); err != nil {
				return 0, 0, fmt.Errorf("central %s password opens under neither key: %w", r.key, err)
			}
		}
		sealed, err := next.Seal(plain)
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE centrals SET password_plain = ? WHERE name = ?`, sealed, r.key); err != nil {
			return 0, 0, err
		}
		centrals++
	}
	return sections, centrals, tx.Commit()
}

// resealMatterStore moves a sealed Matter store from old's derived key to
// next's (go-fabric store.Reseal: one transaction, then a scrub). A store
// never sealed is left as it is — the next start seals it under the new
// key — and one already under next (an interrupted run) is done. It
// returns what it did, for the report.
func resealMatterStore(ctx context.Context, db *gosql.DB, old, next *secret.Cipher) (string, error) {
	sealed, err := matterStoreSealed(ctx, db)
	if err != nil {
		return "", err
	}
	if !sealed {
		return "not sealed yet (the next start seals it under the new key)", nil
	}
	oldSealer, err := matterSealer(old)
	if err != nil {
		return "", err
	}
	nextSealer, err := matterSealer(next)
	if err != nil {
		return "", err
	}
	err = matterstore.Reseal(ctx, db, oldSealer, nextSealer)
	switch {
	case err == nil:
		return "re-sealed", nil
	case errors.Is(err, matterstore.ErrScrubIncomplete):
		return "re-sealed (the scrub of the old bytes is owed; the next start finishes it)", nil
	case errors.Is(err, matterstore.ErrUnseal):
		if _, _, nerr := openMatterStore(ctx, db, nextSealer); nerr == nil {
			return "already under the new key", nil
		}
		return "", err
	default:
		return "", err
	}
}
