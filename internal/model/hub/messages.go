// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AlarmMessage is one CCU alarm entry. The ID is the CCU ISE ID.
//
// An alarm entry has no device, channel, room or trigger: it is backed
// by an alarm system variable that programs raise, so the CCU reports
// its trigger data point as the 65535 "unknown" sentinel. Device-bound
// alerts arrive as [ServiceMessage] instead, which does carry them.
type AlarmMessage struct {
	ID          string
	Name        string
	Description string
	// Timestamp is when the alarm was raised, LastTimestamp when the
	// backing variable last changed. Both are zero when the CCU reports
	// no such occurrence — for a variable raised exactly once,
	// LastTimestamp normally is.
	Timestamp     time.Time
	LastTimestamp time.Time
	Counter       int
	// DisplayName is the human-readable translation of the message code
	// extracted from the raw CCU name (the part after the last dot).
	DisplayName string
}

// AlarmMessages aggregates active alarm entries. Updates replace the
// whole set at once (the CCU script always returns the full list).
//
// AlarmMessages embeds [datapoint.BaseDataPointFields] so it
// CallbackDataPoint (hub/alarm_messages.py:34). The promoted
// [datapoint.BaseDataPointFields.IsRegistered]
// [datapoint.BaseDataPointFields.MarkRegistered]
// [datapoint.BaseDataPointFields.ModifiedAt]
// [datapoint.BaseDataPointFields.RefreshedAt] surfaces are activated.
// Multi-CCU-safe constructors must set the central name — use
// [NewAlarmMessagesWithCentral] in production code.
type AlarmMessages struct {
	datapoint.BaseDataPointFields

	// Ack acknowledges a single alarm message by id. BulkAck acknowledges
	// every active alarm message in one CCU pass. Both are set at
	// construction (nil for tests / single-CCU convenience) or re-wired
	// under the aggregate mutex via [AlarmMessages.SetAcknowledgers].
	Ack     MessageAcknowledger
	BulkAck BulkMessageAcknowledger

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each AlarmMessages instance gets its own registry so the dismiss
	// service is registered per-instance.
	payload.ServiceRegistry

	mu        sync.RWMutex
	messages  map[string]AlarmMessage
	observed  bool
	callbacks []func(messages []AlarmMessage)
}

// NewAlarmMessages constructs an AlarmMessages aggregate with no central
// scoping (suitable for tests and single-CCU deployments). Production
// multi-CCU callers MUST use [NewAlarmMessagesWithCentral].
func NewAlarmMessages(ack MessageAcknowledger) *AlarmMessages {
	return NewAlarmMessagesWithCentral("", ack)
}

// NewAlarmMessagesWithCentral is the multi-CCU-safe constructor. The
// embedded [datapoint.BaseDataPointFields] is initialised with the
// `central` scope so the resulting [UniqueID] is
// `<central>::alarm_messages`. ADR 0002 requires production callers
// to set `central`.
//
// loom:reachable:reason="called by NewAlarmMessages (legacy wrapper) which is used by hub.NewHub to populate the Hub.Messages field"
func NewAlarmMessagesWithCentral(centralName string, ack MessageAcknowledger) *AlarmMessages {
	a := &AlarmMessages{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, "", "alarm_messages"),
		Ack:                 ack,
		messages:            map[string]AlarmMessage{},
	}
	a.RegisterService("dismiss", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		id, err := payload.ParamString(params, "item_id")
		if err != nil {
			return err
		}
		return a.Acknowledge(ctx, id)
	})
	return a
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 alarm-messages aggregate is published to
// `<base>/<central>/hub/alarm_messages`. Read-only; no Set topic.
func (a *AlarmMessages) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	return payload.MQTTTopicSet{
		State: naming.MQTTHubAlarmMessages(base, centralName),
	}
}

// List returns the current alarm set sorted by Timestamp descending
// (most recent first).
func (a *AlarmMessages) List() []AlarmMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AlarmMessage, 0, len(a.messages))
	for id := range a.messages {
		out = append(out, a.messages[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	return out
}

// Count returns the number of active alarm messages.
func (a *AlarmMessages) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.messages)
}

// Observed reports whether the coordinator has ever delivered a set.
func (a *AlarmMessages) Observed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.observed
}

// Replace swaps the entire alarm set. Fires callbacks when the new
// set differs from the previous (by message IDs or their timestamps).
func (a *AlarmMessages) Replace(messages []AlarmMessage) {
	a.mu.Lock()
	next := make(map[string]AlarmMessage, len(messages))
	for i := range messages {
		next[messages[i].ID] = messages[i]
	}
	changed := !sameAlarmSet(a.messages, next) || !a.observed
	a.messages = next
	a.observed = true
	cbs := make([]func(messages []AlarmMessage), len(a.callbacks))
	copy(cbs, a.callbacks)
	a.mu.Unlock()

	if !changed {
		return
	}
	snapshot := a.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snapshot)
		}
	}
}

// SetAcknowledgers wires (or re-wires) the single- and bulk-message
// acknowledgers under the aggregate mutex. Use this from the hub-wiring
// adapter so a background WireHub recovery can re-apply them without
// racing a concurrent [AlarmMessages.Acknowledge] / [AlarmMessages.AcknowledgeAll]
// call.
func (a *AlarmMessages) SetAcknowledgers(single MessageAcknowledger, bulk BulkMessageAcknowledger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Ack = single
	a.BulkAck = bulk
}

func (a *AlarmMessages) acker() MessageAcknowledger {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Ack
}

func (a *AlarmMessages) bulkAcker() BulkMessageAcknowledger {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.BulkAck
}

// Acknowledge dispatches an acknowledge for id through the writer and
// removes the entry from the local set.
func (a *AlarmMessages) Acknowledge(ctx context.Context, id string) error {
	ack := a.acker()
	if ack == nil {
		return errors.New("alarm messages: no acknowledger configured")
	}
	if err := ack.AcknowledgeMessage(ctx, id); err != nil {
		return err
	}
	a.mu.Lock()
	_, existed := a.messages[id]
	delete(a.messages, id)
	cbs := make([]func(messages []AlarmMessage), len(a.callbacks))
	copy(cbs, a.callbacks)
	a.mu.Unlock()
	if !existed {
		return nil
	}
	snapshot := a.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snapshot)
		}
	}
	return nil
}

// AcknowledgeAll acknowledges every active alarm message on the CCU in one
// pass and returns the number acknowledged. On success the local set is
// cleared (all alarm messages are unconditionally acknowledgeable) and the
// change callbacks fire once. A nil bulk acknowledger yields an error.
func (a *AlarmMessages) AcknowledgeAll(ctx context.Context) (int, error) {
	bulk := a.bulkAcker()
	if bulk == nil {
		return 0, errors.New("alarm messages: no bulk acknowledger configured")
	}
	n, err := bulk.AcknowledgeAllAlarmMessages(ctx)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	changed := len(a.messages) > 0
	a.messages = map[string]AlarmMessage{}
	cbs := make([]func(messages []AlarmMessage), len(a.callbacks))
	copy(cbs, a.callbacks)
	a.mu.Unlock()
	if changed {
		snapshot := a.List()
		for _, cb := range cbs {
			if cb != nil {
				cb(snapshot)
			}
		}
	}
	return n, nil
}

// LegacyName returns the original pre-slug name stored on the CCU.
// AlarmMessages is a structural aggregate without a CCU-side variable
// name, so this always returns "".
func (*AlarmMessages) LegacyName() string { return "" }

// Description returns the optional human-readable description. AlarmMessages
// has no CCU-side description field, so this always returns "".
func (*AlarmMessages) Description() string { return "" }

// OnUpdate registers a change handler.
func (a *AlarmMessages) OnUpdate(fn func([]AlarmMessage)) func() {
	a.mu.Lock()
	a.callbacks = append(a.callbacks, fn)
	idx := len(a.callbacks) - 1
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if idx < len(a.callbacks) {
				a.callbacks[idx] = nil
			}
		})
	}
}

// ServiceMessage is one CCU service message.
type ServiceMessage struct {
	ID         string
	Name       string
	Address    string
	DeviceName string
	// Parameter is the service parameter that raised the message (the
	// segment after the last dot in the raw CCU name, e.g. "LOWBAT").
	// Empty when the raw name carries no parameter segment. Used by
	// [ServiceMessages.Disable] to target the JSON-RPC
	// Interface.suppressServiceMessages call at the exact parameter.
	Parameter string
	// InterfaceID is the CCU interface the channel lives on
	// (e.g. "HmIP-RF"), resolved from [ServiceMessage.Address] at load
	// time. May be empty when the owning device was not (yet) registered;
	// the suppressor then re-resolves it from the channel address.
	InterfaceID string
	Type        hmenum.ServiceMessageType
	// Timestamp is when the message first appeared, LastTimestamp when it
	// last recurred. Both are zero when the CCU reports no such
	// occurrence, mirroring [AlarmMessage].
	Timestamp     time.Time
	LastTimestamp time.Time
	Counter       int
	Rooms         []string
	Functions     []string
	Quittable     bool
	// DisplayName is the human-readable translation of the message code
	// extracted from the raw CCU name (the part after the last dot).
	DisplayName string
}

// ServiceMessages aggregates service messages.
//
// ServiceMessages embeds [datapoint.BaseDataPointFields] mirroring
// CallbackDataPoint. The promoted lifecycle / timestamp methods are
// thereby activated. Multi-CCU callers MUST use
// [NewServiceMessagesWithCentral].
type ServiceMessages struct {
	datapoint.BaseDataPointFields

	// Ack acknowledges a single service message by id. BulkAck acknowledges
	// every quittable service message in one CCU pass. Both are set at
	// construction (nil for tests / single-CCU convenience) or re-wired
	// under the aggregate mutex via [ServiceMessages.SetAcknowledgers].
	Ack     MessageAcknowledger
	BulkAck BulkMessageAcknowledger

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each ServiceMessages instance gets its own registry so the dismiss
	// service is registered per-instance.
	payload.ServiceRegistry

	mu        sync.RWMutex
	messages  map[string]ServiceMessage
	observed  bool
	callbacks []func(messages []ServiceMessage)

	// suppressor durably suppresses a channel parameter on the CCU.
	// Wired via [ServiceMessages.SetSuppressor]; nil disables the
	// suppress / unsuppress path ([Disable] returns an error).
	suppressor ServiceMessageSuppressor
	// suppressed records the channel parameters this daemon has
	// suppressed on the CCU, keyed by suppressKey(interface, channel,
	// parameter). It seeds the management view exposed via [Suppressed]
	// so an operator can later clear the suppression.
	suppressed map[string]SuppressedServiceMessage
}

// SuppressedServiceMessage is one durably-suppressed channel parameter.
// It is the management-view counterpart of a [ServiceMessage]: after
// [ServiceMessages.Disable] suppresses a message it is recorded here so
// the operator can list and later clear ([ServiceMessages.Unsuppress])
// the suppression.
type SuppressedServiceMessage struct {
	InterfaceID string
	Channel     string
	Parameter   string
	DeviceName  string
	Name        string
}

// suppressKey builds the map key for the suppressed-parameter registry.
// The NUL separator cannot occur in any of the three components, so the
// key is collision-free.
func suppressKey(interfaceID, channel, parameter string) string {
	return interfaceID + "\x00" + channel + "\x00" + parameter
}

// NewServiceMessages constructs a ServiceMessages aggregate with no
// central scoping. Multi-CCU callers MUST use
// [NewServiceMessagesWithCentral].
func NewServiceMessages(ack MessageAcknowledger) *ServiceMessages {
	return NewServiceMessagesWithCentral("", ack)
}

// NewServiceMessagesWithCentral is the multi-CCU-safe constructor.
func NewServiceMessagesWithCentral(centralName string, ack MessageAcknowledger) *ServiceMessages {
	s := &ServiceMessages{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, "", "service_messages"),
		Ack:                 ack,
		messages:            map[string]ServiceMessage{},
		suppressed:          map[string]SuppressedServiceMessage{},
	}
	s.RegisterService("dismiss", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		id, err := payload.ParamString(params, "item_id")
		if err != nil {
			return err
		}
		return s.Acknowledge(ctx, id)
	})
	return s
}

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 service-messages aggregate is published to
// `<base>/<central>/hub/service_messages`. Read-only; no Set topic.
func (s *ServiceMessages) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	return payload.MQTTTopicSet{
		State: naming.MQTTHubServiceMessages(base, centralName),
	}
}

// List returns the current service messages sorted Timestamp-desc.
func (s *ServiceMessages) List() []ServiceMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServiceMessage, 0, len(s.messages))
	for id := range s.messages {
		out = append(out, s.messages[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	return out
}

// Count returns the active message count.
func (s *ServiceMessages) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// Observed reports whether the coordinator has ever delivered a set.
func (s *ServiceMessages) Observed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.observed
}

// Replace swaps the entire service-message set.
func (s *ServiceMessages) Replace(messages []ServiceMessage) {
	s.mu.Lock()
	next := make(map[string]ServiceMessage, len(messages))
	for i := range messages {
		next[messages[i].ID] = messages[i]
	}
	changed := !sameServiceSet(s.messages, next) || !s.observed
	s.messages = next
	s.observed = true
	cbs := make([]func(messages []ServiceMessage), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()

	if !changed {
		return
	}
	snapshot := s.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snapshot)
		}
	}
}

// SetAcknowledgers wires (or re-wires) the single- and bulk-message
// acknowledgers under the aggregate mutex. Use this from the hub-wiring
// adapter so a background WireHub recovery can re-apply them without
// racing a concurrent [ServiceMessages.Acknowledge] / [ServiceMessages.AcknowledgeAll]
// call.
func (s *ServiceMessages) SetAcknowledgers(single MessageAcknowledger, bulk BulkMessageAcknowledger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Ack = single
	s.BulkAck = bulk
}

func (s *ServiceMessages) acker() MessageAcknowledger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Ack
}

func (s *ServiceMessages) bulkAcker() BulkMessageAcknowledger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.BulkAck
}

// Acknowledge dispatches an acknowledge.
func (s *ServiceMessages) Acknowledge(ctx context.Context, id string) error {
	ack := s.acker()
	if ack == nil {
		return errors.New("service messages: no acknowledger configured")
	}
	if err := ack.AcknowledgeMessage(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	_, existed := s.messages[id]
	delete(s.messages, id)
	cbs := make([]func(messages []ServiceMessage), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()
	if !existed {
		return nil
	}
	snapshot := s.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snapshot)
		}
	}
	return nil
}

// AcknowledgeAll acknowledges every quittable service message on the CCU in
// one pass and returns the number acknowledged. On success the quittable
// entries are removed from the local set and the change callbacks fire once.
// Non-quittable messages are left in place (the CCU leaves them untouched
// too). A nil bulk acknowledger yields an error.
func (s *ServiceMessages) AcknowledgeAll(ctx context.Context) (int, error) {
	bulk := s.bulkAcker()
	if bulk == nil {
		return 0, errors.New("service messages: no bulk acknowledger configured")
	}
	n, err := bulk.AcknowledgeAllServiceMessages(ctx)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	changed := false
	for id := range s.messages {
		if s.messages[id].Quittable {
			delete(s.messages, id)
			changed = true
		}
	}
	cbs := make([]func(messages []ServiceMessage), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()
	if changed {
		snapshot := s.List()
		for _, cb := range cbs {
			if cb != nil {
				cb(snapshot)
			}
		}
	}
	return n, nil
}

// SetSuppressor wires (or re-wires) the durable service-message
// suppressor under the aggregate mutex. Use this from the hub-wiring
// adapter so a background WireHub recovery can re-apply it without racing
// a concurrent [ServiceMessages.Disable] / [ServiceMessages.Unsuppress]
// call.
func (s *ServiceMessages) SetSuppressor(sup ServiceMessageSuppressor) {
	s.mu.Lock()
	s.suppressor = sup
	s.mu.Unlock()
}

// Disable durably suppresses a single service message by id. Unlike
// [Acknowledge] (a one-shot dismiss), Disable calls the CCU's
// Interface.suppressServiceMessages so the message's channel parameter
// stops raising service messages until it is unsuppressed
// ([ServiceMessages.Unsuppress]). It resolves the target channel
// (Address) and parameter from the stored message, records the
// suppression for the management view, and removes the message from the
// active set. Returns an error when no suppressor is wired, the id is
// unknown, or the CCU call fails.
//
// Backs REST `POST .../service-messages/{id}/disable` and the WS
// `service_messages.disable` command.
func (s *ServiceMessages) Disable(ctx context.Context, id string) error {
	s.mu.RLock()
	msg, ok := s.messages[id]
	sup := s.suppressor
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("service messages: unknown message id %q", id)
	}
	if sup == nil {
		return errors.New("service messages: no suppressor configured")
	}
	if err := sup.SuppressServiceMessage(ctx, msg.InterfaceID, msg.Address, msg.Parameter, true); err != nil {
		return err
	}
	s.mu.Lock()
	s.suppressed[suppressKey(msg.InterfaceID, msg.Address, msg.Parameter)] = SuppressedServiceMessage{
		InterfaceID: msg.InterfaceID,
		Channel:     msg.Address,
		Parameter:   msg.Parameter,
		DeviceName:  msg.DeviceName,
		Name:        msg.Name,
	}
	_, existed := s.messages[id]
	delete(s.messages, id)
	cbs := make([]func(messages []ServiceMessage), len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.Unlock()
	if !existed {
		return nil
	}
	snapshot := s.List()
	for _, cb := range cbs {
		if cb != nil {
			cb(snapshot)
		}
	}
	return nil
}

// Unsuppress clears a previously-applied suppression for the given
// channel parameter via the CCU's Interface.suppressServiceMessages
// (suppress=false). An empty interfaceID is resolved from the stored
// suppression record when possible; an empty parameter targets every
// service parameter of the channel. On success the record is dropped
// from the management view. Returns an error when no suppressor is wired
// or the CCU call fails.
func (s *ServiceMessages) Unsuppress(ctx context.Context, interfaceID, channel, parameter string) error {
	s.mu.RLock()
	sup := s.suppressor
	if interfaceID == "" {
		for _, e := range s.suppressed {
			if e.Channel == channel && e.Parameter == parameter {
				interfaceID = e.InterfaceID
				break
			}
		}
	}
	s.mu.RUnlock()
	if sup == nil {
		return errors.New("service messages: no suppressor configured")
	}
	if err := sup.SuppressServiceMessage(ctx, interfaceID, channel, parameter, false); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.suppressed, suppressKey(interfaceID, channel, parameter))
	s.mu.Unlock()
	return nil
}

// Suppressed returns the durably-suppressed channel parameters for the
// management view, sorted by channel then parameter. When a suppressor
// is wired it reconciles the local record against the CCU's live
// Interface.getSuppressedServiceMessages so entries cleared elsewhere
// (e.g. via the CCU WebUI) drop out; a per-channel read error is
// tolerated and leaves that channel's records in place. Returns an empty
// slice when nothing is suppressed.
func (s *ServiceMessages) Suppressed(ctx context.Context) []SuppressedServiceMessage {
	s.mu.RLock()
	sup := s.suppressor
	entries := make([]SuppressedServiceMessage, 0, len(s.suppressed))
	for _, e := range s.suppressed {
		entries = append(entries, e)
	}
	s.mu.RUnlock()

	if sup != nil {
		// live holds the CCU's current suppressed-parameter set per
		// (interface, channel). A nil value marks a channel whose read
		// failed — its records are kept unconditionally.
		type chanKey struct{ iface, channel string }
		live := make(map[chanKey]map[string]bool)
		for _, e := range entries {
			k := chanKey{e.InterfaceID, e.Channel}
			if _, done := live[k]; done {
				continue
			}
			params, err := sup.GetSuppressedServiceMessages(ctx, e.InterfaceID, e.Channel)
			if err != nil {
				live[k] = nil
				continue
			}
			set := make(map[string]bool, len(params))
			for _, p := range params {
				set[p] = true
			}
			live[k] = set
		}
		kept := entries[:0]
		for _, e := range entries {
			set := live[chanKey{e.InterfaceID, e.Channel}]
			// Keep when the read failed (set == nil), when the record
			// targets all parameters (Parameter == ""), or when the CCU
			// still reports this parameter as suppressed.
			if set == nil || e.Parameter == "" || set[e.Parameter] {
				kept = append(kept, e)
			}
		}
		entries = kept
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Channel != entries[j].Channel {
			return entries[i].Channel < entries[j].Channel
		}
		return entries[i].Parameter < entries[j].Parameter
	})
	return entries
}

// SuppressedCount returns the number of recorded suppressions without
// contacting the CCU.
func (s *ServiceMessages) SuppressedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.suppressed)
}

// LegacyName returns the original pre-slug name stored on the CCU.
// ServiceMessages is a structural aggregate without a CCU-side variable
// name, so this always returns "".
func (*ServiceMessages) LegacyName() string { return "" }

// Description returns the optional human-readable description. ServiceMessages
// has no CCU-side description field, so this always returns "".
func (*ServiceMessages) Description() string { return "" }

// OnUpdate registers a change handler.
func (s *ServiceMessages) OnUpdate(fn func([]ServiceMessage)) func() {
	s.mu.Lock()
	s.callbacks = append(s.callbacks, fn)
	idx := len(s.callbacks) - 1
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if idx < len(s.callbacks) {
				s.callbacks[idx] = nil
			}
		})
	}
}

// sameAlarmSet reports whether two AlarmMessage sets are equivalent
// for change-detection. Messages must match by (ID, Timestamp,
// Counter) — equality of those three is a strong proxy for "no
// user-visible change".
func sameAlarmSet(a, b map[string]AlarmMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		left := a[id]
		right, ok := b[id]
		if !ok {
			return false
		}
		if !left.Timestamp.Equal(right.Timestamp) || left.Counter != right.Counter {
			return false
		}
	}
	return true
}

// sameServiceSet is the ServiceMessage equivalent of sameAlarmSet.
func sameServiceSet(a, b map[string]ServiceMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		left := a[id]
		right, ok := b[id]
		if !ok {
			return false
		}
		if !left.Timestamp.Equal(right.Timestamp) || left.Counter != right.Counter {
			return false
		}
	}
	return true
}

// QuittableCount returns the number of service messages that the operator can
// acknowledge.
func (s *ServiceMessages) QuittableCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for k := range s.messages {
		if s.messages[k].Quittable {
			n++
		}
	}
	return n
}

// LatestTimestamp returns the most recent timestamp across the
// current service-message set, or the zero value when the aggregate
// is empty. Used by the diagnostics page that shows "last incident
// xyz minutes ago".
func (s *ServiceMessages) LatestTimestamp() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest time.Time
	for k := range s.messages {
		if s.messages[k].Timestamp.After(latest) {
			latest = s.messages[k].Timestamp
		}
	}
	return latest
}

// LatestTimestamp is the alarm-side counterpart of
// [ServiceMessages.LatestTimestamp].
func (a *AlarmMessages) LatestTimestamp() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var latest time.Time
	for k := range a.messages {
		if a.messages[k].Timestamp.After(latest) {
			latest = a.messages[k].Timestamp
		}
	}
	return latest
}

// TranslationKey returns the HA translation key used to localise the
// alarm-messages sensor entity.
func (a *AlarmMessages) TranslationKey() string { return "alarm_messages" }

// DataType returns the CCU data type for the alarm-messages aggregate.
func (a *AlarmMessages) DataType() string { return "INTEGER" }

// Available reports whether the alarm-messages aggregate is ready to serve
// data. Returns true when at least one delivery has been made.
func (a *AlarmMessages) Available() bool { return a.Observed() }

// AdditionalInformation returns a slice of maps, one per active service
// message, containing the message's key fields.
func (s *ServiceMessages) AdditionalInformation() []map[string]any {
	msgs := s.List()
	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		entry := map[string]any{
			"id":          msgs[i].ID,
			"name":        msgs[i].Name,
			"address":     msgs[i].Address,
			"device_name": msgs[i].DeviceName,
			"type":        msgs[i].Type.String(),
			"quittable":   msgs[i].Quittable,
			"counter":     msgs[i].Counter,
			"timestamp":   msgs[i].Timestamp.Unix(),
		}
		if msgs[i].DisplayName != "" {
			entry["display_name"] = msgs[i].DisplayName
		}
		if len(msgs[i].Rooms) > 0 {
			entry["rooms"] = msgs[i].Rooms
		}
		if len(msgs[i].Functions) > 0 {
			entry["functions"] = msgs[i].Functions
		}
		if !msgs[i].LastTimestamp.IsZero() {
			entry["last_timestamp"] = msgs[i].LastTimestamp.Unix()
		}
		out = append(out, entry)
	}
	return out
}

// TranslationKey returns the HA translation key used to localise the
// service-messages sensor entity.
func (s *ServiceMessages) TranslationKey() string { return "service_messages" }

// AdditionalInformation returns a slice of maps, one per active alarm
// message, each containing the alarm's key fields. The map view is the
// machine-readable form consumed by REST and the diagnostic dumps.
func (a *AlarmMessages) AdditionalInformation() []map[string]any {
	msgs := a.List()
	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		entry := map[string]any{
			"id":        msgs[i].ID,
			"name":      msgs[i].Name,
			"counter":   msgs[i].Counter,
			"timestamp": msgs[i].Timestamp.Unix(),
		}
		if msgs[i].DisplayName != "" {
			entry["display_name"] = msgs[i].DisplayName
		}
		if !msgs[i].LastTimestamp.IsZero() {
			entry["last_timestamp"] = msgs[i].LastTimestamp.Unix()
		}
		out = append(out, entry)
	}
	return out
}

// AdditionalInformationIndexed returns the indexed-dict form of the
// alarm-messages list that HA's MQTT json_attributes_template expects:
// keys "alarm_1" .. "alarm_n" mapped to the alarm's Name (an alarm entry
// has no device to prefix — see [AlarmMessage]). The first alarm's index
// is 1 to match the human-counted reference; the dict follows the same
// order as [AlarmMessages.List] (timestamp-desc).
func (a *AlarmMessages) AdditionalInformationIndexed() map[string]string {
	msgs := a.List()
	out := make(map[string]string, len(msgs))
	for i := range msgs {
		key := fmt.Sprintf("alarm_%d", i+1)
		out[key] = msgs[i].Name
	}
	return out
}

// Counter returns the number of active alarm messages — i.e. the
// length of the message set. HA's MQTT statistics-template expects
// the active-alarm count, so summing per-entry trigger counters
// would surface a different number than the entity uses.
func (a *AlarmMessages) Counter() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.messages)
}

// AdditionalInformationIndexed returns the indexed-dict form of the
// service-messages list that HA's MQTT json_attributes_template expects:
// keys "message_1" .. "message_n" mapped to "<device_name>: <name>"
// strings. The first message's index is 1 to match the human-counted
// reference; the dict is returned in timestamp-desc order (same as
// [ServiceMessages.List]).
func (s *ServiceMessages) AdditionalInformationIndexed() map[string]string {
	msgs := s.List()
	out := make(map[string]string, len(msgs))
	for i := range msgs {
		key := fmt.Sprintf("message_%d", i+1)
		dev := msgs[i].DeviceName
		if dev == "" {
			dev = msgs[i].Address
		}
		out[key] = fmt.Sprintf("%s: %s", dev, msgs[i].Name)
	}
	return out
}
