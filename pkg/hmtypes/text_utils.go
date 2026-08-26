// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import (
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// FixXMLRPCEncoding reverses the encoding mis-interpretation that occurs
// when the CCU XML-RPC interface stores user-defined strings (link names,
// room names, …) as raw UTF-8 bytes inside an ISO-8859-1 XML document.
//
// When Go's xml.Decoder reads those bytes under the ISO-8859-1 charset
// reader, multi-byte UTF-8 sequences are decoded as individual ISO-8859-1
// code points (e.g. 'ü' → 'Ã¼').  This function reverses the
// mis-interpretation by re-encoding the string as ISO-8859-1 to recover
// the original byte sequence and then decoding those bytes as UTF-8.
//
// If the string is already correct ASCII/genuine ISO-8859-1, or if the
// recovered bytes are not valid UTF-8, the original string is returned
// unchanged.
//
// Mirrors the Python reference implementation's fix_xml_rpc_encoding.
func FixXMLRPCEncoding(text string) string {
	// Re-encode: each Go rune that fell into the ISO-8859-1 range is mapped
	// back to a single byte. Characters outside that range cannot have come
	// from ISO-8859-1 decoding, so the string is already correct.
	raw, err := charmap.ISO8859_1.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return text
	}
	// Attempt to interpret the recovered bytes as UTF-8. If they are not
	// valid UTF-8 the mis-interpretation theory is wrong; return original.
	if !utf8.Valid(raw) {
		return text
	}
	decoded := string(raw)
	// Additional guard: if the decoded result equals the input, there was
	// nothing to fix (pure ASCII path).
	return decoded
}
