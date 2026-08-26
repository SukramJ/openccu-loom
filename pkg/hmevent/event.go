// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmevent

import "time"

// EventType is the stable tag of a domain event. Its string form is
// used in metrics labels and log fields.
type EventType string

// Event is implemented by every domain-event struct.
type Event interface {
	// Type returns the event's stable tag.
	Type() EventType
	// Timestamp returns the publish time. Not wall time — may have been
	// captured inside a handler to preserve causal order across bus
	// handlers.
	Timestamp() time.Time
	// EventKey returns an optional filter key. Subscribers that registered with
	// `events.WithKey(...)` only fire when the event's EventKey matches.
	// Returning "" disables key-filtering for that event.
	//
	// We deliberately do *not* call this method `Key()` so that concrete event
	// structs which already carry a `Key` field (e.g.
	// `DataPointValueChangedEvent.Key`) keep working without renaming.
	EventKey() string
}

// Base is embedded by every concrete event struct to satisfy the
// [Event] interface. Constructors should call NewBase() to initialise
// it; zero-value events are allowed but carry a zero timestamp.
//
// `Base.EventKey()` returns "" by default. Concrete event types that
// want to participate in key-filtering override `EventKey()` on the
// embedding struct (Go method promotion does the right thing).
type Base struct {
	ts time.Time
}

// NewBase returns a Base with the current wall time.
func NewBase() Base { return Base{ts: time.Now()} }

// NewBaseAt returns a Base stamped with t.
func NewBaseAt(t time.Time) Base { return Base{ts: t} }

// Timestamp implements Event.
func (b Base) Timestamp() time.Time { return b.ts }

// EventKey returns "" — the default for events that do not participate
// in key-filtering. Override on concrete event types to enable it.
func (b Base) EventKey() string { return "" }
