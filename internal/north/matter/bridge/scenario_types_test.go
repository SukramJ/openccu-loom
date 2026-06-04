// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// scenarioActor enumerates the three participants the harness drives.
type scenarioActor string

const (
	actorCCU    scenarioActor = "ccu"
	actorPeer   scenarioActor = "peer"
	actorBridge scenarioActor = "bridge"
)

// scenarioKind tags a step's action.
type scenarioKind string

const (
	kindFireAttributeChange   scenarioKind = "fire_attribute_change"
	kindFireViaEngine         scenarioKind = "fire_via_engine"
	kindFireNotifierSource    scenarioKind = "fire_notifier_source"
	kindExpectTX              scenarioKind = "expect_tx"
	kindExpectNoTX            scenarioKind = "expect_no_tx"
	kindSendStatusResponse    scenarioKind = "send_status_response"
	kindExpectLog             scenarioKind = "expect_log"
	kindCloseSession          scenarioKind = "close_session"
	kindDropNextTX            scenarioKind = "drop_next_tx"
	kindTickRetransmit        scenarioKind = "tick_retransmit"
	kindWait                  scenarioKind = "wait"
	kindSendWriteRequest      scenarioKind = "send_write_request"
	kindSendSubscribeRequest  scenarioKind = "send_subscribe_request"
	kindSendReadRequest       scenarioKind = "send_read_request"
	kindAssertGT              scenarioKind = "assert_gt"
	kindDrainSubscribeChunks  scenarioKind = "drain_subscribe_chunks"
	kindEngineTickAt          scenarioKind = "engine_tick_at"
	kindSendInvokeMoveToLevel scenarioKind = "send_invoke_move_to_level"
)

// scenario is the decoded form of one JSON scenario file under
// docs/parity/matter/scenarios/. The schema is documented in
// docs/parity/matter/scenarios/README.md.
type scenario struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags,omitempty"`
	Given       scenarioGiven  `json:"given"`
	Steps       []scenarioStep `json:"steps"`
}

// scenarioGiven captures the pre-conditions a step list assumes.
// Single-subscription scenarios populate session_id +
// peer_subscribe_exchange_id + subscription. Multi-subscription
// scenarios use the subscriptions array instead; the loader expands
// either shape to a uniform internal slice the harness iterates over.
type scenarioGiven struct {
	SessionID               uint16            `json:"session_id,omitempty"`
	PeerSubscribeExchangeID uint16            `json:"peer_subscribe_exchange_id,omitempty"`
	Subscription            scenarioAttrPath  `json:"subscription,omitzero"`
	Subscriptions           []scenarioSubSpec `json:"subscriptions,omitempty"`

	// Topology names a predefined recipe under
	// scenario_topologies_test.go that registers a fake snapshotter
	// with measurement sources / cluster servers. Default (empty)
	// uses wbEmptySnapshotter — the empty topology Phase A-G relies
	// on. Recipes are needed by scenarios that exercise the
	// notifier-wiring path (F2) or read real cluster-server output
	// (DataVersion, FabricFiltered).
	Topology string `json:"topology,omitempty"`

	// FireSourceKey is the DataPointKey of the fake source whose
	// notifier fire_notifier_source should trigger. Empty for
	// topology recipes that expose exactly one source.
	FireSourceKey string `json:"fire_source_key,omitempty"`
}

// scenarioSubSpec is one (session, exchange, paths) triple. Each
// entry gets its own CASE session pair in the harness so
// multi-session isolation scenarios are wire-honest. Two shapes for
// the path set are supported: a single subscription object
// (legacy) or paths as an array — F2 / multi-path scenarios use
// the array.
type scenarioSubSpec struct {
	SessionID               uint16             `json:"session_id"`
	PeerSubscribeExchangeID uint16             `json:"peer_subscribe_exchange_id"`
	Subscription            scenarioAttrPath   `json:"subscription,omitzero"`
	Paths                   []scenarioAttrPath `json:"paths,omitempty"`

	// SkipAutoSubscribe defaults to false so existing scenarios get
	// the Phase A-H behavior (harness pre-registers a subscription
	// in the manager and plants a subTarget). Phase J's
	// peer.send_subscribe_request needs the harness to leave the
	// subscription manager empty so the peer's wire-driven
	// SubscribeRequest registers the subscription via the
	// production handleSubscribeRequest path.
	SkipAutoSubscribe bool `json:"skip_auto_subscribe,omitempty"`

	// MinIntervalFloorSeconds + MaxIntervalCeilingSeconds override
	// the Phase A-H default (1s / 60s). Used by Phase K cadence
	// scenarios to test the MinInterval gate against a longer floor
	// without slowing the rest of the suite. Zero means "use the
	// Phase A-H default".
	MinIntervalFloorSeconds   uint16 `json:"min_interval_floor_seconds,omitempty"`
	MaxIntervalCeilingSeconds uint16 `json:"max_interval_ceiling_seconds,omitempty"`

	// EngineManualOnly skips subMgr.Start so the engine goroutine
	// does NOT tick on its own. Phase-V cadence scenarios then
	// drive the engine via engine_tick_at with controlled wall-
	// clock offsets, making the MinInterval-gate and
	// MaxInterval-keepalive paths deterministic without
	// wall-clock waits. NOT compatible with fire_via_engine —
	// those steps rely on the engine goroutine to drain dirty
	// marks.
	EngineManualOnly bool `json:"engine_manual_only,omitempty"`

	// FabricIndex maps the per-subscription session to a fabric so
	// the bridge's resolveSessionFabric → SessionFabricResolver
	// chain stamps a non-zero FabricIndex into the dispatch context
	// (im.WithFabricFilter). Defaults to 0 (pre-fabric / PASE), which
	// is what the production daemon resolves for an unresolved
	// session. Phase-S fabric-scoped scenarios set this so
	// FabricScopedReader cluster servers see a meaningful fabric ID.
	FabricIndex uint8 `json:"fabric_index,omitempty"`
}

// effectivePaths returns Paths when populated; otherwise wraps
// Subscription as a one-entry slice. Callers should use this rather
// than reading the raw fields.
func (s scenarioSubSpec) effectivePaths() []scenarioAttrPath {
	if len(s.Paths) > 0 {
		return s.Paths
	}
	return []scenarioAttrPath{s.Subscription}
}

// scenarioAttrPath is a serialisable form of im.ConcreteAttributePath.
type scenarioAttrPath struct {
	Endpoint  uint16 `json:"endpoint"`
	Cluster   uint32 `json:"cluster"`
	Attribute uint32 `json:"attribute"`
}

// scenarioStep is the JSON-level tagged union. Exactly one of the
// step-payload fields is populated; the harness selects by `Kind`.
type scenarioStep struct {
	Actor scenarioActor `json:"actor"`
	Kind  scenarioKind  `json:"kind"`

	// Payload fields — each is non-zero only for its matching Kind.
	Value any `json:"value,omitempty"`

	Opcode                 string `json:"opcode,omitempty"`
	Initiator              *bool  `json:"initiator,omitempty"`
	ExchangeIDFresh        bool   `json:"exchange_id_fresh,omitempty"`
	ExchangeIDNeqSubscribe bool   `json:"exchange_id_neq_subscribe,omitempty"`
	BindExchangeIDTo       string `json:"bind_exchange_id_to,omitempty"`
	BindCounterTo          string `json:"bind_counter_to,omitempty"`

	Exchange string `json:"exchange,omitempty"`
	Status   string `json:"status,omitempty"`

	// SubscriptionIdx selects which entry in
	// scenarioGiven.Subscriptions / the legacy single-subscription
	// slot the step targets. Default 0 (the primary subscription).
	SubscriptionIdx int `json:"subscription_idx,omitempty"`

	Msg           string `json:"msg,omitempty"`
	MatchExchange string `json:"match_exchange,omitempty"`

	// TLV body assertions: context-tag presence/absence at the
	// top-level of the decoded IM payload. Each entry is a uint8 tag
	// number (e.g. 4 for tagReportSuppressResponse).
	TLVTagsPresent []uint8 `json:"tlv_tags_present,omitempty"`
	TLVTagsAbsent  []uint8 `json:"tlv_tags_absent,omitempty"`

	// AttributeReportsCount asserts the length of the
	// attributeReports array inside the decoded IM ReportData. Used
	// by F2 narrowing scenarios that verify only the notifier's own
	// cluster paths produce reports. nil → no assertion.
	AttributeReportsCount *int `json:"attribute_reports_count,omitempty"`

	// Fault-injection fields (Phase D).
	// TimeoutMillis bounds expect_no_tx's quiet-window observation
	// and wait's pause duration. Default depends on the kind:
	// expect_no_tx → 500, wait → 500.
	TimeoutMillis int `json:"timeout_millis,omitempty"`

	// Apple-write fields (Phase I). Path defaults to the active
	// subscription's primary path when empty. PeerExchangeID is the
	// exchange the peer opens for the WriteRequest (the bridge's
	// WriteResponse echoes it back). Defaults to a deterministic
	// per-scenario value when zero.
	WritePath      *scenarioAttrPath `json:"write_path,omitempty"`
	PeerExchangeID uint16            `json:"peer_exchange_id,omitempty"`

	// FabricFiltered toggles the ReadRequestMessage's
	// FabricFiltered flag (Matter §10.6.3). Used by Phase L
	// fabric-scoped read scenarios. Defaults to false.
	FabricFiltered bool `json:"fabric_filtered,omitempty"`

	// Wildcard requests the harness to override the active
	// subscription's paths with a single all-wildcard
	// ConcreteAttributePath (HasEndpoint=HasCluster=HasAttribute=false).
	// Used by send_subscribe_request / send_read_request to drive
	// the dispatcher's wildcard expansion path. Default false.
	Wildcard bool `json:"wildcard,omitempty"`

	// MinChunks asserts drain_subscribe_chunks pulled at least this
	// many ReportData chunks before the SubscribeResponse. Used by
	// guaranteed-multi-chunk scenarios.
	MinChunks int `json:"min_chunks,omitempty"`

	// DataVersion-monotonicity (Phase M): bind the DataVersion
	// uint32 from the first AttributeDataIB into a $var, then
	// assert lhs > rhs across two reports via kindAssertGT.
	BindDataVersionTo string           `json:"bind_data_version_to,omitempty"`
	GT                *scenarioCompare `json:"gt,omitempty"`

	// BindAttributeValueTo (Phase S) extracts the first
	// AttributeDataIB's Data field as a uint64 and binds it. Used
	// by fabric-scoped read scenarios to verify the value the bridge
	// encoded matches the FabricIndex injected via the session
	// resolver. ExpectAttributeValue asserts equality with a literal.
	BindAttributeValueTo string  `json:"bind_attribute_value_to,omitempty"`
	ExpectAttributeValue *uint64 `json:"expect_attribute_value,omitempty"`

	// BindMaxIntervalTo (Phase T) extracts MaxInterval (context tag
	// 2 uint16) from a SubscribeResponseMessage payload and binds it.
	// ExpectMaxInterval asserts equality with a literal value.
	BindMaxIntervalTo string  `json:"bind_max_interval_to,omitempty"`
	ExpectMaxInterval *uint16 `json:"expect_max_interval,omitempty"`

	// AtMillis (Phase V) is the offset in milliseconds from the
	// harness's setup-time anchor t0. engine_tick_at calls
	// subMgr.Tick(ctx, t0.Add(at_millis * 1ms)) for deterministic
	// cadence assertions without wall-clock waits.
	AtMillis int `json:"at_millis,omitempty"`

	// ExpectInvokeStatus asserts the InvokeResponse's per-command
	// status code (Matter §10.6.7). 0 = Success. Catches "the bridge
	// accepted my invoke shape but returned a non-success status".
	ExpectInvokeStatus *uint8 `json:"expect_invoke_status,omitempty"`
}

// scenarioCompare references two scenario-bound variables for a
// cross-step assertion. assert_gt verifies bindings[LHS] > bindings[RHS].
type scenarioCompare struct {
	LHS string `json:"lhs"`
	RHS string `json:"rhs"`
}

// loadScenarioFile parses a JSON scenario from disk and rejects
// structurally invalid documents up front so the harness can rely on
// well-formed steps without per-field defensive checks.
func loadScenarioFile(path string) (*scenario, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // tests/contract paths under repo root.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s scenario
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := validateScenario(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	normalizeGiven(&s.Given)
	return &s, nil
}

// normalizeGiven collapses the two shapes (single subscription via
// the top-level fields vs. array via subscriptions) into the array
// form so the harness iterates uniformly. When both shapes are
// present the explicit array wins; the singleton fields are treated
// as a legacy fallback.
func normalizeGiven(g *scenarioGiven) {
	if len(g.Subscriptions) == 0 && g.SessionID != 0 {
		g.Subscriptions = []scenarioSubSpec{{
			SessionID:               g.SessionID,
			PeerSubscribeExchangeID: g.PeerSubscribeExchangeID,
			Subscription:            g.Subscription,
		}}
	}
}

// validateScenario rejects scenarios that the harness would otherwise
// crash on. Catches authoring mistakes (missing kind, unknown actor,
// dangling binding reference) at load time rather than at assertion
// time, which keeps failure attribution honest.
func validateScenario(s *scenario) error {
	if s.Name == "" {
		return errors.New("scenario.name is empty")
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("scenario %q has no steps", s.Name)
	}
	known := map[string]bool{}
	for i := range s.Steps {
		st := &s.Steps[i]
		switch st.Actor {
		case actorCCU, actorPeer, actorBridge:
		default:
			return fmt.Errorf("step[%d]: unknown actor %q", i, st.Actor)
		}
		switch st.Kind {
		case kindFireAttributeChange, kindFireViaEngine, kindFireNotifierSource,
			kindExpectTX, kindExpectNoTX, kindSendStatusResponse, kindExpectLog,
			kindCloseSession, kindDropNextTX, kindTickRetransmit, kindWait,
			kindSendWriteRequest, kindSendSubscribeRequest, kindSendReadRequest,
			kindAssertGT, kindDrainSubscribeChunks, kindEngineTickAt,
			kindSendInvokeMoveToLevel:
		default:
			return fmt.Errorf("step[%d]: unknown kind %q", i, st.Kind)
		}
		if st.BindExchangeIDTo != "" {
			known[st.BindExchangeIDTo] = true
		}
		if st.BindCounterTo != "" {
			known[st.BindCounterTo] = true
		}
		if ref := st.Exchange; ref != "" && ref[0] == '$' && !known[ref] {
			return fmt.Errorf("step[%d]: references unbound variable %q", i, ref)
		}
		if ref := st.MatchExchange; ref != "" && ref[0] == '$' && !known[ref] {
			return fmt.Errorf("step[%d]: references unbound variable %q", i, ref)
		}
	}
	return nil
}
