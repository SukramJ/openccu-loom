// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// cmdCache dispatches the `cache` subcommand. Today the only operation is
// `clear` (ADR 0042): drop the CCU-derivable caches for a scope and re-pull
// them through the daemon, or (with --offline) delete the persisted rows
// straight from the SQLite database when the daemon is down.
func cmdCache(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("cache", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("cache: missing operation (try: clear)")
	}
	if rest[0] != "clear" {
		return fmt.Errorf("cache: unknown operation %q", rest[0])
	}
	return cmdCacheClear(rest[1:], stdout, stderr)
}

// cmdCacheClear parses the flags for `cache clear`, validates the scope and
// its required qualifiers, then dispatches to the online (REST) or offline
// (direct-SQLite) path.
func cmdCacheClear(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("cache clear", flag.ContinueOnError)
	fs.SetOutput(stderr)

	scope := fs.String("scope", "global", "target scope: global|central|interface|device")
	central := fs.String("central", "", "central name (required for scope=central/interface/device)")
	iface := fs.String("interface", "", "interface name (required for scope=interface/device)")
	device := fs.String("device", "", "device address (required for scope=device)")
	offline := fs.Bool("offline", false, "clear persisted rows directly against SQLite instead of calling the daemon")
	url := fs.String("url", "http://localhost:8119", "daemon base URL (online mode)")
	token := fs.String("token", "", "API bearer token (online mode)")
	cfgPath := fs.String("config", "", "config file path (offline mode; required for scope=global/central)")
	dbPath := fs.String("db", "", "override DB path (offline mode; skips the config DataDir lookup)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	kind := cachereset.ScopeKind(*scope)
	switch kind {
	case cachereset.ScopeGlobal, cachereset.ScopeCentral, cachereset.ScopeInterface, cachereset.ScopeDevice:
		// valid
	default:
		return fmt.Errorf("cache clear: --scope must be one of global|central|interface|device, got %q", *scope)
	}

	// Required qualifiers per scope level mirror cachereset.Scope.Validate so
	// the CLI fails fast before any network or DB I/O.
	switch kind {
	case cachereset.ScopeCentral:
		if *central == "" {
			return errors.New("cache clear: --central is required for scope=central")
		}
	case cachereset.ScopeInterface:
		if *central == "" {
			return errors.New("cache clear: --central is required for scope=interface")
		}
		if *iface == "" {
			return errors.New("cache clear: --interface is required for scope=interface")
		}
	case cachereset.ScopeDevice:
		if *central == "" {
			return errors.New("cache clear: --central is required for scope=device")
		}
		if *iface == "" {
			return errors.New("cache clear: --interface is required for scope=device")
		}
		if *device == "" {
			return errors.New("cache clear: --device is required for scope=device")
		}
	case cachereset.ScopeGlobal:
		// no qualifiers required
	}

	if *offline {
		return runCacheClearOffline(kind, *central, *iface, *device, *cfgPath, *dbPath, stdout)
	}
	return runCacheClearOnline(kind, *central, *iface, *device, *url, *token, stdout, stderr)
}

// clearSummary is the human-facing roll-up the CLI prints after a clear. It is
// independent of the wire report so the offline path (which has no daemon
// report) can populate it directly.
type clearSummary struct {
	scope          string
	devices        int64
	paramsets      int64
	values         int64
	master         int64
	centralsReinit []string
	errors         []string
}

// runCacheClearOnline POSTs the scope to the daemon's cache-clear endpoint and
// prints the report it returns. A non-2xx status writes the error body to
// stderr and returns a non-nil error so the process exits non-zero.
func runCacheClearOnline(
	kind cachereset.ScopeKind,
	central, iface, device, baseURL, token string,
	stdout, stderr io.Writer,
) error {
	// The handler ignores qualifiers above the scope level, so sending all four
	// (empty strings included) keeps the body shape uniform.
	body := map[string]string{
		"kind":      string(kind),
		"central":   central,
		"interface": iface,
		"device":    device,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("cache clear: marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/v1/admin/cache/clear",
		bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("cache clear: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cache clear: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "%s\n", strings.TrimSpace(string(respBody)))
		return fmt.Errorf("cache clear: HTTP %s", resp.Status)
	}

	var report cachereset.Report
	if err := json.Unmarshal(respBody, &report); err != nil {
		return fmt.Errorf("cache clear: decode response: %w", err)
	}
	printSummary(stdout, clearSummary{
		scope:          string(kind),
		devices:        report.Devices,
		paramsets:      report.Paramsets,
		values:         report.Values,
		master:         report.Master,
		centralsReinit: report.CentralsReinit,
		errors:         report.Errors,
	}, false)
	return nil
}

// runCacheClearOffline opens the SQLite database directly and deletes the
// CCU-derivable rows for the scope. It never touches operator/system tables —
// only the VALUES and MASTER caches, the two stores whose contents the daemon
// re-pulls from the CCU on the next start. Because it cannot re-pull itself, it
// prints a restart notice.
func runCacheClearOffline(
	kind cachereset.ScopeKind,
	central, iface, device, cfgPath, dbOverride string,
	stdout io.Writer,
) error {
	dsn, cfg, err := resolveOfflineDSN(kind, cfgPath, dbOverride)
	if err != nil {
		return err
	}

	openCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := sqlite.Open(openCtx, dsn)
	if err != nil {
		return fmt.Errorf("cache clear: open DB: %w", err)
	}
	defer func() { _ = db.Close() }()

	valStore := sqlite.NewValuesCacheStore(db)
	masterStore := sqlite.NewMasterValuesStore(db)
	sum := clearSummary{scope: string(kind)}

	ctx := context.Background()
	if kind == cachereset.ScopeDevice {
		if errV := valStore.DeleteDevice(ctx, central, iface, device); errV != nil {
			sum.errors = append(sum.errors, fmt.Sprintf("values[%s/%s]: %v", central, iface, errV))
		}
		if errM := masterStore.DeleteDevice(ctx, central, iface, device); errM != nil {
			sum.errors = append(sum.errors, fmt.Sprintf("master[%s/%s]: %v", central, iface, errM))
		}
		// The device-scoped deletes do not report row counts; leave them at 0.
		printSummary(stdout, sum, true)
		return nil
	}

	units, err := resolveOfflineUnits(kind, central, iface, cfg)
	if err != nil {
		return err
	}
	for _, u := range units {
		n, errV := valStore.DeleteForInterface(ctx, u.central, u.iface)
		if errV != nil {
			sum.errors = append(sum.errors, fmt.Sprintf("values[%s/%s]: %v", u.central, u.iface, errV))
		} else {
			sum.values += n
		}
		m, errM := masterStore.DeleteForInterface(ctx, u.central, u.iface)
		if errM != nil {
			sum.errors = append(sum.errors, fmt.Sprintf("master[%s/%s]: %v", u.central, u.iface, errM))
		} else {
			sum.master += m
		}
	}

	printSummary(stdout, sum, true)
	return nil
}

// ifaceUnit is one (central, interface) the offline clear loop deletes at.
type ifaceUnit struct{ central, iface string }

// resolveOfflineDSN derives the SQLite DSN for offline mode. --db wins when
// set; otherwise the DSN is computed from the config DataDir (default ./var),
// mirroring the daemon's values_cache wiring. Config is loaded whenever a path
// is given so the global/central scopes can enumerate interfaces from it.
func resolveOfflineDSN(kind cachereset.ScopeKind, cfgPath, dbOverride string) (dsn string, cfg *config.Config, err error) {
	needsConfig := kind == cachereset.ScopeGlobal || kind == cachereset.ScopeCentral
	if dbOverride == "" && cfgPath == "" {
		return "", nil, errors.New("cache clear: offline mode requires --config or --db")
	}
	if needsConfig && cfgPath == "" {
		return "", nil, fmt.Errorf("cache clear: offline scope=%s requires --config to enumerate interfaces", kind)
	}

	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return "", nil, fmt.Errorf("cache clear: load config: %w", err)
		}
	}

	if dbOverride != "" {
		dsn = "file:" + dbOverride + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
		return dsn, cfg, nil
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn = "file:" + filepath.Join(dataDir, "openccu-loom.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
	return dsn, cfg, nil
}

// resolveOfflineUnits expands a non-device scope into the (central, interface)
// pairs to clear. global/central enumerate from the loaded config; interface
// uses the explicit qualifiers and needs no config.
func resolveOfflineUnits(kind cachereset.ScopeKind, central, iface string, cfg *config.Config) ([]ifaceUnit, error) {
	switch kind {
	case cachereset.ScopeInterface:
		return []ifaceUnit{{central: central, iface: iface}}, nil
	case cachereset.ScopeGlobal:
		if cfg == nil {
			return nil, errors.New("cache clear: --config required to enumerate centrals for scope=global")
		}
		var units []ifaceUnit
		for i := range cfg.Centrals {
			cc := &cfg.Centrals[i]
			for j := range cc.Interfaces {
				units = append(units, ifaceUnit{central: cc.Name, iface: cc.Interfaces[j].Name})
			}
		}
		return units, nil
	case cachereset.ScopeCentral:
		if cfg == nil {
			return nil, errors.New("cache clear: --config required to enumerate interfaces for scope=central")
		}
		for i := range cfg.Centrals {
			cc := &cfg.Centrals[i]
			if cc.Name != central {
				continue
			}
			units := make([]ifaceUnit, 0, len(cc.Interfaces))
			for j := range cc.Interfaces {
				units = append(units, ifaceUnit{central: cc.Name, iface: cc.Interfaces[j].Name})
			}
			return units, nil
		}
		return nil, fmt.Errorf("cache clear: central %q not found in config", central)
	default:
		return nil, fmt.Errorf("cache clear: scope %q has no interface units", kind)
	}
}

// printSummary writes a human-readable roll-up. Online runs show which centrals
// were re-pulled; offline runs replace that with a restart notice, since no
// daemon is available to re-ingest.
func printSummary(w io.Writer, s clearSummary, offline bool) {
	_, _ = fmt.Fprintf(w, "Cache cleared: scope=%s\n", s.scope)
	_, _ = fmt.Fprintf(w, "  devices:    %d\n", s.devices)
	_, _ = fmt.Fprintf(w, "  paramsets:  %d\n", s.paramsets)
	_, _ = fmt.Fprintf(w, "  values:     %d\n", s.values)
	_, _ = fmt.Fprintf(w, "  master:     %d\n", s.master)

	if offline {
		_, _ = fmt.Fprintln(w, "Restart the daemon to re-pull the cleared caches.")
	} else if len(s.centralsReinit) == 0 {
		_, _ = fmt.Fprintln(w, "  re-pulled:  (none)")
	} else {
		_, _ = fmt.Fprintf(w, "  re-pulled:  %s\n", strings.Join(s.centralsReinit, ", "))
	}

	for _, e := range s.errors {
		_, _ = fmt.Fprintf(w, "error: %s\n", e)
	}
}
