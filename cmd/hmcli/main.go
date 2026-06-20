// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Command hmcli is the admin CLI counterpart to the openccu-loom daemon.
// It offers `version`, `config validate`, and `openapi show` so operators
// can sanity-check a deployment without reaching for curl.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing subcommand")
	}
	switch args[0] {
	case "version", "--version", "-v":
		printVersion(stdout)
		return nil
	case "config":
		return cmdConfig(args[1:], stdout, stderr)
	case "cache":
		return cmdCache(args[1:], stdout, stderr)
	case "export-def":
		return cmdExportDef(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	}
	printUsage(stderr)
	return fmt.Errorf("unknown subcommand %q", args[0])
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `hmcli %s — openccu-loom admin CLI

Usage:
  hmcli version                 print version metadata
  hmcli config validate <file>  validate a openccu-loom YAML config
  hmcli cache clear [flags]     clear CCU-derivable caches (online) or SQLite rows (--offline)
  hmcli export-def -address <a> [-host URL] [-token T] [-out file]
                                download a device-definition zip from a daemon
  hmcli help                    print this help
`, build.Version)
}

func printVersion(w io.Writer) {
	_, _ = fmt.Fprintf(w, "hmcli %s (commit %s, built %s, %s/%s)\n",
		build.Version, build.Commit, build.BuildDate, runtime.GOOS, runtime.GOARCH)
}

func cmdConfig(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("config: missing operation (try: validate)")
	}
	if rest[0] != "validate" {
		return fmt.Errorf("config: unknown operation %q", rest[0])
	}
	if len(rest) < 2 {
		return errors.New("config validate: missing path")
	}
	cfg, err := config.Load(rest[1])
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "ok: %d centrals, locale=%s\n", len(cfg.Centrals), cfg.Locale)
	return nil
}
