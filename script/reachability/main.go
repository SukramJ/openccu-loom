// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package main implements the dead-code reachability analyzer for openccu-loom.
//
// Lädt alle Packages des Repos via golang.org/x/tools/go/packages, baut ein SSA-Programm
// auf und verwendet golang.org/x/tools/go/callgraph/rta (Rapid Type Analysis) um alle
// exported Identifiers zu finden, die von keinem Production- oder Test-Entry-Point aus
// erreichbar sind.
//
// Output (default run, both modes):
//   - notes/parity/dead-code-inventory.json       — vollständiges Inventory (inkl. Test-Roots)
//   - notes/parity/dead-code-summary.md           — menschenlesbares Summary (Top-20 Packages, Top-50 Funcs)
//   - notes/parity/dead-code-production-only.json — Inventory ohne Test-Roots als Entry-Points
//
// Flags:
//   - -production-only: nur production-only Inventory erzeugen (kein combined run)
//
// Whitelist: exported Identifiers, die mit `// loom:reachable:reason="..."` annotiert sind,
// werden nicht als Dead-Code gelistet. Zusätzlich greift eine automatische Whitelist für
// Test-Files (_test.go, tests/ Verzeichnis), Mock/Fake/Stub/Dummy-Identifier und
// script/_tools.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// WhitelistEntry repräsentiert einen Identifier, der explizit als erreichbar markiert wurde.
type WhitelistEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	Reason     string `json:"reason"`
	File       string `json:"file"`
	Line       int    `json:"line"`
}

// UnreachableEntry repräsentiert einen exported Identifier ohne Production-Caller.
type UnreachableEntry struct {
	Package    string `json:"package"`
	Identifier string `json:"identifier"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"` // "func", "type", "var", "const"
}

// PackageSummary fasst die Dead-Code-Counts pro Package zusammen.
type PackageSummary struct {
	Package          string `json:"package"`
	UnreachableFuncs int    `json:"unreachable_funcs"`
	UnreachableTypes int    `json:"unreachable_types"`
	UnreachableOther int    `json:"unreachable_other"`
}

// Summary fasst die Ergebnisse zusammen.
type Summary struct {
	TotalExported int `json:"total_exported"`
	Reachable     int `json:"reachable"`
	Whitelisted   int `json:"whitelisted"`
	Unreachable   int `json:"unreachable"`
}

// Inventory ist das Output-Format für notes/parity/dead-code-inventory.json.
type Inventory struct {
	Generated   string             `json:"generated"`
	Head        string             `json:"head"`
	EntryPoints []string           `json:"entry_points"`
	Summary     Summary            `json:"summary"`
	ByPackage   []PackageSummary   `json:"by_package"`
	Unreachable []UnreachableEntry `json:"unreachable"`
	Whitelisted []WhitelistEntry   `json:"whitelisted"`
}

// whitelistKey identifiziert einen Whitelist-Eintrag eindeutig.
type whitelistKey struct {
	pkg  string
	name string
}

// autoWhitelistReason gibt an warum ein Item automatisch whitelisted wurde.
type autoWhitelistReason string

const (
	autoWhitelistTestFile        autoWhitelistReason = "auto-whitelist:pattern=test-file"
	autoWhitelistTestsDir        autoWhitelistReason = "auto-whitelist:pattern=tests-dir"
	autoWhitelistMockPrefix      autoWhitelistReason = "auto-whitelist:pattern=mock-fake-stub-dummy"
	autoWhitelistScriptTool      autoWhitelistReason = "auto-whitelist:pattern=script-tools"
	autoWhitelistRESTHandler     autoWhitelistReason = "auto-whitelist:pattern=rest-handler-pkg"
	autoWhitelistTypeAlias       autoWhitelistReason = "auto-whitelist:pattern=type-alias"
	autoWhitelistWSHandler       autoWhitelistReason = "auto-whitelist:pattern=ws-command-pkg"
	autoWhitelistDiscovery       autoWhitelistReason = "auto-whitelist:pattern=mqtt-discovery-builder"
	autoWhitelistMatterImpl      autoWhitelistReason = "auto-whitelist:pattern=matter-cluster-impl"
	autoWhitelistCalculatedDP    autoWhitelistReason = "auto-whitelist:pattern=calculated-dp-no-identity-wrapper"
	autoWhitelistHubFactory      autoWhitelistReason = "auto-whitelist:pattern=hub-factory-wrapper"
	autoWhitelistVisibilityRules autoWhitelistReason = "auto-whitelist:pattern=visibility-rules-registry"
	autoWhitelistMQTTLookup      autoWhitelistReason = "auto-whitelist:pattern=mqtt-entity-lookup"
	autoWhitelistWeekprofile     autoWhitelistReason = "auto-whitelist:pattern=weekprofile-converter"
	autoWhitelistCustomMixin     autoWhitelistReason = "auto-whitelist:pattern=custom-mixin-factory"
	autoWhitelistGenericHelper   autoWhitelistReason = "auto-whitelist:pattern=generic-model-helper"
	autoWhitelistTestSeam        autoWhitelistReason = "auto-whitelist:pattern=test-seam-with-clock"
	autoWhitelistMatterProtocol  autoWhitelistReason = "auto-whitelist:pattern=matter-protocol-handler"
	autoWhitelistXMLRPCExtract   autoWhitelistReason = "auto-whitelist:pattern=xmlrpc-extract-helper"
)

func main() {
	outPath := flag.String("out", "notes/parity/dead-code-inventory.json", "Output-Pfad für das Inventory (combined)")
	summaryPath := flag.String("summary", "notes/parity/dead-code-summary.md", "Output-Pfad für das Markdown-Summary")
	prodOutPath := flag.String("prod-out", "notes/parity/dead-code-production-only.json", "Output-Pfad für das production-only Inventory")
	repoRoot := flag.String("root", ".", "Repo-Root (default: cwd)")
	verbose := flag.Bool("verbose", false, "Verbose-Logging")
	productionOnly := flag.Bool("production-only", false, "Nur production-only Inventory erzeugen (Test-Roots werden nicht als Entry-Points behandelt)")
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if *productionOnly {
		// Nur production-only run
		if err := run(logger, *repoRoot, *prodOutPath, *summaryPath, true); err != nil {
			logger.Error("analyzer failed (production-only)", "err", err)
			os.Exit(1)
		}
		return
	}

	// Standard: combined run (inkl. Test-Roots)
	if err := run(logger, *repoRoot, *outPath, *summaryPath, false); err != nil {
		logger.Error("analyzer failed (combined)", "err", err)
		os.Exit(1)
	}

	// Dann production-only run für zweites Inventory
	logger.Info("starte production-only run...")
	if err := run(logger, *repoRoot, *prodOutPath, "", true); err != nil {
		logger.Error("analyzer failed (production-only)", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, repoRoot, outPath, summaryPath string, productionOnly bool) error { //nolint:gocognit,gocyclo,funlen // build tooling; many CLI branches
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	logger.Debug("repo root", "path", absRoot)

	head := gitHead(absRoot)
	logger.Debug("git HEAD", "rev", head)

	// --- 1. Alle Packages laden ---
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes,
		Dir:   absRoot,
		Tests: true,
		Env:   append(os.Environ(), "GOFLAGS=-tags=!ignore"),
	}

	patterns := []string{
		"github.com/SukramJ/openccu-loom/cmd/openccu-loom",
		"github.com/SukramJ/openccu-loom/cmd/openccu-loom-remote",
		"github.com/SukramJ/openccu-loom/cmd/hmcli",
		"github.com/SukramJ/openccu-loom/internal/...",
		"github.com/SukramJ/openccu-loom/pkg/...",
		"github.com/SukramJ/openccu-loom/tests/...",
	}

	logger.Info("lade packages...", "patterns", len(patterns))
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return fmt.Errorf("packages.Load: %w", err)
	}

	var loadErrors int
	packages.Visit(pkgs, func(p *packages.Package) bool {
		for _, e := range p.Errors {
			loadErrors++
			logger.Warn("package load error", "pkg", p.PkgPath, "err", e)
		}
		return true
	}, nil)
	if loadErrors > 0 {
		logger.Warn("packages mit Ladefehlern", "count", loadErrors)
	}
	logger.Info("packages geladen", "count", len(pkgs))

	// --- 2. SSA bauen ---
	logger.Info("baue SSA-Programm...")
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	logger.Info("SSA gebaut", "packages", len(ssaPkgs))

	// --- 3. Entry-Points sammeln ---
	var entryFuncs []*ssa.Function
	var entryPointNames []string

	for _, p := range ssaPkgs {
		if p == nil {
			continue
		}
		pkgPath := p.Pkg.Path()

		if pkgPath == "github.com/SukramJ/openccu-loom/cmd/openccu-loom" ||
			pkgPath == "github.com/SukramJ/openccu-loom/cmd/openccu-loom-remote" ||
			pkgPath == "github.com/SukramJ/openccu-loom/cmd/hmcli" {
			if fn := p.Func("main"); fn != nil {
				entryFuncs = append(entryFuncs, fn)
				name := strings.TrimPrefix(pkgPath, "github.com/SukramJ/openccu-loom/")
				entryPointNames = append(entryPointNames, name+"/main.go")
			}
			if fn := p.Func("init"); fn != nil {
				entryFuncs = append(entryFuncs, fn)
			}
		}

		// In production-only mode, Test* and Benchmark* functions are NOT
		// treated as entry-points.  Callers that are only reachable from tests
		// will therefore appear as unreachable — which is the correct signal
		// for dead-code that has no production caller.
		if !productionOnly {
			if strings.HasSuffix(pkgPath, "_test") || isTestPkg(p) {
				for name, mem := range p.Members {
					if fn, ok := mem.(*ssa.Function); ok {
						if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") {
							entryFuncs = append(entryFuncs, fn)
						}
					}
				}
			}
		}
	}

	// REST/WS request handlers (and the closures they return) are mounted on
	// the router reflectively, so RTA never sees them being called. Without
	// help it treats them — AND everything reachable ONLY through them
	// (adapters, providers, the Matter eligibility collector) — as unreachable.
	// checkAutoWhitelist papers over the handler functions themselves, but their
	// transitive callees in domain packages are left as false-negative dead code
	// that a benign refactor elsewhere can flip into the ratchet (see
	// docs/adr/0049-matter-one-endpoint-per-device.md, where the Matter
	// eligibility entry points went dark this way). Seed RTA with the handler
	// functions and their nested closures as extra entry points so their real
	// callees are traced instead of needing a manual loom:reachable annotation.
	reflectiveEntries := 0
	for fn := range ssautil.AllFunctions(prog) {
		if fn == nil || fn.Pkg == nil || !fn.Pos().IsValid() {
			continue
		}
		relFile := strings.TrimPrefix(prog.Fset.Position(fn.Pos()).Filename, absRoot+"/")
		if isReflectiveEntryFile(relFile) {
			entryFuncs = append(entryFuncs, fn)
			reflectiveEntries++
		}
	}
	logger.Info("entry-points gesammelt", "count", len(entryFuncs),
		"reflective_handlers", reflectiveEntries, "production_only", productionOnly)

	// --- 4. RTA-Analyse ---
	logger.Info("starte RTA-Analyse (kann 30-60s dauern)...")
	rtaResult := rta.Analyze(entryFuncs, true)
	logger.Info("RTA abgeschlossen")

	reachableFuncs := make(map[*ssa.Function]bool)
	for fn := range rtaResult.Reachable {
		reachableFuncs[fn] = true
	}

	// Unify test-instrumented package variants. go/packages loads a package in
	// several configurations (normal, package-under-test, external test), so one
	// logical function has several *ssa.Function instances. RTA reaches only the
	// instance on the live call path, but the classification below may inspect a
	// different variant — which would then read as unreachable even though the
	// function is genuinely live. Mark every instance whose logical identity
	// (RelString: package + receiver + name, unique per logical function so this
	// can never conflate two distinct functions) matches a reachable one.
	reachableSig := make(map[string]bool, len(reachableFuncs))
	for fn := range reachableFuncs {
		reachableSig[fn.RelString(nil)] = true
	}
	for _, p := range ssaPkgs {
		if p == nil {
			continue
		}
		for _, mem := range p.Members {
			fn, ok := mem.(*ssa.Function)
			if !ok || reachableFuncs[fn] {
				continue
			}
			if reachableSig[fn.RelString(nil)] {
				reachableFuncs[fn] = true
			}
		}
	}
	logger.Debug("erreichbare Funktionen", "count", len(reachableFuncs))

	// --- 5. Explizite Whitelist aus AST-Kommentaren einlesen ---
	whitelisted := make(map[whitelistKey]WhitelistEntry)
	collectWhitelisted(pkgs, absRoot, whitelisted, logger)
	logger.Info("whitelist geladen", "entries", len(whitelisted))

	// --- 6. Alle exported Identifiers sammeln und klassifizieren ---
	var unreachableItems []UnreachableEntry
	var whitelistedItems []WhitelistEntry
	totalExported := 0
	reachableCount := 0

	for _, p := range ssaPkgs {
		if p == nil {
			continue
		}
		pkgPath := p.Pkg.Path()

		if !strings.HasPrefix(pkgPath, "github.com/SukramJ/openccu-loom/") {
			continue
		}
		if strings.Contains(pkgPath, "script/_tools") || strings.Contains(pkgPath, "/script/") {
			continue
		}
		if strings.HasSuffix(pkgPath, "_test") || isTestPkg(p) {
			continue
		}

		for name, member := range p.Members {
			if !ast.IsExported(name) {
				continue
			}
			totalExported++

			relPkg := strings.TrimPrefix(pkgPath, "github.com/SukramJ/openccu-loom/")

			// Position bestimmen (für Auto-Whitelist-Checks benötigt)
			pos := prog.Fset.Position(member.Pos())
			relFile := strings.TrimPrefix(pos.Filename, absRoot+"/")

			// Type aliases re-export a foreign type under a local name;
			// every use resolves to the aliased type, so RTA can never
			// observe the alias itself as reachable. Listing them is noise.
			if t, isType := member.(*ssa.Type); isType {
				if tn, isName := t.Object().(*types.TypeName); isName && tn.IsAlias() {
					whitelistedItems = append(whitelistedItems, WhitelistEntry{
						Package:    relPkg,
						Identifier: name,
						Reason:     string(autoWhitelistTypeAlias),
						File:       relFile,
						Line:       pos.Line,
					})
					continue
				}
			}

			// Auto-Whitelist Verfeinerung 2+5: Test-Files und weitere Patterns
			if reason, ok := checkAutoWhitelist(relFile, name); ok {
				whitelistedItems = append(whitelistedItems, WhitelistEntry{
					Package:    relPkg,
					Identifier: name,
					Reason:     string(reason),
					File:       relFile,
					Line:       pos.Line,
				})
				continue
			}

			key := whitelistKey{pkg: relPkg, name: name}

			// Explizite Whitelist prüfen
			if entry, ok := whitelisted[key]; ok {
				whitelistedItems = append(whitelistedItems, entry)
				continue
			}

			// Erreichbarkeit prüfen (Verfeinerung 1: Type via Methoden)
			if isReachable(member, reachableFuncs, p) {
				reachableCount++
				continue
			}

			unreachableItems = append(unreachableItems, UnreachableEntry{
				Package:    relPkg,
				Identifier: name,
				File:       relFile,
				Line:       pos.Line,
				Kind:       memberKind(member),
			})
		}
	}

	logger.Info(
		"analyse fertig",
		"total_exported", totalExported,
		"reachable", reachableCount,
		"whitelisted", len(whitelistedItems),
		"unreachable", len(unreachableItems),
	)

	// --- 7. By-Package-Summary berechnen (Verfeinerung 3) ---
	byPackage := buildPackageSummary(unreachableItems)

	// --- 8. Inventory schreiben ---
	// Deterministic output: sort every slice by a stable key and use the
	// git HEAD (not a wall-clock timestamp) as the "generated" marker, so
	// re-running the analysis at the same commit yields a byte-identical
	// file — no spurious per-run diffs.
	if head == "" {
		head = "unknown"
	}
	sort.Strings(entryPointNames)
	sort.Slice(unreachableItems, func(i, j int) bool {
		a, b := unreachableItems[i], unreachableItems[j]
		switch {
		case a.Package != b.Package:
			return a.Package < b.Package
		case a.Identifier != b.Identifier:
			return a.Identifier < b.Identifier
		case a.File != b.File:
			return a.File < b.File
		default:
			return a.Line < b.Line
		}
	})
	sort.Slice(whitelistedItems, func(i, j int) bool {
		a, b := whitelistedItems[i], whitelistedItems[j]
		switch {
		case a.Package != b.Package:
			return a.Package < b.Package
		case a.Identifier != b.Identifier:
			return a.Identifier < b.Identifier
		case a.File != b.File:
			return a.File < b.File
		default:
			return a.Line < b.Line
		}
	})
	inv := Inventory{
		Generated:   head,
		Head:        head,
		EntryPoints: entryPointNames,
		Summary: Summary{
			TotalExported: totalExported,
			Reachable:     reachableCount,
			Whitelisted:   len(whitelistedItems),
			Unreachable:   len(unreachableItems),
		},
		ByPackage:   byPackage,
		Unreachable: unreachableItems,
		Whitelisted: whitelistedItems,
	}

	absOut := outPath
	if !filepath.IsAbs(outPath) {
		absOut = filepath.Join(absRoot, outPath)
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil { //nolint:gosec // G301: 0755 is the standard directory permission for CLI tool output dirs
		return fmt.Errorf("mkdir output dir: %w", err)
	}

	f, err := os.Create(absOut) //nolint:gosec // G304: absOut is derived from a user-supplied flag; the tool intentionally writes to the requested path
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(inv); err != nil {
		return fmt.Errorf("encode inventory: %w", err)
	}

	// --- 9. Markdown-Summary schreiben (Verfeinerung 4) ---
	// Skip summary when summaryPath is empty (e.g. production-only secondary run).
	if summaryPath != "" {
		absSummary := summaryPath
		if !filepath.IsAbs(summaryPath) {
			absSummary = filepath.Join(absRoot, summaryPath)
		}
		if err := writeSummaryMD(absSummary, inv); err != nil {
			return fmt.Errorf("write summary markdown: %w", err)
		}
		fmt.Printf("dead-code-summary.md    geschrieben: %s\n", absSummary)
	}

	fmt.Printf("dead-code-inventory.json geschrieben: %s\n", absOut)
	fmt.Printf("  total_exported: %d\n", totalExported)
	fmt.Printf("  reachable:      %d\n", reachableCount)
	fmt.Printf("  whitelisted:    %d\n", len(whitelistedItems))
	fmt.Printf("  unreachable:    %d\n", len(unreachableItems))

	if len(byPackage) > 0 {
		fmt.Println("\nTop-10 Pakete nach Dead-Code-Count:")
		limit := min(len(byPackage), 10)
		for _, ps := range byPackage[:limit] {
			fmt.Printf("  %-60s funcs=%d types=%d\n", ps.Package, ps.UnreachableFuncs, ps.UnreachableTypes)
		}
	}

	return nil
}

// isReflectiveEntryFile reports whether relFile holds request handlers the
// router mounts reflectively (HTTP handlers + WebSocket command handlers). RTA
// cannot observe these being called, so [main] seeds them — and their nested
// closures — as extra entry points so their real callees are traced. Keep this
// in sync with the rest-handler / ws-command auto-whitelist patterns in
// checkAutoWhitelist below.
func isReflectiveEntryFile(relFile string) bool {
	return strings.Contains(relFile, "internal/north/rest/handlers/") ||
		strings.Contains(relFile, "internal/north/rest/ws/")
}

// checkAutoWhitelist prüft ob ein Item automatisch whitelisted werden soll.
// Gibt den Reason-String zurück wenn ja, sonst ("", false).
func checkAutoWhitelist(relFile, identifier string) (autoWhitelistReason, bool) { //nolint:gocognit,gocyclo,funlen // build tooling; many CLI branches
	// Verfeinerung 2: _test.go Files
	if strings.HasSuffix(relFile, "_test.go") {
		return autoWhitelistTestFile, true
	}
	// Verfeinerung 5: tests/ Verzeichnis
	if strings.HasPrefix(relFile, "tests/") {
		return autoWhitelistTestsDir, true
	}
	// Verfeinerung 5: script/_tools
	if strings.Contains(relFile, "script/_tools") {
		return autoWhitelistScriptTool, true
	}
	// Verfeinerung 5: Mock/Fake/Stub/Dummy Identifier-Prefix (case-insensitive)
	lower := strings.ToLower(identifier)
	if strings.HasPrefix(lower, "mock") ||
		strings.HasPrefix(lower, "fake") ||
		strings.HasPrefix(lower, "stub") ||
		strings.HasPrefix(lower, "dummy") {
		return autoWhitelistMockPrefix, true
	}
	// REST-Handler werden via Router-Mount aufgerufen (reflective, nicht in RTA sichtbar)
	if strings.Contains(relFile, "internal/north/rest/handlers/") {
		return autoWhitelistRESTHandler, true
	}
	// WS-Commands werden via Router.Register aufgerufen
	if strings.Contains(relFile, "internal/north/rest/ws/") {
		return autoWhitelistWSHandler, true
	}
	// MQTT-Discovery-Builder: Build*Discovery oder Publish*Discovery
	if strings.HasPrefix(identifier, "Build") && strings.HasSuffix(identifier, "Discovery") {
		return autoWhitelistDiscovery, true
	}
	if strings.HasPrefix(identifier, "Publish") && (strings.HasSuffix(identifier, "Discovery") || strings.Contains(identifier, "Discovery")) {
		return autoWhitelistDiscovery, true
	}
	// Matter-Cluster-Implementierungen: Endpoint/Cluster-Methoden werden via Dispatcher gerufen
	if strings.Contains(relFile, "internal/north/matter/cluster/") {
		return autoWhitelistMatterImpl, true
	}
	// Calculated-DP no-identity wrappers: convenience constructors that delegate to
	// the WithIdentity variant. Production callers always use the WithIdentity form;
	// the plain New* forms serve test fixtures that don't carry CCU address context.
	if strings.Contains(relFile, "internal/model/calculated/") {
		lowerID := strings.ToLower(identifier)
		if strings.HasPrefix(lowerID, "new") || strings.HasPrefix(lowerID, "is") ||
			strings.HasPrefix(lowerID, "lookup") || strings.HasPrefix(lowerID, "make") ||
			strings.HasPrefix(lowerID, "with") {
			return autoWhitelistCalculatedDP, true
		}
	}
	// Hub factory wrappers: thin wrappers over NewConnectivity/NewMetrics/etc.,
	// called via Coordinator setup code.
	if strings.Contains(relFile, "internal/model/hub/factory.go") ||
		strings.Contains(relFile, "internal/model/hub/metrics.go") ||
		strings.Contains(relFile, "internal/model/hub/sysvar.go") {
		return autoWhitelistHubFactory, true
	}
	// Visibility rules registry functions: called via registry setup pipeline.
	if strings.Contains(relFile, "internal/store/visibility/") {
		return autoWhitelistVisibilityRules, true
	}
	// MQTT entity-lookup functions: exported for tests + called internally via dispatch.
	if strings.Contains(relFile, "internal/north/mqtt/entity_description") ||
		strings.Contains(relFile, "internal/north/mqtt/retain_cleanup.go") {
		return autoWhitelistMQTTLookup, true
	}
	// Weekprofile converter helpers: called via profile-converter pipeline.
	if strings.Contains(relFile, "internal/model/weekprofile/") {
		return autoWhitelistWeekprofile, true
	}
	// Custom-DP mixin factories: called via device-profile constructors loaded from registry.
	if strings.Contains(relFile, "internal/model/custom/") {
		lowerID := strings.ToLower(identifier)
		if strings.HasPrefix(lowerID, "new") || strings.HasPrefix(lowerID, "suppress") ||
			strings.HasPrefix(lowerID, "convert") || strings.HasPrefix(lowerID, "fan") {
			return autoWhitelistCustomMixin, true
		}
	}
	// Generic model helpers: resolver / sensor / quantity functions called via device pipeline.
	if strings.Contains(relFile, "internal/model/generic/") {
		return autoWhitelistGenericHelper, true
	}
	// Test seams with clock injection: WithClock / WithXxxClock / NewXxxWithClock patterns.
	if strings.HasSuffix(identifier, "WithClock") ||
		strings.Contains(identifier, "WithClock") ||
		(strings.HasSuffix(identifier, "Capped") && strings.Contains(relFile, "internal/audit/")) ||
		strings.HasSuffix(identifier, "CappedWithClock") {
		return autoWhitelistTestSeam, true
	}
	// Matter protocol handlers: TLV unmarshal, SPAKE2, Sigma, MRP — called via protocol stack.
	// SetForTest in bootid is a test seam (explicitly documented as test-only).
	if strings.Contains(relFile, "internal/north/matter/") {
		lowerID := strings.ToLower(identifier)
		if strings.HasPrefix(lowerID, "unmarshal") || strings.HasPrefix(lowerID, "decode") ||
			strings.HasPrefix(lowerID, "must") || strings.HasPrefix(lowerID, "generate") ||
			strings.HasPrefix(lowerID, "new") || strings.HasSuffix(lowerID, "fortest") {
			return autoWhitelistMatterProtocol, true
		}
	}
	// XML-RPC extract helpers: As* functions called via XML-RPC response parsing.
	if strings.Contains(relFile, "internal/client/transport/xmlrpc/extract.go") ||
		strings.Contains(relFile, "internal/client/transport/xmlrpc/message.go") {
		return autoWhitelistXMLRPCExtract, true
	}
	// Matter conformance test-vector runner: RunVectorSet / MustHex are
	// test-only helpers exported so codec-specific test files can import them.
	if strings.Contains(relFile, "internal/north/matter/conformance/") {
		return autoWhitelistTestSeam, true
	}
	// Hub model constructors: NewAlarmMessagesWithCentral and similar
	// multi-CCU variants are called via the legacy no-central wrappers
	// (NewAlarmMessages → NewAlarmMessagesWithCentral).
	if strings.Contains(relFile, "internal/model/hub/messages.go") ||
		strings.Contains(relFile, "internal/model/hub/service_messages.go") {
		return autoWhitelistHubFactory, true
	}
	// Combined DP constructors: all combined/ New* functions are called via
	// device-profile registry dispatch (cover, light, climate). RTA loses the
	// path through the registry map.
	if strings.Contains(relFile, "internal/model/combined/") {
		lowerID := strings.ToLower(identifier)
		if strings.HasPrefix(lowerID, "new") || lowerID == "recalcunit" {
			return autoWhitelistCustomMixin, true
		}
	}
	// pkg/hmtypes utility functions: address predicates, support helpers,
	// text-normalisation. All are small pure helpers referenced throughout the
	// codebase; RTA misses them when the only callers are in inlined or
	// interface-dispatch paths.
	if strings.Contains(relFile, "pkg/hmtypes/") {
		return autoWhitelistGenericHelper, true
	}
	// pkg/hmproto normalisation + hash helpers: called by protocol normalisation
	// and change-detection pipelines.
	if strings.Contains(relFile, "pkg/hmproto/") {
		return autoWhitelistGenericHelper, true
	}
	// pkg/hmenum utility functions: AllFields is called by the parameter
	// pipeline; ValidateStartup is driven by the contract suite, which this
	// analyzer does not load.
	if strings.Contains(relFile, "pkg/hmenum/field.go") ||
		strings.Contains(relFile, "pkg/hmenum/validate_startup.go") {
		return autoWhitelistGenericHelper, true
	}
	// internal/parameter metadata and converter helpers: called by parameter
	// pipeline read/write paths.
	if strings.Contains(relFile, "internal/parameter/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/model/schedule helpers: called by schedule coordinator.
	if strings.Contains(relFile, "internal/model/schedule/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/client/rega helper functions: script escaping, recorder cleanup.
	if strings.Contains(relFile, "internal/client/rega/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/client/reliability Wire* functions: installed by the
	// Interface-Client constructor; RTA loses the call through the factory.
	if strings.Contains(relFile, "internal/client/reliability/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/config default + option helpers.
	if strings.Contains(relFile, "internal/config/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/central/statemachine option functions: functional-option pattern
	// called via state-machine builder.
	if strings.Contains(relFile, "internal/central/statemachine/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/observability tracing helpers: context-propagation functions
	// invoked via middleware.
	if strings.Contains(relFile, "internal/observability/") {
		return autoWhitelistGenericHelper, true
	}
	// internal/north/rest/middleware context helpers: called by REST router.
	if strings.Contains(relFile, "internal/north/rest/middleware/") {
		return autoWhitelistGenericHelper, true
	}
	return "", false
}

// buildPackageSummary erstellt eine sortierte Per-Package-Summary.
func buildPackageSummary(items []UnreachableEntry) []PackageSummary {
	m := make(map[string]*PackageSummary)
	for _, item := range items {
		ps, ok := m[item.Package]
		if !ok {
			ps = &PackageSummary{Package: item.Package}
			m[item.Package] = ps
		}
		switch item.Kind {
		case "func":
			ps.UnreachableFuncs++
		case "type":
			ps.UnreachableTypes++
		default:
			ps.UnreachableOther++
		}
	}

	result := make([]PackageSummary, 0, len(m))
	for _, v := range m {
		result = append(result, *v)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UnreachableFuncs != result[j].UnreachableFuncs {
			return result[i].UnreachableFuncs > result[j].UnreachableFuncs
		}
		return result[i].Package < result[j].Package
	})
	return result
}

// writeSummaryMD schreibt das Markdown-Summary.
func writeSummaryMD(path string, inv Inventory) error {
	const tmplText = `# Dead-Code Summary

Generated: {{.Generated}}
HEAD: {{.Head}}

## Overview

| Metric | Count |
|---|---|
| Total Exported | {{.Summary.TotalExported}} |
| Reachable | {{.Summary.Reachable}} |
| Whitelisted | {{.Summary.Whitelisted}} |
| **Unreachable** | **{{.Summary.Unreachable}}** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
{{- range .Top20Packages}}
| {{.Package}} | {{.UnreachableFuncs}} | {{.UnreachableTypes}} | {{.UnreachableOther}} |
{{- end}}

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
{{- range .Top50Funcs}}
| {{.Package}} | {{.Identifier}} | {{.File}} | {{.Line}} |
{{- end}}

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
{{- range .ByPackage}}
| {{.Package}} | {{.UnreachableFuncs}} | {{.UnreachableTypes}} | {{.UnreachableOther}} |
{{- end}}
`

	type templateData struct {
		Generated     string
		Head          string
		Summary       Summary
		Top20Packages []PackageSummary
		Top50Funcs    []UnreachableEntry
		ByPackage     []PackageSummary
	}

	top20 := inv.ByPackage
	if len(top20) > 20 {
		top20 = top20[:20]
	}

	var interestingFuncs []UnreachableEntry
	for _, item := range inv.Unreachable {
		if item.Kind == "func" && !strings.HasSuffix(item.File, "_test.go") {
			interestingFuncs = append(interestingFuncs, item)
		}
		if len(interestingFuncs) >= 50 {
			break
		}
	}

	data := templateData{
		Generated:     inv.Generated,
		Head:          inv.Head,
		Summary:       inv.Summary,
		Top20Packages: top20,
		Top50Funcs:    interestingFuncs,
		ByPackage:     inv.ByPackage,
	}

	tmpl, err := template.New("summary").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("parse summary template: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: 0755 is the standard directory permission for CLI tool output dirs
		return fmt.Errorf("mkdir summary dir: %w", err)
	}

	f, err := os.Create(path) //nolint:gosec // G304: path is derived from a user-supplied flag; the tool intentionally writes to the requested path
	if err != nil {
		return fmt.Errorf("create summary file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return tmpl.Execute(f, data)
}

// isTestPkg prüft ob ein SSA-Package ein Test-Package ist.
func isTestPkg(p *ssa.Package) bool {
	if p.Pkg == nil {
		return false
	}
	path := p.Pkg.Path()
	return strings.HasSuffix(path, "_test") || strings.Contains(path, ".test")
}

// isReachable prüft ob ein SSA-Member von einem Entry-Point aus erreichbar ist.
// Verfeinerung 1: Typen gelten als erreichbar wenn irgendeine ihrer Methoden
// (Value- oder Pointer-Receiver) im Callgraph erreichbar ist.
func isReachable(member ssa.Member, reachable map[*ssa.Function]bool, pkg *ssa.Package) bool {
	switch m := member.(type) {
	case *ssa.Function:
		return reachable[m]
	case *ssa.Type:
		named, ok := m.Type().(*types.Named)
		if !ok {
			return false
		}
		// Value-Receiver-Methoden: direkt über named.Method(i) zugänglich
		for method := range named.Methods() {
			fn := pkg.Prog.FuncValue(method)
			if fn != nil && reachable[fn] {
				return true
			}
		}
		// Pointer-Receiver-Methoden: über MethodSet des Pointer-Typs
		ptrType := types.NewPointer(named)
		mset := types.NewMethodSet(ptrType)
		for sel := range mset.Methods() {
			if sel == nil {
				continue
			}
			fn, ok := sel.Obj().(*types.Func)
			if !ok {
				continue
			}
			ssaFn := pkg.Prog.FuncValue(fn)
			if ssaFn != nil && reachable[ssaFn] {
				return true
			}
		}
		return false
	case *ssa.Global:
		// Globals in cmd/-Packages konservativ als erreichbar markieren
		pkgPath := pkg.Pkg.Path()
		if strings.Contains(pkgPath, "/cmd/") {
			return true
		}
		return false
	default:
		// Konservativ: unbekannte Member als erreichbar markieren
		return true
	}
}

// memberKind gibt den Kind-String für ein SSA-Member zurück.
func memberKind(member ssa.Member) string {
	switch member.(type) {
	case *ssa.Function:
		return "func"
	case *ssa.Type:
		return "type"
	case *ssa.Global:
		return "var"
	default:
		return "unknown"
	}
}

// collectWhitelisted liest alle Go-Dateien und sammelt Identifiers mit
// `// loom:reachable:reason="..."` Kommentaren direkt über der Deklaration.
func collectWhitelisted(pkgs []*packages.Package, absRoot string, out map[whitelistKey]WhitelistEntry, logger *slog.Logger) { //nolint:gocognit // build tooling; many CLI branches
	seen := make(map[string]bool)

	packages.Visit(pkgs, func(p *packages.Package) bool {
		if !strings.HasPrefix(p.PkgPath, "github.com/SukramJ/openccu-loom/") {
			return true
		}
		if strings.Contains(p.PkgPath, "script/_tools") {
			return true
		}

		for _, f := range p.GoFiles {
			if seen[f] {
				continue
			}
			seen[f] = true

			fset := token.NewFileSet()
			astFile, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
			if err != nil {
				logger.Debug("parse error", "file", f, "err", err)
				continue
			}

			relPkg := strings.TrimPrefix(p.PkgPath, "github.com/SukramJ/openccu-loom/")

			for _, decl := range astFile.Decls {
				reason, ok := findWhitelistComment(astFile, fset, decl)
				if !ok {
					continue
				}

				pos := fset.Position(decl.Pos())
				relFile := strings.TrimPrefix(pos.Filename, absRoot+"/")

				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name != nil && ast.IsExported(d.Name.Name) {
						key := whitelistKey{pkg: relPkg, name: d.Name.Name}
						out[key] = WhitelistEntry{
							Package:    relPkg,
							Identifier: d.Name.Name,
							Reason:     reason,
							File:       relFile,
							Line:       pos.Line,
						}
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if ast.IsExported(s.Name.Name) {
								key := whitelistKey{pkg: relPkg, name: s.Name.Name}
								out[key] = WhitelistEntry{
									Package:    relPkg,
									Identifier: s.Name.Name,
									Reason:     reason,
									File:       relFile,
									Line:       fset.Position(s.Pos()).Line,
								}
							}
						case *ast.ValueSpec:
							for _, name := range s.Names {
								if ast.IsExported(name.Name) {
									key := whitelistKey{pkg: relPkg, name: name.Name}
									out[key] = WhitelistEntry{
										Package:    relPkg,
										Identifier: name.Name,
										Reason:     reason,
										File:       relFile,
										Line:       fset.Position(s.Pos()).Line,
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	}, nil)
}

// findWhitelistComment sucht einen `// loom:reachable:reason="..."` Kommentar
// direkt vor der gegebenen Deklaration.
func findWhitelistComment(f *ast.File, fset *token.FileSet, decl ast.Decl) (string, bool) {
	declLine := fset.Position(decl.Pos()).Line

	for _, cg := range f.Comments {
		for _, c := range cg.List {
			commentLine := fset.Position(c.Pos()).Line
			if commentLine >= declLine-1 && commentLine < declLine {
				text := strings.TrimPrefix(c.Text, "//")
				text = strings.TrimSpace(text)
				if after, ok := strings.CutPrefix(text, "loom:reachable:reason="); ok {
					reason := after
					reason = strings.Trim(reason, `"`)
					return reason, true
				}
			}
		}
	}
	return "", false
}

// gitHead liest die aktuelle Git-Revision.
func gitHead(dir string) string {
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
