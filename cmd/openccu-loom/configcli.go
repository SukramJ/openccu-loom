// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

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
	ExportedAt    string                     `json:"exported_at"`
	DaemonVersion string                     `json:"daemon_version"`
	Sections      map[string]json.RawMessage `json:"sections"`
	Centrals      []sqlite.CentralRow        `json:"centrals"`
	Users         []exportedUser             `json:"users"`
	Tokens        []exportedToken            `json:"tokens"`
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
	userStore := sqlite.NewUserStore(db)
	tokenStore := sqlite.NewTokenStore(db)

	// Collect sections.
	sectionRows, err := sectionStore.List(ctx)
	if err != nil {
		return fmt.Errorf("config export: list sections: %w", err)
	}
	sections := make(map[string]json.RawMessage, len(sectionRows))
	for _, r := range sectionRows {
		sections[r.Section] = json.RawMessage(r.ValueJSON)
	}

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

func configImport(args []string, stdout, stderr io.Writer) error {
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

	raw, err := os.ReadFile(importFile) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("config import: read file: %w", err)
	}
	var doc configExportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("config import: parse: %w", err)
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

	if *replaceMode {
		// Delete all existing sections.
		existing, err := sectionStore.List(ctx)
		if err != nil {
			return fmt.Errorf("config import: list sections: %w", err)
		}
		for _, r := range existing {
			if err := sectionStore.Delete(ctx, r.Section); err != nil {
				return fmt.Errorf("config import: delete section %s: %w", r.Section, err)
			}
		}
		// Delete all existing centrals.
		existingCentrals, err := centralsStore.List(ctx)
		if err != nil {
			return fmt.Errorf("config import: list centrals: %w", err)
		}
		for i := range existingCentrals {
			if err := centralsStore.Delete(ctx, existingCentrals[i].Name); err != nil {
				return fmt.Errorf("config import: delete central %s: %w", existingCentrals[i].Name, err)
			}
		}
	}

	// Upsert sections.
	for section, valueJSON := range doc.Sections {
		if _, err := sectionStore.Put(ctx, section, []byte(valueJSON), "cli-import"); err != nil {
			return fmt.Errorf("config import: put section %s: %w", section, err)
		}
	}

	// Upsert centrals.
	for i := range doc.Centrals {
		if err := centralsStore.Put(ctx, doc.Centrals[i]); err != nil {
			return fmt.Errorf("config import: put central %s: %w", doc.Centrals[i].Name, err)
		}
	}

	_, _ = fmt.Fprintf(stdout, "config import: imported %d section(s) and %d central(s)\n",
		len(doc.Sections), len(doc.Centrals))
	return nil
}

// dbDSN returns the DSN for the daemon SQLite database given a DataDir.
func dbDSN(dataDir string) string {
	return dataDir + "/openccu-loom.db"
}
