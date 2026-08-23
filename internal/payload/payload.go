// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"maps"
	"reflect"
	"strings"
	"sync"
)

// Kind enumerates the payload categories. It mirrors the Python
// reference implementation's `Kind` enum; the wire-level names match
// the sibling projects.
type Kind string

// Kind values.
const (
	KindInfo   Kind = "info"
	KindConfig Kind = "config"
	KindState  Kind = "state"
)

// Options tweak the [For] / [ForWith] extraction.
type Options struct {
	// UseAltNames tells the extractor to prefer the tag's `alt=`
	// override over the field's lower-cased name.
	UseAltNames bool

	// IncludeZero retains zero-valued fields. Default (false) omits
	// them, matching the Python reference implementation's default
	// omit-zero behavior.
	IncludeZero bool
}

// For returns the k-partitioned view of obj. Nil / non-struct inputs
// produce an empty map.
func For(obj any, k Kind) map[string]any {
	return ForWith(obj, k, Options{})
}

// ForWith is [For] with explicit options.
//
// Read-only contract: values in the returned map are the field's live
// `any`-boxed value, not a deep copy. A field holding a map, slice, or
// pointer hands back a reference to the same backing storage obj owns.
// Callers must treat the returned map (and any composite value inside it)
// as read-only; mutating a nested map/slice mutates obj's field too. A
// deep copy is deliberately not taken here — payload extraction happens on
// every state publish, and obj's underlying data points are not shaped in
// a way that this call site would ever mutate them afterward.
// ExtraProperties lets a type contribute properties that reflection over
// its exported fields cannot see.
//
// The reason it exists: a field that several goroutines read while one
// writes it cannot stay a plain exported field — the readers tear. Moving
// such a field behind a mutex and an accessor makes it correct and, in the
// same stroke, invisible to [ForWith], which walks exported fields only.
// Losing a property that way is silent: the harvest simply returns one key
// fewer, the consumer falls back, and nothing fails. `Device.name` went
// through exactly that and dropped the device name out of every
// HA-Discovery device block.
//
// Implementations return the properties for kind k, using the same alt
// naming [Options.UseAltNames] selects, and honour opts.IncludeZero.
// Anything they return overrides a same-named reflected field.
type ExtraProperties interface {
	PayloadExtra(k Kind, opts Options) map[string]any
}

func ForWith(obj any, k Kind, opts Options) map[string]any {
	if obj == nil {
		return map[string]any{}
	}
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return map[string]any{}
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return map[string]any{}
	}
	desc := describe(v.Type())
	out := make(map[string]any, len(desc.fields))
	for _, f := range desc.fields {
		if f.kind != k {
			continue
		}
		val := v.FieldByIndex(f.index)
		if !opts.IncludeZero && isZero(val) {
			continue
		}
		name := f.name
		if opts.UseAltNames && f.altName != "" {
			name = f.altName
		}
		out[name] = val.Interface()
	}
	if ep, ok := obj.(ExtraProperties); ok {
		for name, val := range ep.PayloadExtra(k, opts) {
			out[name] = val
		}
	}
	return out
}

// Merge combines two maps into a fresh one. The second argument wins
// on key collisions.
func Merge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

// --- reflection cache ---

type fieldDesc struct {
	index   []int
	name    string
	altName string
	kind    Kind
}

type typeDesc struct {
	fields []fieldDesc
}

var descCache sync.Map // reflect.Type → *typeDesc

func describe(t reflect.Type) *typeDesc {
	if cached, ok := descCache.Load(t); ok {
		if td, ok := cached.(*typeDesc); ok {
			return td
		}
	}
	td := &typeDesc{}
	collectFields(t, nil, td)
	descCache.Store(t, td)
	return td
}

func collectFields(t reflect.Type, prefix []int, out *typeDesc) {
	for i := range t.NumField() {
		f := t.Field(i)
		idx := append(append([]int(nil), prefix...), i)

		// Embedded structs recurse. Tagged embedded fields are
		// treated as their own entry — matching the Python reference
		// implementation's treatment of wrapper classes.
		if f.Anonymous && f.Tag.Get("payload") == "" {
			if f.Type.Kind() == reflect.Struct {
				collectFields(f.Type, idx, out)
			}
			continue
		}
		tag := f.Tag.Get("payload")
		if tag == "" || tag == "-" {
			continue
		}
		kindStr, rest, _ := strings.Cut(tag, ",")
		k := Kind(strings.TrimSpace(kindStr))
		switch k {
		case KindInfo, KindConfig, KindState:
		default:
			continue
		}
		altName := ""
		for opt := range strings.SplitSeq(rest, ",") {
			opt = strings.TrimSpace(opt)
			if after, ok := strings.CutPrefix(opt, "alt="); ok {
				altName = after
			}
		}
		out.fields = append(out.fields, fieldDesc{
			index:   idx,
			name:    strings.ToLower(f.Name),
			altName: altName,
			kind:    k,
		})
	}
}

func isZero(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() { //nolint:exhaustive // complex/unsafeptr handled by default branch
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	case reflect.String:
		return v.Len() == 0
	case reflect.Array:
		return v.Len() == 0
	case reflect.Struct:
		return v.IsZero()
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	}
	return false
}
