// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// End-to-end integration tests for the Security & Safety domain
// (internal/security; notes/concepts/security-safety-concept.md) against the
// in-process godevccu simulator. The harness mirrors
// alarm_engine_e2e_test.go / alarm_rest_e2e_test.go: the full central →
// device-pipeline stack comes from newSPAHarness (tests/integration/
// spa_e2e_harness_test.go), a migrated daemon SQLite database backs the
// domain's fault ledger and operator-override store, and the REST layer
// is reached over a real httptest.Server built with only the Security
// dependency wired.
//
// Unlike the alarm tests, no alarm.Service is ever started here. That is
// deliberate: the domain's own package documentation states its central
// claim as "it deliberately runs independently of the alarm engine" —
// every test in this file exercises exactly that configuration, and
// TestSecurityDomainReportsHazardsWithoutAlarmEngine makes the claim an
// explicit assertion rather than an incidental side effect of the
// others' setup.
//
// The fleet is a water sensor (HmIP-SWD — WATER_DETECTION_TRANSMITTER on
// channel :1, MAINTENANCE on channel :0) and a smoke detector (HmIP-SWSD
// — SMOKE_DETECTOR on channel :1), chosen because both hazard classes
// they cover (water, smoke) are reachable from godevccu's embedded
// device set and because HmIP-SWSD's SMOKE_DETECTOR_COMMAND is the
// actuator-feedback parameter the source-inventory test uses to pin the
// safety.Excluded gate.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// securityModels is the godevccu fleet the security domain tests load.
var securityModels = []string{"HmIP-SWD", "HmIP-SWSD"}

// securityHarness bundles the in-process central stack (via
// newSPAHarness) with a migrated daemon database and a started
// security.Service. Deps.AlarmBus is always left nil: no alarm.Service
// runs in this file.
type securityHarness struct {
	t      *testing.T
	h      *spaHarness
	db     *sql.DB
	stores *security.Stores
	svc    *security.Service
}

// newSecurityHarness builds the central + registry + migrated security
// stores and starts the service.
func newSecurityHarness(t *testing.T) *securityHarness {
	t.Helper()
	h := newSPAHarness(t, securityModels)

	reg := central.NewRegistry()
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "openccu-loom.db")
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(dbPath))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A real Catalogs is required, not merely convenient: renderer.render
	// (internal/security/render.go) calls cat.TF unconditionally on every
	// hazard/fault transition, and *i18n.Catalogs has no nil-receiver
	// guard — a nil Catalogs panics the first time a class activates.
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}

	stores := &security.Stores{
		Faults:  sqlitestore.NewSecurityFaultStore(db),
		Sources: sqlitestore.NewSecuritySourceStore(db),
		Sensors: sqlitestore.NewAlarmSensorStore(db),
		Zones:   sqlitestore.NewAlarmZoneStore(db),
	}

	svc, err := security.New(security.Deps{
		Registry: reg,
		Stores:   stores,
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	})
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("security.Service.Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	return &securityHarness{t: t, h: h, db: db, stores: stores, svc: svc}
}

// centralName is the scoping dimension the security refs share.
func (sh *securityHarness) centralName() string { return sh.h.central.Name() }

// channelParam finds the data-point key of one (model, channel number,
// parameter) triple. Fails the test when the model, channel or
// parameter is missing so a fleet change surfaces as a clear failure.
func (sh *securityHarness) channelParam(model string, channelNo int, param hmenum.Parameter) hmtypes.DataPointKey {
	sh.t.Helper()
	d := sh.h.findDevice(model)
	for _, ch := range d.Channels() {
		if ch.Number != channelNo {
			continue
		}
		dp := ch.Parameter(param)
		if dp == nil {
			sh.t.Fatalf("%s channel :%d has no %s parameter", model, channelNo, string(param))
			return hmtypes.DataPointKey{}
		}
		return dp.DataPointKey()
	}
	sh.t.Fatalf("%s channel :%d not found", model, channelNo)
	return hmtypes.DataPointKey{}
}

// waterMoistureKey returns the HmIP-SWD WATER_DETECTION_TRANSMITTER
// channel's MOISTURE_DETECTED data point.
func (sh *securityHarness) waterMoistureKey() hmtypes.DataPointKey {
	return sh.channelParam("HmIP-SWD", 1, hmenum.ParameterMoistureDetected)
}

// waterUnreachKey returns the HmIP-SWD MAINTENANCE channel's UNREACH data
// point — the technical-fault source the device carries once it is
// alarm-enrolled (classify()'s deviceRelevant gate,
// internal/security/index.go).
func (sh *securityHarness) waterUnreachKey() hmtypes.DataPointKey {
	return sh.channelParam("HmIP-SWD", 0, hmenum.ParameterUnreach)
}

// injectBool publishes a CCU→daemon value change of a BOOL data point on
// the central bus the security service subscribes to. Mirrors the
// injectWindow pattern of alarm_engine_e2e_test.go for a boolean rather
// than an enumerated parameter.
func (sh *securityHarness) injectBool(key hmtypes.DataPointKey, on bool) {
	sh.t.Helper()
	if ch := sh.h.central.GetChannel(key.ChannelAddress); ch != nil {
		if dp := ch.Parameter(hmenum.Parameter(key.Parameter)); dp != nil {
			if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
				setter.OnWireValue(on)
			}
		}
	}
	events.Publish(sh.h.central.EventBus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		Key:      key,
		OldValue: hmtypes.BoolValue(!on),
		NewValue: hmtypes.BoolValue(on),
	})
}

// seedAlarmSensor enrols one data point as an alarm sensor directly in
// the shared alarm_sensors table the security domain reads read-only —
// no alarm.Service needs to run for this: the domain only consults the
// row set (indexUnit's deviceRelevant gate), not a live engine.
func (sh *securityHarness) seedAlarmSensor(id string, key hmtypes.DataPointKey, typ hmenum.AlarmSensorType) {
	sh.t.Helper()
	now := time.Now().UnixMilli()
	if err := sh.stores.Sensors.Upsert(context.Background(), sqlitestore.AlarmSensorRow{
		ID: id, ZoneID: "zone-security-test", CentralName: sh.centralName(),
		InterfaceID: key.InterfaceID, ChannelAddress: key.ChannelAddress, Parameter: key.Parameter,
		SensorType: typ, Name: id, ConfigJSON: "{}", CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		sh.t.Fatalf("seed alarm sensor: %v", err)
	}
}

// securityRestHarness wraps a securityHarness with an httptest.Server
// bound to the /security/* routes, mirroring alarmRestHarness
// (alarm_rest_e2e_test.go): a fixed operator identity, no full daemon
// composition root — only the Security dependency is wired.
type securityRestHarness struct {
	*securityHarness
	api    *httptest.Server
	client *http.Client
}

// newSecurityRestHarness builds the central + registry + stores + a
// started security.Service via newSecurityHarness, then wraps it in a
// REST router reachable over HTTP.
func newSecurityRestHarness(t *testing.T) *securityRestHarness {
	t.Helper()
	sh := newSecurityHarness(t)

	mw := auth.NewMiddleware(nil, nil)
	operatorResolve := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: "test-operator", Role: auth.RoleOperator})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	router := rest.NewRouter(rest.Deps{
		StartedAt:       time.Now(),
		Security:        sh.svc,
		AuditRecorder:   audit.NewBuffer(100),
		AuthResolve:     operatorResolve,
		AuthRequire:     mw.Require,
		RequireOperator: func(next http.Handler) http.Handler { return mw.RequireRole(auth.RoleOperator, next) },
	})
	api := httptest.NewServer(router)
	t.Cleanup(api.Close)

	return &securityRestHarness{securityHarness: sh, api: api, client: &http.Client{Timeout: 10 * time.Second}}
}

// do issues one JSON request against the harness's REST listener and
// decodes a JSON response body into out. Mirrors alarmRestHarness.do.
func (h *securityRestHarness) do(method, path string, body, out any) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal %s %s body: %v", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.api.URL+"/api/v1"+path, rdr)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("read %s %s body: %v", method, path, err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decode %s %s body %q: %v", method, path, raw, err)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return res
}

// classByName finds one class in a snapshot, reporting presence. A class
// the index knows nothing about is omitted by the handler rather than
// published as present-and-inactive (internal/security/aggregate.go
// snapshot), so "not found here" is the expected shape for an
// installation without that hazard.
func classByName(snap hmapi.SecuritySnapshot, class string) (hmapi.SecurityClassState, bool) {
	for _, c := range snap.Classes {
		if c.Class == class {
			return c, true
		}
	}
	return hmapi.SecurityClassState{}, false
}

// waitClassState polls GET /security until class matches the wanted
// presence and (when present) active flag and known count, or the
// timeout elapses. wantKnown < 0 skips the known-count check.
func (h *securityRestHarness) waitClassState(class string, wantPresent, wantActive bool, wantKnown int, timeout time.Duration) (hmapi.SecurityClassState, bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	var lastState hmapi.SecurityClassState
	var lastPresent bool
	for {
		var snap hmapi.SecuritySnapshot
		res := h.do(http.MethodGet, "/security", nil, &snap)
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("GET /security: status %d", res.StatusCode)
		}
		st, present := classByName(snap, class)
		lastState, lastPresent = st, present
		match := present == wantPresent
		if match && wantPresent {
			match = st.Active == wantActive && (wantKnown < 0 || st.Known == wantKnown)
		}
		if match {
			return lastState, lastPresent
		}
		if time.Now().After(deadline) {
			return lastState, lastPresent
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFaultReason polls GET /security/faults for an open fault of the
// given reason.
func (h *securityRestHarness) waitFaultReason(reason string, timeout time.Duration) (hmapi.SecurityFault, bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var faults []hmapi.SecurityFault
		res := h.do(http.MethodGet, "/security/faults", nil, &faults)
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("GET /security/faults: status %d", res.StatusCode)
		}
		for _, f := range faults {
			if f.Reason == reason {
				return f, true
			}
		}
		if time.Now().After(deadline) {
			return hmapi.SecurityFault{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFaultAcknowledged polls GET /security/faults until the fault with
// id carries a non-empty AcknowledgedBy.
func (h *securityRestHarness) waitFaultAcknowledged(id string, timeout time.Duration) (hmapi.SecurityFault, bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var faults []hmapi.SecurityFault
		res := h.do(http.MethodGet, "/security/faults", nil, &faults)
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("GET /security/faults: status %d", res.StatusCode)
		}
		for _, f := range faults {
			if f.ID == id && f.AcknowledgedBy != "" {
				return f, true
			}
		}
		if time.Now().After(deadline) {
			return hmapi.SecurityFault{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFaultGone polls GET /security/faults until no open fault of the
// given reason remains.
func (h *securityRestHarness) waitFaultGone(reason string, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var faults []hmapi.SecurityFault
		res := h.do(http.MethodGet, "/security/faults", nil, &faults)
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("GET /security/faults: status %d", res.StatusCode)
		}
		found := false
		for _, f := range faults {
			if f.Reason == reason {
				found = true
				break
			}
		}
		if !found {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// listSources fetches the whole classified inventory.
func (h *securityRestHarness) listSources() []hmapi.SecuritySourceView {
	h.t.Helper()
	var out []hmapi.SecuritySourceView
	res := h.do(http.MethodGet, "/security/sources", nil, &out)
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /security/sources: status %d", res.StatusCode)
	}
	return out
}

// securityOverrideRefPath builds the PUT /security/sources/{ref} path,
// URL-escaping the pipe-delimited routing key
// (pkg/hmevent.SecurityRefKey / internal/north/rest/handlers/security.go
// PutSecuritySourceOverride).
func securityOverrideRefPath(centralName, interfaceID, channelAddress, parameter string) string {
	ref := strings.Join([]string{centralName, interfaceID, channelAddress, parameter}, "|")
	return "/security/sources/" + url.PathEscape(ref)
}

// TestSecurityDomainBootReportsKnownHazardClasses asserts the boot shape
// of GET /api/v1/security against the fleet actually loaded: exactly the
// hazard classes the devices can source (smoke, water) are known, every
// other defined class is absent — not present-and-inactive — and a class
// with no source 404s from GET /security/classes/{class} too.
func TestSecurityDomainBootReportsKnownHazardClasses(t *testing.T) {
	h := newSecurityRestHarness(t)

	var snap hmapi.SecuritySnapshot
	res := h.do(http.MethodGet, "/security", nil, &snap)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /security: status %d", res.StatusCode)
	}
	if snap.Severity != string(hmenum.SecuritySeverityOK) {
		t.Fatalf("severity = %q, want %q with nothing driven", snap.Severity, hmenum.SecuritySeverityOK)
	}

	want := map[string]bool{"smoke": true, "water": true}
	got := map[string]hmapi.SecurityClassState{}
	for _, c := range snap.Classes {
		got[c.Class] = c
	}
	if len(got) != len(want) {
		names := make([]string, 0, len(got))
		for k := range got {
			names = append(names, k)
		}
		t.Fatalf("classes = %v, want exactly {smoke, water}", names)
	}
	for name := range want {
		st, ok := got[name]
		if !ok {
			t.Fatalf("class %q missing from GET /security, want present with Known>0", name)
		}
		if st.Active {
			t.Fatalf("class %q reports active at boot with nothing driven", name)
		}
		if st.Known == 0 {
			t.Fatalf("class %q present but Known=0 — should have been omitted instead", name)
		}
	}
	for _, absent := range []string{"gas", "co", "intrusion", "panic", "tamper", "technical", "battery"} {
		if _, ok := got[absent]; ok {
			t.Fatalf("class %q present with no source in this fleet; want it absent, not present-and-inactive", absent)
		}
	}

	// A class the installation genuinely has no source for reads back as
	// 404, the same "not here" verdict GetSecurityClass documents.
	res = h.do(http.MethodGet, "/security/classes/gas", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /security/classes/gas: status %d, want 404 (no gas source in this fleet)", res.StatusCode)
	}
}

// TestSecurityWaterSensorReachesAggregateAndClears drives the HmIP-SWD
// water sensor's MOISTURE_DETECTED true on the wire and follows it
// through classification into the aggregate: the water class activates,
// names the right channel and parameter in its Sources, and folds the
// domain severity to alarm; driving it false clears the class again.
func TestSecurityWaterSensorReachesAggregateAndClears(t *testing.T) {
	h := newSecurityRestHarness(t)
	key := h.waterMoistureKey()

	h.injectBool(key, true)
	st, present := h.waitClassState("water", true, true, -1, 2*time.Second)
	if !present || !st.Active {
		t.Fatalf("water class present=%v state=%+v, want active after MOISTURE_DETECTED went true", present, st)
	}
	if len(st.Sources) != 1 {
		t.Fatalf("water sources = %d, want exactly 1 (only MOISTURE_DETECTED was driven): %+v", len(st.Sources), st.Sources)
	}
	if src := st.Sources[0]; src.ChannelAddress != key.ChannelAddress || src.Parameter != string(hmenum.ParameterMoistureDetected) {
		t.Fatalf("source = %+v, want channel %q parameter %q", src, key.ChannelAddress, hmenum.ParameterMoistureDetected)
	}

	var snap hmapi.SecuritySnapshot
	res := h.do(http.MethodGet, "/security", nil, &snap)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /security: status %d", res.StatusCode)
	}
	if snap.Severity != string(hmenum.SecuritySeverityAlarm) {
		t.Fatalf("severity = %q, want %q while water is active", snap.Severity, hmenum.SecuritySeverityAlarm)
	}

	h.injectBool(key, false)
	st, present = h.waitClassState("water", true, false, -1, 2*time.Second)
	if !present || st.Active {
		t.Fatalf("water class did not clear: present=%v state=%+v", present, st)
	}
}

// TestSecuritySourceInventoryReflectsClassificationAndExclusion checks
// GET /security/sources against the classifier tables directly: all
// three WATER_DETECTION_TRANSMITTER parameters list as class water and
// relevant (hazard classes are always relevant, enrolled or not), and
// the smoke detector's actuator-feedback SMOKE_DETECTOR_COMMAND — on
// safety.Excluded because the alarm engine writes it — is absent from
// the inventory entirely, not merely unclassified.
func TestSecuritySourceInventoryReflectsClassificationAndExclusion(t *testing.T) {
	h := newSecurityRestHarness(t)
	waterKey := h.waterMoistureKey()

	sources := h.listSources()

	byParam := map[string]hmapi.SecuritySourceView{}
	for _, s := range sources {
		if s.ChannelAddress == waterKey.ChannelAddress {
			byParam[s.Parameter] = s
		}
	}
	for _, param := range []hmenum.Parameter{
		hmenum.ParameterAlarmState, hmenum.ParameterMoistureDetected, hmenum.ParameterWaterLevelDetected,
	} {
		s, ok := byParam[string(param)]
		if !ok {
			t.Fatalf("water parameter %s missing from /security/sources", param)
		}
		if s.Class != string(hmenum.SecurityClassWater) {
			t.Fatalf("%s class = %q, want water", param, s.Class)
		}
		if !s.Relevant {
			t.Fatalf("%s relevant = false, want true (hazard classes are always relevant)", param)
		}
	}

	if !safety.Excluded(hmenum.ParameterSmokeDetectorCommand) {
		t.Fatal("test assumption stale: SMOKE_DETECTOR_COMMAND is no longer on safety.Excluded")
	}
	for _, s := range sources {
		if s.Parameter == string(hmenum.ParameterSmokeDetectorCommand) {
			t.Fatalf("excluded parameter %s present in /security/sources: %+v", s.Parameter, s)
		}
	}
}

// TestSecuritySourceOverrideCanBeUndone walks the operator-override undo
// path: excluding the MOISTURE_DETECTED source removes it from the water
// class aggregate (Known drops, the class stops reporting active) and
// from the source inventory entirely; restoring it brings Known back and
// a fresh observation of the still-true value re-activates the class.
func TestSecuritySourceOverrideCanBeUndone(t *testing.T) {
	h := newSecurityRestHarness(t)
	key := h.waterMoistureKey()
	refPath := securityOverrideRefPath(h.centralName(), key.InterfaceID, key.ChannelAddress, string(hmenum.ParameterMoistureDetected))

	// Baseline: the sensor is active and one of three known water
	// sources (ALARMSTATE, MOISTURE_DETECTED, WATERLEVEL_DETECTED).
	h.injectBool(key, true)
	st, present := h.waitClassState("water", true, true, 3, 2*time.Second)
	if !present || !st.Active {
		t.Fatalf("baseline: water class present=%v state=%+v, want active with Known=3", present, st)
	}

	// Exclude the source. included=false must remove it from the class
	// aggregate: Known drops to 2 and the class stops being active, even
	// though the last value the daemon observed is still true.
	res := h.do(http.MethodPut, refPath, hmapi.SecuritySourceOverride{Included: boolPtr(false)}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT override included=false: status %d", res.StatusCode)
	}
	st, present = h.waitClassState("water", true, false, 2, 2*time.Second)
	if !present || st.Active || st.Known != 2 {
		t.Fatalf("after exclude: water class present=%v state=%+v, want inactive with Known=2", present, st)
	}

	// The excluded source stays visible in the inventory, marked
	// irrelevant and overridden. That is load-bearing rather than
	// cosmetic: the inventory is the only place an operator can find the
	// row again to undo the exclusion. Dropping it from the index would
	// mean they had to already know the raw routing key.
	var found *hmapi.SecuritySourceView
	for i, s := range h.listSources() {
		if s.ChannelAddress == key.ChannelAddress && s.Parameter == string(hmenum.ParameterMoistureDetected) {
			found = &h.listSources()[i]
			break
		}
	}
	if found == nil {
		t.Fatal("excluded source vanished from /security/sources; the undo path needs it to stay listed")
	}
	if found.Relevant {
		t.Errorf("excluded source still relevant: %+v", *found)
	}
	if !found.Overridden {
		t.Errorf("excluded source not marked overridden: %+v", *found)
	}

	// Undo: empty class + included=true deletes the override row and
	// restores the source to the index.
	res = h.do(http.MethodPut, refPath, hmapi.SecuritySourceOverride{Included: boolPtr(true)}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT override undo: status %d", res.StatusCode)
	}
	st, present = h.waitClassState("water", true, false, 3, 2*time.Second)
	if !present || st.Known != 3 {
		t.Fatalf("after undo: water class present=%v state=%+v, want Known=3 restored", present, st)
	}
	// The undo re-reads the current value from the model rather than
	// waiting for the next wire event. A latching hazard detector may
	// never send another one, so "restored but reported inactive" would
	// be a silent hole exactly where it matters most.
	if !st.Active {
		t.Error("after undo: water class inactive although the observed value is still true")
	}

	// The physical value never changed; re-observing it (the same event
	// a real CCU resends on its own cadence) brings the class back to
	// active — the state RebuildIndex could not restore on its own
	// because the excluded key was absent from the index during the
	// exclusion, and activation only survives a rebuild for keys that
	// stayed present throughout (internal/security/index.go RebuildIndex).
	h.injectBool(key, true)
	st, present = h.waitClassState("water", true, true, 3, 2*time.Second)
	if !present || !st.Active {
		t.Fatalf("after re-observing the still-true value: water class present=%v state=%+v, want active", present, st)
	}
}

// TestSecurityFaultOpensPersistsAndCanBeAcknowledged enrols the water
// device as an alarm sensor (making its MAINTENANCE channel's UNREACH a
// security source per the deviceRelevant gate), drives UNREACH true,
// asserts the resulting fault carries the right class/reason/source and
// a since timestamp, acknowledges it, and asserts it stays open with an
// acknowledged_by — acknowledgement never clears a fault. Driving
// UNREACH false afterward confirms the fault does clear once the
// condition itself resolves.
func TestSecurityFaultOpensPersistsAndCanBeAcknowledged(t *testing.T) {
	h := newSecurityRestHarness(t)
	waterKey := h.waterMoistureKey()
	unreachKey := h.waterUnreachKey()

	h.seedAlarmSensor("sensor-water-fault-test", waterKey, hmenum.AlarmSensorTypeHazard)
	if err := h.svc.RebuildIndex(context.Background()); err != nil {
		t.Fatalf("RebuildIndex after enrollment: %v", err)
	}

	h.injectBool(unreachKey, true)
	f, ok := h.waitFaultReason(string(hmenum.SecurityFaultReasonUnreachable), 2*time.Second)
	if !ok {
		t.Fatal("no unreachable fault opened after driving UNREACH true on an alarm-enrolled device")
	}
	if f.Class != string(hmenum.SecurityClassTechnical) {
		t.Fatalf("fault class = %q, want technical", f.Class)
	}
	if f.Source.ChannelAddress != unreachKey.ChannelAddress || f.Source.Parameter != string(hmenum.ParameterUnreach) {
		t.Fatalf("fault source = %+v, want channel %q parameter %q", f.Source, unreachKey.ChannelAddress, hmenum.ParameterUnreach)
	}
	if f.Since.IsZero() {
		t.Fatal("fault since is zero, want the time the fault opened")
	}
	if f.AcknowledgedBy != "" {
		t.Fatalf("fault already acknowledged before the acknowledge call: %+v", f)
	}

	res := h.do(http.MethodPost, "/security/faults/"+f.ID+"/acknowledge", nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /security/faults/%s/acknowledge: status %d", f.ID, res.StatusCode)
	}

	f2, ok := h.waitFaultAcknowledged(f.ID, 2*time.Second)
	if !ok {
		t.Fatalf("fault %s did not carry an acknowledged_by after acknowledge", f.ID)
	}
	if f2.AcknowledgedBy != "test-operator" {
		t.Fatalf("acknowledged_by = %q, want test-operator", f2.AcknowledgedBy)
	}
	if f2.AcknowledgedAt.IsZero() {
		t.Fatal("acknowledged_at is zero after acknowledge")
	}

	// Still open after acknowledge — the fault is only present in
	// GET /security/faults (ListOpen) while cleared_at_ms is zero, and
	// waitFaultAcknowledged only just found it there.
	h.injectBool(unreachKey, false)
	if !h.waitFaultGone(string(hmenum.SecurityFaultReasonUnreachable), 2*time.Second) {
		t.Fatal("unreachable fault did not clear after UNREACH went false")
	}
}

// TestSecurityDomainReportsHazardsWithoutAlarmEngine pins the domain's
// stated central requirement (internal/security/service.go package doc:
// "it deliberately runs independently of the alarm engine"): with no
// alarm.Service ever wired in (Deps.AlarmBus nil, as every harness in
// this file builds it), the hazard classes still work and the zone half
// is simply empty rather than broken or absent.
func TestSecurityDomainReportsHazardsWithoutAlarmEngine(t *testing.T) {
	h := newSecurityRestHarness(t)
	key := h.waterMoistureKey()

	h.injectBool(key, true)
	st, present := h.waitClassState("water", true, true, -1, 2*time.Second)
	if !present || !st.Active {
		t.Fatalf("water class present=%v state=%+v, want active even with no alarm engine wired", present, st)
	}

	var snap hmapi.SecuritySnapshot
	res := h.do(http.MethodGet, "/security", nil, &snap)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /security: status %d", res.StatusCode)
	}
	if len(snap.Zones) != 0 {
		t.Fatalf("zones = %+v, want empty with no alarm engine wired", snap.Zones)
	}
}

// boolPtr addresses a literal for the override body, whose Included field
// is a pointer so an omitted value stays distinguishable from an explicit
// false.
func boolPtr(v bool) *bool { return &v }
