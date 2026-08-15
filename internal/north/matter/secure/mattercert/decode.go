// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// Top-level certificate field tags (Matter §6.5.1.1).
const (
	tagSerialNumber       uint8 = 1
	tagSignatureAlgorithm uint8 = 2
	tagIssuer             uint8 = 3
	tagNotBefore          uint8 = 4
	tagNotAfter           uint8 = 5
	tagSubject            uint8 = 6
	tagPublicKeyAlgorithm uint8 = 7
	tagEllipticCurveID    uint8 = 8
	tagEllipticCurvePub   uint8 = 9
	tagExtensions         uint8 = 10
	tagSignature          uint8 = 11
)

// DN attribute tags inside Issuer / Subject (Matter Core Spec §6.5.6.1
// Table 60). Every tag identifies a single 64-bit Matter ID; matter-noc-cat
// is uint32 and may appear multiple times.
const (
	dnTagMatterNodeID         uint8 = 17 // matter-node-id (uint64)
	dnTagMatterFirmwareSignID uint8 = 18 // matter-firmware-signing-id (uint64)
	dnTagMatterICACID         uint8 = 19 // matter-icac-id (uint64)
	dnTagMatterRCACID         uint8 = 20 // matter-rcac-id (uint64)
	dnTagMatterFabricID       uint8 = 21 // matter-fabric-id (uint64)
	dnTagMatterCASEAuth       uint8 = 22 // matter-noc-cat (uint32, multiple)
)

// SignatureAlgorithm values (Matter §6.5.1.2). Only ECDSA-with-SHA256
// is defined.
const (
	SigAlgoECDSAWithSHA256 uint64 = 1
)

// PublicKeyAlgorithm values (Matter §6.5.1.5). Only EC is defined.
const (
	PubKeyAlgoEC uint64 = 1
)

// EllipticCurveIdentifier values (Matter §6.5.1.6). Only P-256.
const (
	CurvePrime256v1 uint64 = 1
)

// Errors returned during decoding / structural validation.
var (
	// ErrTruncated is wrapped onto truncation surfaced by the TLV
	// codec.
	ErrTruncated = errors.New("mattercert: certificate truncated")
	// ErrMalformed is returned when the TLV structure violates the
	// Matter §6.5 cert schema (missing mandatory fields, wrong types).
	ErrMalformed = errors.New("mattercert: malformed certificate")
	// ErrUnsupportedAlgorithm is returned for non-ECDSA signatures or
	// non-P-256 keys.
	ErrUnsupportedAlgorithm = errors.New("mattercert: unsupported algorithm")
	// ErrSubjectIncomplete is returned when the Subject DN does not
	// carry the required identifiers (NodeID for NOC, FabricID where
	// applicable).
	ErrSubjectIncomplete = errors.New("mattercert: subject identifiers missing")
)

// Certificate is the decoded form of a Matter operational certificate.
// All fields are populated from the TLV payload; the TBS bytes are
// retained for subsequent signature verification.
type Certificate struct {
	SerialNumber       []byte
	SignatureAlgorithm uint64
	Issuer             DistinguishedName
	NotBefore          uint64
	NotAfter           uint64
	Subject            DistinguishedName
	PublicKeyAlgorithm uint64
	EllipticCurveID    uint64
	PublicKey          []byte // 65-byte uncompressed P-256
	Extensions         CertificateExtensions
	Signature          []byte // 64-byte r||s
	Raw                []byte // full TLV bytes (for chain hashing)
}

// CertificateExtensions captures the Matter-cert extension structure
// (Matter §6.5.1.4). All fields are optional; presence is tracked via
// the Has* booleans because zero-valued slices and IDs are legitimate
// for real-world certs (e.g. KeyUsage = 0 is technically encodable).
//
// Field layout in the TLV List at top-level tag 10:
//
//	[1] basic-constraints   STRUCT { [1] is-ca BOOL, [2] path-len UINT8? }
//	[2] key-usage           UINT16 (RFC-5280 KeyUsage bitmap)
//	[3] extended-key-usage  ARRAY of UINT8 (1=serverAuth, 2=clientAuth, …)
//	[4] subject-key-id      OCTET STRING (20 bytes, SHA-1-derived)
//	[5] authority-key-id    OCTET STRING (20 bytes, SHA-1-derived)
//	[6] future-extension    OCTET STRING (raw DER, 0+ instances)
//
// X.509 DER reconstruction (used by signature verification per
// matter.js Certificate.verifyChain) requires every extension to ride
// in the rebuilt TBS in the SAME order they appeared in the source TLV.
type CertificateExtensions struct {
	HasBasicConstraints        bool
	BasicConstraintsIsCA       bool
	BasicConstraintsHasPathLen bool
	BasicConstraintsPathLen    uint8
	HasKeyUsage                bool
	KeyUsage                   uint16
	HasExtendedKeyUsage        bool
	ExtendedKeyUsage           []uint8
	HasSubjectKeyID            bool
	SubjectKeyID               []byte // 20 bytes
	HasAuthorityKeyID          bool
	AuthorityKeyID             []byte // 20 bytes
	FutureExtensions           [][]byte
}

// DistinguishedName captures the Matter-specific DN attributes that
// the bridge needs from Issuer + Subject. Other DN entries are
// preserved on Other for diagnostic logging but otherwise unused.
type DistinguishedName struct {
	// MatterRCACID is the 64-bit Root CA ID. Set on Root + ICAC
	// certificates' Subject and on every certificate's Issuer.
	MatterRCACID uint64
	// MatterICACID is the 64-bit Intermediate CA ID. Set on ICAC
	// Subject; absent when the cert is signed directly by the root.
	MatterICACID uint64
	// MatterNodeID is the 64-bit operational Node ID. Set on NOC
	// Subject only.
	MatterNodeID uint64
	// MatterFabricID is the 64-bit Fabric ID. Set on NOC + ICAC
	// Subject.
	MatterFabricID uint64
	// CASEAuthTags collects the (multi-valued) CASE Authenticated
	// Tags from a NOC subject.
	CASEAuthTags []uint32
	// HasNodeID / HasFabricID / HasRCACID / HasICACID convey
	// presence; the spec uses zero-as-sentinel which conflicts with
	// real fabric IDs that may legitimately be 0.
	HasNodeID   bool
	HasFabricID bool
	HasRCACID   bool
	HasICACID   bool
	// Order records the DN attribute tags in their original TLV
	// arrival sequence. The X.509 DER reconstruction (TBSToDER)
	// MUST emit the RDNs in this order — matter.js's Object.entries
	// iteration preserves insertion order, and signature verification
	// relies on the byte-exact reproduction of the issuer's DN. A
	// reordered DN produces a different SHA-256 over the TBS and
	// fails ECDSA.Verify even though every value is correct.
	Order []uint8
}

// IsRoot reports whether the cert's Subject identifies a Root CA.
// Root CAs carry a matter-rcac-id and never a matter-node-id; per
// Matter §6.5.6.1 the matter-fabric-id attribute is OPTIONAL on a
// root (chip-tool and Apple Home both ship roots with a fabric-id
// pre-bound). The earlier `!HasFabricID` clause rejected every
// real-world commissioner's trust root and aborted AddNOC with
// "cert is not a root CA".
func (c *Certificate) IsRoot() bool {
	return c.Subject.HasRCACID && !c.Subject.HasNodeID
}

// IsICA reports whether the cert's Subject identifies an Intermediate
// CA (ICAC). ICACs carry a matter-icac-id and never a matter-node-id;
// per Matter §6.5.6.1 the matter-fabric-id attribute is OPTIONAL on
// an ICAC (mirroring the same allowance on RCAC). chip-tool ships
// ICACs without a fabric-id; the earlier `HasFabricID` requirement
// rejected them with "cert is not an ICAC" mid-Sigma3.
func (c *Certificate) IsICA() bool {
	return c.Subject.HasICACID && !c.Subject.HasNodeID
}

// IsNOC reports whether the cert's Subject identifies a Node
// Operational Certificate (NOC).
func (c *Certificate) IsNOC() bool {
	return c.Subject.HasNodeID && c.Subject.HasFabricID
}

// PublicKeyECDSA returns the parsed public key. Returns
// [ErrUnsupportedAlgorithm] when the curve is not P-256 or
// [ErrMalformed] when the point is off-curve.
func (c *Certificate) PublicKeyECDSA() (*ecdsa.PublicKey, error) {
	if c.PublicKeyAlgorithm != PubKeyAlgoEC {
		return nil, fmt.Errorf("%w: pub-key-algo=%d", ErrUnsupportedAlgorithm, c.PublicKeyAlgorithm)
	}
	if c.EllipticCurveID != CurvePrime256v1 {
		return nil, fmt.Errorf("%w: ec-curve-id=%d", ErrUnsupportedAlgorithm, c.EllipticCurveID)
	}
	if len(c.PublicKey) != 65 || c.PublicKey[0] != 0x04 {
		return nil, fmt.Errorf("%w: malformed P-256 point", ErrMalformed)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), c.PublicKey) //nolint:staticcheck // SA1019: required for raw point decode
	if x == nil {
		return nil, fmt.Errorf("%w: point not on curve", ErrMalformed)
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

// Decode parses raw TLV bytes into a [Certificate]. The supplied
// buffer is retained on Certificate.Raw for downstream signature
// verification — callers MUST NOT mutate it after Decode returns.
func Decode(raw []byte) (*Certificate, error) {
	cert := &Certificate{Raw: append([]byte(nil), raw...)}

	d := tlv.NewDecoder(raw)
	top, err := d.Next()
	if err != nil {
		return nil, fmt.Errorf("%w: top: %w", ErrTruncated, err)
	}
	if top.Type != tlv.TypeStructure {
		return nil, fmt.Errorf("%w: top element must be Structure (got %d)", ErrMalformed, top.Type)
	}

	for {
		el, err := d.Next()
		if err != nil {
			return nil, fmt.Errorf("%w: cert body: %w", ErrTruncated, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return nil, fmt.Errorf("%w: cert field with non-context tag %v", ErrMalformed, el.Tag)
		}
		if err := assignField(cert, el, d); err != nil {
			return nil, err
		}
	}

	if err := validateMandatory(cert); err != nil {
		return nil, err
	}
	return cert, nil
}

// assignField dispatches on the context tag and reads the field value
// from d. For container fields (Issuer/Subject/Extensions) the parser
// is invoked recursively.
func assignField(cert *Certificate, el tlv.Element, d *tlv.Decoder) error {
	tag := uint8(el.Tag.Number & 0xFF)
	switch tag {
	case tagSerialNumber:
		if el.Type < tlv.TypeOctetStr1 || el.Type > tlv.TypeOctetStr8 {
			return fmt.Errorf("%w: SerialNumber must be octet string", ErrMalformed)
		}
		cert.SerialNumber = append([]byte(nil), el.Octets...)
	case tagSignatureAlgorithm:
		cert.SignatureAlgorithm = el.Uint
	case tagIssuer:
		dn, err := decodeDN(d)
		if err != nil {
			return fmt.Errorf("issuer: %w", err)
		}
		cert.Issuer = dn
	case tagNotBefore:
		cert.NotBefore = el.Uint
	case tagNotAfter:
		cert.NotAfter = el.Uint
	case tagSubject:
		dn, err := decodeDN(d)
		if err != nil {
			return fmt.Errorf("subject: %w", err)
		}
		cert.Subject = dn
	case tagPublicKeyAlgorithm:
		cert.PublicKeyAlgorithm = el.Uint
	case tagEllipticCurveID:
		cert.EllipticCurveID = el.Uint
	case tagEllipticCurvePub:
		cert.PublicKey = append([]byte(nil), el.Octets...)
	case tagExtensions:
		ext, err := decodeExtensions(d)
		if err != nil {
			return fmt.Errorf("extensions: %w", err)
		}
		cert.Extensions = ext
	case tagSignature:
		cert.Signature = append([]byte(nil), el.Octets...)
	default:
		// Unknown future field — skip if container, else accept.
		if el.IsContainer {
			if err := skipContainer(d); err != nil {
				return fmt.Errorf("unknown container tag %d: %w", tag, err)
			}
		}
	}
	return nil
}

// decodeDN reads a Matter Distinguished Name list. The List container
// header has already been consumed by [tlv.Decoder.Next] — but in our
// caller's flow the *element* representing the List was just returned;
// that element is itself only the container marker, so we read its
// children until EndContainer.
func decodeDN(d *tlv.Decoder) (DistinguishedName, error) {
	var dn DistinguishedName
	for {
		el, err := d.Next()
		if err != nil {
			return dn, fmt.Errorf("%w: dn child: %w", ErrTruncated, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return dn, fmt.Errorf("%w: dn entry with non-context tag", ErrMalformed)
		}
		tag := uint8(el.Tag.Number & 0xFF)
		switch tag {
		case dnTagMatterNodeID:
			dn.MatterNodeID = el.Uint
			dn.HasNodeID = true
			dn.Order = append(dn.Order, tag)
		case dnTagMatterICACID:
			dn.MatterICACID = el.Uint
			dn.HasICACID = true
			dn.Order = append(dn.Order, tag)
		case dnTagMatterRCACID:
			dn.MatterRCACID = el.Uint
			dn.HasRCACID = true
			dn.Order = append(dn.Order, tag)
		case dnTagMatterFabricID:
			dn.MatterFabricID = el.Uint
			dn.HasFabricID = true
			dn.Order = append(dn.Order, tag)
		case dnTagMatterCASEAuth:
			dn.CASEAuthTags = append(dn.CASEAuthTags, uint32(el.Uint&0xFFFFFFFF))
			dn.Order = append(dn.Order, tag)
		default:
			// Skip — DN attributes the bridge does not consume.
			if el.IsContainer {
				if err := skipContainer(d); err != nil {
					return dn, err
				}
			}
		}
	}
	return dn, nil
}

// skipContainer consumes elements (recursively if nested) until the
// matching End-of-Container marker.
func skipContainer(d *tlv.Decoder) error {
	depth := 1
	for depth > 0 {
		el, err := d.Next()
		if err != nil {
			return fmt.Errorf("%w: skip: %w", ErrTruncated, err)
		}
		if el.IsContainer {
			depth++
		}
		if el.IsEndContainer {
			depth--
		}
	}
	return nil
}

// validateMandatory checks the spec-required fields are present and
// well-formed (Matter §6.5.1).
func validateMandatory(c *Certificate) error {
	// Matter §6.5 caps the serial number at 20 octets, but matter.js
	// tolerates 21 (observed in the wild, e.g. some LG TVs) and only
	// throws above that. Mirrors OperationalBase.ts:44-57 generalVerify:
	// it warns (does not reject) at 21 octets and rejects only >21.
	if len(c.SerialNumber) == 0 || len(c.SerialNumber) > 21 {
		return fmt.Errorf("%w: serial number length=%d (want 1..21)", ErrMalformed, len(c.SerialNumber))
	}
	if c.SignatureAlgorithm != SigAlgoECDSAWithSHA256 {
		return fmt.Errorf("%w: signature algorithm %d", ErrUnsupportedAlgorithm, c.SignatureAlgorithm)
	}
	if c.PublicKeyAlgorithm != PubKeyAlgoEC {
		return fmt.Errorf("%w: public-key algorithm %d", ErrUnsupportedAlgorithm, c.PublicKeyAlgorithm)
	}
	if c.EllipticCurveID != CurvePrime256v1 {
		return fmt.Errorf("%w: elliptic curve %d", ErrUnsupportedAlgorithm, c.EllipticCurveID)
	}
	// The prefix is only reportable once the length check has passed —
	// tag 9 may be absent or empty in a peer-supplied cert, and Go
	// evaluates the whole argument list before the fmt call.
	if len(c.PublicKey) != 65 {
		return fmt.Errorf("%w: ec-pub-key length=%d (want 65)", ErrMalformed, len(c.PublicKey))
	}
	if c.PublicKey[0] != 0x04 {
		return fmt.Errorf("%w: ec-pub-key prefix=%#x (want 0x04)", ErrMalformed, c.PublicKey[0])
	}
	if len(c.Signature) != 64 {
		return fmt.Errorf("%w: signature length=%d (want 64)", ErrMalformed, len(c.Signature))
	}
	if c.NotAfter != 0 && c.NotAfter <= c.NotBefore {
		return fmt.Errorf("%w: NotAfter (%d) <= NotBefore (%d)", ErrMalformed, c.NotAfter, c.NotBefore)
	}
	if c.IsNOC() {
		if !c.Subject.HasNodeID || !c.Subject.HasFabricID {
			return fmt.Errorf("%w: NOC subject must carry NodeID + FabricID", ErrSubjectIncomplete)
		}
	}
	return nil
}

// Extension TLV tags inside the [tagExtensions] List per Matter
// §6.5.1.4. Tag 6 (future-extension) is repeatable; the others are
// each at most once.
const (
	extTagBasicConstraints uint8 = 1
	extTagKeyUsage         uint8 = 2
	extTagExtendedKeyUsage uint8 = 3
	extTagSubjectKeyID     uint8 = 4
	extTagAuthorityKeyID   uint8 = 5
	extTagFutureExtension  uint8 = 6
	extBCInnerTagIsCa      uint8 = 1
	extBCInnerTagPathLen   uint8 = 2
)

// decodeExtensions reads the children of the top-level Extensions
// container (the container marker has already been consumed by the
// outer [Decode] loop).
func decodeExtensions(d *tlv.Decoder) (CertificateExtensions, error) {
	var ext CertificateExtensions
	for {
		el, err := d.Next()
		if err != nil {
			return ext, fmt.Errorf("%w: extension child: %w", ErrTruncated, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return ext, fmt.Errorf("%w: extension entry with non-context tag", ErrMalformed)
		}
		tag := uint8(el.Tag.Number & 0xFF)
		switch tag {
		case extTagBasicConstraints:
			if !el.IsContainer {
				return ext, fmt.Errorf("%w: basic-constraints not a container", ErrMalformed)
			}
			if err := decodeBasicConstraints(d, &ext); err != nil {
				return ext, fmt.Errorf("basic-constraints: %w", err)
			}
		case extTagKeyUsage:
			ext.KeyUsage = uint16(el.Uint & 0xFFFF)
			ext.HasKeyUsage = true
		case extTagExtendedKeyUsage:
			if !el.IsContainer {
				return ext, fmt.Errorf("%w: extended-key-usage not a container", ErrMalformed)
			}
			eku, err := decodeUint8Array(d)
			if err != nil {
				return ext, fmt.Errorf("extended-key-usage: %w", err)
			}
			ext.ExtendedKeyUsage = eku
			ext.HasExtendedKeyUsage = true
		case extTagSubjectKeyID:
			if len(el.Octets) == 0 {
				return ext, fmt.Errorf("%w: subject-key-id empty", ErrMalformed)
			}
			ext.SubjectKeyID = append([]byte(nil), el.Octets...)
			ext.HasSubjectKeyID = true
		case extTagAuthorityKeyID:
			if len(el.Octets) == 0 {
				return ext, fmt.Errorf("%w: authority-key-id empty", ErrMalformed)
			}
			ext.AuthorityKeyID = append([]byte(nil), el.Octets...)
			ext.HasAuthorityKeyID = true
		case extTagFutureExtension:
			ext.FutureExtensions = append(ext.FutureExtensions, append([]byte(nil), el.Octets...))
		default:
			if el.IsContainer {
				if err := skipContainer(d); err != nil {
					return ext, fmt.Errorf("unknown extension container tag %d: %w", tag, err)
				}
			}
		}
	}
	return ext, nil
}

// decodeBasicConstraints reads the inner struct (consumed marker) of
// the basic-constraints extension.
func decodeBasicConstraints(d *tlv.Decoder, ext *CertificateExtensions) error {
	ext.HasBasicConstraints = true
	for {
		el, err := d.Next()
		if err != nil {
			return fmt.Errorf("%w: bc child: %w", ErrTruncated, err)
		}
		if el.IsEndContainer {
			return nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return fmt.Errorf("%w: bc entry with non-context tag", ErrMalformed)
		}
		tag := uint8(el.Tag.Number & 0xFF)
		switch tag {
		case extBCInnerTagIsCa:
			ext.BasicConstraintsIsCA = el.Type == tlv.TypeBoolTrue
		case extBCInnerTagPathLen:
			ext.BasicConstraintsPathLen = uint8(el.Uint & 0xFF)
			ext.BasicConstraintsHasPathLen = true
		default:
			if el.IsContainer {
				if err := skipContainer(d); err != nil {
					return fmt.Errorf("unknown bc tag %d: %w", tag, err)
				}
			}
		}
	}
}

// decodeUint8Array reads a TLV array of UInt elements, returning each
// as a uint8 (matter spec ExtendedKeyUsage uses 1..6 enum values).
func decodeUint8Array(d *tlv.Decoder) ([]uint8, error) {
	var out []uint8
	for {
		el, err := d.Next()
		if err != nil {
			return nil, fmt.Errorf("%w: array child: %w", ErrTruncated, err)
		}
		if el.IsEndContainer {
			return out, nil
		}
		out = append(out, uint8(el.Uint&0xFF))
	}
}
