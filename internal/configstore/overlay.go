// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configstore

import (
	"encoding/json"
	"reflect"
	"strings"
)

// overlaySection decodes a section payload onto dst, which points at the
// section's sub-tree of a [config.Config].
//
// The overlay rule is "a key the payload omits keeps its stored value, a
// key the payload carries is authoritative". encoding/json honours the
// first half for every kind, but breaks the second half for maps: it
// decodes a JSON object into an already-populated Go map by *adding* to
// it, so entries the payload dropped survive. A section PUT that removes
// one entry from north.rest.auth.ccu.role_mapping would then persist the
// union of old and new — the removal is reported as saved and is back on
// the next load. Clearing every map the payload speaks about before the
// decode makes both halves of the rule hold.
func overlaySection(raw []byte, dst any) error {
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err == nil {
		clearPresentMaps(reflect.ValueOf(dst), tree)
	}
	return json.Unmarshal(raw, dst)
}

// clearPresentMaps zeroes every map-typed field of the struct v that the
// decoded payload tree carries a key for, recursing into nested structs
// along the keys the payload actually names. Fields the payload does not
// mention are left alone, which is what preserves the stored value.
func clearPresentMaps(v reflect.Value, tree map[string]any) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || len(tree) == 0 {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, ok := jsonFieldName(f)
		if !ok {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous && name == "" {
			// An embedded struct without a json name flattens into the
			// parent object, so its fields are addressed by the same tree.
			clearPresentMaps(fv, tree)
			continue
		}
		child, present := lookupJSONKey(tree, name)
		if !present {
			continue
		}
		switch fv.Kind() {
		case reflect.Map:
			if !fv.IsNil() && fv.CanSet() {
				fv.Set(reflect.Zero(fv.Type()))
			}
		case reflect.Struct, reflect.Pointer:
			if sub, isObject := child.(map[string]any); isObject {
				clearPresentMaps(fv, sub)
			}
		default:
			// Slices, scalars and interfaces are replaced wholesale by
			// encoding/json already.
		}
	}
}

// jsonFieldName returns the object key encoding/json uses for f, and
// false when the field is skipped (`json:"-"`).
func jsonFieldName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		if f.Anonymous {
			return "", true
		}
		name = f.Name
	}
	return name, true
}

// lookupJSONKey resolves a field name against a decoded payload the way
// encoding/json does: exact match first, then case-insensitive.
func lookupJSONKey(tree map[string]any, name string) (any, bool) {
	if v, ok := tree[name]; ok {
		return v, true
	}
	for k, v := range tree {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}
