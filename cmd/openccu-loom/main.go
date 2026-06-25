// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Command openccu-loom is the daemon entrypoint.
// It recognises `version`, `run --version`, and `run`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

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
	case "version", "-v", "--version":
		printVersion(stdout)
		return nil
	case "run":
		return runDaemon(args[1:], stdout, stderr)
	case "backup":
		return runBackup(args[1:], stdout, stderr)
	case "config":
		return runConfigCLI(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runDaemon(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	configPath := fs.String("config", "", "YAML config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		printVersion(stdout)
		return nil
	}
	// Resolve the effective config path: an explicit --config wins; with
	// no flag the daemon auto-discovers a config file at conventional
	// locations (see config.ConfigSearchPaths). When nothing is found it
	// runs on built-in defaults.
	effectiveConfig := *configPath
	autoDiscovered := false
	if effectiveConfig == "" {
		if p := config.DiscoverConfigPath(); p != "" {
			effectiveConfig, autoDiscovered = p, true
		}
	}

	// Load the env-file (default ".env" or whatever the resolved config's
	// `env_file:` key points at) BEFORE parsing the full config so
	// the env-overlay layer + the configstore secret resolver both
	// see the populated environment. Missing files are not an
	// error; the loader returns nil quietly.
	envFile := config.DefaultEnvFile
	if effectiveConfig != "" {
		if buf, err := os.ReadFile(effectiveConfig); err == nil {
			if bs, perr := config.ParseBootstrap(buf); perr == nil && bs.EnvFile != "" {
				envFile = bs.EnvFile
			}
		}
	}
	if envFile != "" && envFile != "-" && envFile != "/dev/null" {
		if err := config.LoadEnvFile(envFile); err != nil {
			_, _ = fmt.Fprintf(stderr, "openccu-loom: env-file %s: %v\n", envFile, err)
			return err
		}
	}

	var cfg *config.Config
	if effectiveConfig == "" {
		// No config file: still apply the env overlay so OPENCCU_LOOM_* (above
		// all OPENCCU_LOOM_DATA_DIR, which the HA add-on sets to /data) takes
		// effect. A bare config.Default() here would pin DataDir to the
		// ephemeral "./var" and lose the database on every restart/update.
		cfg = config.DefaultWithEnv()
		if err := cfg.Validate(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stderr, "openccu-loom: no config file found, running with defaults")
	} else {
		if autoDiscovered {
			_, _ = fmt.Fprintf(stderr, "openccu-loom: using discovered config %s\n", effectiveConfig)
		}
		loaded, err := config.LoadWithEnv(effectiveConfig)
		if err != nil {
			return err
		}
		cfg = loaded
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Pass the config path so the daemon can install a [config.Watcher]
	// for hot-reload of the subset of fields documented in
	// [daemon.hotReloadHandler]. Empty path → no watcher (defaults
	// or test mode).
	return daemonServeWithReload(ctx, cfg, effectiveConfig, stdout, stderr)
}

func printVersion(w io.Writer) {
	_, _ = fmt.Fprintf(w, "openccu-loom %s\n", build.Version)
	_, _ = fmt.Fprintf(w, "  commit:    %s\n", build.Commit)
	_, _ = fmt.Fprintf(w, "  built:     %s\n", build.BuildDate)
	_, _ = fmt.Fprintf(w, "  go:        %s\n", runtime.Version())
	_, _ = fmt.Fprintf(w, "  platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: openccu-loom <command> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "commands:")
	_, _ = fmt.Fprintln(w, "  run        start the daemon")
	_, _ = fmt.Fprintln(w, "  backup     create or restore a backup archive")
	_, _ = fmt.Fprintln(w, "  config     export or import config sections and centrals")
	_, _ = fmt.Fprintln(w, "  version    print version, commit, build date")
	_, _ = fmt.Fprintln(w, "  help       show this help")
}
