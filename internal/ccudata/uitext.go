// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "html"

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
