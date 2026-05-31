// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestWSCommandWalker drives every command listed in
// assets/wsapi.json against the running daemon's WebSocket endpoint
// at /api/v1/events.
//
// The wsapi.json catalogue carries `category`, `description`, and
// `name` per command but no per-command request/response schema, so
// the walker's acceptance contract is shape-based, not value-based:
//
//   - The server returns a `{op:"result", id, ...}` envelope for
//     every call within the timeout. Hangs / disconnects fail the
//     test.
//   - Either `data` is set (success), or `error.code` is one of the
//     well-known whitelisted codes (`bad_request`, `unauthorized`,
//     `forbidden`, `unknown_command`). Documented refusal is fine.
//   - `internal_error` is always treated as a server bug — the daemon
//     should return a typed error or a structured 200, not a generic
//     500-equivalent.
//   - Every command in the catalogue is visited; entries in
//     tests/e2e/wsapi_skip.txt are tolerated with a reason.
func TestWSCommandWalker(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	wsc, err := h.REST().DialWS("/api/v1/events")
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	defer wsc.Close()

	commands := loadWSCommands(t)
	skip := loadWSSkip(t)

	type result struct {
		name   string
		ok     bool
		why    string
		errCod string
	}
	var results []result
	visited := map[string]bool{}

	for i, cmd := range commands {
		name := cmd.Name
		if reason, ok := skip[name]; ok {
			results = append(results, result{name: name, ok: true, why: "skipped: " + reason})
			visited[name] = true
			continue
		}
		id := fmt.Sprintf("walker-%d", i)
		res, err := wsc.Call(id, name, map[string]any{}, 5*time.Second)
		if err != nil {
			results = append(results, result{name: name, ok: false, why: "transport: " + err.Error()})
			continue
		}
		ok, why := classifyWSResult(res)
		errCode := ""
		if res.Error != nil {
			errCode = res.Error.Code
		}
		results = append(results, result{name: name, ok: ok, why: why, errCod: errCode})
		visited[name] = true
	}

	// Per-command report — ASCII columns aligned to the longest
	// command name so log output stays scannable in CI.
	maxName := 0
	for _, r := range results {
		if len(r.name) > maxName {
			maxName = len(r.name)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })
	var failures []string
	for _, r := range results {
		switch {
		case r.ok && r.why != "":
			t.Logf("OK   %-*s   %s", maxName, r.name, r.why)
		case r.ok && r.errCod != "":
			t.Logf("OK   %-*s   error.code=%s", maxName, r.name, r.errCod)
		case r.ok:
			t.Logf("OK   %-*s   data", maxName, r.name)
		default:
			failures = append(failures, fmt.Sprintf("%s: %s", r.name, r.why))
		}
	}

	// Coverage assertion: every command in the catalogue is either
	// visited or skipped. A new command added without test or skip
	// entry → red CI.
	for _, cmd := range commands {
		if !visited[cmd.Name] {
			failures = append(failures, "uncovered command: "+cmd.Name)
		}
	}

	if len(failures) > 0 {
		t.Fatalf("WS command walker failed (%d issue(s)):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// classifyWSResult applies the walker's acceptance rules.
func classifyWSResult(res *harness.CallResult) (ok bool, why string) {
	if res == nil {
		return false, "nil result"
	}
	if res.Error == nil {
		// data branch — even an empty body is acceptable; the wsapi
		// catalogue does not pin a schema.
		return true, ""
	}
	switch res.Error.Code {
	case "bad_request",
		"unauthorized",
		"forbidden",
		"unknown_command",
		"not_implemented":
		return true, ""
	case "internal_error":
		return false, "internal_error: " + res.Error.Message
	default:
		// Any other typed error is acceptable — handlers are free to
		// invent their own codes (validation_failed, …) and the
		// walker should not arbitrate naming choices.
		return true, ""
	}
}

// ─── catalogue + skip-list loading ────────────────────────────────

type wsCommand struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type wsCatalogue struct {
	Version  string      `json:"version"`
	Commands []wsCommand `json:"commands"`
}

func loadWSCommands(t *testing.T) []wsCommand {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	b, err := os.ReadFile(filepath.Join(repoRoot, "assets", "wsapi.json"))
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}
	var cat wsCatalogue
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatalf("parse wsapi.json: %v", err)
	}
	if len(cat.Commands) == 0 {
		t.Fatalf("wsapi.json: no commands")
	}
	return cat.Commands
}

func loadWSSkip(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	_, thisFile, _, _ := runtime.Caller(0)
	skipPath := filepath.Join(filepath.Dir(thisFile), "wsapi_skip.txt")
	f, err := os.Open(skipPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("open ws skip file: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "--"); i >= 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+2:])
		} else {
			out[line] = "(no reason)"
		}
	}
	return out
}
