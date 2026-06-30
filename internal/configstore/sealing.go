// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
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
	case SectionWebhook:
		// Has the north.webhook.secret signing key (cfg:"secret"), so the
		// section store seals it at rest by reflection.
		return new(config.NorthWebhook)
	case SectionREST:
		return new(config.NorthREST)
	case SectionOIDC:
		return new(config.OIDCConfig)
	case SectionCCUAuth:
		// No secret fields (credentials come from the login form, not
		// config), so secretPaths is empty and TransformSectionJSON
		// no-ops — the target is returned only to keep the config-backed
		// sections symmetric with applySection/marshalSection.
		return new(config.CCUAuthConfig)
	case SectionHAIngress:
		// No secret fields — trust is by network + build stamp, not a
		// stored credential. Returned only for section symmetry.
		return new(config.HAIngressConfig)
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
	changed := false
	for _, p := range paths {
		if err := applyAtPath(m, p, c, seal, &changed); err != nil {
			return nil, err
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(m)
}

// pathKind classifies the JSON value type at a secret leaf so the
// transform round-trips it correctly.
type pathKind int

const (
	kindString pathKind = iota // a string leaf
	kindMap                    // a map[string]string whose values are secrets
	kindNumber                 // an integer leaf, sealed as its decimal string
)

// secretPath locates one secret-tagged leaf within a section's JSON,
// addressed by its JSON keys.
type secretPath struct {
	keys []string
	kind pathKind
}

// secretPaths reflects over the section struct behind target and returns
// the JSON paths of every cfg:"secret" leaf that can be encrypted: string
// fields, map[string]string fields, and integer fields. Integer secrets
// (e.g. the Matter commissioning passcode) are sealed as their decimal
// string and decoded back to a number on open — see ADR 0027.
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
		switch {
		case ft.Kind() == reflect.Struct:
			collectSecretPaths(ft, path, fieldSecret, out)
		case !fieldSecret:
			// non-secret leaf — nothing to collect
		case ft.Kind() == reflect.String:
			*out = append(*out, secretPath{keys: path, kind: kindString})
		case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && ft.Elem().Kind() == reflect.String:
			*out = append(*out, secretPath{keys: path, kind: kindMap})
		case isIntegerKind(ft.Kind()):
			*out = append(*out, secretPath{keys: path, kind: kindNumber})
		}
	}
}

func isIntegerKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

// applyAtPath walks m to the secret leaf addressed by p and seals/opens it.
// A path that is absent is silently skipped. changed is set only when a
// value is actually rewritten, so an unchanged value never alters the JSON
// — including its type, which matters for numeric leaves (a number stays a
// number until it is actually sealed into a string).
func applyAtPath(m map[string]any, p secretPath, c *secret.Cipher, seal bool, changed *bool) error {
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

	switch p.kind {
	case kindString:
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out, err := transform(c, seal, s)
		if err != nil {
			return err
		}
		if out != s {
			cur[last] = out
			*changed = true
		}
	case kindMap:
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		for k, val := range mm {
			s, ok := val.(string)
			if !ok {
				continue
			}
			out, err := transform(c, seal, s)
			if err != nil {
				return err
			}
			if out != s {
				mm[k] = out
				*changed = true
			}
		}
	case kindNumber:
		return applyNumber(cur, last, v, c, seal, changed)
	}
	return nil
}

// applyNumber handles an integer secret leaf. On seal the JSON number is
// formatted to its decimal string and encrypted (the leaf becomes a
// string); on open the sealed string is decrypted and parsed back to a
// number. A value already in the target representation is left untouched.
func applyNumber(cur map[string]any, key string, v any, c *secret.Cipher, seal bool, changed *bool) error {
	if seal {
		n, ok := v.(float64)
		if !ok { // already a sealed string (or absent) — nothing to seal
			return nil
		}
		s := strconv.FormatInt(int64(n), 10)
		out, err := c.Seal(s)
		if err != nil {
			return err
		}
		if out != s {
			cur[key] = out
			*changed = true
		}
		return nil
	}
	s, ok := v.(string)
	if !ok { // plaintext number — nothing to open
		return nil
	}
	out, err := c.Open(s)
	if err != nil {
		return err
	}
	if out == s { // not sealed — leave as-is
		return nil
	}
	n, perr := strconv.ParseInt(out, 10, 64)
	if perr != nil {
		return fmt.Errorf("configstore: decoded numeric secret %q: %w", key, perr)
	}
	cur[key] = float64(n)
	*changed = true
	return nil
}

// transform applies the cipher in the requested direction.
func transform(c *secret.Cipher, seal bool, s string) (string, error) {
	if seal {
		return c.Seal(s)
	}
	return c.Open(s)
}
