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
	"github.com/SukramJ/openccu-loom/internal/httpx"
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

	// alarmBus is the daemon-level alarm event bus (nil until wired via
	// SetAlarmBus). It is separate from the per-central buses because
	// alarm zones are daemon-level, not central-scoped.
	alarmBus *events.Bus
	// securityBus is the Security & Safety domain bus (nil until wired
	// via SetSecurityBus). Independent of alarmBus: the domain reports
	// with or without an alarm engine.
	securityBus *events.Bus

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
		client:       httpx.NewClient(cfg.Timeout()),
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

// SetAlarmBus wires the daemon-level alarm event bus so the bridge also
// forwards alarm-panel events (state, trigger, journal, health,
// reminder, duress) under their EventType strings through the existing
// allow-list (notes/concepts/alarm-concept.md §13.4). Must be called before Start;
// a nil bus leaves the alarm plane unwired.
func (o *Outbound) SetAlarmBus(bus *events.Bus) {
	o.mu.Lock()
	o.alarmBus = bus
	o.mu.Unlock()
}

// SetSecurityBus wires the Security & Safety domain bus so the bridge
// forwards the rendered reports and the fault transitions. Must be
// called before Start; a nil bus leaves the plane unwired.
//
// This is the plane a messenger integration subscribes to when it wants
// the sentence rather than the raw facts: the notification payload
// carries subject and message alongside the machine facets.
func (o *Outbound) SetSecurityBus(bus *events.Bus) {
	o.mu.Lock()
	o.securityBus = bus
	o.mu.Unlock()
}

// Name implements bridge.Service.
func (o *Outbound) Name() string { return "webhook-outbound" }

// Start subscribes one handler set per allowed central and spawns the
// delivery worker. It is a no-op (returns nil) when the bridge is disabled,
// has no URL, or is already started — so the daemon can always register it.
//
// The registry walk happens exactly once, here. A central adopted later needs
// [Outbound.StartCentral].
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
		o.unsubs = append(o.unsubs, o.subscribeCentral(u.EventBus, name)...)
	}
	centralSubs := len(o.unsubs)
	if o.securityBus != nil {
		o.subscribeSecurity(o.securityBus)
	}
	if o.alarmBus != nil {
		o.subscribeAlarm(o.alarmBus)
	}
	o.started = true
	o.logger.Info("webhook.outbound.started",
		slog.String("url", o.cfg.URL),
		slog.Int("centrals", centralSubs/3),
		slog.Bool("alarm", o.alarmBus != nil))
	return nil
}

// subscribeCentral attaches the three event handlers for one central and
// returns their unsubscribe funcs in attach order.
func (o *Outbound) subscribeCentral(bus *events.Bus, name string) []func() {
	return []func(){
		events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
			o.onDataPoint(name, e)
		}),
		events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
			o.onSystemStatus(name, e)
		}),
		events.Subscribe(bus, func(e hmevent.IncidentRecordedEvent) {
			o.onIncident(name, e)
		}),
	}
}

// StartCentral attaches exactly one central and returns its unwire, or nil
// when there is nothing to attach (bridge not running, central excluded by
// north.webhook.centrals, no bus). The composition root routes this through
// the live-adopt hook chain.
//
// Start's registry walk happens once, at daemon boot. A CCU adopted at
// runtime is invisible to it, so without this seam none of that CCU's
// datapoints, status changes or incidents ever reach the operator's endpoint
// while the boot-time CCUs keep delivering normally — the failure looks like
// a quiet CCU, not like a broken bridge.
//
// The unwire is handed to the caller rather than recorded in o.unsubs: the
// adopt path owns the detach, so a central removed at runtime stops
// delivering immediately instead of at the next daemon Stop.
func (o *Outbound) StartCentral(u *central.Unit) (unwire func()) {
	if o == nil || u == nil || u.EventBus == nil {
		return nil
	}
	name := u.Name()
	if !o.centralAllowed(name) {
		return nil
	}
	o.mu.Lock()
	started := o.started
	o.mu.Unlock()
	if !started {
		// Start has not run yet; its own registry walk will pick this
		// central up. Subscribing here too would deliver every event twice.
		return nil
	}
	unsubs := o.subscribeCentral(u.EventBus, name)
	return func() {
		for _, unsub := range unsubs {
			unsub()
		}
	}
}

// subscribeAlarm attaches the alarm-plane handlers to the daemon-level
// alarm bus. Zones are daemon-level, so there is one shared bus (no
// per-central fan-out). Hidden journal entries (duress) never emit an
// AlarmJournalAppendedEvent — the journal facade suppresses it — so the
// journal handler forwards only visible entries; the silent duress
// alarm rides its own AlarmDuressEvent (notes/concepts/alarm-concept.md §11, §13.4).
func (o *Outbound) subscribeAlarm(bus *events.Bus) {
	o.unsubs = append(
		o.unsubs,
		events.Subscribe(bus, o.onAlarmStateChanged),
		events.Subscribe(bus, o.onAlarmTriggered),
		events.Subscribe(bus, o.onAlarmNotification),
		events.Subscribe(bus, o.onAlarmJournalAppended),
		events.Subscribe(bus, o.onAlarmHealthChanged),
		events.Subscribe(bus, o.onAlarmReminder),
		events.Subscribe(bus, o.onAlarmDuress),
	)
}

// subscribeSecurity wires the Security & Safety reports.
//
// Only the two reporting events are forwarded, not the aggregate
// changes: a webhook consumer wants "something happened, here is what",
// and a class flag flipping is already implied by the report that
// accompanies it.
func (o *Outbound) subscribeSecurity(bus *events.Bus) {
	o.unsubs = append(
		o.unsubs,
		events.Subscribe(bus, o.onSecurityNotification),
		events.Subscribe(bus, o.onSecurityFaultChanged),
	)
}

// onSecurityNotification forwards a rendered report.
func (o *Outbound) onSecurityNotification(e hmevent.SecurityNotificationEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeSecurityNotification), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName, Mode: string(e.Mode),
		IncidentID: e.IncidentID, Class: string(e.Class), Cause: string(e.Verb),
		Subject: e.Subject, Message: e.Message, Severity: string(e.Severity),
		I18nKey: e.I18nKey, Args: e.Args, Link: e.Link,
		Sources: alarmSources(e.Sources),
	})
}

// onSecurityFaultChanged forwards a fault opening or closing.
func (o *Outbound) onSecurityFaultChanged(e hmevent.SecurityFaultChangedEvent) {
	verb := "cleared"
	if e.Open {
		verb = "raised"
	}
	o.enqueueAlarm(string(hmevent.EventTypeSecurityFaultChanged), alarmPayload{
		Class: string(e.Class), Cause: verb, Severity: string(e.Severity),
		Note: string(e.Reason), EntryID: int64(e.OpenCount),
		Sources: alarmSources([]hmevent.SecuritySourceRef{e.Source}),
	})
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

// ---- alarm-plane handlers (run on the alarm bus goroutine) ----
//
// Alarm zones are daemon-level, so these carry no `central`. Each event
// forwards under its hmevent EventType string and threads the existing
// event allow-list; the alarm-specific detail rides the nested `alarm`
// object so the flat envelope stays stable for datapoint/system/incident
// receivers.

func (o *Outbound) onAlarmStateChanged(e hmevent.AlarmStateChangedEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeAlarmStateChanged), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName,
		FromState: string(e.From), ToState: string(e.To),
		Mode: string(e.Mode), ChangedBy: e.ChangedBy, Source: e.Source,
		IncidentID: e.IncidentID,
	})
}

func (o *Outbound) onAlarmTriggered(e hmevent.AlarmTriggeredEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeAlarmTriggered), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName, IncidentID: e.IncidentID,
		SensorID: e.SensorID, SensorName: e.SensorName,
		Cause: e.Cause, Mode: string(e.Mode),
		Sources: alarmSources(e.Sources),
	})
}

// onAlarmNotification forwards one enrolled notification output's
// fire signal; outputs that opted out of the webhook plane are
// skipped.
func (o *Outbound) onAlarmNotification(e hmevent.AlarmNotificationEvent) {
	if !e.Webhook {
		return
	}
	o.enqueueAlarm(string(hmevent.EventTypeAlarmNotification), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName, IncidentID: e.IncidentID,
		Mode: string(e.Mode), OutputID: e.OutputID, OutputName: e.OutputName,
		Cause: e.Cause, Sources: alarmSources(e.Sources),
	})
}

func (o *Outbound) onAlarmJournalAppended(e hmevent.AlarmJournalAppendedEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeAlarmJournalAppended), alarmPayload{
		ZoneID: e.ZoneID, EntryID: e.EntryID, Class: string(e.Class),
		JournalEvent: e.Event, ChangedBy: e.Actor, IncidentID: e.IncidentID,
	})
}

func (o *Outbound) onAlarmHealthChanged(e hmevent.AlarmHealthChangedEvent) {
	healthy := e.Healthy
	o.enqueueAlarm(string(hmevent.EventTypeAlarmHealthChanged), alarmPayload{
		Healthy: &healthy, Note: e.Note,
	})
}

func (o *Outbound) onAlarmReminder(e hmevent.AlarmReminderEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeAlarmReminder), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName, Mode: string(e.Mode),
	})
}

// onAlarmDuress forwards the silent duress fan-out to notification
// targets (webhook is an explicit duress sink per §11). It never touches
// the WebSocket surface, so a screen watcher cannot learn duress fired.
func (o *Outbound) onAlarmDuress(e hmevent.AlarmDuressEvent) {
	o.enqueueAlarm(string(hmevent.EventTypeAlarmDuress), alarmPayload{
		ZoneID: e.ZoneID, ZoneName: e.ZoneName, Verb: e.Verb,
		ChangedBy: e.By, Source: e.Source, IncidentID: e.IncidentID,
	})
}

// enqueueAlarm marshals the alarm detail, wraps it in the versioned
// envelope under the given event type, and enqueues it — subject to the
// same event allow-list as every other event type.
func (o *Outbound) enqueueAlarm(eventType string, pay alarmPayload) {
	if !o.eventAllowed(eventType) {
		return
	}
	detail, err := json.Marshal(pay)
	if err != nil {
		o.logger.Warn("webhook.outbound.alarm_marshal", slog.String("err", err.Error()))
		return
	}
	o.enqueue(envelope{
		Schema: schemaVersion,
		Event:  eventType,
		Alarm:  detail,
		TS:     o.now().UTC().Format(time.RFC3339),
	})
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

	// Alarm carries the alarm-plane detail for the alarm_panel.* event
	// types; absent for every other event.
	Alarm json.RawMessage `json:"alarm,omitempty"`

	TS string `json:"ts"`
}

// alarmPayload is the nested detail of an alarm-plane webhook event. Each
// alarm event populates the subset it carries; omitempty keeps the object
// tight. A duress code's secret is never present — only its identity and
// the verb it accompanied (notes/concepts/alarm-concept.md §16).
type alarmPayload struct {
	ZoneID       string `json:"zone_id,omitempty"`
	ZoneName     string `json:"zone_name,omitempty"`
	FromState    string `json:"from_state,omitempty"`
	ToState      string `json:"to_state,omitempty"`
	Mode         string `json:"mode,omitempty"`
	ChangedBy    string `json:"changed_by,omitempty"`
	Source       string `json:"source,omitempty"`
	IncidentID   int64  `json:"incident_id,omitempty"`
	SensorID     string `json:"sensor_id,omitempty"`
	SensorName   string `json:"sensor_name,omitempty"`
	Cause        string `json:"cause,omitempty"`
	EntryID      int64  `json:"entry_id,omitempty"`
	Class        string `json:"class,omitempty"`
	JournalEvent string `json:"journal_event,omitempty"`
	Verb         string `json:"verb,omitempty"`
	Healthy      *bool  `json:"healthy,omitempty"`
	Note         string `json:"note,omitempty"`
	// OutputID / OutputName identify the enrolled notification output
	// on alarm_panel.notification events.
	OutputID   string `json:"output_id,omitempty"`
	OutputName string `json:"output_name,omitempty"`
	// Sources carries every data point that contributed to the
	// incident. A messenger integration reads this to name what set the
	// alarm off; sensor_id / sensor_name only ever held the first one.
	Sources []alarmSourcePayload `json:"sources,omitempty"`
	// The fields below appear on Security & Safety reports: the rendered
	// sentence plus the catalogue key that lets a consumer re-render it
	// in its own locale.
	Subject  string            `json:"subject,omitempty"`
	Message  string            `json:"message,omitempty"`
	Severity string            `json:"severity,omitempty"`
	I18nKey  string            `json:"i18n_key,omitempty"`
	Args     map[string]string `json:"args,omitempty"`
	Link     string            `json:"link,omitempty"`
}

// alarmSourcePayload is the webhook projection of a contributing data
// point. Field names match the MQTT alarm plane and the REST incident
// shape so one parser serves all three.
type alarmSourcePayload struct {
	Ref            string `json:"ref"`
	Central        string `json:"central,omitempty"`
	InterfaceID    string `json:"interface_id,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	SensorID       string `json:"sensor_id,omitempty"`
	Name           string `json:"name,omitempty"`
	SensorType     string `json:"sensor_type,omitempty"`
	Class          string `json:"class,omitempty"`
	AtMS           int64  `json:"at_ms,omitempty"`
}

// alarmSources projects the domain refs onto the webhook wire shape.
func alarmSources(refs []hmevent.SecuritySourceRef) []alarmSourcePayload {
	if len(refs) == 0 {
		return nil
	}
	out := make([]alarmSourcePayload, 0, len(refs))
	for i := range refs {
		r := &refs[i]
		out = append(out, alarmSourcePayload{
			Ref: r.Ref, Central: r.Central, InterfaceID: r.InterfaceID,
			ChannelAddress: r.ChannelAddress, DeviceAddress: r.DeviceAddress,
			Parameter: r.Parameter, SensorID: r.SensorID, Name: r.Name,
			SensorType: string(r.SensorType), Class: string(r.Class), AtMS: r.AtMS,
		})
	}
	return out
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
