// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// stripSignature returns the certificate raw bytes with the
// Signature field (context tag 11) removed. The result is the TBS
// (To-Be-Signed) encoding the issuer hashed and signed.
//
// Matter §6.5.1.10: the signature is computed over the deterministic
// TLV encoding of every field *except* the signature itself. The
// signature is mandatorily encoded as the last field of the
// top-level Structure, so the strip is a simple byte-range cut once
// we have the start/end offsets of element 11.
func stripSignature(raw []byte) ([]byte, error) {
	d := tlv.NewDecoder(raw)

	// Consume the top-level Structure marker — it stays in the TBS.
	top, err := d.Next()
	if err != nil {
		return nil, fmt.Errorf("strip signature: top: %w", err)
	}
	if top.Type != tlv.TypeStructure {
		return nil, fmt.Errorf("%w: top element must be Structure", ErrMalformed)
	}

	var (
		sigStart int
		sigEnd   int
		foundSig bool
		endOfTop int
	)
	for {
		startPos := d.Pos()
		el, err := d.Next()
		if err != nil {
			return nil, fmt.Errorf("strip signature: %w", err)
		}
		if el.IsEndContainer {
			endOfTop = d.Pos()
			break
		}
		endPos := d.Pos()
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(tagSignature) {
			sigStart = startPos
			sigEnd = endPos
			foundSig = true
		}
		// Containers (Issuer / Subject / Extensions) need their
		// children consumed so endPos covers the whole container; the
		// embedded End-of-Container moves d.Pos() past it. tlv.Decoder
		// returns IsContainer for the marker only — drain manually.
		if el.IsContainer {
			if err := skipContainer(d); err != nil {
				return nil, fmt.Errorf("strip signature: skip container: %w", err)
			}
		}
	}

	if !foundSig {
		return nil, fmt.Errorf("%w: signature field absent", ErrMalformed)
	}

	// Re-emit: prefix (top marker through field-before-sig) + suffix
	// (everything after sig up to and including the End-of-Container).
	out := make([]byte, 0, len(raw))
	out = append(out, raw[:sigStart]...)
	out = append(out, raw[sigEnd:endOfTop]...)
	return out, nil
}
