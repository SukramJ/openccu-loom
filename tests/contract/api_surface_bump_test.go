// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

var updateAPISurface = flag.Bool("update-api-surface", false,
	"rewrite tests/contract/testdata/api_surface.json from the current spec")

// apiSurface is the committed inventory of everything a client can depend on.
// It is deliberately coarse: names and types, no descriptions, no examples —
// a description edit must not make this guard fire.
type apiSurface struct {
	APIVersion string              `json:"api_version"`
	Operations map[string]string   `json:"operations"` // "GET /path" -> operationId
	Schemas    map[string][]string `json:"schemas"`    // schema name -> ["field:type", …] sorted
}

// valueSemanticsChanges records contract changes no schema diff can see:
// the shape is identical on both sides, so the only honest mechanism is a
// list somebody writes by hand.
//
// Two kinds qualify, and an entry says which. A field whose *meaning*
// changed while its name and type stayed the same is the original case and
// a major bump. A field whose *vocabulary* grew — a new value in an
// open-ended set, a reference that became a declared type — is additive and
// a minor bump, but it belongs here for the same reason: a client that
// hardcoded the old set has no other way to learn it moved.
//
// The entry format is "<major-version-that-carried-it> <schema>.<field>: what
// changed". Entries are never deleted: the list is the answer to "why did this
// field stop meaning what my client assumed", asked by someone reading an old
// integration years later.
var valueSemanticsChanges = []string{
	"7.0.0 CaptureIndex: the diagnostics capture response became an array, having been declared an object",
	"7.1.0 DataPoint.value: unchanged, but display_value was added beside it — value stays the raw CCU wire value",
	"7.7.0 DataPointValueChangedPayload.display_value: documented, not introduced — the daemon has emitted it on the push since 7.2.0, so a client can observe the field from a 7.2.0..7.6.0 daemon whose spec does not declare it",
	"7.11.0 DataPointKey.interface/parameter/paramset_key: vocabulary — three properties in assets/schemas/types.json carried a $ref into #/definitions/ for targets the file never held, so no generator could resolve them; they are declared strings now, each naming the enum in enums.json that supplies its values",
	"7.12.0 Info.capabilities: vocabulary — four tokens added to the open set (mqtt.raw.v1, webhook.inbound.v1, diagrams.v1, admin.persistence.v1). The field is an array of strings either way, so no diff sees it; a client that hardcoded the token list learns of them only from here",
	"7.13.0 HubMetricsEntry.connection_latency_ms: meaning — the number is the same shape and unit but a different measurement. It was the duration of one JSON-RPC Interface.listInterfaces call on the reconciler's five-minute cadence, a one-way surface covering neither BIN-RPC nor the callback leg; it is now the matched PING→PONG round-trip over each interface's own transport on the 30-second connection-check cadence. A client that calibrated thresholds against the old figure will read higher values for a healthy CCU, because the old one omitted the reply path",
	"7.13.0 (wsapi 1.2) heartbeat.echo / heartbeat.rtt_ms: vocabulary — the server ping carries an optional opaque echo token, and reports the previous heartbeat's round-trip as rtt_ms once a client echoes it. Both are absent unless used and neither appears in the OpenAPI schema, so no diff sees them; a bare {op:\"pong\"} stays a valid heartbeat",
	"7.14.0 DeviceCreatedPayload (device.created broadcast): meaning — the broadcast stopped carrying the CCU's full inventory. The daemon answers listDevices with an empty array, so the CCU re-announces every device it has after each reconnect, and all of it was published as device.created; only an address the device registry does not already hold is announced now. The shape is identical either way, so no diff sees it, and it inverts what a subscriber should do: the frame is a genuine arrival to act on rather than a fleet-wide repeat to filter out. A client that rebuilt its model from that burst no longer receives one",
	"7.25.0 EventGroupSummary.unique_id: meaning — the key changed shape, from `loom_<channel>_event_group/<kind>` (channel first, a slash, the kind unshortened) to `loom_event_group_<kind>_<channel>`, which is the layout the reference stack and the Python client build. Same field, same type, so no schema diff sees it. Nothing consumed the old value: the client recomputes the reference spelling itself and never reads this field, which is why the divergence survived — the field's own description invited a client to key its registry on it, and doing so would have bound entities to a key nothing else uses. The central-id slot moved too, from in front of the whole key into the channel id, which matters only for the address families that carry one (virtual remotes)",
	"7.22.0 CustomDPSummary.kind / CustomDPSummary.capabilities: vocabulary — both token sets are now published in full as open vocabularies in assets/schemas/enums.json (CustomDPKind, CustomDPCapability). Neither field changed shape, so no schema diff sees it; the spec previously listed a partial \"e.g.\" set in prose, which is how a client came to hardcode a subset. Both stay open on purpose: kind has a reachable empty value, and an absent capability key means the flag does not apply to that category rather than that it is false",
	"7.22.0 (wsapi 1.8) envelope.kind: vocabulary — the initial|change|refresh set is a named type now (hmenum.WSEnvelopeKind) and ships in assets/schemas/enums.json like every other wire vocabulary. The wire is unchanged; before this the three literals existed only as prose in wsapi.json, so a generated client had to spell them itself",
	"9.0.0 (wsapi) links.put_paramset.edit_token / paramset.put.paramset_key: meaning — links.put_paramset now REQUIRES an edit-lock token, and its lock key carries the peer (channel:{address}:LINK:{peer_address}), because two links on one channel are two resources. A client that wrote a link paramset without a session is refused with a locked error now; nothing in the OpenAPI schema moved, so no diff sees it. In the same change paramset.put stopped accepting LINK: a LINK paramset is addressed by its partner channel and that command has no partner argument, so the key reached the wire as the literal string \"LINK\" and addressed no link. Callers use links.put_paramset, which has always carried the peer",
	"7.28.0 SmokeDetectorAlarmStatus: vocabulary — the four SMOKE_DETECTOR_ALARM_STATUS wire labels (IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM) are a named type now and ship in assets/schemas/enums.json like every other wire vocabulary. The wire is unchanged and no OpenAPI schema references the type, so no schema diff sees it. It exists because the set had been written out twice inside the domain — once for the derived SMOKE_ALARM binary sensor, once for the safety classifier — and INTRUSION_ALARM is the label that makes the set worth publishing: it is the installation reading back its own siren command, not a detection, so a client that treats every non-idle label as smoke is wrong about exactly one of the four",
	"7.15.0 DeviceCreatedPayload.source: vocabulary — the documented value set was wrong rather than merely incomplete. It named NEW_DEVICE, which hmenum.SourceOfDeviceCreation has never had, so a client filtering on the documented token matched nothing, and the token reached generated client packages (openccu-loom-types) as their documentation. The real values are NEW (a pairing), REFRESH (a factory-reset re-pair), MANUAL (an operator accepting out of the deferred-creation inbox) and CACHE (a boot restore from the persisted description cache); INIT is defined by the enum but no producer sets it on this broadcast",
}

// TestAPISurfaceChangesCarryTheRightBump pins the two halves of this project's
// versioning policy to each other: what [handlers.APIVersion] claims about a
// change, and what the specification actually did.
//
// The policy is written on APIVersion itself — "addition of capabilities is a
// minor bump, removal or rename of an existing capability or payload field is
// a major bump" — and until this guard nothing enforced it. Two payload fields
// had already changed value semantics under a minor and a patch bump, and the
// nearest thing to a check was a test pinning the version string to itself.
//
// What this catches:
//   - a removed or renamed operation or field under anything less than a major
//     bump: a generated client stops compiling, or silently reads a zero;
//   - a retyped field under anything less than a major bump: the same, with the
//     failure moved to runtime;
//   - an added operation or field with no bump at all: a client has no way to
//     detect the capability it could now use.
//
// What it cannot catch, by construction: a field that keeps its name and type
// and changes what it *means*. That is what [valueSemanticsChanges] is for, and
// why the review rule matters more than the guard.
func TestAPISurfaceChangesCarryTheRightBump(t *testing.T) {
	spec := loadOpenAPISpec(t)
	current := buildAPISurface(spec)
	addCompanionSchemas(t, &current)

	path := apiSurfacePath(t)
	if *updateAPISurface {
		writeAPISurface(t, path, current)
		t.Logf("rewrote %s at api_version %s", path, current.APIVersion)
		return
	}

	raw, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-api-surface)", path, err)
	}
	var baseline apiSurface
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var breaking, additive []string

	for key, id := range baseline.Operations {
		cur, ok := current.Operations[key]
		switch {
		case !ok:
			breaking = append(breaking, "operation removed: "+key)
		case id == unnamedOperation && cur != unnamedOperation:
			// Naming a previously bare route is an addition, not a rename.
			// operationId is optional, and a generator keyed on it never
			// emitted an entry for the route at all — openapi-typescript
			// leaves such a route out of its `operations` table entirely, so
			// there is no symbol a client could have bound to and none can
			// break. The reverse direction stays below: dropping a name does
			// remove a symbol.
			additive = append(additive, fmt.Sprintf("operationId named on %s: %q", key, cur))
		case cur != id:
			breaking = append(breaking, fmt.Sprintf("operationId renamed on %s: %q -> %q", key, id, cur))
		}
	}
	for key := range current.Operations {
		if _, ok := baseline.Operations[key]; !ok {
			additive = append(additive, "operation added: "+key)
		}
	}

	for name, oldFields := range baseline.Schemas {
		newFields, ok := current.Schemas[name]
		if !ok {
			breaking = append(breaking, "schema removed: "+name)
			continue
		}
		oldSet := map[string]string{}
		for _, f := range oldFields {
			n, typ := splitField(f)
			oldSet[n] = typ
		}
		newSet := map[string]string{}
		for _, f := range newFields {
			n, typ := splitField(f)
			newSet[n] = typ
		}
		for n, oldType := range oldSet {
			newType, ok := newSet[n]
			switch {
			case !ok:
				breaking = append(breaking, fmt.Sprintf("field removed: %s.%s", name, n))
			case newType != oldType:
				breaking = append(breaking, fmt.Sprintf("field retyped: %s.%s %s -> %s", name, n, oldType, newType))
			}
		}
		for n := range newSet {
			if _, ok := oldSet[n]; !ok {
				additive = append(additive, fmt.Sprintf("field added: %s.%s", name, n))
			}
		}
	}
	for name := range current.Schemas {
		if _, ok := baseline.Schemas[name]; !ok {
			additive = append(additive, "schema added: "+name)
		}
	}

	if len(breaking) == 0 && len(additive) == 0 {
		if baseline.APIVersion != current.APIVersion {
			t.Errorf("APIVersion moved %s -> %s with no surface change recorded.\n"+
				"If the change is a value-semantics one, add it to valueSemanticsChanges\n"+
				"and refresh the baseline; a schema diff cannot see it.",
				baseline.APIVersion, current.APIVersion)
		}
		return
	}

	oldMajor, oldMinor := majorMinor(t, baseline.APIVersion)
	newMajor, newMinor := majorMinor(t, current.APIVersion)

	sort.Strings(breaking)
	sort.Strings(additive)

	switch {
	case len(breaking) > 0 && newMajor <= oldMajor:
		t.Errorf("the specification lost or reshaped %d thing(s) while APIVersion went %s -> %s.\n"+
			"A removal, rename or retype is a MAJOR bump — a generated client either stops\n"+
			"compiling or silently reads a zero.\n\n  %s\n\n"+
			"Either restore the field additively (new name alongside the old one, old one\n"+
			"deprecated for a release), or bump the major and refresh the baseline with:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface",
			len(breaking), baseline.APIVersion, current.APIVersion, strings.Join(breaking, "\n  "))
	case len(additive) > 0 && newMajor == oldMajor && newMinor <= oldMinor:
		t.Errorf("the specification gained %d thing(s) while APIVersion stayed at %s.\n"+
			"An addition is a MINOR bump — without one a client has no way to detect the\n"+
			"capability it could now use.\n\n  %s\n\n"+
			"Bump the minor, then refresh the baseline with:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface",
			len(additive), baseline.APIVersion, strings.Join(additive, "\n  "))
	default:
		// The bump matches what the surface did. The baseline is simply stale;
		// say so rather than passing silently, so it is refreshed in the same
		// commit as the change it describes and never drifts a release behind.
		t.Errorf("the surface changed and APIVersion moved %s -> %s correctly, but the\n"+
			"committed baseline is stale. Refresh it in this same commit:\n"+
			"  GOMAXPROCS=2 go test -p 2 -run TestAPISurfaceChangesCarryTheRightBump ./tests/contract/ -update-api-surface\n\n"+
			"breaking (%d):\n  %s\nadditive (%d):\n  %s",
			baseline.APIVersion, current.APIVersion,
			len(breaking), strings.Join(breaking, "\n  "),
			len(additive), strings.Join(additive, "\n  "))
	}
}

// TestAPIVersionMatchesTheSpecDocument keeps the constant and the document
// from drifting: they are two spellings of one number, and a bump applied to
// only one of them makes every other check here meaningless.
func TestAPIVersionMatchesTheSpecDocument(t *testing.T) {
	spec := loadOpenAPISpec(t)
	if spec.Info == nil {
		t.Fatal("openapi.yaml has no info block")
	}
	if spec.Info.Version != handlers.APIVersion {
		t.Errorf("openapi.yaml info.version = %q but handlers.APIVersion = %q",
			spec.Info.Version, handlers.APIVersion)
	}
}

// unnamedOperation is the sentinel the surface records for a route that
// carries no operationId. It is not a name: the diff above treats a move
// away from it as additive rather than as a rename.
const unnamedOperation = "(unnamed)"

func buildAPISurface(spec *openapi3.T) apiSurface {
	out := apiSurface{
		APIVersion: handlers.APIVersion,
		Operations: map[string]string{},
		Schemas:    map[string][]string{},
	}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			if id == "" {
				id = unnamedOperation
			}
			out.Operations[method+" "+path] = id
		}
	}
	for name, ref := range spec.Components.Schemas {
		if ref == nil || ref.Value == nil {
			continue
		}
		fields := make([]string, 0, len(ref.Value.Properties))
		for prop, pref := range ref.Value.Properties {
			fields = append(fields, prop+":"+schemaTypeOf(pref))
		}
		sort.Strings(fields)
		out.Schemas[name] = fields
	}
	return out
}

// addCompanionSchemas folds assets/schemas/types.json into the same inventory
// under a prefixed key.
//
// It is here because the two halves of this project's versioning policy
// disagreed without it. script/check_api_version_bump.sh treats four files as
// contract assets — openapi.yaml, wsapi.json, enums.json and types.json — and
// demands a bump when any of them changes. This guard read only openapi.yaml,
// so a bump earned by one of the other three arrived here as a version that
// moved for no visible reason, and the only way past was to refresh the
// baseline: the version change recorded, the change that caused it not.
//
// That happened the first time a types.json-only fix needed a release. Three
// DataPointKey properties carried a `$ref` into `#/definitions/` for targets
// that were never in the file, so no generator could resolve them; replacing
// them with declared strings is a real contract change to a published asset,
// and this guard had no way to say what it was.
//
// enums.json stays out: its entries are value lists rather than typed fields,
// so this diff would report every added wire value as a schema change. CI
// regenerates it and fails on a diff, which is the check that fits its shape.
func addCompanionSchemas(t *testing.T, out *apiSurface) {
	t.Helper()

	const asset = "assets/schemas/types.json"

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), asset)) //nolint:gosec // fixed repo-relative asset
	if err != nil {
		t.Fatalf("read %s: %v", asset, err)
	}
	var doc struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Type string `json:"type"`
				Ref  string `json:"$ref"`
			} `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", asset, err)
	}
	if len(doc.Definitions) == 0 {
		t.Fatalf("%s declares no definitions — the parse is wrong, and an inventory "+
			"that reads nothing reports every later change as an addition", asset)
	}

	for name, def := range doc.Definitions {
		fields := make([]string, 0, len(def.Properties))
		for prop, spec := range def.Properties {
			typ := spec.Type
			if spec.Ref != "" {
				typ = "$ref:" + spec.Ref[strings.LastIndex(spec.Ref, "/")+1:]
			}
			if typ == "" {
				typ = "unknown"
			}
			fields = append(fields, prop+":"+typ)
		}
		sort.Strings(fields)
		out.Schemas["types.json:"+name] = fields
	}
}

// schemaTypeOf reduces a property to the coarse shape a client's generated
// type depends on. A $ref is reported by target name, so re-pointing a field
// at a different schema reads as a retype — which for a client it is.
func schemaTypeOf(ref *openapi3.SchemaRef) string {
	if ref == nil {
		return "unknown"
	}
	if ref.Ref != "" {
		return "$ref:" + ref.Ref[strings.LastIndex(ref.Ref, "/")+1:]
	}
	if ref.Value == nil {
		return "unknown"
	}
	v := ref.Value
	if v.Type == nil || len(*v.Type) == 0 {
		switch {
		case len(v.OneOf) > 0:
			return "oneOf"
		case len(v.AnyOf) > 0:
			return "anyOf"
		case len(v.AllOf) > 0:
			return "allOf"
		}
		return "unknown"
	}
	types := append([]string(nil), (*v.Type)...)
	sort.Strings(types)
	base := strings.Join(types, "|")
	if base == "array" {
		return "array<" + schemaTypeOf(v.Items) + ">"
	}
	return base
}

func splitField(f string) (name, typ string) {
	i := strings.Index(f, ":")
	if i < 0 {
		return f, "unknown"
	}
	return f[:i], f[i+1:]
}

func majorMinor(t *testing.T, v string) (major, minor int) {
	t.Helper()
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("APIVersion %q is not semver", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("APIVersion %q has a non-numeric major: %v", v, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("APIVersion %q has a non-numeric minor: %v", v, err)
	}
	return major, minor
}

func apiSurfacePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "api_surface.json")
}

func writeAPISurface(t *testing.T, path string, s apiSurface) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	blob, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		t.Fatalf("marshal surface: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestValueSemanticsChangesAreWellFormed keeps [valueSemanticsChanges] a
// register rather than a comment block.
//
// The list is the only record of a change no schema diff can see: a field that
// keeps its name and its type and starts meaning something else. Nothing can
// verify such an entry mechanically — that is the whole reason the list exists
// — so what this checks is the two things that would make the register useless
// to the person reading it years later: an entry that names no version, and an
// entry that names a version the API has not reached.
func TestValueSemanticsChangesAreWellFormed(t *testing.T) {
	currentMajor, currentMinor := majorMinor(t, handlers.APIVersion)

	for _, entry := range valueSemanticsChanges {
		version, rest, ok := strings.Cut(entry, " ")
		if !ok || rest == "" {
			t.Errorf("value-semantics entry %q does not start with the version that carried "+
				"it followed by a description", entry)
			continue
		}
		// Compared as a pair. Comparing majors alone let the guard miss
		// every case the register can actually produce: its entries are
		// minor-level ("7.0.0", "7.1.0", "7.7.0") and the API has stayed
		// inside major 7 since the register was introduced, so `major >
		// currentMajor` was false for any entry anyone would plausibly
		// write. A "7.99.0" line against APIVersion 7.11.0 passed — the
		// exact promise-not-a-record case the error text below describes.
		major, minor := majorMinor(t, version)
		if major > currentMajor || (major == currentMajor && minor > currentMinor) {
			t.Errorf("value-semantics entry %q names API %d.%d, but the API is only at %s. "+
				"An entry recorded before the bump that carries it is a promise, not a record.",
				entry, major, minor, handlers.APIVersion)
		}
		if !strings.Contains(rest, ":") {
			t.Errorf("value-semantics entry %q does not say WHICH field changed meaning; the "+
				"format is \"<version> <schema>.<field>: what changed\"", entry)
		}
	}
}
