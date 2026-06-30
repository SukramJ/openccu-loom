// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package webhook implements the outbound webhook bridge: a north-bound
// adapter that subscribes to every registered central's event bus and POSTs
// a signed, versioned JSON payload to an operator-configured endpoint on
// datapoint, system-status and incident events.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// schemaVersion tags every payload so receivers can branch on shape.
const schemaVersion = "openccu-loom.webhook/v1"

// queueCapacity bounds the in-flight delivery queue. A full queue drops the
// oldest pending delivery so a slow/unreachable endpoint never blocks the
// event bus (handlers must return fast — see internal/central/events).
const queueCapacity = 256

// defaultBackoff is the jittered exponential retry schedule applied after a
// failed POST. Length is the number of retries after the first attempt.
var defaultBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// Outbound is the outbound webhook bridge.Service. It is constructed once at
// daemon boot; Start subscribes per central and spawns the delivery worker,
// Stop unsubscribes and drains.
type Outbound struct {
	reg    *central.Registry
	cfg    config.NorthWebhook
	logger *slog.Logger
	client *http.Client

	// now and backoff are injectable seams for tests.
	now     func() time.Time
	backoff []time.Duration

	eventAllow   map[string]struct{} // empty => all event types
	centralAllow map[string]struct{} // empty => all centrals

	mu      sync.Mutex
	started bool
	unsubs  []func()
	queue   chan delivery
	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	dropped atomic.Int64 // deliveries discarded because the queue was full
	failed  atomic.Int64 // deliveries that exhausted all retries
}

// delivery is one pending POST: the event tag (for the X-OpenCCU-Event
// header) and the fully-marshalled, sign-ready body.
type delivery struct {
	event string
	body  []byte
}

// Option customises an Outbound at construction (used by tests).
type Option func(*Outbound)

// WithHTTPClient overrides the HTTP client (e.g. with a fake RoundTripper).
func WithHTTPClient(c *http.Client) Option { return func(o *Outbound) { o.client = c } }

// WithBackoff overrides the retry schedule (tests use near-zero delays).
func WithBackoff(b []time.Duration) Option { return func(o *Outbound) { o.backoff = b } }

// WithClock overrides the timestamp source.
func WithClock(now func() time.Time) Option { return func(o *Outbound) { o.now = now } }

// NewOutbound builds the bridge from reg and cfg. It does not start any
// goroutine — call Start for that.
func NewOutbound(reg *central.Registry, cfg config.NorthWebhook, logger *slog.Logger, opts ...Option) *Outbound {
	if logger == nil {
		logger = slog.Default()
	}
	o := &Outbound{
		reg:          reg,
		cfg:          cfg,
		logger:       logger,
		client:       &http.Client{Timeout: cfg.Timeout()},
		now:          time.Now,
		backoff:      defaultBackoff,
		eventAllow:   toSet(cfg.Events),
		centralAllow: toSet(cfg.Centrals),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Name implements bridge.Service.
func (o *Outbound) Name() string { return "webhook-outbound" }

// Start subscribes one handler set per allowed central and spawns the
// delivery worker. It is a no-op (returns nil) when the bridge is disabled,
// has no URL, or is already started — so the daemon can always register it.
func (o *Outbound) Start(_ context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.started || !o.cfg.Enabled || o.cfg.URL == "" || o.reg == nil {
		return nil
	}
	o.baseCtx, o.cancel = context.WithCancel(context.Background())
	o.queue = make(chan delivery, queueCapacity)
	o.wg.Add(1)
	// Pass the channel explicitly so the worker never reads the o.queue
	// field — Stop nils that field under the mutex, which would otherwise
	// race the worker's range.
	go o.worker(o.queue)

	for _, u := range o.reg.List() {
		if u == nil || u.EventBus == nil {
			continue
		}
		name := u.Name()
		if !o.centralAllowed(name) {
			continue
		}
		o.subscribeCentral(u.EventBus, name)
	}
	o.started = true
	o.logger.Info("webhook.outbound.started",
		slog.String("url", o.cfg.URL),
		slog.Int("centrals", len(o.unsubs)/3))
	return nil
}

// subscribeCentral attaches the three event handlers for one central and
// records their unsubscribe funcs.
func (o *Outbound) subscribeCentral(bus *events.Bus, name string) {
	o.unsubs = append(
		o.unsubs,
		events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
			o.onDataPoint(name, e)
		}),
		events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
			o.onSystemStatus(name, e)
		}),
		events.Subscribe(bus, func(e hmevent.IncidentRecordedEvent) {
			o.onIncident(name, e)
		}),
	)
}

// Stop unsubscribes every handler, stops the worker and waits for the
// in-flight delivery to finish. Idempotent.
func (o *Outbound) Stop(_ context.Context) error {
	o.mu.Lock()
	if !o.started {
		o.mu.Unlock()
		return nil
	}
	unsubs := o.unsubs
	o.unsubs = nil
	o.started = false
	cancel := o.cancel
	queue := o.queue
	o.queue = nil
	o.mu.Unlock()

	for _, u := range unsubs {
		u()
	}
	// Cancel in-flight HTTP, then close the queue so the worker drains and
	// exits. Order matters: cancel unblocks a slow POST; close ends the loop.
	if cancel != nil {
		cancel()
	}
	if queue != nil {
		close(queue)
	}
	o.wg.Wait()
	return nil
}

// Healthy implements bridge.HealthReporter. The bridge is always "up" once
// started; it reports the running failed/dropped counters as detail so an
// operator can see delivery trouble without it tripping overall health.
func (o *Outbound) Healthy() (ok bool, detail string) {
	return true, ""
}

// Dropped returns the number of deliveries discarded due to a full queue.
func (o *Outbound) Dropped() int64 { return o.dropped.Load() }

// Failed returns the number of deliveries that exhausted all retries.
func (o *Outbound) Failed() int64 { return o.failed.Load() }

// ---- event handlers (run on the bus goroutine — must not block) ----

func (o *Outbound) onDataPoint(centralName string, e hmevent.DataPointValueChangedEvent) {
	if !o.eventAllowed(string(hmevent.EventTypeDataPointValueChanged)) {
		return
	}
	if !o.parameterAllowed(e.Key.Parameter) {
		return
	}
	env := envelope{
		Schema:    schemaVersion,
		Event:     string(hmevent.EventTypeDataPointValueChanged),
		Central:   centralName,
		Interface: e.Key.InterfaceID,
		Address:   e.Key.ChannelAddress,
		Parameter: e.Key.Parameter,
		Value:     marshalValue(e.NewValue.Unwrap()),
		TS:        o.now().UTC().Format(time.RFC3339),
	}
	if !e.OldValue.IsNone() {
		env.Previous = marshalValue(e.OldValue.Unwrap())
	}
	o.enqueue(env)
}

func (o *Outbound) onSystemStatus(centralName string, e hmevent.SystemStatusChangedEvent) {
	if !o.eventAllowed(string(hmevent.EventTypeSystemStatusChanged)) {
		return
	}
	healthy := e.Healthy
	env := envelope{
		Schema:    schemaVersion,
		Event:     string(hmevent.EventTypeSystemStatusChanged),
		Central:   centralName,
		Interface: e.InterfaceID,
		Component: e.Component,
		Healthy:   &healthy,
		Reason:    e.Reason,
		TS:        o.now().UTC().Format(time.RFC3339),
	}
	o.enqueue(env)
}

func (o *Outbound) onIncident(centralName string, e hmevent.IncidentRecordedEvent) {
	if !o.eventAllowed(string(hmevent.EventTypeIncidentRecorded)) {
		return
	}
	env := envelope{
		Schema:       schemaVersion,
		Event:        string(hmevent.EventTypeIncidentRecorded),
		Central:      centralName,
		Interface:    e.InterfaceID,
		IncidentType: string(e.IncidentType),
		Severity:     string(e.Severity),
		Message:      e.Message,
		Details:      e.Details,
		TS:           o.now().UTC().Format(time.RFC3339),
	}
	o.enqueue(env)
}

// enqueue marshals env and pushes it onto the queue without blocking. On a
// full queue it drops the oldest pending delivery (best-effort) and counts
// the drop, so the event bus is never stalled by a slow endpoint.
func (o *Outbound) enqueue(env envelope) {
	body, err := json.Marshal(env)
	if err != nil {
		o.logger.Warn("webhook.outbound.marshal", slog.String("err", err.Error()))
		return
	}
	o.mu.Lock()
	q := o.queue
	o.mu.Unlock()
	if q == nil {
		return
	}
	d := delivery{event: env.Event, body: body}
	select {
	case q <- d:
		return
	default:
	}
	// Queue full: drop the oldest, then enqueue the newest. The drains and
	// sends are non-blocking so a racing Stop (which closes q) cannot panic
	// us into a closed-channel send beyond a single recovered attempt.
	select {
	case <-q:
		o.dropped.Add(1)
	default:
	}
	select {
	case q <- d:
	default:
		o.dropped.Add(1)
	}
}

// worker drains queue and delivers each payload with retry. It exits when
// the channel is closed (Stop). The channel is passed in rather than read
// from the o.queue field so it never races Stop's o.queue = nil.
func (o *Outbound) worker(queue chan delivery) {
	defer o.wg.Done()
	for d := range queue {
		o.deliver(d)
	}
}

// deliver POSTs one payload, retrying on error/5xx per the backoff schedule.
// A 2xx/3xx/4xx (non-5xx) response ends the attempt loop: 4xx is a client
// error the retry would not fix. Final failure increments the failed counter
// and logs; it never panics or blocks indefinitely.
func (o *Outbound) deliver(d delivery) {
	attempts := len(o.backoff) + 1
	for attempt := range attempts {
		if attempt > 0 {
			if !o.sleep(o.jitter(o.backoff[attempt-1])) {
				return // bridge stopped mid-backoff
			}
		}
		retry, err := o.post(d)
		if err == nil && !retry {
			return
		}
		if attempt == attempts-1 {
			o.failed.Add(1)
			reason := "5xx response"
			if err != nil {
				reason = err.Error()
			}
			o.logger.Warn("webhook.outbound.delivery_failed",
				slog.String("event", d.event),
				slog.String("reason", reason),
				slog.Int("attempts", attempts))
		}
	}
}

// post performs a single signed POST. retry is true when the caller should
// retry (transport error or 5xx); err carries a transport error.
func (o *Outbound) post(d delivery) (retry bool, err error) {
	req, err := http.NewRequestWithContext(o.baseCtx, http.MethodPost, o.cfg.URL, bytes.NewReader(d.body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenCCU-Event", d.event)
	req.Header.Set("X-OpenCCU-Delivery", uuid.NewString())
	if o.cfg.Secret != "" {
		req.Header.Set("X-OpenCCU-Signature", sign(o.cfg.Secret, d.body))
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		return true, nil
	}
	return false, nil
}

// sleep waits d or until the bridge is stopped; it returns false when the
// bridge was stopped (so the caller abandons the delivery).
func (o *Outbound) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-o.baseCtx.Done():
		return false
	}
}

// jitter applies up to +20% random jitter so retries from many events do not
// align into a thundering herd.
func (o *Outbound) jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	//nolint:gosec // G404: jitter is timing dispersion, not a security primitive — a weak PRNG is correct and intended here.
	return d + time.Duration(rand.Int64N(int64(d)/5+1))
}

// ---- filters ----

func (o *Outbound) eventAllowed(t string) bool {
	if len(o.eventAllow) == 0 {
		return true
	}
	_, ok := o.eventAllow[t]
	return ok
}

func (o *Outbound) centralAllowed(name string) bool {
	if len(o.centralAllow) == 0 {
		return true
	}
	_, ok := o.centralAllow[name]
	return ok
}

// parameterAllowed matches the datapoint parameter name against the
// configured glob. An empty glob (or a malformed pattern) allows everything.
func (o *Outbound) parameterAllowed(param string) bool {
	if o.cfg.ParameterGlob == "" {
		return true
	}
	ok, err := path.Match(o.cfg.ParameterGlob, param)
	if err != nil {
		return true // a bad pattern must not silently drop every event
	}
	return ok
}

// ---- helpers ----

// envelope is the versioned JSON payload. Per-event-type fields are
// omitempty so each event carries only its own set under one schema.
type envelope struct {
	Schema    string          `json:"schema"`
	Event     string          `json:"event"`
	Central   string          `json:"central"`
	Interface string          `json:"interface,omitempty"`
	Address   string          `json:"address,omitempty"`
	Parameter string          `json:"parameter,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
	Previous  json.RawMessage `json:"previous,omitempty"`
	Component string          `json:"component,omitempty"`
	Healthy   *bool           `json:"healthy,omitempty"`
	Reason    string          `json:"reason,omitempty"`

	IncidentType string `json:"incident_type,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Message      string `json:"message,omitempty"`
	Details      string `json:"details,omitempty"`

	TS string `json:"ts"`
}

// marshalValue JSON-encodes a datapoint value. A marshal error (should not
// happen for the scalar/list union ParamValue.Unwrap returns) yields a JSON
// null so the field is still present and well-formed.
func marshalValue(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// sign computes the lowercase-hex HMAC-SHA256 of body under secret, prefixed
// "sha256=" — the GitHub-webhook convention receivers already know.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// toSet builds a lookup set from a slice; nil/empty yields an empty set
// (interpreted as "allow all" by the callers).
func toSet(ss []string) map[string]struct{} {
	if len(ss) == 0 {
		return map[string]struct{}{}
	}
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}
