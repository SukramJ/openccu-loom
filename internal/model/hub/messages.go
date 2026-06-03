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
type AlarmMessage struct {
	ID          string
	Name        string
	Description string
	DeviceName  string
	// Address is the CCU device/channel address that generated the alarm. May be
	// empty for legacy or synthesised alarms where the CCU does not report a
	// channel address.
	Address string
	// StateValue is the raw alarm state string as reported by the CCU Rega
	// script.
	StateValue  string
	Timestamp   time.Time
	Counter     int
	LastTrigger string
	Rooms       []string
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

	Ack MessageAcknowledger

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

// Acknowledge dispatches an acknowledge for id through the writer and
// removes the entry from the local set.
func (a *AlarmMessages) Acknowledge(ctx context.Context, id string) error {
	if a.Ack == nil {
		return errors.New("alarm messages: no acknowledger configured")
	}
	if err := a.Ack.AcknowledgeMessage(ctx, id); err != nil {
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
	Type       hmenum.ServiceMessageType
	// Description is the optional human-readable message text returned by
	// the CCU Rega script.
	Description string
	// Priority is the integer priority level from the CCU (0 = normal, higher
	// values = more critical).
	Priority  int
	Timestamp time.Time
	Counter   int
	Rooms     []string
	Functions []string
	Quittable bool
}

// ServiceMessages aggregates service messages.
//
// ServiceMessages embeds [datapoint.BaseDataPointFields] mirroring
// CallbackDataPoint. The promoted lifecycle / timestamp methods are
// thereby activated. Multi-CCU callers MUST use
// [NewServiceMessagesWithCentral].
type ServiceMessages struct {
	datapoint.BaseDataPointFields

	Ack MessageAcknowledger

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each ServiceMessages instance gets its own registry so the dismiss
	// service is registered per-instance.
	payload.ServiceRegistry

	mu        sync.RWMutex
	messages  map[string]ServiceMessage
	observed  bool
	callbacks []func(messages []ServiceMessage)
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

// Acknowledge dispatches an acknowledge.
func (s *ServiceMessages) Acknowledge(ctx context.Context, id string) error {
	if s.Ack == nil {
		return errors.New("service messages: no acknowledger configured")
	}
	if err := s.Ack.AcknowledgeMessage(ctx, id); err != nil {
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
			"priority":    msgs[i].Priority,
			"quittable":   msgs[i].Quittable,
			"counter":     msgs[i].Counter,
			"timestamp":   msgs[i].Timestamp.Unix(),
		}
		if msgs[i].Description != "" {
			entry["description"] = msgs[i].Description
		}
		if len(msgs[i].Rooms) > 0 {
			entry["rooms"] = msgs[i].Rooms
		}
		if len(msgs[i].Functions) > 0 {
			entry["functions"] = msgs[i].Functions
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
			"id":          msgs[i].ID,
			"name":        msgs[i].Name,
			"address":     msgs[i].Address,
			"device_name": msgs[i].DeviceName,
			"state_value": msgs[i].StateValue,
			"counter":     msgs[i].Counter,
			"timestamp":   msgs[i].Timestamp.Unix(),
		}
		if len(msgs[i].Rooms) > 0 {
			entry["rooms"] = msgs[i].Rooms
		}
		out = append(out, entry)
	}
	return out
}

// AdditionalInformationIndexed returns the indexed-dict form of the
// alarm-messages list that HA's MQTT json_attributes_template expects:
// keys "alarm_1" .. "alarm_n" mapped to "<device_name>: <name>"
// strings. The first alarm's index is 1 to match the human-counted
// reference; the dict is returned in deterministic ID order.
func (a *AlarmMessages) AdditionalInformationIndexed() map[string]string {
	msgs := a.List()
	out := make(map[string]string, len(msgs))
	for i := range msgs {
		key := fmt.Sprintf("alarm_%d", i+1)
		dev := msgs[i].DeviceName
		if dev == "" {
			dev = msgs[i].Address
		}
		out[key] = fmt.Sprintf("%s: %s", dev, msgs[i].Name)
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
