// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package orderedjson models a JSON value whose object member order is
// preserved, and serialises it byte-for-byte the way Python's orjson
// (OPT_INDENT_2) does.
//
// The package exists for one reason: the device-definition export must be
// indistinguishable from the Python reference's `export_device_definition`, whose
// output is consumed verbatim as godevccu fixtures. The Python reference
// emits the raw CCU descriptions in wire order via orjson, so we must (a)
// keep the member order the CCU sent and (b) reproduce orjson's exact
// formatting — including its float repr, which differs from Go's strconv.
//
// A [Value] is one of: nil, bool, any signed/unsigned integer, float64,
// string, [Array], or *[Object]. Anything else makes [Marshal] return an
// error.
package orderedjson

// Member is one key/value pair inside an [Object]. The CCU does not emit
// duplicate keys; [Object] preserves insertion order rather than enforcing
// uniqueness so the wire shape survives untouched.
type Member struct {
	Key   string
	Value any
}

// Object is a JSON object that remembers the order its members were added,
// mirroring a Python dict's insertion order (which is what orjson serialises).
type Object struct {
	Members []Member
}

// NewObject returns an empty object with capacity hint n.
func NewObject(n int) *Object {
	return &Object{Members: make([]Member, 0, n)}
}

// Set appends key/value in order. It does not deduplicate — callers feed it
// the CCU's member stream, which is already unique.
func (o *Object) Set(key string, value any) *Object {
	o.Members = append(o.Members, Member{Key: key, Value: value})
	return o
}

// Len reports the member count.
func (o *Object) Len() int { return len(o.Members) }

// Update replaces the value of the first member with the given key, keeping
// its position, and reports whether the key was present. It never adds a key —
// the anonymiser only rewrites existing ADDRESS/PARENT/CHILDREN members so the
// wire member order is untouched.
func (o *Object) Update(key string, value any) bool {
	for i := range o.Members {
		if o.Members[i].Key == key {
			o.Members[i].Value = value
			return true
		}
	}
	return false
}

// Get returns the first value stored under key and whether it was present.
// Linear scan — objects here are small (a single paramset/description).
func (o *Object) Get(key string) (any, bool) {
	for i := range o.Members {
		if o.Members[i].Key == key {
			return o.Members[i].Value, true
		}
	}
	return nil, false
}

// Array is a JSON array. Elements are [Value]s.
type Array []any
