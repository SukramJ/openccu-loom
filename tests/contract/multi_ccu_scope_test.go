// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// multi_ccu_scope_test.go — ADR 0002 tripwire
//
// Enforces the Multi-CCU invariant from ADR 0002 and audit §11/11:
// every coordinator / store method must carry centralName plumbing;
// no static singletons may hold a single-CCU assumption.
//
// The tests use go/parser + go/ast to walk production source files at
// static-analysis time — they run in milliseconds, need no imports
// from the packages under test, and catch regressions at the
// structural level before any integration test would see them.
//
// Pattern mirrors coordinator_size_test.go in this package.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// ── helpers ─────────────────────────────────────────────────────────

// repoRoot resolves the repository root relative to tests/contract/.
func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..")
}

// parseDir parses all non-test Go files in dir. Returns a fset + map
// of filename → *ast.File in sorted order.
func parseDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool { //nolint:staticcheck // SA1019 — go/packages migration deferred
		// skip test files
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parseDir %s: %v", dir, err)
	}
	names := make([]string, 0)
	files := make([]*ast.File, 0, len(names))
	allFiles := map[string]*ast.File{}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			names = append(names, name)
			allFiles[name] = f
		}
	}
	sort.Strings(names) // stable iteration order
	for _, n := range names {
		files = append(files, allFiles[n])
	}
	return fset, files
}

// receiverName returns the receiver type name (without pointer) for a
// FuncDecl, or "" for top-level functions.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// paramNames returns all parameter names for a function.
func paramNames(fn *ast.FuncDecl) []string {
	var names []string
	if fn.Type == nil || fn.Type.Params == nil {
		return names
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// paramTypes returns all parameter type name-strings (best-effort,
// for simple ident / selector types only).
func paramTypes(fn *ast.FuncDecl) []string {
	var types []string
	if fn.Type == nil || fn.Type.Params == nil {
		return types
	}
	for _, field := range fn.Type.Params.List {
		types = append(types, exprTypeName(field.Type))
	}
	return types
}

func exprTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprTypeName(t.X)
	case *ast.SelectorExpr:
		return exprTypeName(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprTypeName(t.Elt)
	}
	return "?"
}

// containsCentralParam returns true when any parameter name (case-
// insensitive) contains "central".
func containsCentralParam(fn *ast.FuncDecl) bool {
	for _, name := range paramNames(fn) {
		if strings.Contains(strings.ToLower(name), "central") {
			return true
		}
	}
	return false
}

// structHasCentralField returns true when the struct type declaration
// named typeName inside files has a field whose name contains
// "central" (case-insensitive). Used to check whether the receiver
// itself carries centralName.
func structHasCentralField(typeName string, files []*ast.File) bool {
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, name := range field.Names {
						if strings.Contains(strings.ToLower(name.Name), "central") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// ── Test 1: coordinator exported I/O methods carry centralName ───────

// ioMethodPrefixes lists method name prefixes whose implementations
// are expected to be aware of the multi-CCU scope. Pure helper or
// accessor verbs are NOT in this list.
var ioMethodPrefixes = []string{
	"Get", "List", "Refresh", "Pull", "Run", "Handle", "Update",
	"Set", "Delete", "Create", "Upsert", "Insert", "Record",
	"ReconnectInterface", "AddLink", "RemoveLink", "SetLink", "GetLink",
}

func looksLikeIOMethod(name string) bool {
	for _, prefix := range ioMethodPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// TestCoordinatorMethodsCarryCentralName walks every exported method
// on coordinator structs and asserts that I/O-flavoured methods either
// (a) have a parameter whose name contains "central", OR
// (b) the receiver struct itself has a "centralName" / "central" field
// (meaning the whole coordinator instance is already scoped).
//
// Allow-listed entries document pre-existing patterns that are
// correct-by-design and do not constitute Multi-CCU violations.
func TestCoordinatorMethodsCarryCentralName(t *testing.T) {
	t.Parallel()

	// reason: these methods are correct-by-design — their receiver
	// struct carries centralName injected at construction time, so the
	// method doesn't need a separate central param. OR the coordinator
	// is a per-Unit instance already (only one central reaches
	// it), making an explicit central param redundant.
	//
	// Key: "ReceiverType.MethodName"
	allowList := map[string]string{
		// HubCoordinator stores centralName as a field set at New(…);
		// events published by this coordinator are tagged with that field.
		"HubCoordinator.UpdateSysvar":           "reason: HubCoordinator.centralName carries scope; no per-call central param needed",
		"HubCoordinator.RefreshPrograms":        "reason: HubCoordinator.centralName carries scope",
		"HubCoordinator.RefreshSysvars":         "reason: HubCoordinator.centralName carries scope",
		"HubCoordinator.RefreshInbox":           "reason: HubCoordinator.centralName carries scope",
		"HubCoordinator.RefreshServiceMessages": "reason: HubCoordinator.centralName carries scope",
		"HubCoordinator.RefreshAlarmMessages":   "reason: HubCoordinator.centralName carries scope",
		"HubCoordinator.RefreshSystemUpdate":    "reason: HubCoordinator.centralName carries scope",
		// DeviceCoordinator stores centralName as a field set at New(…).
		"DeviceCoordinator.HandleNewDevices":    "reason: DeviceCoordinator.centralName carries scope",
		"DeviceCoordinator.HandleDeleteDevices": "reason: DeviceCoordinator.centralName carries scope",
		"DeviceCoordinator.RefreshAfterPair":    "reason: DeviceCoordinator.centralName carries scope",
		"DeviceCoordinator.RefreshAfterUnpair":  "reason: DeviceCoordinator.centralName carries scope",
		// ConnectionRecoveryCoordinator stores centralName.
		"ConnectionRecoveryCoordinator.Run":           "reason: ConnectionRecoveryCoordinator.centralName carries scope",
		"ConnectionRecoveryCoordinator.ResetAttempts": "reason: ConnectionRecoveryCoordinator.centralName carries scope",
		// CacheCoordinator is per-Unit (one instance per central
		// owned by Unit). DataPointKey carries InterfaceID which
		// is already unique per central — no separate central key needed.
		// All cache callers hold an already-scoped coordinator reference.
		"CacheCoordinator.Set":                    "reason: CacheCoordinator is per-Unit; DataPointKey is the discriminator — no additional central param needed",
		"CacheCoordinator.Get":                    "reason: CacheCoordinator is per-Unit; DataPointKey is the discriminator",
		"CacheCoordinator.Delete":                 "reason: CacheCoordinator is per-Unit; DataPointKey is the discriminator",
		"CacheCoordinator.SetSizeProviders":       "reason: wiring method only — installs size providers at central-construction time, not a data-path I/O operation",
		"CacheCoordinator.SetParamsetInvalidator": "reason: wiring method only — installs a paramset-invalidator hook at construction time, not a data-path I/O operation",
		// ClientCoordinator is per-Unit — one instance per central.
		// The map is keyed by interface_id which is unique within a central.
		"ClientCoordinator.Get":               "reason: ClientCoordinator is per-Unit; interface_id is the discriminator within one central",
		"ClientCoordinator.List":              "reason: ClientCoordinator is per-Unit; returns only that central's clients",
		"ClientCoordinator.CreateClient":      "reason: ClientCoordinator is per-Unit; CreateClientConfig.InterfaceID is the discriminator within one central",
		"ClientCoordinator.RecordLastFailure": "reason: ClientCoordinator is per-Unit; interface_id passed in already identifies the failing wire",
		// EventCoordinator is per-Unit. HandleRawEvent is invoked
		// by the callback server which has already routed to the correct
		// central's coordinator via the URL path / envelope interface_id.
		// SetOnConfigSettled is a wiring method, not a data-path method.
		"EventCoordinator.HandleRawEvent":     "reason: EventCoordinator is per-Unit; callback server pre-routes to the correct instance",
		"EventCoordinator.SetOnConfigSettled": "reason: wiring/hook method, not a data-path I/O operation; SetOnConfigSettled configures behaviour at startup",
		// LinkCoordinator has no centralName field; it delegates through
		// ClientResolver which is already scoped to one central's clients.
		"LinkCoordinator.SetRecorder":                  "reason: wiring method only — installs an observability recorder, not a data operation",
		"LinkCoordinator.SetResolver":                  "reason: wiring method only — installs the client resolver, not a data operation",
		"LinkCoordinator.AddLink":                      "reason: LinkCoordinator is per-Unit; resolver is pre-scoped to one central's clients",
		"LinkCoordinator.RemoveLink":                   "reason: LinkCoordinator is per-Unit; resolver is pre-scoped",
		"LinkCoordinator.GetLinks":                     "reason: LinkCoordinator is per-Unit; resolver is pre-scoped",
		"LinkCoordinator.GetLinkableChannels":          "reason: LinkCoordinator is per-Unit; resolver is pre-scoped",
		"LinkCoordinator.GetLinksForLocale":            "reason: LinkCoordinator is per-Unit; resolver is pre-scoped; locale/role-filtered variant",
		"LinkCoordinator.GetLinkableChannelsForLocale": "reason: LinkCoordinator is per-Unit; resolver is pre-scoped; locale/role-filtered variant",
		"LinkCoordinator.SetLinkInfo":                  "reason: LinkCoordinator is per-Unit; resolver is pre-scoped",
		"LinkCoordinator.GetLinkInfo":                  "reason: LinkCoordinator is per-Unit; resolver is pre-scoped",
		// ConfigurationCoordinator reads registries scoped to one central
		// — no runtime central param needed.
		"ConfigurationCoordinator.GetParameterData":           "reason: ConfigurationCoordinator wraps per-central registries; scope is established at construction",
		"ConfigurationCoordinator.GetChannelParamset":         "reason: per-central registries",
		"ConfigurationCoordinator.HasParameter":               "reason: per-central registries",
		"ConfigurationCoordinator.SetParameter":               "reason: per-central registries",
		"ConfigurationCoordinator.SetLinkParameter":           "reason: per-central registries",
		"ConfigurationCoordinator.GetLinkParameter":           "reason: per-central registries",
		"ConfigurationCoordinator.GetLinkParamset":            "reason: per-central registries",
		"ConfigurationCoordinator.PatchParameter":             "reason: per-central registries",
		"ConfigurationCoordinator.GetLinkParamsets":           "reason: per-central registries",
		"ConfigurationCoordinator.GetParamset":                "reason: per-central registries; takes injected LiveParamsetReader scoped at wire-up",
		"ConfigurationCoordinator.GetLinkParamsetDescription": "reason: per-central registries; takes injected LinkParamsetDescriptionFetcher scoped at wire-up",
		"ConfigurationCoordinator.GetLinkParameterData":       "reason: per-central registries",
		"ConfigurationCoordinator.ConfigurableChannels":       "reason: per-central registries",
		"ConfigurationCoordinator.GetAllParamsetDescriptions": "reason: per-central registries — paramset registry is pre-scoped to one central at construction",
		"ConfigurationCoordinator.GetConfigurableDevices":     "reason: per-central registries — description + paramset registries are pre-scoped at construction",
		// CacheCoordinator is per-Unit; SetPersister wires a storage
		// back-end at construction time — no runtime central param needed.
		"CacheCoordinator.SetPersister": "reason: CacheCoordinator is per-Unit; persister is pre-scoped to the central",
		// SetDataCacheInitializationComplete and IsDataCacheInitializationComplete
		// are per-Unit lifecycle markers — no runtime central param needed.
		"CacheCoordinator.SetDataCacheInitializationComplete": "reason: CacheCoordinator is per-Unit; lifecycle state needs no runtime central scoping",
		"CacheCoordinator.IsDataCacheInitializationComplete":  "reason: CacheCoordinator is per-Unit; query on per-central lifecycle state",
		// SessionRecorder + IncidentRecorder slots — wired at construction time
		// ( ), per-central by design. The slots themselves carry no
		// scope; they are looked up from the already-scoped CacheCoordinator.
		"CacheCoordinator.SetSessionRecorder":  "reason: wiring method only — installs the per-central session recorder at construction time",
		"CacheCoordinator.RecordSession":       "reason: per-central session recorder; the in-memory recorder is itself per-central",
		"CacheCoordinator.SetIncidentRecorder": "reason: wiring method only — installs the per-central incident recorder at construction time",
		"CacheCoordinator.GetIncidentRecorder": "reason: per-central incident recorder accessor; pre-scoped at construction",
	}

	dir := filepath.Join(repoRoot(t), "internal", "central", "coordinators")
	fset, files := parseDir(t, dir)

	checked := 0
	flagged := 0

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Only exported methods (has receiver, starts with uppercase).
			recv := receiverName(fn)
			if recv == "" || !fn.Name.IsExported() {
				continue
			}
			if !looksLikeIOMethod(fn.Name.Name) {
				continue
			}
			checked++
			key := recv + "." + fn.Name.Name
			if _, allowed := allowList[key]; allowed {
				continue
			}
			// Pass if: a param contains "central" OR receiver struct has
			// a "central*" field.
			if containsCentralParam(fn) || structHasCentralField(recv, files) {
				continue
			}
			pos := fset.Position(fn.Pos())
			t.Errorf("MULTI-CCU VIOLATION: %s (%s:%d) — "+
				"I/O method has no centralName param and receiver struct has no centralName field. "+
				"Add a `central string` param or move scope into the constructor. "+
				"If this is intentional, add to the allow-list with a documented reason.",
				key, filepath.Base(pos.Filename), pos.Line)
			flagged++
		}
	}

	if checked == 0 {
		t.Fatal("no coordinator methods checked — directory may have moved; update test path")
	}
	t.Logf("TestCoordinatorMethodsCarryCentralName: checked %d I/O methods, flagged %d, allow-listed %d",
		checked, flagged, len(allowList))
}

// ── Test 2: no package-level mutable singleton in central / store ───

// TestNoPackageLevelMutableSingletons scans top-level var declarations
// in internal/central/ and internal/store/sqlite/ for anything that
// looks like a struct or map holding CCU-specific state without being
// keyed by centralName.
//
// Allowed: sentinel errors, embedded FS, immutable string/int vars.
func TestNoPackageLevelMutableSingletons(t *testing.T) {
	t.Parallel()

	// reason-tagged allow-list for pre-existing package-level vars.
	allowList := map[string]string{
		// Sentinel errors are immutable value types — no multi-CCU risk.
		"central:ErrAlreadyRegistered": "reason: immutable sentinel error",
		"sqlite:ErrParamsetNotFound":   "reason: immutable sentinel error",
		"sqlite:ErrDeviceNotFound":     "reason: immutable sentinel error",
		"sqlite:ErrMigrationFailed":    "reason: immutable sentinel error",
		"sqlite:migrationsFS":          "reason: go:embed FS is read-only at runtime",
		"sqlite:energyParameters":      "reason: immutable lookup table of energy parameter names (POWER/ENERGY_COUNTER/…), no CCU-scoped state",
	}

	dirs := []struct {
		label string
		path  string
	}{
		{"central", filepath.Join(repoRoot(t), "internal", "central")},
		{"sqlite", filepath.Join(repoRoot(t), "internal", "store", "sqlite")},
	}

	checked := 0
	flagged := 0

	for _, d := range dirs {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			t.Fatalf("readdir %s: %v", d.path, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(d.path, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range vs.Names {
						checked++
						key := d.label + ":" + name.Name
						if _, allowed := allowList[key]; allowed {
							continue
						}
						// Determine whether this var looks dangerous:
						// struct literals, map literals, or pointer-to-struct
						// without "Err" prefix are suspicious.
						typStr := ""
						if vs.Type != nil {
							typStr = exprTypeName(vs.Type)
						}
						// Allow error-named vars regardless of type.
						if strings.HasPrefix(name.Name, "Err") || strings.HasSuffix(name.Name, "Error") {
							continue
						}
						// Allow vars whose inferred type is a simple string / int / bool.
						if typStr == "string" || typStr == "int" || typStr == "bool" || typStr == "uint" {
							continue
						}
						// Check inferred type from value expression.
						isStructOrMap := false
						if len(vs.Values) > 0 {
							switch vs.Values[0].(type) {
							case *ast.CompositeLit:
								isStructOrMap = true
							case *ast.UnaryExpr: // &T{…}
								isStructOrMap = true
							}
						}
						if isStructOrMap {
							pos := fset.Position(vs.Pos())
							t.Errorf("MULTI-CCU SINGLETON RISK: %s/%s (%s:%d) — "+
								"package-level composite var may hold CCU-scoped state. "+
								"Use a per-Unit field or per-call parameter instead. "+
								"If benign (e.g. an immutable lookup table), add to the allow-list "+
								"with a documented reason.",
								d.label, name.Name, filepath.Base(pos.Filename), pos.Line)
							flagged++
						}
					}
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no package-level vars checked — directory layout may have changed")
	}
	t.Logf("TestNoPackageLevelMutableSingletons: checked %d vars, flagged %d, allow-listed %d",
		checked, flagged, len(allowList))
}

// ── Test 3: store methods have centralName as first non-ctx param ────

// storeIOVerbs are the prefixes / exact names that imply a DB round-
// trip in a store method.
var storeIOVerbs = []string{
	"Upsert", "Get", "List", "Delete", "Update", "Insert",
	"Record", "Recent", "Append", "Bump", "BumpIfRecent",
}

func looksLikeStoreIOMethod(name string) bool {
	for _, v := range storeIOVerbs {
		if name == v || strings.HasPrefix(name, v) {
			return true
		}
	}
	return false
}

// firstNonCtxParamName returns the name of the first parameter that
// is not "ctx" (i.e. context.Context). Returns ("", false) when every
// param is ctx or the list is empty.
func firstNonCtxParamName(fn *ast.FuncDecl) (string, bool) {
	if fn.Type == nil || fn.Type.Params == nil {
		return "", false
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "ctx" || name.Name == "_" {
				continue
			}
			return name.Name, true
		}
	}
	return "", false
}

// TestStoreMethodsHaveCentralNameAsFirstNonCtxParam asserts that
// every store I/O method's first non-ctx parameter name contains
// "central" (case-insensitive) OR the method accepts a *Record struct
// that itself carries CentralName (e.g. Upsert(ctx, rec T)).
func TestStoreMethodsHaveCentralNameAsFirstNonCtxParam(t *testing.T) {
	t.Parallel()

	// reason-tagged allow-list.
	allowList := map[string]string{
		// Open / Migrate / Pragma helpers are schema-level, not CCU-scoped.
		"*:Open":         "reason: global DB open — no central scope",
		"*:Migrate":      "reason: schema migration — global operation",
		"*:applyPragmas": "reason: PRAGMA tuning — global DB operation",
		"*:isMemoryDSN":  "reason: utility predicate — no CCU state",
		// AuditStore.Append and List take a per-audit.Entry struct (which
		// carries no CentralName because the audit log is not per-CCU).
		// This is an intentional design choice; adding to allow-list.
		"AuditStore:Append": "reason: audit_log is daemon-global (one auth realm); not per-CCU by design",
		"AuditStore:List":   "reason: audit_log is daemon-global; deviceAddress is the filter dimension",
		// IncidentStore.RecordIncident takes a reliability.IncidentRecord
		// struct that carries CentralName, but the AST scanner cannot
		// resolve the field across packages (record type lives in
		// internal/client/reliability/, scanner only walks
		// internal/store/sqlite/). The struct DOES carry CentralName.
		"IncidentStore:RecordIncident": "reason: record struct carries CentralName; cross-package field check beyond AST scanner",
		// User/token/config_sections tables are daemon-global, not
		// per-CCU. The auth realm and section snapshots are one
		// authority per daemon; centrals reach into the runtime config
		// via [config.Config.Centrals]. See Wave-B SQL migration 017.
		"UserStore:Delete":            "reason: users table is daemon-global; subject is the natural key",
		"TokenStore:Delete":           "reason: tokens table is daemon-global; fingerprint is the natural key",
		"TokenStore:DeleteBySubject":  "reason: tokens table is daemon-global; subject is the natural key, not a CCU",
		"UserPreferencesStore:Get":    "reason: user_preferences is per-user daemon-global UI state; subject+key is the natural key, not a CCU",
		"UserPreferencesStore:Delete": "reason: user_preferences is per-user daemon-global UI state; subject+key is the natural key, not a CCU",
		// diagram_configs is per-user daemon-global metadata; a diagram
		// spans multiple centrals (each series carries its own central), so
		// the owner subject / diagram id is the natural key, not a CCU.
		"DiagramConfigStore:List":   "reason: diagram_configs is per-user daemon-global; owner subject is the key, and a diagram spans centrals",
		"DiagramConfigStore:Get":    "reason: diagram_configs is per-user daemon-global; diagram id is the key, and a diagram spans centrals",
		"DiagramConfigStore:Create": "reason: diagram_configs is per-user daemon-global; owner subject is the key, and a diagram spans centrals",
		"DiagramConfigStore:Update": "reason: diagram_configs is per-user daemon-global; diagram id is the key, and a diagram spans centrals",
		"DiagramConfigStore:Delete": "reason: diagram_configs is per-user daemon-global; diagram id is the key, and a diagram spans centrals",
		"ConfigSectionStore:Get":    "reason: config_sections is daemon-global; section is the natural key",
		"ConfigSectionStore:Delete": "reason: config_sections is daemon-global; section is the natural key",
		// auth_sessions is daemon-global, not per-CCU: a login session
		// belongs to a user in the single auth realm, never to a CCU.
		// DeleteSession's natural key is the session id; the purge sweep
		// is a global time-based delete over the expiry. See ADR 0041.
		"AuthSessionStore:DeleteSession":         "reason: auth_sessions is daemon-global (one auth realm); session id is the natural key",
		"AuthSessionStore:DeleteExpiredSessions": "reason: auth_sessions purge is a global time-based delete across the one auth realm; central scoping would be incorrect",
		// Measurement-history retention is a time-based purge over the one
		// history.db file: it drops every row older than the cutoff
		// regardless of central. Per-central scoping would be wrong — the
		// retention window is global. Per-central deletes use DeleteDevice
		// (which DOES carry central) instead. See ADR 0040.
		"MeasurementStore:DeleteOlderThan": "reason: retention is a global time-based purge across all centrals in history.db; central scoping would be incorrect",
		// The hourly/daily rollup tiers are purged the same way as the raw
		// tier above: a global time-based cutoff over history.db, not a
		// per-central operation. See ADR 0040.
		"MeasurementStore:DeleteHourlyOlderThan": "reason: rollup retention is a global time-based purge across all centrals in history.db; central scoping would be incorrect",
		"MeasurementStore:DeleteDailyOlderThan":  "reason: rollup retention is a global time-based purge across all centrals in history.db; central scoping would be incorrect",
		// Alarm areas are daemon-level partitions that may span
		// multiple centrals (docs/alarm-concept.md §13.1, §14): the
		// area/incident/journal keys are area-scoped by design, and the
		// central reference lives inside each sensor/output row
		// (CentralName column) instead of on the store surface. Scoping
		// these methods by central would be incorrect.
		"AlarmAreaStore:Upsert":            "reason: alarm areas are daemon-level and may span centrals; area id is the natural key",
		"AlarmAreaStore:Get":               "reason: alarm areas are daemon-level and may span centrals; area id is the natural key",
		"AlarmAreaStore:Delete":            "reason: alarm areas are daemon-level and may span centrals; area id is the natural key",
		"AlarmSensorStore:Get":             "reason: alarm sensors are keyed by daemon-level sensor/area ids; the row carries CentralName as data",
		"AlarmSensorStore:ListByArea":      "reason: alarm sensors are keyed by daemon-level sensor/area ids; the row carries CentralName as data",
		"AlarmSensorStore:Delete":          "reason: alarm sensors are keyed by daemon-level sensor/area ids; the row carries CentralName as data",
		"AlarmSensorStore:DeleteByArea":    "reason: alarm sensors are keyed by daemon-level sensor/area ids; the row carries CentralName as data",
		"AlarmOutputStore:Get":             "reason: alarm outputs are keyed by daemon-level output/area ids; the row carries CentralName as data",
		"AlarmOutputStore:ListByArea":      "reason: alarm outputs are keyed by daemon-level output/area ids; the row carries CentralName as data",
		"AlarmOutputStore:Delete":          "reason: alarm outputs are keyed by daemon-level output/area ids; the row carries CentralName as data",
		"AlarmOutputStore:DeleteByArea":    "reason: alarm outputs are keyed by daemon-level output/area ids; the row carries CentralName as data",
		"AlarmStateStore:Upsert":           "reason: alarm state rows are keyed by daemon-level area id; areas may span centrals",
		"AlarmStateStore:Get":              "reason: alarm state rows are keyed by daemon-level area id; areas may span centrals",
		"AlarmStateStore:Delete":           "reason: alarm state rows are keyed by daemon-level area id; areas may span centrals",
		"AlarmIncidentStore:Get":           "reason: alarm incidents belong to daemon-level areas; incident id is the natural key",
		"AlarmIncidentStore:GetOpenByArea": "reason: alarm incidents belong to daemon-level areas; incident id is the natural key",
		"AlarmIncidentStore:ListByArea":    "reason: alarm incidents belong to daemon-level areas; incident id is the natural key",
		"AlarmJournalStore:Append":         "reason: the alarm journal is daemon-global like the audit log; entries reference areas, not centrals",
		"AlarmCodeStore:Upsert":            "reason: alarm codes are daemon-level user/hardware identities keyed by code id; areas may span centrals",
		"AlarmCodeStore:Get":               "reason: alarm codes are daemon-level user/hardware identities keyed by code id; areas may span centrals",
		"AlarmCodeStore:Delete":            "reason: alarm codes are daemon-level user/hardware identities keyed by code id; areas may span centrals",
	}

	dir := filepath.Join(repoRoot(t), "internal", "store", "sqlite")
	fset, files := parseDir(t, dir)

	checked := 0
	flagged := 0

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if !looksLikeStoreIOMethod(fn.Name.Name) {
				continue
			}
			recv := receiverName(fn)
			checked++

			key := recv + ":" + fn.Name.Name
			wildcardKey := "*:" + fn.Name.Name
			if _, allowed := allowList[key]; allowed {
				continue
			}
			if _, allowed := allowList[wildcardKey]; allowed {
				continue
			}

			firstParam, ok := firstNonCtxParamName(fn)
			if !ok {
				// No non-ctx params — likely a no-arg helper; check by type
				// list instead.
				continue
			}

			// Pass if: first non-ctx param name contains "central".
			if strings.Contains(strings.ToLower(firstParam), "central") {
				continue
			}

			// Also pass if the first param is a record struct (name often
			// ends in "rec", "r", "entry", "inc") and the struct type has
			// a CentralName field — check the type name.
			paramTypeList := paramTypes(fn)
			if len(paramTypeList) >= 2 { // ctx + at least one param
				// The second element is the first non-ctx type.
				firstParamType := paramTypeList[1]
				// strip pointer
				firstParamType = strings.TrimPrefix(firstParamType, "*")
				if structHasCentralField(firstParamType, files) {
					continue
				}
			}

			pos := fset.Position(fn.Pos())
			t.Errorf("MULTI-CCU VIOLATION: store method %s.%s (%s:%d) — "+
				"first non-ctx param is %q which does not contain 'central'. "+
				"Add a `central string` param or use a record struct with CentralName. "+
				"If this is intentional, add to the allow-list with a reason.",
				recv, fn.Name.Name, filepath.Base(pos.Filename), pos.Line, firstParam)
			flagged++
		}
	}

	if checked == 0 {
		t.Fatal("no store methods checked — directory may have moved")
	}
	t.Logf("TestStoreMethodsHaveCentralNameAsFirstNonCtxParam: checked %d methods, flagged %d, allow-listed %d",
		checked, flagged, len(allowList))
}

// ── Test 4: CentralRegistry / Registry lookup requires name param ────

// TestCentralRegistryLookupRequiresName asserts that every exported
// lookup / modification method on the central Registry (and the
// registry.CentralRegistry in the sub-package) accepts a string name
// parameter — so callers can never accidentally retrieve "the one
// central" without naming it.
func TestCentralRegistryLookupRequiresName(t *testing.T) {
	t.Parallel()

	// Methods that must carry a name/string param.
	// Note: "Register" is intentionally excluded here — the concrete
	// Registry.Register takes a *Unit (which carries Name()
	// internally) rather than a plain string. Only lookup / removal
	// methods must take an explicit name string.
	mustHaveNameParam := []string{"Get", "Remove"}

	// Methods that are correctly name-free (they return all or nothing).
	allowedWithoutName := map[string]string{
		"Names":    "reason: returns all names — correct to not take a name param",
		"List":     "reason: returns all centrals — aggregation, not lookup",
		"StartAll": "reason: fans out to all centrals",
		"StopAll":  "reason: fans out to all centrals",
	}

	registryDirs := []string{
		filepath.Join(repoRoot(t), "internal", "central"),
		filepath.Join(repoRoot(t), "internal", "central", "registry"),
	}

	registryTypes := map[string]bool{
		"Registry":        true,
		"CentralRegistry": true,
	}

	checked := 0
	violations := 0

	for _, dir := range registryDirs {
		fset, files := parseDir(t, dir)
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				recv := receiverName(fn)
				if !registryTypes[recv] {
					continue
				}
				if !fn.Name.IsExported() {
					continue
				}
				if _, skip := allowedWithoutName[fn.Name.Name]; skip {
					continue
				}
				// Only assert the "must have name" methods.
				mustCheck := slices.Contains(mustHaveNameParam, fn.Name.Name)
				if !mustCheck {
					continue
				}
				checked++
				// Verify at least one string-typed param exists.
				hasStringParam := false
				if fn.Type != nil && fn.Type.Params != nil {
					for _, field := range fn.Type.Params.List {
						if exprTypeName(field.Type) == "string" {
							hasStringParam = true
							break
						}
					}
				}
				if !hasStringParam {
					pos := fset.Position(fn.Pos())
					t.Errorf("MULTI-CCU VIOLATION: %s.%s (%s:%d) — "+
						"registry lookup/modify method has no string name parameter. "+
						"All registry operations must identify the central by name.",
						recv, fn.Name.Name, filepath.Base(pos.Filename), pos.Line)
					violations++
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no registry methods checked — type names may have changed; update registryTypes map")
	}
	t.Logf("TestCentralRegistryLookupRequiresName: checked %d registry methods, violations %d",
		checked, violations)
}
