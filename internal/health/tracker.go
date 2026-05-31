// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package health is the daemon-wide health tracker.
//
// Components register a label and report healthy/unhealthy samples;
// the tracker aggregates into per-component status plus a composite
// overall status. REST, metrics, and the UI read through [Tracker.
// Snapshot] to render the "system is healthy" indicators.
//
// The tracker keeps a per-component sample history so dashboards can
// render a sparkline and so [WindowedScore] computes "healthy fraction
// over the last N seconds". Samples older than [Tracker.StaleAfter]
// downgrade the component to [StatusUnknown] — an interface that has
// gone silent should not look healthy just because its last reading
// was OK.
package health

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// Status is the coarse-grained health verdict.
type Status string

// Status values. "unknown" covers components we've never heard from yet.
const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
	StatusUnknown   Status = "unknown"
)

// Sample is one recorded data point for a component.
type Sample struct {
	Healthy   bool
	Note      string
	Timestamp time.Time
}

// Component holds the latest state for one registered component.
type Component struct {
	Name       string
	Status     Status
	LastSample Sample
}

// DefaultHistorySize caps the per-component sample ring buffer. 200
// samples covers ~10 min of sparkline data at a 3 s connection-check
// cadence and leaves headroom for windowed-score queries that look
// back further. Older deployments that want the tighter 30-sample
// ring can opt back via [WithHistorySize].
const DefaultHistorySize = 200

// DefaultEventFreshness is the cut-off used by [Tracker.CanReceiveEvents]
// when the caller does not specify one. Five minutes — longer than
// the typical 10 s connection probe so a single CCU hiccup does not
// flip the verdict.
const DefaultEventFreshness = 5 * time.Minute

// DefaultStaleAfter is the cut-off after which the latest sample is
// considered stale and the component decays to [StatusUnknown]. Picks a value
// comfortably larger than the connection-check cadence (default 10 s in
// `internal/central/jobs.go`) so a single missed poll does not flip the
// status.
const DefaultStaleAfter = 90 * time.Second

// Tracker aggregates component health. Safe for concurrent use.
type Tracker struct {
	mu                sync.RWMutex
	components        map[string]Component
	history           map[string][]Sample
	reconnectAttempts map[string]int // per-component reconnect counter
	clients           map[string]*ClientHealth
	primaryInterface  string
	clk               clock.Clock
	historySize       int
	staleAfter        time.Duration

	// gaugesMu guards gauges. Separate mutex so gauge registration does
	// not contend with the high-frequency Record/Snapshot paths.
	gaugesMu sync.RWMutex
	gauges   map[string]GaugeFunc

	// onChangeMu guards onChangeFn.
	onChangeMu sync.RWMutex
	// onChangeFn is an optional hook called after every Record when the
	// overall status changes. Wired at boot by the central to drive
	// EvaluateCentralState.
	onChangeFn func(overall Status)
}

// GaugeFunc is a producer that returns a current numeric reading on
// demand. Surfaces an internal counter (e.g. event-bus deferred
// high-water, throttle queue depth) into the health snapshot without
// requiring a periodic pusher goroutine.
type GaugeFunc func() float64

// Option configures a [Tracker] at construction time.
type Option func(*Tracker)

// WithClock injects a [clock.Clock] — primarily for tests.
func WithClock(c clock.Clock) Option {
	return func(t *Tracker) {
		if c == nil {
			c = clock.New()
		}
		t.clk = c
	}
}

// WithHistorySize overrides the per-component ring buffer size. Pass
// a positive value; non-positive arguments fall back to
// [DefaultHistorySize].
//
// loom:reachable:reason="functional option for NewTracker; consumed in tests and by future production callers that need non-default history depth"
func WithHistorySize(n int) Option {
	return func(t *Tracker) {
		if n > 0 {
			t.historySize = n
		}
	}
}

// WithStaleAfter overrides the duration after which the latest sample
// downgrades the component to [StatusUnknown]. Zero or negative
// disables the decay (every component sticks at its last reported
// status forever).
//
// loom:reachable:reason="functional option for NewTracker; production callers that need custom stale windows will consume this"
func WithStaleAfter(d time.Duration) Option {
	return func(t *Tracker) { t.staleAfter = d }
}

// NewTracker returns an empty tracker.
func NewTracker(opts ...Option) *Tracker {
	t := &Tracker{
		components:        make(map[string]Component),
		history:           make(map[string][]Sample),
		reconnectAttempts: make(map[string]int),
		clients:           make(map[string]*ClientHealth),
		gauges:            make(map[string]GaugeFunc),
		clk:               clock.New(),
		historySize:       DefaultHistorySize,
		staleAfter:        DefaultStaleAfter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t
}

// OnClientStateChange registers a callback that fires whenever the
// overall health status changes after a [Record] call. At most one
// callback is active at a time — a second call replaces the previous
// registration. Pass nil to remove an existing callback.
//
// The callback is invoked outside the tracker's internal write lock
// so it may safely call read-only Tracker methods (Overall, Snapshot,
// etc.) but must not call Record or Unregister (deadlock risk).
func (t *Tracker) OnClientStateChange(fn func(overall Status)) {
	t.onChangeMu.Lock()
	t.onChangeFn = fn
	t.onChangeMu.Unlock()
}

// clientLocked returns the [ClientHealth] for name, creating one on
// first reference so callers never need to register clients
// explicitly. Caller must hold mu.
func (t *Tracker) clientLocked(name string) *ClientHealth {
	c, ok := t.clients[name]
	if !ok {
		c = &ClientHealth{}
		t.clients[name] = c
	}
	return c
}

// Record stores a sample for the named component. The component is
// registered implicitly on first Record — no separate Register call.
//
// The sample is appended to the per-component history ring (capped at
// the tracker's [WithHistorySize]); older samples are evicted FIFO.
func (t *Tracker) Record(name string, sample Sample) {
	if sample.Timestamp.IsZero() {
		sample.Timestamp = t.clk.Now()
	}
	t.mu.Lock()
	prev, known := t.components[name]
	status := statusFromSample(sample)
	// Flap-damp: a single unhealthy sample after a healthy run yields
	// DEGRADED; two consecutive unhealthy samples escalate to UNHEALTHY.
	if known && !sample.Healthy && prev.Status == StatusHealthy {
		status = StatusDegraded
	}
	t.components[name] = Component{Name: name, Status: status, LastSample: sample}

	hist := t.history[name]
	hist = append(hist, sample)
	if len(hist) > t.historySize {
		hist = hist[len(hist)-t.historySize:]
	}
	t.history[name] = hist
	t.mu.Unlock()

	// Notify the change hook outside the write lock so the callback may
	// safely call read-only Tracker methods without deadlocking.
	t.onChangeMu.RLock()
	fn := t.onChangeFn
	t.onChangeMu.RUnlock()
	if fn != nil {
		fn(t.Overall())
	}
}

func statusFromSample(s Sample) Status {
	if s.Healthy {
		return StatusHealthy
	}
	return StatusUnhealthy
}

// Get returns the recorded component and reports whether it exists.
// Stale samples (older than [WithStaleAfter]) decay the component's
// status to [StatusUnknown]; the underlying [LastSample] is returned
// unchanged so callers can render "last seen X minutes ago".
func (t *Tracker) Get(name string) (Component, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.components[name]
	if !ok {
		return c, false
	}
	return t.applyStaleLocked(c), true
}

// Snapshot returns every component sorted alphabetically. Stale
// components are reported as [StatusUnknown].
func (t *Tracker) Snapshot() []Component {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Component, 0, len(t.components))
	for _, c := range t.components {
		out = append(out, t.applyStaleLocked(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// applyStaleLocked returns c with its status downgraded to
// [StatusUnknown] when the sample timestamp is older than
// [Tracker.staleAfter]. Caller must hold mu.
func (t *Tracker) applyStaleLocked(c Component) Component {
	if t.staleAfter <= 0 {
		return c
	}
	if c.LastSample.Timestamp.IsZero() {
		return c
	}
	if t.clk.Now().Sub(c.LastSample.Timestamp) > t.staleAfter {
		c.Status = StatusUnknown
	}
	return c
}

// Overall returns the worst observed status across all components.
// Returns [StatusUnknown] when no component has been recorded yet.
//
// Stale components count as [StatusUnknown] for the worst-of
// computation: a fleet with one healthy and one stale component
// Degrades to "unknown" overall
// heartbeat → not healthy" semantics.
func (t *Tracker) Overall() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.components) == 0 {
		return StatusUnknown
	}
	worst := StatusHealthy
	hasUnknown := false
	for _, raw := range t.components {
		c := t.applyStaleLocked(raw)
		if c.Status == StatusUnhealthy {
			return StatusUnhealthy
		}
		if c.Status == StatusDegraded {
			worst = StatusDegraded
		}
		if c.Status == StatusUnknown {
			hasUnknown = true
		}
	}
	if worst == StatusHealthy && hasUnknown {
		return StatusUnknown
	}
	return worst
}

// ServiceAvailability collapses a component snapshot into the status that
// reflects whether the daemon can still SERVE — the basis for the /health
// endpoint's HTTP code. It is deliberately more lenient than [Tracker.Overall]
// (which reports the raw worst case): a single south-bound interface down on a
// multi-CCU daemon, or the MQTT bridge dropping, only DEGRADES service — the
// REST/UI surface and the other interfaces keep working. Only a fatal
// dependency (persistence) or a total south-bound outage (every interface
// down) makes the service genuinely unavailable.
//
//   - any critical component unhealthy            → Unhealthy
//   - every interface component unhealthy         → Unhealthy
//   - otherwise any unhealthy/degraded component  → Degraded
//   - all healthy (or only unknown)               → Healthy / Unknown
func ServiceAvailability(components []Component) Status {
	if len(components) == 0 {
		return StatusUnknown
	}
	worst := StatusHealthy
	hasUnknown := false
	ifaceTotal, ifaceUnhealthy := 0, 0
	for _, c := range components {
		iface := isInterfaceComponent(c.Name)
		if iface {
			ifaceTotal++
		}
		switch c.Status {
		case StatusUnhealthy:
			if isCriticalComponent(c.Name) {
				return StatusUnhealthy
			}
			if iface {
				ifaceUnhealthy++
			}
			worst = StatusDegraded
		case StatusDegraded:
			if worst == StatusHealthy {
				worst = StatusDegraded
			}
		case StatusUnknown:
			hasUnknown = true
		case StatusHealthy:
		}
	}
	if ifaceTotal > 0 && ifaceUnhealthy == ifaceTotal {
		return StatusUnhealthy
	}
	if worst == StatusHealthy && hasUnknown {
		return StatusUnknown
	}
	return worst
}

// isCriticalComponent reports whether a component's failure means the daemon
// cannot serve at all (mapped to HTTP 503), regardless of south-bound state:
// the persistence layer (sqlite) and the central coordinator heartbeat. A
// single interface or the MQTT bridge dropping only degrades service.
func isCriticalComponent(name string) bool {
	return name == "sqlite" || name == "central"
}

// isInterfaceComponent reports whether name identifies a per-central south-bound
// interface health entry (e.g. "OttoGo-HmIP-RF", "KearneyGo-CUxD") rather than
// an infrastructure entry. Interface names carry a "<central>-<interface>"
// shape; the known infra entries are single tokens.
func isInterfaceComponent(name string) bool {
	switch name {
	case "central", "mqtt", "sqlite":
		return false
	}
	return strings.Contains(name, "-")
}

// Score returns a numeric health summary in [0.0, 1.0] derived from the
// per-component status mix. 1.0 means every component is healthy; 0.0 means
// every component is unhealthy. DEGRADED counts as 0.5 and UNKNOWN counts as
// 0 so newly-registered-but-silent components do not get a free pass.
//
// Empty trackers return 0 — the caller must inspect [Overall] to distinguish
// "unknown" from "unhealthy".
func (t *Tracker) Score() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.components) == 0 {
		return 0
	}
	total := 0.0
	for _, raw := range t.components {
		c := t.applyStaleLocked(raw)
		switch c.Status {
		case StatusHealthy:
			total += 1.0
		case StatusDegraded:
			total += 0.5
		case StatusUnhealthy, StatusUnknown:
			// adds 0
		}
	}
	return total / float64(len(t.components))
}

// ScoreInt returns the aggregate health as an integer in [0, 100]. It is a
// convenience wrapper around [Score] for callers that prefer an integer gauge
// (metrics, REST response fields). The mapping is `round(Score() * 100)` —
// HEALTHY=100, all-DEGRADED=50, all-UNHEALTHY/UNKNOWN=0.
func (t *Tracker) ScoreInt() int {
	return int(t.Score() * 100)
}

// CentralScore returns the aggregate score restricted to components
// whose name carries `central` as a substring. Use this in multi-CCU
// setups to render a per-CCU tile in the UI without leaking the
// other CCU's health into the verdict. Returns 0 when no component
// matches.
//
// Matching rule: case-sensitive substring. Wiring already prefixes
// interface ids with the central name (e.g. `ccu-main-HmIP-RF`,
// `hub.ccu-main`), so the substring match catches both the
// transport-level entries and the per-bridge / per-store entries
// that carry the same prefix.
func (t *Tracker) CentralScore(central string) float64 {
	if central == "" {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	total := 0.0
	count := 0
	for name, raw := range t.components {
		if !strings.Contains(name, central) {
			continue
		}
		c := t.applyStaleLocked(raw)
		count++
		switch c.Status {
		case StatusHealthy:
			total += 1.0
		case StatusDegraded:
			total += 0.5
		case StatusUnhealthy, StatusUnknown:
			// adds 0
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

// CentralScoreInt is the integer convenience wrapper for
// [CentralScore], mirroring [ScoreInt] for the global aggregate.
func (t *Tracker) CentralScoreInt(central string) int {
	return int(t.CentralScore(central) * 100)
}

// OverallStatus returns the worst-case status across all components.
// It is a named alias for [Overall].
func (t *Tracker) OverallStatus() Status {
	return t.Overall()
}

// IsAvailable reports whether every registered component is reporting
// [StatusHealthy] — the simple "everything green" boolean verdict.
func (t *Tracker) IsAvailable() bool {
	return t.Overall() == StatusHealthy
}

// IsDegraded reports whether the overall status is [StatusDegraded]
// (at least one component degraded, none unhealthy).
func (t *Tracker) IsDegraded() bool {
	return t.Overall() == StatusDegraded
}

// IsFailed reports whether the overall status is [StatusUnhealthy]
// (at least one component reporting hard failure).
func (t *Tracker) IsFailed() bool {
	return t.Overall() == StatusUnhealthy
}

// CanReceiveEvents reports whether the named component has produced a
// `event-received` sample within freshness. A freshness <= 0 falls
// back to [DefaultEventFreshness]. Unknown components return false —
// "we don't know" is treated as "no" so the diagnostics surface does
// not mislead with optimistic defaults.
//
// Use this to gate UI affordances that only make sense while the CCU
// is actually pushing events (e.g. live-status overlays, optimistic
// state hints).
func (t *Tracker) CanReceiveEvents(name string, freshness time.Duration) bool {
	if freshness <= 0 {
		freshness = DefaultEventFreshness
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	src := t.history[name]
	if len(src) == 0 {
		return false
	}
	cutoff := t.clk.Now().Add(-freshness)
	for i := len(src) - 1; i >= 0; i-- {
		s := src[i]
		if s.Timestamp.Before(cutoff) {
			return false
		}
		if s.Note == "event-received" && s.Healthy {
			return true
		}
	}
	return false
}

// DegradedComponents returns the names of components whose latest status is
// DEGRADED, sorted alphabetically.
func (t *Tracker) DegradedComponents() []string {
	return t.componentsByStatus(StatusDegraded)
}

// UnhealthyComponents returns the names of components whose latest status is
// UNHEALTHY, sorted alphabetically.
func (t *Tracker) UnhealthyComponents() []string {
	return t.componentsByStatus(StatusUnhealthy)
}

func (t *Tracker) componentsByStatus(want Status) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.components))
	for name, raw := range t.components {
		c := t.applyStaleLocked(raw)
		if c.Status == want {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// History returns up to limit most-recent samples for the named
// component, oldest-first. limit <= 0 returns the full ring buffer.
// Unknown components return nil.
func (t *Tracker) History(name string, limit int) []Sample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	src, ok := t.history[name]
	if !ok || len(src) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(src) {
		limit = len(src)
	}
	out := make([]Sample, limit)
	copy(out, src[len(src)-limit:])
	return out
}

// WindowedScore returns the healthy fraction (0..1) of samples whose
// timestamp falls within window from now for the named component.
// Returns 0 when no sample is in window — callers that need to
// distinguish "no data" from "all unhealthy" should check
// [Tracker.History] separately.
//
// The window is a sliding view computed against the injected clock,
// so [WithClock] gives tests a deterministic anchor.
func (t *Tracker) WindowedScore(name string, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	src := t.history[name]
	if len(src) == 0 {
		return 0
	}
	cutoff := t.clk.Now().Add(-window)
	healthy, total := 0, 0
	for _, s := range src {
		if s.Timestamp.Before(cutoff) {
			continue
		}
		total++
		if s.Healthy {
			healthy++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(healthy) / float64(total)
}

// MetricsHealthSummaryView is the immutable view the metrics provider
// returns. Field shape mirrors `metrics.HealthSummary` exactly so the
// metrics-wiring adapter is a one-to-one copy. Defined here (instead
// of importing internal/metrics directly) to keep `internal/health`
// at the bottom of the dependency tree.
type MetricsHealthSummaryView struct {
	OverallScore      float64
	ClientsHealthy    int
	ClientsDegraded   int
	ClientsFailed     int
	ReconnectAttempts int
}

// MetricsHealthSummary returns a snapshot of the tracker counts the
// metrics aggregator surfaces in [HealthMetrics]. The returned view
// counts every registered component — there is no per-client
// distinction in the tracker beyond "healthy / degraded / unhealthy".
//
// ReconnectAttempts is left at 0 here: reconnect bookkeeping lives in
// the recovery coordinator, not the health tracker. The metrics
// wiring layer combines both providers into the final
// [HealthMetrics] snapshot.
//
// Multi-CCU safe: each [CentralUnit] owns its own [Tracker]; the
// returned counts only reflect components recorded against that
// tracker.
func (t *Tracker) MetricsHealthSummary() MetricsHealthSummaryView {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.components) == 0 {
		return MetricsHealthSummaryView{OverallScore: 1.0}
	}

	view := MetricsHealthSummaryView{}
	total := 0.0
	for _, raw := range t.components {
		c := t.applyStaleLocked(raw)
		switch c.Status {
		case StatusHealthy:
			view.ClientsHealthy++
			total += 1.0
		case StatusDegraded:
			view.ClientsDegraded++
			total += 0.5
		case StatusUnhealthy, StatusUnknown:
			view.ClientsFailed++
		}
	}
	view.OverallScore = total / float64(len(t.components))
	return view
}

// RecordEventReceived registers a monotonic timestamp for the named
// component (typically an interfaceID). It is used by the health-wiring
// adapter to track when the last push-event arrived from a client, so
// the health UI can display "last event received N s ago". The timestamp
// is stored as the Note field of a healthy sample.
func (t *Tracker) RecordEventReceived(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clk.Now()
	sample := Sample{Healthy: true, Note: "event-received", Timestamp: now}
	// We only update the last-event note without flipping the status — the
	// component may be degraded or unhealthy for other reasons and we do
	// not want a received-event to reset that. Store as a separate sample so
	// the history ring captures event-receive density.
	prev, known := t.components[name]
	status := statusFromSample(sample)
	if known && !sample.Healthy && prev.Status == StatusHealthy {
		status = StatusDegraded
	}
	if known {
		// Preserve existing non-healthy status — event receipt alone does not
		// make a broken client healthy.
		if prev.Status == StatusUnhealthy || prev.Status == StatusDegraded {
			status = prev.Status
		}
	}
	t.components[name] = Component{Name: name, Status: status, LastSample: sample}
	hist := t.history[name]
	hist = append(hist, sample)
	if len(hist) > t.historySize {
		hist = hist[len(hist)-t.historySize:]
	}
	t.history[name] = hist
}

// RecordReconnectAttempt increments the reconnect-attempt counter for
// the named component (typically an interfaceID). The counter is
// surfaced by the health REST endpoint and the SPA sparkline.
//
// The tracker maintains a per-component counter (separate from the
// [Connection] reconnect counter) so the aggregate [MetricsHealthSummary]
// view can include reconnect activity without knowing about the
// [ConnectionRegistry].
func (t *Tracker) RecordReconnectAttempt(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reconnectAttempts[name]++
	c := t.clientLocked(name)
	c.ReconnectAttempts = t.reconnectAttempts[name]
}

// RecordRequest pumps one RPC outcome into the per-interface
// [ClientHealth]. success=true records `LastSuccessfulRequest` and
// resets `ConsecutiveFailures`; success=false records
// `LastFailedRequest` and increments the failure counter.
//
// The method does NOT change the component's [Status] — that stays
// driven by the higher-level [Record] / state-machine path. Only the
// per-client detail metrics evolve here, so a long string of
// non-retryable semantic faults does not flip a healthy interface to
// degraded.
func (t *Tracker) RecordRequest(name string, success bool) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.clientLocked(name)
	now := t.clk.Now()
	if success {
		c.LastSuccessfulRequest = now
		c.ConsecutiveFailures = 0
		return
	}
	c.LastFailedRequest = now
	c.ConsecutiveFailures++
}

// SetRecoveryFlag pins the `InRecovery` field of the per-interface
// [ClientHealth]. Called by the recovery coordinator on
// `RecoveryStartedEvent` (true) and on `RecoveryCompletedEvent` /
// `RecoveryFailedEvent` (false).
func (t *Tracker) SetRecoveryFlag(name string, inRecovery bool) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.clientLocked(name)
	c.InRecovery = inRecovery
}

// ResetReconnects clears the reconnect-attempt counter for the named
// component. Called by the wiring layer on a successful
// `ClientStateChanged → Connected` transition so a once-healed
// interface does not carry its restart history forever.
func (t *Tracker) ResetReconnects(name string) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reconnectAttempts[name] = 0
	c := t.clientLocked(name)
	c.ReconnectAttempts = 0
}

// SetPrimaryInterface designates name as the primary interface for
// [Tracker.PrimaryClientHealthy]. Passing an empty string clears the
// pin and falls back to the [PrimaryInterfaceHmIP] preference rule.
func (t *Tracker) SetPrimaryInterface(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.primaryInterface = name
}

// ClientDetail returns a snapshot of the per-interface [ClientHealth]
// for name. The second return value is false when no client has been
// registered yet — registration happens implicitly on the first
// [RecordRequest] / [SetRecoveryFlag] / [RecordReconnectAttempt]
// touching the name, so a freshly-booted daemon may legitimately
// report `false` while events trickle in.
func (t *Tracker) ClientDetail(name string) (ClientHealth, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.clients[name]
	if !ok || c == nil {
		return ClientHealth{}, false
	}
	// Layer the latest event-received timestamp from the sample
	// history on top of the stored snapshot so the field always
	// reflects what other tracker paths have observed.
	out := *c
	out.LastEventReceived = t.lastEventReceivedLocked(name)
	return out, true
}

// ClientScore returns the per-interface health score in [0, 1]
// computed from the same 40 % state + 30 % circuit + 30 % recent
// activity weighting (40 % state + 30 % circuit + 30 % activity). Unknown components return 0.
func (t *Tracker) ClientScore(name string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	comp, known := t.components[name]
	if !known {
		return 0
	}
	comp = t.applyStaleLocked(comp)
	state := clientScoreState(comp.Status)
	// The current LastSample.Note may carry a non-breaker tag (e.g.
	// "event-received") because every recorded sample shares the same
	// component entry. Walk the history backwards to find the most
	// recent breaker observation; default to "closed" (full credit)
	// when no breaker event has ever been recorded.
	circuit := clientScoreCircuit(t.lastBreakerNoteLocked(name))
	last := t.lastEventReceivedLocked(name)
	age := time.Hour // treat "never seen an event" as fully decayed
	if !last.IsZero() {
		age = t.clk.Now().Sub(last)
	}
	activity := clientScoreActivity(age)
	return composeClientScore(state, circuit, activity)
}

// lastBreakerNoteLocked walks the per-component history backwards and
// returns the Note of the newest sample whose note mentions
// "breaker" (i.e. was emitted by the CircuitBreakerStateChanged
// subscriber in `health_wiring.go`). Returns "" when no breaker
// sample has been observed yet — callers then treat the breaker as
// closed (the default state at construction). Caller must hold mu.
func (t *Tracker) lastBreakerNoteLocked(name string) string {
	src := t.history[name]
	for i := len(src) - 1; i >= 0; i-- {
		if strings.Contains(src[i].Note, "breaker") {
			return src[i].Note
		}
	}
	return ""
}

// PrimaryClientHealthy reports whether the primary interface — the
// one [SetPrimaryInterface] pinned, or [PrimaryInterfaceHmIP] when no
// pin is set — is currently reporting healthy. The JSON-RPC hub
// fallback consults this to decide whether to attempt a connection.
//
// Returns false when the primary interface has not been registered
// yet, so callers cannot mistake "not configured" for "healthy".
func (t *Tracker) PrimaryClientHealthy() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	name := t.primaryInterface
	if name == "" {
		name = PrimaryInterfaceHmIP
	}
	// Resolve against every registered component — the wiring layer
	// scopes interface ids with the central name (e.g.
	// `ccu-main-HmIP-RF`), so a substring match is the simplest
	// portable rule.
	for compName, raw := range t.components {
		if !strings.Contains(compName, name) {
			continue
		}
		return t.applyStaleLocked(raw).Status == StatusHealthy
	}
	return false
}

// lastEventReceivedLocked returns the timestamp of the newest sample
// with `Note == "event-received"` for name, or the zero value when no
// such sample exists. Caller must hold mu.
func (t *Tracker) lastEventReceivedLocked(name string) time.Time {
	src := t.history[name]
	for i := len(src) - 1; i >= 0; i-- {
		s := src[i]
		if s.Note == "event-received" {
			return s.Timestamp
		}
	}
	return time.Time{}
}

// ReconnectAttempts returns the current reconnect-attempt counter for
// the named component. Returns 0 for unknown components.
func (t *Tracker) ReconnectAttempts(name string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reconnectAttempts[name]
}

// Unregister removes the named component and its sample history from the
// tracker. Subsequent calls to Record re-register it. Idempotent when the
// component is unknown.
func (t *Tracker) Unregister(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.components, name)
	delete(t.history, name)
	delete(t.reconnectAttempts, name)
}

// OverallWindowedScore averages [WindowedScore] over every component.
func (t *Tracker) OverallWindowedScore(window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.history) == 0 {
		return 0
	}
	cutoff := t.clk.Now().Add(-window)
	totalScore := 0.0
	contributing := 0
	for _, src := range t.history {
		if len(src) == 0 {
			continue
		}
		healthy, total := 0, 0
		for _, s := range src {
			if s.Timestamp.Before(cutoff) {
				continue
			}
			total++
			if s.Healthy {
				healthy++
			}
		}
		if total == 0 {
			continue
		}
		totalScore += float64(healthy) / float64(total)
		contributing++
	}
	if contributing == 0 {
		return 0
	}
	return totalScore / float64(contributing)
}

// RegisterGauge installs a pull-based numeric gauge under the given
// name. Subsequent calls overwrite. The gauge is read on demand via
// [Tracker.Gauges]; callers expose it through admin / health
// endpoints (e.g. event-bus deferred high-water, throttle queue
// depth).
//
// Pass a nil fn to remove a previously registered gauge.
func (t *Tracker) RegisterGauge(name string, fn GaugeFunc) {
	t.gaugesMu.Lock()
	defer t.gaugesMu.Unlock()
	if t.gauges == nil {
		t.gauges = make(map[string]GaugeFunc)
	}
	if fn == nil {
		delete(t.gauges, name)
		return
	}
	t.gauges[name] = fn
}

// Gauges returns a snapshot of every registered gauge's current
// reading. The producer functions are invoked while holding only the
// gauge mutex (not the tracker mutex), so a slow gauge cannot stall
// Record / Snapshot calls. Returns an empty (non-nil) map when no
// gauges are registered.
func (t *Tracker) Gauges() map[string]float64 {
	t.gaugesMu.RLock()
	fns := make(map[string]GaugeFunc, len(t.gauges))
	for k, v := range t.gauges {
		fns[k] = v
	}
	t.gaugesMu.RUnlock()
	out := make(map[string]float64, len(fns))
	for k, fn := range fns {
		out[k] = fn()
	}
	return out
}

// SyncCentralStateFunc is the callback type used by [Tracker.OnClientStateChange].
// It receives the current overall [Status] so the caller can drive a state
// machine or publish a bus event in response.
type SyncCentralStateFunc func(overall Status)

// SyncCentralState evaluates the current overall health status and
// invokes fn with the result. This is the single-entry-point for
// components that need to re-evaluate central state after any client-
// health change — instead of embedding the evaluation logic in each
// caller, they defer to SyncCentralState.
//
// fn is invoked synchronously under a read lock on the tracker's
// component map; it must not call any Tracker methods that acquire
// the write lock (Record, Unregister) as that would deadlock.
func (t *Tracker) SyncCentralState(fn SyncCentralStateFunc) {
	if fn == nil {
		return
	}
	fn(t.Overall())
}
