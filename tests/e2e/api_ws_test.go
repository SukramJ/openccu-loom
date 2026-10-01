// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestWSCommandWalker drives every callable command listed in
// assets/wsapi.json against the running daemon's WebSocket endpoint
// at /api/v1/events. It is the declared-equals-published round trip
// for the WS command plane: the catalogue is the declaration, and a
// declared command the daemon does not serve fails the walk.
//
// Catalogue entries with `kind: "broadcast"` are server-pushed event
// topics, not commands — they are excluded from the walk by that
// field, never by name.
//
// The catalogue carries no per-command request/response schema, so the
// walker's acceptance contract is shape-based, not value-based:
//
//   - The server returns a `{op:"result", id, ...}` envelope for
//     every call within the timeout. Hangs / disconnects fail the
//     test.
//   - Either `data` is set (success), or `error.code` is an accepted
//     refusal (`bad_request`, `unauthorized`, `forbidden`).
//   - `unknown_command` always fails: it means the command is declared
//     but not registered. (It used to be whitelisted, which made the
//     walker blind to exactly that defect class.)
//   - `not_implemented` is accepted only for the commands wsapi.json's
//     own `optional_deployment_providers` list names — the catalogue's
//     contract for a nil provider. On any other command it fails: a
//     core command answering not_implemented is a silently gutted
//     handler.
//   - `rate_limited` always fails — it is lost measurement, not an
//     answer (the walker paces itself under the WS command limiter;
//     see the loop).
//   - `internal_error` is always treated as a server bug — the daemon
//     should return a typed error or a structured 200, not a generic
//     500-equivalent.
//   - Every callable command in the catalogue is visited; entries in
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
	optional := loadWSOptionalProviders(t)
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
		// Pace the walk under the daemon's per-identity WS command limiter
		// (internal/north/rest/ws/ws_ratelimit.go: 20 commands/s, burst
		// 60). Without the pause the walk burns the burst and every later
		// command answers rate_limited — which this walker treats as lost
		// measurement, not as an acceptable refusal.
		time.Sleep(60 * time.Millisecond)
		id := fmt.Sprintf("walker-%d", i)
		res, err := wsc.Call(id, name, map[string]any{}, 5*time.Second)
		if err != nil {
			results = append(results, result{name: name, ok: false, why: "transport: " + err.Error()})
			continue
		}
		ok, why := classifyWSResult(res, optional[name])
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
// optionalProvider says whether wsapi.json's optional_deployment_providers
// list names the command — the one class allowed to answer not_implemented.
func classifyWSResult(res *harness.CallResult, optionalProvider bool) (ok bool, why string) {
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
		"forbidden":
		return true, ""
	case "unknown_command":
		return false, "declared in wsapi.json but not registered (unknown_command)"
	case "not_implemented":
		if optionalProvider {
			return true, ""
		}
		return false, "core command answers not_implemented but is not in optional_deployment_providers"
	case "rate_limited":
		return false, "rate_limited: the walker lost this measurement to the command limiter"
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
	// Kind is "broadcast" for server-pushed event topics, empty for
	// callable commands — the catalogue's own distinction.
	Kind string `json:"kind"`
}

type wsCatalogue struct {
	Version  string      `json:"version"`
	Commands []wsCommand `json:"commands"`
	// OptionalProviders mirrors `optional_deployment_providers`: the
	// commands whose domain provider may legitimately be unwired, in
	// which case they answer not_implemented.
	OptionalProviders struct {
		Commands []string `json:"commands"`
	} `json:"optional_deployment_providers"`
}

func loadWSCatalogue(t *testing.T) wsCatalogue {
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
	return cat
}

// loadWSCommands returns the callable commands: every catalogue entry
// that is not a `kind: "broadcast"` event topic.
func loadWSCommands(t *testing.T) []wsCommand {
	t.Helper()
	cat := loadWSCatalogue(t)
	callable := make([]wsCommand, 0, len(cat.Commands))
	for _, c := range cat.Commands {
		if c.Kind == "broadcast" {
			continue
		}
		callable = append(callable, c)
	}
	if len(callable) == 0 {
		t.Fatalf("wsapi.json: no callable commands")
	}
	return callable
}

// loadWSOptionalProviders returns the optional_deployment_providers set —
// the commands allowed to answer not_implemented when their provider is
// unwired.
func loadWSOptionalProviders(t *testing.T) map[string]bool {
	t.Helper()
	cat := loadWSCatalogue(t)
	out := make(map[string]bool, len(cat.OptionalProviders.Commands))
	for _, name := range cat.OptionalProviders.Commands {
		out[name] = true
	}
	if len(out) == 0 {
		t.Fatalf("wsapi.json: optional_deployment_providers is empty — the walker's not_implemented acceptance would be untethered")
	}
	return out
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
