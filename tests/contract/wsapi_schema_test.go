// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// wsapi_schema_test.go — WS-API schema-drift contract test
//
// Pins the set of registered WebSocket commands so that any future
// addition, removal, or rename shows up as a test failure rather than
// silently breaking clients.
//
// Strategy: extract every literal string passed to `router.Register(…)`
// from commands_default.go and commands_extended.go via go/ast, then
// compare the resulting set against the hand-curated catalogue in
// assets/wsapi.json. We prefer AST walking over importing the ws
// package directly because the latter would require constructing all
// dependency interfaces (DeviceQuery, HubQuery, …) just to call
// RegisterDefaultCommands — heavy scaffolding for a structural test.
// AST walking runs in milliseconds and catches regressions at the
// source level before any integration test would see them.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// wsSchemaCommand is one entry in assets/wsapi.json.
type wsSchemaCommand struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	// Kind distinguishes client-callable commands from server-pushed
	// broadcast events. Empty == "command" (the default). Setting
	// "broadcast" exempts the entry from the registered-command match
	// in TestWSCommandsMatchPinnedSchema (broadcasts don't go through
	// router.Register; they are emitted via hub.Publish from the
	// daemon).
	Kind string `json:"kind,omitempty"`
	// Payload names the broadcast's push-payload schema (a key under
	// `components.schemas` in assets/openapi.yaml). Set only on
	// `kind: "broadcast"` entries. Generated client type packages
	// resolve it from the OpenAPI components, so it must have a matching
	// schema — see TestWSBroadcastPayloadsHaveOpenAPISchema.
	Payload string `json:"payload,omitempty"`
	// Args is the optional argument schema documented in
	// assets/wsapi.json under the `command_schemas` header. Keys are
	// argument names; values are typed strings like `"string"`,
	// `"integer?"`, `"object"`, or `"any"`. Absent means the command
	// takes no arguments or its shape lives only in the Go handler.
	Args map[string]any `json:"args,omitempty"`
	// Result is the optional return-value schema. Same conventions as
	// Args; may also be the literal string `"array"` for list-style
	// returns.
	Result any `json:"result,omitempty"`
}

// wsTypeRE accepts the leaf-type tokens used in command_schemas.
// Anything outside this set is rejected so a typo (e.g. `"strng"`)
// fails the contract test rather than silently shipping.
var wsTypeTokens = map[string]bool{
	"string":  true,
	"integer": true,
	"number":  true,
	"boolean": true,
	"object":  true,
	"array":   true,
	"any":     true,
}

// wsSchema is the top-level shape of assets/wsapi.json.
type wsSchema struct {
	Version  string            `json:"version"`
	Commands []wsSchemaCommand `json:"commands"`
}

// knownWSCategories is the exhaustive set of categories that may appear
// in assets/wsapi.json. Add a new entry here when a new category is
// introduced — the test will then enforce that every command uses only
// known categories.
var knownWSCategories = map[string]bool{
	"alarms":         true,
	"alarm_panel":    true,
	"backup":         true,
	"calc_dp":        true,
	"ccu":            true,
	"cdp":            true,
	"central":        true,
	"change_history": true,
	"config":         true,
	"devices":        true,
	"firmware":       true,
	"groups":         true,
	"inbox":          true,
	"incidents":      true,
	"install_mode":   true,
	"links":          true,
	"matter":         true,
	"paramsets":      true,
	"programs":       true,
	"recording":      true,
	"schedules":      true,
	"system":         true,
	"sysvars":        true,
	// Broadcast-only categories — push events that surface a
	// dedicated namespace (added by the external-client wire
	// contract — ADR 0020).
	"custom_data_point": true,
	"datapoint":         true,
	"hub":               true,
	// The Security & Safety domain is its own namespace, separate from
	// alarm_panel: it aggregates hazards and faults with or without an
	// alarm engine, so its broadcasts must not read as panel events.
	"security": true,
}

// loadWSSchema reads and decodes assets/wsapi.json relative to the
// repository root.
func loadWSSchema(t *testing.T) wsSchema {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "assets", "wsapi.json"))
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}
	var s wsSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parse wsapi.json: %v", err)
	}
	return s
}

// extractRegisteredWSCommands walks commands_default.go and
// commands_extended.go with go/ast and collects every literal string
// argument to router.Register(…) calls.
//
// Pattern: an *ast.CallExpr whose function is a *ast.SelectorExpr with
// selector "Register", where the first argument is a *ast.BasicLit of
// kind token.STRING. This matches all call sites in both files.
func extractRegisteredWSCommands(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	wsDir := filepath.Join(root, "internal", "north", "rest", "ws")

	sourceFiles := []string{
		filepath.Join(wsDir, "commands_default.go"),
		filepath.Join(wsDir, "commands_extended.go"),
		filepath.Join(wsDir, "commands_missing.go"),
		filepath.Join(wsDir, "custom_data_points.go"),
		filepath.Join(wsDir, "alarm_panel.go"),
	}

	seen := map[string]bool{}
	fset := token.NewFileSet()

	for _, path := range sourceFiles {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Base(path), err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			if len(call.Args) < 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind.String() != "STRING" {
				return true
			}
			// Unquote: "foo.bar" → foo.bar
			name := strings.Trim(lit.Value, `"`)
			seen[name] = true
			return true
		})
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ── Test 1: pinned schema vs. registered commands ────────────────────

// TestWSCommandsMatchPinnedSchema asserts that the set of command names
// extracted from the source files matches the set pinned in
// assets/wsapi.json exactly. Any drift in either direction is a
// breaking change for WS clients and must be documented by updating
// the schema file.
func TestWSCommandsMatchPinnedSchema(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	registered := extractRegisteredWSCommands(t)

	if len(registered) == 0 {
		t.Fatal("no registered WS commands found — source paths may have moved; update extractRegisteredWSCommands")
	}

	// Build sets.
	registeredSet := make(map[string]bool, len(registered))
	for _, name := range registered {
		registeredSet[name] = true
	}
	schemaSet := make(map[string]bool, len(schema.Commands))
	// broadcastSet collects schema entries marked `kind: "broadcast"` —
	// server-pushed events documented in the schema for client-side
	// type generation; they don't go through router.Register and must
	// be exempted from the registered-vs-schema match.
	broadcastSet := make(map[string]bool)
	for _, cmd := range schema.Commands {
		schemaSet[cmd.Name] = true
		if cmd.Kind == "broadcast" {
			broadcastSet[cmd.Name] = true
		}
	}

	// Commands in source but missing from schema (need schema update).
	var notInSchema []string
	for name := range registeredSet {
		if !schemaSet[name] {
			notInSchema = append(notInSchema, name)
		}
	}
	// Commands in schema but no longer registered (need schema cleanup or restore).
	var notRegistered []string
	for name := range schemaSet {
		if registeredSet[name] || broadcastSet[name] {
			continue
		}
		notRegistered = append(notRegistered, name)
	}

	sort.Strings(notInSchema)
	sort.Strings(notRegistered)

	if len(notInSchema) > 0 {
		t.Errorf("WS COMMANDS REGISTERED BUT NOT IN assets/wsapi.json — "+
			"add them to the schema file to document the contract:\n  %s",
			strings.Join(notInSchema, "\n  "))
	}
	if len(notRegistered) > 0 {
		t.Errorf("WS COMMANDS IN assets/wsapi.json BUT NOT REGISTERED — "+
			"remove them from the schema or restore their handler:\n  %s",
			strings.Join(notRegistered, "\n  "))
	}

	t.Logf("TestWSCommandsMatchPinnedSchema: %d registered, %d in schema — sets match",
		len(registered), len(schema.Commands))
}

// ── Test 2: JSON validity ────────────────────────────────────────────

// TestWSSchemaIsValidJSON asserts that assets/wsapi.json is
// well-formed JSON and can be decoded into the expected shape.
func TestWSSchemaIsValidJSON(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "assets", "wsapi.json"))
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}

	var s wsSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("wsapi.json is not valid JSON: %v", err)
	}
	if s.Version == "" {
		t.Error("wsapi.json: missing or empty 'version' field")
	}
	if len(s.Commands) == 0 {
		t.Error("wsapi.json: 'commands' array is empty")
	}

	t.Logf("TestWSSchemaIsValidJSON: version=%s commands=%d", s.Version, len(s.Commands))
}

// ── Test 3: no duplicate names in schema ─────────────────────────────

// TestWSSchemaHasNoDuplicateNames asserts that every command name
// in assets/wsapi.json is unique. Duplicates would cause undefined
// dispatch behaviour and confuse API consumers.
func TestWSSchemaHasNoDuplicateNames(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)

	seen := make(map[string]int, len(schema.Commands))
	for _, cmd := range schema.Commands {
		seen[cmd.Name]++
	}

	var dups []string
	for name, count := range seen {
		if count > 1 {
			dups = append(dups, name)
		}
	}
	sort.Strings(dups)

	if len(dups) > 0 {
		t.Errorf("wsapi.json contains duplicate command names: %v", dups)
	}

	t.Logf("TestWSSchemaHasNoDuplicateNames: %d unique command names", len(seen))
}

// ── Test 4: categories are from the known set ────────────────────────

// TestWSSchemaCategoriesAreFromKnownSet asserts that every category
// value in assets/wsapi.json is one of the categories declared in
// knownWSCategories. Unknown categories indicate a typo or a new
// category that must be explicitly acknowledged here.
func TestWSSchemaCategoriesAreFromKnownSet(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)

	var unknown []string
	for _, cmd := range schema.Commands {
		if cmd.Category == "" {
			unknown = append(unknown, cmd.Name+": (empty category)")
			continue
		}
		if !knownWSCategories[cmd.Category] {
			unknown = append(unknown, cmd.Name+": "+cmd.Category)
		}
	}
	sort.Strings(unknown)

	if len(unknown) > 0 {
		t.Errorf("wsapi.json commands use unknown categories — add to knownWSCategories "+
			"if intentional:\n  %s", strings.Join(unknown, "\n  "))
	}

	t.Logf("TestWSSchemaCategoriesAreFromKnownSet: all %d commands use known categories", len(schema.Commands))
}

// ── Test: WS command catalogue vs. router registration parity ───────

// TestWSCommandCatalogParity verifies that every non-broadcast entry
// in assets/wsapi.json (the client-callable commands) has a matching
// handler registered in the WS router source files. Broadcast events
// (kind == "broadcast") are server-push channels, not commands — they
// are emitted via hub.Publish rather than router.Register, so they
// are correctly excluded from this check.
//
// The discriminator is the "kind" field: absent or empty means
// "command"; "broadcast" marks a server-push event. The command set
// is compared against the set of names extracted from router.Register
// calls via AST walking of the ws package source files.
func TestWSCommandCatalogParity(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	registered := extractRegisteredWSCommands(t)

	registeredSet := make(map[string]bool, len(registered))
	for _, name := range registered {
		registeredSet[name] = true
	}

	var missing []string
	for _, cmd := range schema.Commands {
		if cmd.Kind == "broadcast" {
			// Server-push event — not dispatched through the command router.
			continue
		}
		if !registeredSet[cmd.Name] {
			missing = append(missing, cmd.Name)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("WS catalogue commands with no matching router.Register call "+
			"(add a handler or mark kind:broadcast if server-push):\n  %s",
			strings.Join(missing, "\n  "))
	}

	commandCount := 0
	for _, cmd := range schema.Commands {
		if cmd.Kind != "broadcast" {
			commandCount++
		}
	}
	t.Logf("TestWSCommandCatalogParity: %d catalogue commands, %d registered — all covered",
		commandCount, len(registered))
}

// ── Test 5: args/result use the documented type vocabulary ──────────

// TestWSSchemaArgsResultUseTypedVocabulary asserts that any `args` or
// `result` block in assets/wsapi.json uses leaf types from the
// documented vocabulary (`string`, `integer`, `number`, `boolean`,
// `object`, `array`, `any`), optionally suffixed with `?` to mark the
// field as optional. Nested objects are allowed (recursive map). This
// blocks typos like `"strng"`.
//
// What it does not do is keep the file consumable, and the doc comment
// used to claim otherwise by naming "clients that want to generate
// type-safe wrappers". No such client exists: scripts/gen_ws.py in
// openccu-loom-types reads the broadcast half of this document and
// nothing else, so the command vocabulary is checked for spelling and
// consumed by no generator. Two different numbers describe that gap and
// they are easy to conflate: 104 of the 136 commands declare no `result`
// at all, and 82 of those 104 have a handler that returns a payload —
// only the 82 are a mismatch, the other 22 are ack-only commands whose
// silence is correct. TestWSCommandResultsMatchTheirHandlers holds the 82
// as a declared backlog; the reasoning is in
// notes/plans/round-6-audit-strategy.md rather than closed here.
func TestWSSchemaArgsResultUseTypedVocabulary(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	var problems []string

	var check func(cmd, field, key string, val any)
	check = func(cmd, field, key string, val any) {
		switch v := val.(type) {
		case string:
			token := strings.TrimSuffix(v, "?")
			if !wsTypeTokens[token] {
				problems = append(problems, cmd+"."+field+"."+key+": unknown leaf type "+v)
			}
		case map[string]any:
			for sub, subVal := range v {
				check(cmd, field+"."+key, sub, subVal)
			}
		default:
			problems = append(problems, cmd+"."+field+"."+key+": value must be a typed string or nested object")
		}
	}

	for _, cmd := range schema.Commands {
		for k, v := range cmd.Args {
			check(cmd.Name, "args", k, v)
		}
		switch r := cmd.Result.(type) {
		case nil:
			// no result schema
		case string:
			tok := strings.TrimSuffix(r, "?")
			if !wsTypeTokens[tok] {
				problems = append(problems, cmd.Name+".result: unknown leaf type "+r)
			}
		case map[string]any:
			for k, v := range r {
				check(cmd.Name, "result", k, v)
			}
		default:
			problems = append(problems, cmd.Name+".result: must be a typed string or nested object")
		}
	}

	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("wsapi.json args/result blocks contain unknown types:\n  %s",
			strings.Join(problems, "\n  "))
	}
}
