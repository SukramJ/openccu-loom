// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	hapublisher "github.com/SukramJ/go-hamqtt/publisher"
)

// statusExtKey is this daemon's one project-extension key in every
// mqtt-smarthome 2.0 status object (spec §5.2, ADR 0083): the fields that
// are Homematic's rather than the convention's — a data point's `available`
// flag and its `additional_information`, an event's `available` marker, the
// security plane's facets — travel under it, so a consumer can tell project
// fields from future spec fields.
const statusExtKey = "hm"

// statusValue is the `val` of a status object whose value arrives as bytes a
// publisher already rendered: a JSON document is taken as is, so a document
// without a primary value is `val` whole; anything else — a bare Home
// Assistant token such as an alarm state, which was never JSON on the wire —
// is a JSON string. ON/OFF is the convention's boolean (§5.1).
func statusValue(body []byte) any {
	trimmed := bytes.TrimSpace(body)
	switch string(trimmed) {
	case "ON":
		return true
	case "OFF":
		return false
	}
	if json.Valid(trimmed) {
		return json.RawMessage(bytes.Clone(trimmed))
	}
	return string(trimmed)
}

// statusClock remembers, per topic, the `val` a status object last carried
// and when it changed, which is what `lc` is (spec §5.2). It serves the
// planes that render their own status objects instead of publishing through
// [hapublisher.StatePublisher.PublishStatus] — the per-data-point plane takes
// `ts` and `lc` from the domain, but the custom-DP aggregates and the
// device snapshots carry no change time of their own.
//
// Unlike the shared publisher's memory it gates nothing: a publish that
// went out before still goes out, so moving to the status object does not
// change how often any plane publishes.
type statusClock struct {
	mu   sync.Mutex
	last map[string]statusClockEntry
}

type statusClockEntry struct {
	val []byte
	lc  int64
}

// observe records val for topic at ts and returns the `lc` to publish: the
// previous change time while the value stands still, ts when it moved.
func (c *statusClock) observe(topic string, val []byte, ts int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = make(map[string]statusClockEntry)
	}
	prev, ok := c.last[topic]
	lc := ts
	if ok && bytes.Equal(prev.val, val) && prev.lc <= ts {
		lc = prev.lc
	}
	c.last[topic] = statusClockEntry{val: bytes.Clone(val), lc: lc}
	return lc
}

// forget drops topic, so the next observation starts a new `lc`.
func (c *statusClock) forget(topics ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range topics {
		delete(c.last, t)
	}
}

// statusExt is hm as the status object's extension, or nil when hm holds a
// nil map, slice or pointer. The shared renderer omits the key only for an
// untyped nil, and a typed nil marshals to `"hm":null` — which the event
// template's `value_json.hm or {}` survives, but which no consumer should
// have to: an absent extension is spelled by its absence.
func statusExt(hm any) any {
	if hm == nil {
		return nil
	}
	switch v := reflect.ValueOf(hm); v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
	default:
	}
	return hm
}

// renderStatus renders one status object through the shared
// [hapublisher.StatusObject], so this daemon's bytes are the convention's
// bytes, with `lc` from the bridge's [statusClock]. at is the observation
// time; zero means now.
func (b *Bridge) renderStatus(topic string, val, hm any, at time.Time) ([]byte, error) {
	raw, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = time.Now()
	}
	ts := at.UnixMilli()
	lc := b.clock.observe(topic, raw, ts)
	return hapublisher.StatusObject{
		Val: json.RawMessage(raw), TS: ts, LC: lc,
		ExtKey: statusExtKey, Ext: statusExt(hm),
	}.JSON()
}

// renderPulse renders one occurrence — an event, an impulse, a device
// error — as a status object: the occurrence's type in `val`, `ts` and `lc`
// both the occurrence's time (zero means now), because every occurrence is
// a change.
func renderPulse(val, hm any, at time.Time) ([]byte, error) {
	if at.IsZero() {
		at = time.Now()
	}
	ms := at.UnixMilli()
	return hapublisher.StatusObject{
		Val: val, TS: ms, LC: ms,
		ExtKey: statusExtKey, Ext: statusExt(hm),
	}.JSON()
}

// perDPExtension is the `hm` extension of a data point's status object: the
// Homematic fields the convention has no key for.
type perDPExtension struct {
	Available             bool           `json:"available"`
	AdditionalInformation map[string]any `json:"additional_information,omitempty"`
}

// pulseExtension is the `hm` extension of a channel event, impulse or device
// error: an occurrence is reported by a reachable device, so `available` is
// always true, kept for the consumers that read it beside the type.
type pulseExtension struct {
	Available bool `json:"available"`
}
