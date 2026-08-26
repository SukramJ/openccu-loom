// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Command hmcli is the admin CLI counterpart to the openccu-loom daemon.
// It offers `version`, `config validate`, `cache clear`, `devices`, and
// `export-def` subcommands so operators can inspect and drive a daemon
// without curl. Commands are structured with the Cobra CLI framework; the
// command groups that carry their own `flag.FlagSet` (config, cache,
// devices, export-def) run with Cobra flag-parsing disabled so their
// existing flag surface is preserved verbatim.
//
// Exit codes: 0 on success. On failure the code distinguishes the failure
// class so scripts do not have to parse the error text: exitAuthFailed (3)
// when the daemon rejected the request with 401 Unauthorized or 403
// Forbidden, exitNotFound (4) for 404 Not Found, exitServerFail (5) for any
// 5xx response, and exitGeneral (1) for everything else — usage errors,
// local I/O, config/offline-DB failures, network failures below the HTTP
// layer, and REST failures with any other status. Only an error from a
// completed HTTP round-trip carries a distinguishable code; see
// exitCodeFor.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// Process exit codes. Documented in the package doc comment above; keep both
// in sync when adding a new class.
const (
	exitGeneral    = 1
	exitAuthFailed = 3
	exitNotFound   = 4
	exitServerFail = 5
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(reportError(err, os.Stderr))
}

// reportError is main's test seam for its error-reporting tail: given the
// error run returned (nil on success), it writes a terminal-safe message to
// stderr and returns the process exit code. A server-controlled string
// (a device name, an error body relayed from the daemon, …) can otherwise
// carry ANSI escapes or control bytes that rewrite the operator's terminal,
// so the message is routed through sanitizeForTerminal before it is
// printed — mirroring the sanitization already applied to every
// human-readable table (see sanitize.go).
func reportError(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, sanitizeForTerminal(err.Error()))
	return exitCodeFor(err)
}

// exitCodeFor maps err to a process exit code. Only an *httpStatusError
// (a REST call that reached the daemon and got a non-2xx response) yields a
// class distinct from exitGeneral, whether returned directly or wrapped
// with %w — see the package doc comment for the code table.
func exitCodeFor(err error) int {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return exitGeneral
	}
	switch {
	case statusErr.StatusCode == http.StatusUnauthorized, statusErr.StatusCode == http.StatusForbidden:
		return exitAuthFailed
	case statusErr.StatusCode == http.StatusNotFound:
		return exitNotFound
	case statusErr.StatusCode >= 500:
		return exitServerFail
	default:
		return exitGeneral
	}
}

// run is the test seam: it builds the Cobra command tree wired to the given
// writers and executes it against args. It returns the error Cobra surfaces so
// callers (and tests) decide how to report it. On a missing or unknown
// subcommand the root usage block is written to stderr, mirroring the pre-Cobra
// behaviour operators relied on.
func run(args []string, stdout, stderr io.Writer) error {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil && isUsageError(err) {
		_, _ = fmt.Fprint(stderr, root.UsageString())
	}
	return err
}

// isUsageError reports whether err is a top-level dispatch failure (no
// subcommand given, or an unknown one) for which the usage block should be
// printed. Errors raised inside a resolved subcommand (config validation,
// network failures, …) are left alone so operators are not buried in usage.
func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	if err.Error() == "missing subcommand" {
		return true
	}
	return strings.Contains(err.Error(), "unknown command")
}

// newRootCmd assembles the hmcli command tree. The root carries the shared
// connection flags as persistent flags for discoverability, but the leaf
// command groups parse their own flags (via flag.FlagSet) — so the persistent
// flags are declarative help only and each group runs with DisableFlagParsing.
func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	var showVersion bool

	root := &cobra.Command{
		Use:   "hmcli",
		Short: "openccu-loom admin CLI",
		Long:  "hmcli " + build.Version + " — openccu-loom admin CLI",
		// Errors are returned through run() rather than printed by Cobra;
		// run() re-prints the usage block to stderr only for top-level
		// dispatch failures (see isUsageError), so command-internal errors
		// stay quiet.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if showVersion {
				printVersion(stdout)
				return nil
			}
			return errors.New("missing subcommand")
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	// `-v` / `--version` mirror the legacy short/long version flags. Cobra's
	// default help flag (`-h` / `--help`) already prints usage to stdout.
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "print version metadata and exit")

	// Declarative shared connection flags. The command groups below re-parse
	// them from their own flag.FlagSet, so these only shape `--help` output.
	root.PersistentFlags().String("host", defaultHost, "daemon REST base URL")
	root.PersistentFlags().String("token", "", "API bearer token (Authorization: Bearer; or set "+envToken+")")
	root.PersistentFlags().String("user", "", "basic-auth username (alternative to --token)")
	root.PersistentFlags().String("password", "", "basic-auth password (or set "+envPassword+")")
	root.PersistentFlags().String("cacert", "", "path to a PEM CA bundle to trust for TLS")
	root.PersistentFlags().Bool("insecure", false, "skip TLS certificate verification (dangerous; off by default)")
	root.PersistentFlags().Duration("timeout", 0, "request timeout")

	root.AddCommand(
		newVersionCmd(stdout),
		newDelegateCmd("config", "Validate a openccu-loom YAML config (config validate <file>)", cmdConfig, stdout, stderr),
		newDelegateCmd("cache", "Clear CCU-derivable caches (online) or SQLite rows (--offline)", cmdCache, stdout, stderr),
		newDelegateCmd("devices", "List and drive devices (list, get, get-value, set)", cmdDevices, stdout, stderr),
		newDelegateCmd("export-def", "Download a device-definition zip from a daemon", cmdExportDef, stdout, stderr),
		newDelegateCmd("sysvar", "Manage system variables (list, get, set, fetch)", cmdSysvar, stdout, stderr),
		newDelegateCmd("program", "Manage CCU programs (list, get, run, enable, disable)", cmdProgram, stdout, stderr),
		newDelegateCmd("paramset", "Read and write device paramsets (get, set)", cmdParamset, stdout, stderr),
		newDelegateCmd("events", "Tail the daemon's live event stream (events tail)", cmdEvents, stdout, stderr),
		newDelegateCmd("alarm", "Break-glass alarm-panel control (status, arm, disarm, silence, ack)", cmdAlarm, stdout, stderr),
	)
	return root
}

// newVersionCmd is the `version` subcommand; it prints the same metadata as the
// `--version` flag.
func newVersionCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version metadata",
		RunE: func(_ *cobra.Command, _ []string) error {
			printVersion(stdout)
			return nil
		},
	}
}

// newDelegateCmd wraps an existing flag.FlagSet-based subcommand as a Cobra
// command. Cobra flag parsing is disabled so every argument after the command
// name (including flag-shaped tokens like `--scope` or `-host`) is handed to
// the delegate verbatim, preserving its self-contained parsing.
func newDelegateCmd(
	use, short string,
	delegate func(args []string, stdout, stderr io.Writer) error,
	stdout, stderr io.Writer,
) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return delegate(args, stdout, stderr)
		},
	}
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
