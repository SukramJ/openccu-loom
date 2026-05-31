// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"fmt"
	"reflect"
	"strings"
)

// FieldClass describes how a config field is exposed to the SPA's
// Settings surface and to the Wave-C config-write endpoints.
type FieldClass string

const (
	// FieldBasic is the everyday-operator surface. Shown without
	// any expert-mode opt-in.
	FieldBasic FieldClass = "basic"

	// FieldExpert is the deep-tuning surface. Hidden behind the
	// SPA's expert-mode toggle. Examples: reliability tunables,
	// callback port ranges, Matter sigma parameters, custom
	// logging subsystem overrides.
	FieldExpert FieldClass = "expert"

	// FieldSecret marks fields whose value the daemon never
	// persists in YAML or DB. The SPA shows a placeholder + an
	// env-variable-name resolver; rotation goes via env or a
	// dedicated secret-store. Examples: CCU passwords, OIDC
	// client_secret, MQTT password, TLS private keys.
	FieldSecret FieldClass = "secret"
)

// FieldDescriptor describes one struct field for the schema endpoint
// and contract tests. Fields without a yaml tag are reported under
// their Go name; nested structs flatten with dot-separated paths.
type FieldDescriptor struct {
	// Path is the dot-separated YAML path
	// (e.g. "north.mqtt.broker_url").
	Path string
	// Class is the classification tag value.
	Class FieldClass
	// GoType is the underlying Go type name for diagnostics.
	GoType string
	// Source identifies the Go field origin
	// (e.g. "BootstrapConfig.DataDir") for debug/contract test
	// failure messages.
	Source string
}

// ClassifyFields walks v with reflection and returns one
// [FieldDescriptor] per leaf field. Sub-structs are recursed into
// using their yaml tag as the path prefix; slices and maps are
// reported as leaves (their elements are not introspected, which is
// the right call for [map[string]string] credential tables).
//
// Fields without a `cfg:"..."` tag are reported with an empty
// Class — callers (typically the contract test) use this to detect
// unclassified fields.
//
// v must be a struct or a pointer to a struct; passing anything
// else returns nil to keep the helper test-safe.
func ClassifyFields(v any) []FieldDescriptor {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	rt := rv.Type()
	var out []FieldDescriptor
	walkStruct(rt, "", rt.Name(), &out)
	return out
}

// walkStruct recurses into a struct type, appending one descriptor
// per field. Anonymous (embedded) fields are inlined; named
// sub-struct fields prefix their yaml tag onto the path.
func walkStruct(rt reflect.Type, pathPrefix, source string, out *[]FieldDescriptor) {
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		yamlTag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if yamlTag == "-" {
			continue
		}
		if yamlTag == "" {
			// Honour anonymous embedding by inlining; named fields
			// without a tag fall back to the Go name.
			if f.Anonymous {
				walkStruct(f.Type, pathPrefix, source+"."+f.Name, out)
				continue
			}
			yamlTag = f.Name
		}
		path := yamlTag
		if pathPrefix != "" {
			path = pathPrefix + "." + yamlTag
		}
		ft := derefType(f.Type)
		fieldSource := source + "." + f.Name
		// Recurse into named struct sub-trees; skip map /
		// time.Duration / primitive types.
		if ft.Kind() == reflect.Struct && !isOpaqueStruct(ft) {
			walkStruct(ft, path, fieldSource, out)
			continue
		}
		// Slices / arrays of struct: emit the leaf for the slice
		// field itself AND descend into the element type so the
		// schema endpoint can describe per-element fields (e.g.
		// centrals[].password). Map values are intentionally left
		// opaque — they carry credential tables whose values are
		// not part of the operator-facing field schema.
		if (ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array) &&
			derefType(ft.Elem()).Kind() == reflect.Struct &&
			!isOpaqueStruct(derefType(ft.Elem())) {
			*out = append(*out, FieldDescriptor{
				Path:   path,
				Class:  FieldClass(f.Tag.Get("cfg")),
				GoType: f.Type.String(),
				Source: fieldSource,
			})
			walkStruct(derefType(ft.Elem()), path, fieldSource+"[]", out)
			continue
		}
		*out = append(*out, FieldDescriptor{
			Path:   path,
			Class:  FieldClass(f.Tag.Get("cfg")),
			GoType: f.Type.String(),
			Source: fieldSource,
		})
	}
}

// derefType unwraps pointers so we can introspect the underlying
// struct shape (handles `*bool`, `*Sub` etc.).
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// isOpaqueStruct returns true for struct types we treat as leaves —
// types whose internals are not part of the operator-facing schema.
// Currently only time.Duration falls into this bucket; everything
// else under our own config tree is recursed into.
func isOpaqueStruct(t reflect.Type) bool {
	return t.PkgPath() == "time"
}

// UnclassifiedFields filters the descriptors to those without a cfg
// tag — used by the contract test to fail when a new field lands
// without classification.
func UnclassifiedFields(desc []FieldDescriptor) []FieldDescriptor {
	var out []FieldDescriptor
	for _, d := range desc {
		switch d.Class {
		case FieldBasic, FieldExpert, FieldSecret:
			continue
		default:
			out = append(out, d)
		}
	}
	return out
}

// FormatUnclassifiedError renders an actionable error message
// listing fields the contract test rejected.
func FormatUnclassifiedError(unclassified []FieldDescriptor) string {
	if len(unclassified) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config: %d field(s) missing cfg:\"basic|expert|secret\" tag:\n", len(unclassified))
	for _, d := range unclassified {
		b.WriteString("  - ")
		b.WriteString(d.Path)
		b.WriteString("  (")
		b.WriteString(d.Source)
		b.WriteString(", ")
		b.WriteString(d.GoType)
		b.WriteString(")\n")
	}
	return b.String()
}
