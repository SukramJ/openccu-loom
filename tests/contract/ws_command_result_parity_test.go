// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// wsCommandResultExemptions names commands whose declared/published
// mismatch is a decision, with the reason. Empty: no command declares a
// result it does not send, and none is deliberately silent about one it
// does.
var wsCommandResultExemptions = map[string]string{}

// wsCommandsAwaitingResultShape is the declared backlog: commands that
// return a payload and have never had its shape written into the
// catalogue.
//
// A second map on purpose, kept apart from the exemptions above for the
// reason CLAUDE.md keeps `wiringSettersWithoutCaller` and
// `wiringSeamsUnderInvestigation` apart: merging them would let "we looked
// and decided the caller does not need it" and "nobody has written it down
// yet" wear the same face, and the second quietly becomes the first.
//
// Entries are expected to disappear, and a new command that returns a
// payload without declaring one fails outright — the backlog can shrink
// but never grow unnoticed. Its size is the finding: `wsapi.json` is the
// only description of this surface a client has, and for these 82 the
// answer is "read the daemon's source".
var wsCommandsAwaitingResultShape = map[string]string{
	"addon_update.check":                  "",
	"addon_update.install":                "",
	"alarm_messages.ack":                  "",
	"alarm_messages.list":                 "",
	"alarm_panel.acknowledge":             "",
	"alarm_panel.disarm":                  "",
	"alarm_panel.journal":                 "",
	"alarm_panel.panels":                  "",
	"alarm_panel.readiness":               "",
	"alarm_panel.silence":                 "",
	"alarm_panel.silence_all":             "",
	"alarm_panel.state":                   "",
	"alarm_panel.walktest_status":         "",
	"backup.status":                       "",
	"backup.trigger":                      "",
	"calc_dp.get":                         "",
	"calc_dp.list":                        "",
	"ccu.get_hub_data":                    "",
	"ccu.get_signal_quality":              "",
	"ccu.reload_channel_config":           "",
	"ccu.reload_device_config":            "",
	"cdp.get":                             "",
	"cdp.invoke":                          "",
	"cdp.list":                            "",
	"config.reload_channel_config":        "",
	"config.reload_device_config":         "",
	"config.session.changes":              "",
	"config.session.discard":              "",
	"config.session.open":                 "",
	"config.session.redo":                 "",
	"config.session.save":                 "",
	"config.session.set":                  "",
	"config.session.undo":                 "",
	"device.install_mode":                 "",
	"devices.export_definition":           "",
	"devices.get":                         "",
	"devices.list":                        "",
	"firmware.info":                       "",
	"firmware.refresh":                    "",
	"firmware.update":                     "",
	"groups.create":                       "",
	"groups.delete":                       "",
	"groups.list":                         "",
	"groups.suitable_members":             "",
	"groups.types":                        "",
	"groups.update":                       "",
	"inbox.accept":                        "",
	"inbox.list":                          "",
	"install_mode.enable":                 "",
	"install_mode.status":                 "",
	"links.activate_paramset":             "",
	"links.add":                           "",
	"links.get_paramset":                  "",
	"links.linkable_channels":             "",
	"links.list":                          "",
	"links.list_all":                      "",
	"links.put_paramset":                  "",
	"links.remove":                        "",
	"links.set_info":                      "",
	"master_profiles.get":                 "",
	"master_profiles.list":                "",
	"master_profiles.match":               "",
	"paramset.description":                "",
	"paramset.get":                        "",
	"programs.list":                       "",
	"schedules.active_profile.set":        "",
	"schedules.climate.copy_profile":      "",
	"schedules.climate.get":               "",
	"schedules.climate.set":               "",
	"schedules.copy":                      "",
	"schedules.device.active_profile.set": "",
	"schedules.device.get":                "",
	"schedules.device.set":                "",
	"schedules.list_devices":              "",
	"service_messages.list":               "",
	"system.commands":                     "",
	"system.health":                       "",
	"system.user_permissions":             "",
	"sysvars.fetch":                       "",
	"sysvars.list":                        "",
	"sysvars.set":                         "",
	"sysvars.usage":                       "",
}

// TestWSCommandResultsMatchTheirHandlers pins the WS command plane to the
// rule the rest of this repository is held to: declared and published
// must be the same set.
//
// `assets/wsapi.json` is the only description of the WS call surface a
// client has. A command that returns a payload without declaring one
// leaves the caller to read the daemon's source; a command that declares
// one its handler never sends is worse, because the caller waits for or
// parses something that will not arrive. Neither fails anywhere today.
//
// "Publishes a value" is decided from the handler's own returns: a
// CommandHandler is `func(context.Context, json.RawMessage) (any, error)`,
// so a `return <non-nil>, nil` anywhere in its body is a payload. That is
// read from the AST rather than by matching text — a first attempt used a
// regular expression and reported sixteen commands as ack-only that
// return a composite literal spanning several lines.
func TestWSCommandResultsMatchTheirHandlers(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	handlers, registered := wsCommandHandlers(t, filepath.Join(root, "internal", "north", "rest", "ws"))
	if len(registered) == 0 {
		t.Fatal("found no router.Register call sites — the guard is measuring nothing")
	}

	declared := wsDeclaredResults(t, filepath.Join(root, "assets", "wsapi.json"))
	if len(declared) == 0 {
		t.Fatal("parsed no commands from wsapi.json — the guard is measuring nothing")
	}

	var undeclared, overdeclared, unresolved []string
	for name, fn := range registered {
		hasResult, known := declared[name]
		if !known {
			// The catalogue is guarded for completeness elsewhere; this
			// guard is only about the result half.
			continue
		}
		if _, exempt := wsCommandResultExemptions[name]; exempt {
			continue
		}
		_, backlogged := wsCommandsAwaitingResultShape[name]
		publishes, ok := handlers[fn]
		if !ok {
			unresolved = append(unresolved, name+" (handler "+fn+" not found)")
			continue
		}
		switch {
		case publishes && !hasResult:
			if !backlogged {
				undeclared = append(undeclared, name+" -> "+fn)
			}
		case !publishes && hasResult:
			overdeclared = append(overdeclared, name+" -> "+fn)
		}
	}

	sort.Strings(undeclared)
	sort.Strings(overdeclared)
	sort.Strings(unresolved)

	if len(undeclared) > 0 {
		t.Errorf("%d command(s) return a payload and declare no `result` in wsapi.json:\n  %s\n\n"+
			"Declare the shape, or add the command to wsCommandResultExemptions with the reason a "+
			"caller does not need it. The catalogue is the only description of this surface a "+
			"client has.",
			len(undeclared), strings.Join(undeclared, "\n  "))
	}
	if len(overdeclared) > 0 {
		t.Errorf("%d command(s) declare a `result` their handler never returns:\n  %s\n\n"+
			"This is the worse direction: the caller waits for or parses a payload that will not "+
			"arrive. Remove the declaration, or make the handler send it.",
			len(overdeclared), strings.Join(overdeclared, "\n  "))
	}
	if len(unresolved) > 0 {
		t.Errorf("%d registered command(s) whose handler this guard could not locate:\n  %s\n\n"+
			"An unlocatable handler is not a pass — it is a command this guard silently stopped "+
			"checking.",
			len(unresolved), strings.Join(unresolved, "\n  "))
	}
}

// wsCommandHandlers returns, per handler-constructor name, whether its
// returned CommandHandler ever yields a non-nil payload, plus the
// command -> constructor mapping taken from the Register call sites.
func wsCommandHandlers(t *testing.T, dir string) (publishes map[string]bool, registered map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	publishes = map[string]bool{}
	registered = map[string]string{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", e.Name(), perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Register" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			inner, ok := call.Args[1].(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := inner.Fun.(*ast.Ident); ok {
				registered[name] = id.Name
			}
			return true
		})
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Body == nil || fn.Recv != nil {
				continue
			}
			publishes[fn.Name.Name] = returnsAPayload(fn.Body)
		}
	}
	return publishes, registered
}

// returnsAPayload reports whether any `return X, nil` in the body yields a
// non-nil first result.
func returnsAPayload(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 2 {
			return true
		}
		if id, isIdent := ret.Results[1].(*ast.Ident); !isIdent || id.Name != "nil" {
			return true
		}
		if id, isIdent := ret.Results[0].(*ast.Ident); isIdent && id.Name == "nil" {
			return true
		}
		found = true
		return false
	})
	return found
}

// wsDeclaredResults maps command name -> whether the catalogue declares a
// result shape for it.
func wsDeclaredResults(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Commands []struct {
			Name    string          `json:"name"`
			Command string          `json:"command"`
			Result  json.RawMessage `json:"result"`
		} `json:"commands"`
	}
	if uerr := json.Unmarshal(raw, &doc); uerr != nil {
		t.Fatalf("parse %s: %v", path, uerr)
	}
	out := map[string]bool{}
	for _, c := range doc.Commands {
		name := c.Name
		if name == "" {
			name = c.Command
		}
		out[name] = len(c.Result) > 0 && string(c.Result) != "null"
	}
	return out
}
