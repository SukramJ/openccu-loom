// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproperty

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Descriptor records the metadata attached to a single exported field
// that carries a `payload` struct tag in openccu-loom's domain model.
//
// Mirrors the _GenericProperty / DelegatedProperty metadata in
// Py.
type Descriptor struct {
	// FieldName is the Go exported field name.
	FieldName string
	// Kind is the property category.
	Kind Kind
	// LogContext reports whether this field should be included in
	// structured log context collections.
	LogContext bool
	// AltName is the alternative key used in JSON payloads when set.
	// If empty the FieldName is used.
	AltName string
}

// Key returns the key used for payload maps: AltName when set,
// otherwise FieldName.
func (d Descriptor) Key() string {
	if d.AltName != "" {
		return d.AltName
	}
	return d.FieldName
}

// --------------------------------------------------------------------------
// Per-type cache
// --------------------------------------------------------------------------

// descriptorCache stores the computed descriptor slices per type so
// that the reflect.Type.NumField scan runs only once per concrete type.
var (
	cacheMu sync.RWMutex
	cache   = make(map[reflect.Type][]Descriptor)
)

func descriptorsFor(t reflect.Type) []Descriptor {
	// Dereference pointer types.
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	cacheMu.RLock()
	if ds, ok := cache[t]; ok {
		cacheMu.RUnlock()
		return ds
	}
	cacheMu.RUnlock()

	// Compute descriptors from struct tags.
	var ds []Descriptor
	if t.Kind() == reflect.Struct {
		for f := range t.Fields() {
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("payload")
			if tag == "" || tag == "-" {
				continue
			}
			d := Descriptor{FieldName: f.Name}
			for part := range strings.SplitSeq(tag, ",") {
				part = strings.TrimSpace(part)
				switch part {
				case "config":
					d.Kind = KindConfig
				case "info":
					d.Kind = KindInfo
				case "state":
					d.Kind = KindState
				case "simple":
					d.Kind = KindSimple
				case "log_context":
					d.LogContext = true
				default:
					if after, ok := strings.CutPrefix(part, "alt="); ok {
						d.AltName = after
					} else if d.Kind == "" {
						// First unrecognised non-empty token is treated
						// as the kind string for forward-compat.
						d.Kind = Kind(part)
					}
				}
			}
			if d.Kind == "" {
				d.Kind = KindSimple
			}
			ds = append(ds, d)
		}
	}

	cacheMu.Lock()
	cache[t] = ds
	cacheMu.Unlock()

	return ds
}

// --------------------------------------------------------------------------
// GetPropertyByKind
// --------------------------------------------------------------------------

// GetPropertyByKind collects all fields of dataObject whose `payload`
// tag matches kind and returns them as a key→value map.
//
// When logContextOnly is true only fields tagged with "log_context" are
// Returned
// get_hm_property_by_kind.
//
// Values are normalised via [NormalizeValue] for consistent
// JSON / log representations.
//
// (property_decorators.py).
func GetPropertyByKind(dataObject any, kind Kind, logContextOnly bool) map[string]any {
	if dataObject == nil {
		return nil
	}
	v := reflect.ValueOf(dataObject)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil
	}
	ds := descriptorsFor(v.Type())
	out := make(map[string]any, len(ds))
	for _, d := range ds {
		if d.Kind != kind {
			continue
		}
		if logContextOnly && !d.LogContext {
			continue
		}
		fv := v.FieldByName(d.FieldName)
		if !fv.IsValid() {
			continue
		}
		out[d.Key()] = NormalizeValue(fv.Interface())
	}
	return out
}

// GetPropertyByLogContext returns the combined log-context attributes
// across all property kinds. It is the equivalent of iterating over
// every Kind and calling GetPropertyByKind with logContextOnly=true.
//
// (property_decorators.py).
func GetPropertyByLogContext(dataObject any) map[string]any {
	out := make(map[string]any)
	for _, k := range AllKinds {
		maps.Copy(out, GetPropertyByKind(dataObject, k, true))
	}
	return out
}

// --------------------------------------------------------------------------
// NormalizeValue
// --------------------------------------------------------------------------

// NormalizeValue converts v to a stable, JSON/log-friendly type.
//
// - []T, [N]T, map → recursively normalised - fmt.Stringer → string via
// .String() - time.Time → Unix timestamp (float64) - everything else →
// unchanged
func NormalizeValue(v any) any {
	if v == nil {
		return nil
	}
	if t, ok := v.(time.Time); ok {
		return float64(t.Unix())
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // only collection kinds need recursion
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = NormalizeValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			out[fmt.Sprintf("%v", k.Interface())] = NormalizeValue(rv.MapIndex(k).Interface())
		}
		return out
	}
	return v
}
