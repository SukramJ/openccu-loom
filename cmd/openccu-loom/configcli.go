// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/secret"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// wireConfigStoreCrypto resolves the at-rest cipher (ADR 0027) and wires
// it into the section + centrals stores so `config import` seals secrets
// and `config export` opens them (the export's own redaction / the
// --include-secrets flag then decide what actually leaves). Mirrors the
// daemon's wiring so the CLI and the running daemon agree on the key.
func wireConfigStoreCrypto(sectionStore *sqlite.ConfigSectionStore, centralsStore *sqlite.CentralsStore, dataDir string) {
	cipher, err := secret.Load(dataDir, os.Getenv, nil)
	if err != nil {
		cipher = &secret.Cipher{}
	}
	centralsStore.SetCipher(cipher)
	sectionStore.SetSecretTransform(func(section string, value []byte, seal bool) ([]byte, error) {
		return configstore.TransformSectionJSON(cipher, configstore.Section(section), value, seal)
	})
}

// runConfigCLI is the entry point for the `config` subcommand family.
func runConfigCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printConfigUsage(stderr)
		return errors.New("config: missing subcommand")
	}
	switch args[0] {
	case "export":
		return configExport(args[1:], stdout, stderr)
	case "import":
		return configImport(args[1:], stdout, stderr)
	default:
		printConfigUsage(stderr)
		return fmt.Errorf("config: unknown subcommand: %s", args[0])
	}
}

func printConfigUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: openccu-loom config <subcommand> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "subcommands:")
	_, _ = fmt.Fprintln(w, "  export   export config sections, centrals, user metadata, and token metadata to JSON")
	_, _ = fmt.Fprintln(w, "  import   import config sections and centrals from a JSON file")
}

// configExportDoc is the JSON document written by config export.
type configExportDoc struct {
	ExportedAt    string `json:"exported_at"`
	DaemonVersion string `json:"daemon_version"`
	// Redacted marks a document whose secrets were withheld (the default —
	// anything but --include-secrets). Import reads it to tell "the operator
	// never saw this credential" from "the operator cleared this credential",
	// which decides whether a null secret leaf keeps the stored value or
	// overwrites it. Without the marker an import of the default export would
	// replace every stored credential with null.
	Redacted bool                       `json:"redacted,omitempty"`
	Sections map[string]json.RawMessage `json:"sections"`
	Centrals []sqlite.CentralRow        `json:"centrals"`
	Users    []exportedUser             `json:"users"`
	Tokens   []exportedToken            `json:"tokens"`
}

// exportedUser carries subject + role only — no password hashes.
type exportedUser struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

// exportedToken carries fingerprint + subject + role — no plaintext secret.
type exportedToken struct {
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
}

// exportedSections renders the stored config sections for the export
// document, redacting secrets unless the operator asked for them.
//
// The section store decrypts on read (see wireConfigStoreCrypto), so copying a
// row verbatim writes the operator's MQTT / OIDC / Matter credentials into a
// file whose whole purpose is to be handed around. Redaction is per leaf, not
// per section: "north.mqtt" carries a broker URL next to a password.
func exportedSections(rows []sqlite.SectionRow, includeSecrets bool) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		value := r.ValueJSON
		if !includeSecrets {
			value = configstore.RedactSectionSecrets(configstore.Section(r.Section), value)
		}
		out[r.Section] = json.RawMessage(value)
	}
	return out
}

func configExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("config export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml")
	outPath := fs.String("out", "", "output file (default: stdout)")
	includeSecrets := fs.Bool("include-secrets", false, "include plaintext passwords for centrals (when stored)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	bc, err := loadBootstrapForCLI(*configPath, stderr)
	if err != nil {
		return err
	}

	ctx := context.Background()
	dbPath := dbDSN(bc.DataDir)
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("config export: open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	sectionStore := sqlite.NewConfigSectionStore(db)
	centralsStore := sqlite.NewCentralsStore(db)
	wireConfigStoreCrypto(sectionStore, centralsStore, bc.DataDir)
	userStore := sqlite.NewUserStore(db)
	tokenStore := sqlite.NewTokenStore(db)

	// Collect sections.
	sectionRows, err := sectionStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config export: list sections: %w", err)
	}
	sections := exportedSections(sectionRows, *includeSecrets)

	// Collect centrals — optionally redact plaintext passwords.
	centrals, err := centralsStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config export: list centrals: %w", err)
	}
	if !*includeSecrets {
		for i := range centrals {
			centrals[i].PasswordPlain = ""
		}
	}

	// Collect users (subject + role only).
	userRows, err := userStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config export: list users: %w", err)
	}
	users := make([]exportedUser, 0, len(userRows))
	for _, u := range userRows {
		users = append(users, exportedUser{Subject: u.Subject, Role: string(u.Role)})
	}

	// Collect tokens (fingerprint + subject + role only).
	tokenRows, err := tokenStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config export: list tokens: %w", err)
	}
	tokens := make([]exportedToken, 0, len(tokenRows))
	for _, t := range tokenRows {
		tokens = append(tokens, exportedToken{Fingerprint: t.Fingerprint, Subject: t.Subject, Role: string(t.Role)})
	}

	doc := configExportDoc{
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		DaemonVersion: build.Version,
		Redacted:      !*includeSecrets,
		Sections:      sections,
		Centrals:      centrals,
		Users:         users,
		Tokens:        tokens,
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("config export: marshal: %w", err)
	}
	data = append(data, '\n')

	if *outPath == "" {
		_, err = stdout.Write(data)
		return err
	}
	if err := os.WriteFile(*outPath, data, 0o600); err != nil {
		return fmt.Errorf("config export: write %s: %w", *outPath, err)
	}
	_, _ = fmt.Fprintf(stderr, "config exported to %s\n", *outPath)
	return nil
}

func configImport(args []string, stdout, stderr io.Writer) error { //nolint:funlen // single-purpose CLI command handler with many flag/validate branches
	fs := flag.NewFlagSet("config import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml")
	mergeMode := fs.Bool("merge", false, "merge: upsert sections and centrals, leave users/tokens untouched (default)")
	replaceMode := fs.Bool("replace", false, "replace: delete all sections and centrals first, then upsert")
	dryRun := fs.Bool("dry-run", false, "report changes without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("config import: missing <file> argument")
	}
	importFile := fs.Arg(0)

	if *mergeMode && *replaceMode {
		return errors.New("config import: --merge and --replace are mutually exclusive")
	}
	// Default is merge.
	if !*replaceMode {
		*mergeMode = true
	}

	raw, err := os.ReadFile(importFile) //nolint:gosec // operator-supplied path; see #20
	if err != nil {
		return fmt.Errorf("config import: read file: %w", err)
	}
	var doc configExportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("config import: parse: %w", err)
	}

	// Warn that a redacted document cannot carry credentials back.
	if doc.Redacted {
		_, _ = fmt.Fprintln(stderr,
			"config import: this file was exported without --include-secrets — every secret in it is "+
				"withheld; the values already stored in the database are kept for those fields")
	}

	// Warn that user/token entries are always skipped.
	if len(doc.Users) > 0 || len(doc.Tokens) > 0 {
		_, _ = fmt.Fprintln(stderr,
			"config import: user and token entries in the file are skipped — "+
				"importing users without bcrypt hashes would be a privilege escalation risk; "+
				"manage users and tokens via the SPA or the REST API")
	}

	if *dryRun {
		_, _ = fmt.Fprintf(stdout, "config import (dry-run): would import %d section(s) and %d central(s)\n",
			len(doc.Sections), len(doc.Centrals))
		if *replaceMode {
			_, _ = fmt.Fprintln(stdout, "config import (dry-run): existing sections and centrals would be deleted first")
		}
		return nil
	}

	bc, err := loadBootstrapForCLI(*configPath, stderr)
	if err != nil {
		return err
	}

	ctx := context.Background()
	dbPath := dbDSN(bc.DataDir)
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("config import: open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	sectionStore := sqlite.NewConfigSectionStore(db)
	centralsStore := sqlite.NewCentralsStore(db)
	wireConfigStoreCrypto(sectionStore, centralsStore, bc.DataDir)

	// A redacted document carries JSON null for every secret leaf and an empty
	// password_plain; the stored values are the only place those credentials
	// still exist. Read them before --replace deletes anything, so the merge
	// below has something to fall back to in both modes.
	var (
		storedSections map[string][]byte
		storedCentrals map[string]sqlite.CentralRow
	)
	if doc.Redacted {
		storedSections, storedCentrals, err = snapshotStoredSecrets(ctx, sectionStore, centralsStore)
		if err != nil {
			return err
		}
	}

	if *replaceMode {
		if err := deleteAllConfigRows(ctx, sectionStore, centralsStore); err != nil {
			return err
		}
	}

	if err := upsertImportedSections(ctx, sectionStore, doc, storedSections); err != nil {
		return err
	}
	if err := upsertImportedCentrals(ctx, centralsStore, doc, storedCentrals); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "config import: imported %d section(s) and %d central(s)\n",
		len(doc.Sections), len(doc.Centrals))
	return nil
}

// deleteAllConfigRows clears every stored section and central, the first half
// of an import in --replace mode.
func deleteAllConfigRows(
	ctx context.Context,
	sectionStore *sqlite.ConfigSectionStore,
	centralsStore *sqlite.CentralsStore,
) error {
	existing, err := sectionStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config import: list sections: %w", err)
	}
	for _, r := range existing {
		if err := sectionStore.Delete(ctx, r.Section); err != nil {
			return fmt.Errorf("config import: delete section %s: %w", r.Section, err)
		}
	}
	existingCentrals, err := centralsStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config import: list centrals: %w", err)
	}
	for i := range existingCentrals {
		if err := centralsStore.Delete(ctx, existingCentrals[i].Name); err != nil {
			return fmt.Errorf("config import: delete central %s: %w", existingCentrals[i].Name, err)
		}
	}
	return nil
}

// upsertImportedSections writes the document's sections. For a redacted
// document every withheld secret leaf falls back to the value stored in the
// database, so importing an export that was never allowed to carry the
// credentials cannot destroy them.
func upsertImportedSections(
	ctx context.Context,
	sectionStore *sqlite.ConfigSectionStore,
	doc configExportDoc,
	stored map[string][]byte,
) error {
	for section, valueJSON := range doc.Sections {
		payload := []byte(valueJSON)
		if doc.Redacted {
			payload = configstore.RestoreRedactedSecrets(configstore.Section(section), payload, stored[section])
		}
		if _, err := sectionStore.Put(ctx, section, payload, "cli-import"); err != nil {
			return fmt.Errorf("config import: put section %s: %w", section, err)
		}
	}
	return nil
}

// upsertImportedCentrals writes the document's centrals. An empty
// password_plain in a redacted document is a withheld credential, not a
// cleared one, so the stored CCU login survives the round-trip; an operator
// who wants it gone clears it on the stored row.
func upsertImportedCentrals(
	ctx context.Context,
	centralsStore *sqlite.CentralsStore,
	doc configExportDoc,
	stored map[string]sqlite.CentralRow,
) error {
	for i := range doc.Centrals {
		row := doc.Centrals[i]
		if doc.Redacted && row.PasswordPlain == "" {
			if prev, ok := stored[row.Name]; ok {
				row.PasswordPlain = prev.PasswordPlain
			}
		}
		if err := centralsStore.Put(ctx, row); err != nil {
			return fmt.Errorf("config import: put central %s: %w", row.Name, err)
		}
	}
	return nil
}

// snapshotStoredSecrets reads the section payloads and central rows currently
// in the database, keyed for the redacted-import merge. Both stores decrypt on
// read (see wireConfigStoreCrypto), so the snapshot carries the cleartext the
// import has to preserve; it never leaves the process.
func snapshotStoredSecrets(
	ctx context.Context,
	sectionStore *sqlite.ConfigSectionStore,
	centralsStore *sqlite.CentralsStore,
) (sections map[string][]byte, centrals map[string]sqlite.CentralRow, err error) {
	rows, err := sectionStore.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("config import: list sections: %w", err)
	}
	sections = make(map[string][]byte, len(rows))
	for _, r := range rows {
		sections[r.Section] = r.ValueJSON
	}

	centralRows, err := centralsStore.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("config import: list centrals: %w", err)
	}
	centrals = make(map[string]sqlite.CentralRow, len(centralRows))
	for i := range centralRows {
		centrals[centralRows[i].Name] = centralRows[i]
	}
	return sections, centrals, nil
}

// dbDSN returns the DSN for the daemon SQLite database given a DataDir.
// It routes through [sqlite.FileDSN] so the CLI opens the database with the
// same connection pragmas (foreign_keys in particular) as the daemon.
func dbDSN(dataDir string) string {
	return sqlite.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
}
