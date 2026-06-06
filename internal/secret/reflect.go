// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package secret

import (
	"errors"
	"reflect"
)

// SealStruct walks v (a non-nil pointer to a struct) and seals, in
// place, every leaf field tagged `cfg:"secret"` that is a string or a
// map[string]string value. Nested structs, pointers, and slices are
// recursed. Non-string secret fields (e.g. a uint32 passcode) are left
// unchanged — see ADR 0027. A nil/unavailable Cipher is a no-op.
func (c *Cipher) SealStruct(v any) error { return walkSecrets(v, c.Seal) }

// OpenStruct is the inverse of [Cipher.SealStruct]: it opens every
// sealed secret-tagged leaf field in place. Plaintext (unprefixed)
// values pass through unchanged.
func (c *Cipher) OpenStruct(v any) error { return walkSecrets(v, c.Open) }

func walkSecrets(v any, transform func(string) (string, error)) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("secret: SealStruct/OpenStruct needs a non-nil pointer")
	}
	return walkValue(rv.Elem(), false, transform)
}

// walkValue recurses through rv. secret carries down whether the
// enclosing field was tagged cfg:"secret"; since secret tags only ever
// sit on leaf fields in this config tree, the propagation never
// over-reaches a nested sub-tree.
func walkValue(rv reflect.Value, secret bool, transform func(string) (string, error)) error {
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return walkValue(rv.Elem(), secret, transform)
	case reflect.Struct:
		rt := rv.Type()
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			fieldSecret := secret || f.Tag.Get("cfg") == "secret"
			if err := walkValue(rv.Field(i), fieldSecret, transform); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			if err := walkValue(rv.Index(i), secret, transform); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		if !secret || !rv.CanSet() {
			return nil
		}
		out, err := transform(rv.String())
		if err != nil {
			return err
		}
		rv.SetString(out)
		return nil
	case reflect.Map:
		if !secret {
			return nil
		}
		if rv.Type().Key().Kind() == reflect.String && rv.Type().Elem().Kind() == reflect.String {
			for _, k := range rv.MapKeys() {
				out, err := transform(rv.MapIndex(k).String())
				if err != nil {
					return err
				}
				rv.SetMapIndex(k, reflect.ValueOf(out))
			}
		}
		return nil
	default:
		return nil
	}
}
