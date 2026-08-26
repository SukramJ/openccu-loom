// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"bytes"
	"encoding/json"
	"html"
)

// UnescapeUIText decodes the HTML character references embedded in the
// CCU's own display strings.
//
// The extracts are lifted from the CCU WebUI, whose texts are HTML
// fragments: a profile called "Bewässerungsaktor" is stored as
// "Bew&auml;sserungsaktor", and "on/off & louder" as "on/off &amp;
// louder". Rendering them verbatim shows the entity source to the
// operator, because every north-bound surface (SPA, MQTT, REST) treats
// these as plain text — correctly so, since escaping them again is what
// stops a device name from injecting markup.
//
// Decoding therefore belongs on the read side, once per load, rather than
// in each consumer. It is a no-op for the strings that carry no reference,
// which is the overwhelming majority.
func UnescapeUIText(s string) string {
	// html.UnescapeString allocates a new string only when it finds an
	// '&', so the common case costs one scan and no allocation.
	return html.UnescapeString(s)
}

// UnescapeUITextMap applies [UnescapeUIText] to every value of a
// locale-keyed display map in place and returns it, so callers can chain
// it onto a freshly decoded structure. A nil map is returned unchanged.
func UnescapeUITextMap(m map[string]string) map[string]string {
	for k, v := range m {
		m[k] = UnescapeUIText(v)
	}
	return m
}

// unescapeUITextJSON returns raw with every string value decoded by
// [UnescapeUIText], leaving the document's structure untouched.
//
// The profile documents are handed to the SPA as raw JSON so the UI
// consumes the archive's exact shape without a Go schema mirror that would
// drift on every upstream refresh. That verbatim hand-off also carried the
// CCU WebUI's HTML references through to the operator, so they are decoded
// here — at the one point where the document is loaded — rather than by
// teaching every consumer to decode.
//
// Decoding runs on the parsed document, never on the JSON text: unescaping
// the text would turn a "&quot;" inside a value into a real quote and break
// the document. Numbers are read as [json.Number] so a value re-serialises
// exactly as it arrived instead of round-tripping through float64.
func unescapeUITextJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	out, err := json.Marshal(unescapeWalk(doc))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// unescapeWalk applies [UnescapeUIText] to every string in a decoded JSON
// document, in place where the container allows it.
func unescapeWalk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			t[k] = unescapeWalk(vv)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = unescapeWalk(vv)
		}
		return t
	case string:
		return UnescapeUIText(t)
	default:
		return v
	}
}
