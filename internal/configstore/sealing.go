// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/secret"
)

// sectionTarget returns a fresh pointer to the config sub-struct backing
// sec, or nil for sections that have no struct target ([SectionLocale],
// [SectionSecurity]). The struct is used only to derive the JSON paths of
// secret-tagged fields by reflection — it is never marshalled, so the
// stored payload keeps its original shape (no canonicalisation).
func sectionTarget(sec Section) any {
	switch sec {
	case SectionMQTT:
		return new(config.NorthMQTT)
	case SectionMatter:
		return new(config.NorthMatter)
	case SectionDiscovery:
		return new(config.NorthDiscovery)
	case SectionREST:
		return new(config.NorthREST)
	case SectionOIDC:
		return new(config.OIDCConfig)
	case SectionUI:
		return new(config.NorthUI)
	case SectionCallback:
		return new(config.CallbackConfig)
	case SectionCCUData:
		return new(config.CCUDataConfig)
	case SectionReliability:
		return new(config.ReliabilityConfig)
	case SectionPersistence:
		return new(config.PersistenceConfig)
	default:
		return nil
	}
}

// TransformSectionJSON seals (seal=true) or opens (seal=false) the
// secret-tagged fields inside a section's JSON payload. It is the
// field-aware bridge the persistence layer uses so the section store can
// encrypt without knowing struct shapes.
//
// It is non-destructive: only the values at secret-tagged JSON paths are
// rewritten; every other field — including any the daemon does not model —
// is preserved verbatim. The original bytes are returned unchanged when
// the section has no secret fields, when nothing actually changed
// (e.g. opening already-plaintext rows), or when the payload is not a JSON
// object. A nil/unavailable cipher transforms to a no-op.
func TransformSectionJSON(c *secret.Cipher, sec Section, raw []byte, seal bool) ([]byte, error) {
	tgt := sectionTarget(sec)
	if tgt == nil || len(raw) == 0 {
		return raw, nil
	}
	paths := secretPaths(tgt)
	if len(paths) == 0 {
		return raw, nil
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil //nolint:nilerr // non-object/foreign payloads pass through untouched
	}
	fn := c.Open
	if seal {
		fn = c.Seal
	}
	changed := false
	for _, p := range paths {
		if err := applyAtPath(m, p, fn, &changed); err != nil {
			return nil, err
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(m)
}

// secretPath locates one secret-tagged leaf within a section's JSON,
// addressed by its JSON keys. isMap marks a map[string]string field whose
// values (not the map itself) are the secrets.
type secretPath struct {
	keys  []string
	isMap bool
}

// secretPaths reflects over the section struct behind target and returns
// the JSON paths of every cfg:"secret" string field and map[string]string
// field. Non-string secret fields (e.g. a uint32 passcode) are skipped —
// see ADR 0027.
func secretPaths(target any) []secretPath {
	rt := reflect.TypeOf(target)
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	var out []secretPath
	collectSecretPaths(rt, nil, false, &out)
	return out
}

func collectSecretPaths(rt reflect.Type, prefix []string, inSecret bool, out *[]secretPath) {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return
	}
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if key == "-" {
			continue
		}
		if key == "" {
			key = f.Name
		}
		fieldSecret := inSecret || f.Tag.Get("cfg") == "secret"
		path := append(append([]string(nil), prefix...), key)

		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			collectSecretPaths(ft, path, fieldSecret, out)
		case reflect.String:
			if fieldSecret {
				*out = append(*out, secretPath{keys: path})
			}
		case reflect.Map:
			if fieldSecret && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String {
				*out = append(*out, secretPath{keys: path, isMap: true})
			}
		default:
		}
	}
}

// applyAtPath walks m to the secret leaf addressed by p and rewrites it
// through fn. A path that is absent in m is silently skipped. changed is
// set to true only when a value is actually rewritten.
func applyAtPath(m map[string]any, p secretPath, fn func(string) (string, error), changed *bool) error {
	cur := m
	for _, k := range p.keys[:len(p.keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	last := p.keys[len(p.keys)-1]
	v, ok := cur[last]
	if !ok {
		return nil
	}

	if p.isMap {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		for k, val := range mm {
			s, ok := val.(string)
			if !ok {
				continue
			}
			out, err := fn(s)
			if err != nil {
				return err
			}
			if out != s {
				mm[k] = out
				*changed = true
			}
		}
		return nil
	}

	s, ok := v.(string)
	if !ok {
		return nil
	}
	out, err := fn(s)
	if err != nil {
		return err
	}
	if out != s {
		cur[last] = out
		*changed = true
	}
	return nil
}
