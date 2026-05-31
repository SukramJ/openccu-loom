// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package spake2

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// MatterContext is the SPAKE2+ context string Matter mandates for
// PASE (Core Spec §3.10.4 / Table 13).
const MatterContext = "CHIP PAKE V1 Commissioning"

// Sizes per Matter Core Spec §3.10.
const (
	// PointSize is the encoded length of an uncompressed P-256 point
	// (1 prefix + 32 X + 32 Y).
	PointSize = 65
	// ScalarSize is the encoded length of a P-256 scalar.
	ScalarSize = 32
	// WSize is the byte length of the (w0, w1) PBKDF2 outputs as fed
	// into the modular reduction (40 bytes per Matter spec).
	WSize = 40
	// PBKDF2OutputSize is the total PBKDF2 output for both ws values.
	PBKDF2OutputSize = 2 * WSize
	// ConfirmTagSize is the truncated HMAC length used for cA / cB.
	ConfirmTagSize = 32
	// SharedSecretSize is the size of the resulting Ke session key.
	SharedSecretSize = 16
)

// Errors.
var (
	// ErrInvalidPoint surfaces when a peer sends a point that is not
	// on the curve or is the identity element.
	ErrInvalidPoint = errors.New("spake2: peer point is invalid")
	// ErrConfirmationFailed is returned when a peer's confirmation
	// tag does not match the locally-derived expectation. The caller
	// MUST treat this as a fatal session-establishment error and
	// reset the exchange — it is the canonical "wrong passcode" or
	// "tampered transcript" signal.
	ErrConfirmationFailed = errors.New("spake2: peer confirmation tag mismatch")
	// ErrInvalidPasscode is reserved for future use when the passcode
	// is outside the allowed range (1..99999998 per Matter §5.1.1.6).
	ErrInvalidPasscode = errors.New("spake2: passcode out of range")
	// ErrSessionState surfaces when methods are invoked out of order
	// (e.g., calling [Verifier.Finish] before [Verifier.Pake1]).
	ErrSessionState = errors.New("spake2: invalid session state")
)

// curve returns the P-256 curve instance shared across the package.
//
// raw scalar multiplication needed by SPAKE2+; the deprecated label
// targets ECDSA users that should switch to crypto/ecdsa.
//
//nolint:staticcheck // SA1019: crypto/elliptic is the supported path for
func curve() elliptic.Curve { return elliptic.P256() }

// Matter-mandated SPAKE2+ generator points M and N. Bytes are taken
// from Matter Core Specification §3.10.1, which references the
// SPAKE2+ test-vector points from RFC 9383.
//
// Encoded as uncompressed (0x04 || X || Y) — the form
// crypto/elliptic.UnmarshalCompressed expects with a 0x02/0x03
// prefix is harder to verify against the spec, so we use the
// uncompressed serialisation throughout.
var (
	// matterMHex is the M point per Matter §3.10.1 (uncompressed).
	matterMHex = "" +
		"04" +
		"886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f" +
		"5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20"

	// matterNHex is the N point per Matter §3.10.1 (uncompressed).
	matterNHex = "" +
		"04" +
		"d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49" +
		"07d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7"
)

// pointMN holds the unmarshalled M and N points. Computed once in
// init().
var (
	mPoint *ecdsa.PublicKey
	nPoint *ecdsa.PublicKey
)

func init() {
	var err error
	if mPoint, err = unmarshalUncompressed(matterMHex); err != nil {
		panic(fmt.Sprintf("spake2: invalid M constant: %v", err))
	}
	if nPoint, err = unmarshalUncompressed(matterNHex); err != nil {
		panic(fmt.Sprintf("spake2: invalid N constant: %v", err))
	}
}

// hexToBytes is a tiny encoder/hex helper used only by the M/N
// constant initialisation. We avoid encoding/hex to keep the
// init-time dependency surface minimal.
func hexToBytes(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("hex: odd length")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi, err := hexNibble(s[i])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(s[i+1])
		if err != nil {
			return nil, err
		}
		out[i/2] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	}
	return 0, fmt.Errorf("hex: bad nibble %q", b)
}

// unmarshalUncompressed decodes a hex-encoded uncompressed P-256
// point (0x04 || X || Y) into an ecdsa.PublicKey.
func unmarshalUncompressed(hexStr string) (*ecdsa.PublicKey, error) {
	raw, err := hexToBytes(hexStr)
	if err != nil {
		return nil, err
	}
	x, y := elliptic.Unmarshal(curve(), raw) //nolint:staticcheck // SA1019: see curve()
	if x == nil {
		return nil, ErrInvalidPoint
	}
	return &ecdsa.PublicKey{Curve: curve(), X: x, Y: y}, nil
}

// PBKDF derives (w0, w1) from a passcode + salt + iterations. Matter
// Core Spec §3.10.3 mandates SHA-256 and a 40-byte output per scalar
// (the modular reduction below maps the wider value into the curve
// order).
func PBKDF(passcode uint32, salt []byte, iterations int) (w0, w1 *big.Int, err error) {
	if iterations <= 0 {
		return nil, nil, fmt.Errorf("%w: iterations must be > 0", ErrInvalidPasscode)
	}
	pwd := make([]byte, 4)
	binary.LittleEndian.PutUint32(pwd, passcode)
	out, err := pbkdf2.Key(sha256.New, string(pwd), salt, iterations, PBKDF2OutputSize)
	if err != nil {
		return nil, nil, fmt.Errorf("spake2: pbkdf2: %w", err)
	}
	order := curve().Params().N
	w0 = new(big.Int).Mod(new(big.Int).SetBytes(out[:WSize]), order)
	w1 = new(big.Int).Mod(new(big.Int).SetBytes(out[WSize:]), order)
	return w0, w1, nil
}

// VerifierContext bundles the constants a Matter device stores after
// commissioning passcode setup. The verifier never sees the passcode
// directly — only w0 and L = w1·G.
type VerifierContext struct {
	W0 *big.Int
	L  *ecdsa.PublicKey // w1·G in compressed/uncompressed point form
}

// Matter Core Spec §3.10.1 PBKDF iteration bounds.
const (
	// IterationsMin is the spec-mandated minimum PBKDF2 iteration count.
	IterationsMin = 1000
	// IterationsMax is the spec-mandated maximum PBKDF2 iteration count.
	IterationsMax = 100000
)

// NewVerifierContext computes a fresh VerifierContext from passcode
// material. Used at commissioning-onboarding time; in production the
// derived (W0, L) pair is persisted and reused, not the passcode.
// Returns an error if iterations is outside [IterationsMin, IterationsMax].
func NewVerifierContext(passcode uint32, salt []byte, iterations int) (*VerifierContext, error) {
	if iterations < IterationsMin || iterations > IterationsMax {
		return nil, fmt.Errorf("%w: iterations=%d not in [%d, %d]", ErrInvalidPasscode, iterations, IterationsMin, IterationsMax)
	}
	w0, w1, err := PBKDF(passcode, salt, iterations)
	if err != nil {
		return nil, err
	}
	lx, ly := curve().ScalarBaseMult(w1.Bytes()) //nolint:staticcheck // SA1019: see curve()
	return &VerifierContext{
		W0: w0,
		L:  &ecdsa.PublicKey{Curve: curve(), X: lx, Y: ly},
	}, nil
}

// Verifier drives the device-side of PASE. Construct with
// [NewVerifier], call [Verifier.ProcessPake1] with the prover's pA,
// send the returned [Pake2Output.Y] + [Pake2Output.CB] back, then
// call [Verifier.ProcessPake3] with the prover's cA. On success
// [Verifier.SharedSecret] returns Ke.
type Verifier struct {
	ctx      *VerifierContext
	idA, idB []byte
	context  []byte
	y        *big.Int
	xMarshal []byte
	yMarshal []byte
	zMarshal []byte
	vMarshal []byte
	kcA, kcB []byte
	ke       []byte
	state    verifierState
}

type verifierState uint8

const (
	verifierStateInit verifierState = iota
	verifierStatePake1Processed
	verifierStateFinished
)

// NewVerifier returns a Verifier ready to process Pake1.
//
// The optional idA / idB peer-identity strings ride into the
// transcript hash. Matter sets both to empty per Core Spec §3.10.4
// — pass nil for the default. context defaults to [MatterContext].
func NewVerifier(vc *VerifierContext, idA, idB, context []byte) *Verifier {
	if context == nil {
		context = []byte(MatterContext)
	}
	return &Verifier{
		ctx:     vc,
		idA:     idA,
		idB:     idB,
		context: context,
	}
}

// Pake2Output bundles the bytes the verifier sends back to the prover
// after [Verifier.ProcessPake1].
type Pake2Output struct {
	Y  []byte // uncompressed Y point (65 bytes)
	CB []byte // confirmation tag for the prover (32 bytes)
}

// ProcessPake1 consumes the prover's pA = X and produces the
// verifier's Pake2 reply.
func (v *Verifier) ProcessPake1(pA []byte) (*Pake2Output, error) {
	if v.state != verifierStateInit {
		return nil, fmt.Errorf("%w: ProcessPake1 already called", ErrSessionState)
	}
	xPoint, err := unmarshalAndValidate(pA)
	if err != nil {
		return nil, err
	}

	// Pick random y ∈ [1, n-1].
	y, err := randomScalar()
	if err != nil {
		return nil, err
	}
	v.y = y

	// Y = y·G + w0·N.
	yGx, yGy := curve().ScalarBaseMult(y.Bytes())                          //nolint:staticcheck // SA1019: see curve()
	w0Nx, w0Ny := curve().ScalarMult(nPoint.X, nPoint.Y, v.ctx.W0.Bytes()) //nolint:staticcheck // SA1019: see curve()
	yX, yY := curve().Add(yGx, yGy, w0Nx, w0Ny)                            //nolint:staticcheck // SA1019: see curve()
	v.yMarshal = elliptic.Marshal(curve(), yX, yY)                         //nolint:staticcheck // SA1019: see curve()

	// Z = y·(X - w0·M).
	w0MNegX, w0MNegY := curve().ScalarMult(mPoint.X, mPoint.Y, v.ctx.W0.Bytes()) //nolint:staticcheck // SA1019: see curve()
	w0MNegY = new(big.Int).Sub(curve().Params().P, w0MNegY)                      // negate
	xMinusW0Mx, xMinusW0My := curve().Add(xPoint.X, xPoint.Y, w0MNegX, w0MNegY)  //nolint:staticcheck // SA1019: see curve()
	zX, zY := curve().ScalarMult(xMinusW0Mx, xMinusW0My, y.Bytes())              //nolint:staticcheck // SA1019: see curve()
	v.zMarshal = elliptic.Marshal(curve(), zX, zY)                               //nolint:staticcheck // SA1019: see curve()

	// V = y·L.
	vX, vY := curve().ScalarMult(v.ctx.L.X, v.ctx.L.Y, y.Bytes()) //nolint:staticcheck // SA1019: see curve()
	v.vMarshal = elliptic.Marshal(curve(), vX, vY)                //nolint:staticcheck // SA1019: see curve()

	v.xMarshal = pA
	v.deriveKeys()

	cB := hmacSHA256(v.kcB, v.xMarshal)
	v.state = verifierStatePake1Processed
	return &Pake2Output{Y: v.yMarshal, CB: cB[:ConfirmTagSize]}, nil
}

// ProcessPake3 verifies the prover's cA tag and finalises the
// session. On success [Verifier.SharedSecret] returns the 16-byte Ke.
func (v *Verifier) ProcessPake3(cA []byte) error {
	if v.state != verifierStatePake1Processed {
		return fmt.Errorf("%w: ProcessPake1 must run first", ErrSessionState)
	}
	expected := hmacSHA256(v.kcA, v.yMarshal)
	if subtle.ConstantTimeCompare(cA, expected[:ConfirmTagSize]) != 1 {
		return ErrConfirmationFailed
	}
	v.state = verifierStateFinished
	return nil
}

// SharedSecret returns the 16-byte session key (Ke). Valid only
// after a successful [Verifier.ProcessPake3].
func (v *Verifier) SharedSecret() []byte {
	if v.state != verifierStateFinished {
		return nil
	}
	out := make([]byte, len(v.ke))
	copy(out, v.ke)
	return out
}

// Prover drives the commissioner-side of PASE. The flow mirrors
// [Verifier]: derive (w0, w1), call [Prover.GeneratePake1] to produce
// pA (the bytes sent on the wire), then call [Prover.ProcessPake2]
// with the verifier's reply. On success [Prover.SharedSecret] returns
// the same 16-byte Ke as the verifier and [Prover.Pake3] returns the
// final cA bytes.
type Prover struct {
	w0, w1   *big.Int
	idA, idB []byte
	context  []byte
	x        *big.Int
	xMarshal []byte
	yMarshal []byte
	zMarshal []byte
	vMarshal []byte
	kcA, kcB []byte
	ke       []byte
	state    proverState
}

type proverState uint8

const (
	proverStateInit proverState = iota
	proverStatePake1Generated
	proverStateFinished
)

// NewProver returns a Prover ready to compute Pake1.
func NewProver(passcode uint32, salt []byte, iterations int, idA, idB, context []byte) (*Prover, error) {
	w0, w1, err := PBKDF(passcode, salt, iterations)
	if err != nil {
		return nil, err
	}
	if context == nil {
		context = []byte(MatterContext)
	}
	return &Prover{
		w0: w0, w1: w1,
		idA: idA, idB: idB,
		context: context,
	}, nil
}

// GeneratePake1 returns pA = X for transmission to the verifier.
func (p *Prover) GeneratePake1() ([]byte, error) {
	if p.state != proverStateInit {
		return nil, fmt.Errorf("%w: GeneratePake1 already called", ErrSessionState)
	}
	x, err := randomScalar()
	if err != nil {
		return nil, err
	}
	p.x = x
	xGx, xGy := curve().ScalarBaseMult(x.Bytes())                      //nolint:staticcheck // SA1019: see curve()
	w0Mx, w0My := curve().ScalarMult(mPoint.X, mPoint.Y, p.w0.Bytes()) //nolint:staticcheck // SA1019: see curve()
	xX, xY := curve().Add(xGx, xGy, w0Mx, w0My)                        //nolint:staticcheck // SA1019: see curve()
	p.xMarshal = elliptic.Marshal(curve(), xX, xY)                     //nolint:staticcheck // SA1019: see curve()
	p.state = proverStatePake1Generated
	return p.xMarshal, nil
}

// ProcessPake2 consumes the verifier's Y + cB. Returns the prover's
// cA on success; on cB mismatch returns [ErrConfirmationFailed]
// without exposing Ke.
func (p *Prover) ProcessPake2(yBytes, cB []byte) (cA []byte, err error) {
	if p.state != proverStatePake1Generated {
		return nil, fmt.Errorf("%w: GeneratePake1 must run first", ErrSessionState)
	}
	yPoint, err := unmarshalAndValidate(yBytes)
	if err != nil {
		return nil, err
	}

	// Z = x·(Y - w0·N).
	w0NNegX, w0NNegY := curve().ScalarMult(nPoint.X, nPoint.Y, p.w0.Bytes())    //nolint:staticcheck // SA1019: see curve()
	w0NNegY = new(big.Int).Sub(curve().Params().P, w0NNegY)                     // negate
	yMinusW0Nx, yMinusW0Ny := curve().Add(yPoint.X, yPoint.Y, w0NNegX, w0NNegY) //nolint:staticcheck // SA1019: see curve()
	zX, zY := curve().ScalarMult(yMinusW0Nx, yMinusW0Ny, p.x.Bytes())           //nolint:staticcheck // SA1019: see curve()
	p.zMarshal = elliptic.Marshal(curve(), zX, zY)                              //nolint:staticcheck // SA1019: see curve()

	// V = w1·(Y - w0·N).
	vX, vY := curve().ScalarMult(yMinusW0Nx, yMinusW0Ny, p.w1.Bytes()) //nolint:staticcheck // SA1019: see curve()
	p.vMarshal = elliptic.Marshal(curve(), vX, vY)                     //nolint:staticcheck // SA1019: see curve()

	p.yMarshal = yBytes
	p.deriveKeys()

	expected := hmacSHA256(p.kcB, p.xMarshal)
	if subtle.ConstantTimeCompare(cB, expected[:ConfirmTagSize]) != 1 {
		// Wipe any partial state so an attacker cannot probe Ke after
		// a confirmation-tag failure.
		p.zero()
		return nil, ErrConfirmationFailed
	}

	cA = hmacSHA256(p.kcA, p.yMarshal)[:ConfirmTagSize]
	p.state = proverStateFinished
	return cA, nil
}

// SharedSecret returns the 16-byte session key (Ke). Valid only after
// a successful [Prover.ProcessPake2].
func (p *Prover) SharedSecret() []byte {
	if p.state != proverStateFinished {
		return nil
	}
	out := make([]byte, len(p.ke))
	copy(out, p.ke)
	return out
}

func (p *Prover) zero() {
	zero(p.kcA)
	zero(p.kcB)
	zero(p.ke)
	zero(p.zMarshal)
	zero(p.vMarshal)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// --- shared key-derivation pipeline ---

func (v *Verifier) deriveKeys() {
	v.kcA, v.kcB, v.ke = deriveKeys(v.context, v.idA, v.idB, v.xMarshal, v.yMarshal, v.zMarshal, v.vMarshal, v.ctx.W0)
}

func (p *Prover) deriveKeys() {
	p.kcA, p.kcB, p.ke = deriveKeys(p.context, p.idA, p.idB, p.xMarshal, p.yMarshal, p.zMarshal, p.vMarshal, p.w0)
}

// deriveKeys computes the SPAKE2+ key schedule per Matter Core Spec
// §3.10.4 / RFC 9383 §3.4:
//
//	K_main = SHA-256(TT)
//	Ka = K_main[0:16]
//	Ke = K_main[16:32]
//	KcA || KcB = HKDF(salt=nil, IKM=Ka, info="ConfirmationKeys", L=32)
//
// where HKDF here is the full Extract+Expand pipeline per RFC 5869:
// HKDF-Extract maps the 16-byte Ka through HMAC-SHA-256 (with empty
// salt) into a 32-byte PRK before HKDF-Expand produces the
// confirmation key material. Skipping Extract (passing Ka directly
// to HKDF-Expand) silently produces wrong output bytes — chip-tool's
// prover then rejects our Pake2 with CHIP_ERROR_INTERNAL because
// our cB tag is unrelated to its locally-computed expectation.
func deriveKeys(context, idA, idB, x, y, z, v []byte, w0 *big.Int) (kcA, kcB, ke []byte) {
	tt := buildTranscript(context, idA, idB, x, y, z, v, w0)
	kMain := sha256.Sum256(tt)
	ka := kMain[0:16]
	ke = make([]byte, 16)
	copy(ke, kMain[16:32])

	// connectedhomeip's Spake2p::GenerateKeys (CHIPCryptoPAL.cpp):
	//   KDF(Ka, hash_size/2=16, nullptr, 0,
	//       "ConfirmationKeys", 16, Kcab, hash_size=32)
	//   then split Kcab into KcA (first 16) || KcB (second 16).
	// hkdf.Key does HKDF-Extract(salt=nil, IKM=Ka) followed by
	// HKDF-Expand(PRK, info, L=32) — equivalent to chip-tool's KDF.
	confKeys, err := hkdf.Key(sha256.New, ka, nil, "ConfirmationKeys", 32)
	if err != nil {
		panic(fmt.Sprintf("spake2: hkdf.Key: %v", err))
	}
	kcA = make([]byte, 16)
	kcB = make([]byte, 16)
	copy(kcA, confKeys[0:16])
	copy(kcB, confKeys[16:32])
	return kcA, kcB, ke
}

// buildTranscript assembles TT per Matter §3.10.4 / RFC 9383 §3.3:
// each field is length-prefixed (8-byte little-endian length).
//
// The w0 scalar is encoded as a fixed-length [ScalarSize]-byte (32)
// big-endian unsigned integer — connectedhomeip's Spake2p uses
// `FEWrite(w0, point_buffer, fe_size=32)` regardless of Matter
// §3.10.4's published "40-byte" wording. The 40-byte width applies
// only to the pre-reduction PBKDF2 output, not to the post-mod-n
// scalar that participates in the transcript hash.
func buildTranscript(context, idA, idB, x, y, z, v []byte, w0 *big.Int) []byte {
	mBytes := elliptic.Marshal(curve(), mPoint.X, mPoint.Y) //nolint:staticcheck // SA1019: see curve()
	nBytes := elliptic.Marshal(curve(), nPoint.X, nPoint.Y) //nolint:staticcheck // SA1019: see curve()

	w0Bytes := scalarTo32Bytes(w0)

	var buf bytes.Buffer
	for _, part := range [][]byte{
		context, idA, idB, mBytes, nBytes, x, y, z, v, w0Bytes,
	} {
		_ = binary.Write(&buf, binary.LittleEndian, uint64(len(part)))
		buf.Write(part)
	}
	return buf.Bytes()
}

// scalarTo32Bytes returns a left-zero-padded 32-byte big-endian
// representation of n.
func scalarTo32Bytes(n *big.Int) []byte {
	out := make([]byte, ScalarSize)
	b := n.Bytes()
	copy(out[ScalarSize-len(b):], b)
	return out
}

// --- helpers ---

// randomScalarFn is the indirection [randomScalar] dispatches through
// so unit tests can substitute a deterministic scalar for golden-
// vector validation. Production code never replaces it; the variable
// is unexported and the test seam lives in spake2_test.go alongside
// the [randomScalar] consumers.
var randomScalarFn = defaultRandomScalar

// randomScalar returns a uniformly-distributed scalar in [1, n-1]
// where n is the P-256 group order. Tests may override
// [randomScalarFn] to inject a known value.
func randomScalar() (*big.Int, error) {
	return randomScalarFn()
}

func defaultRandomScalar() (*big.Int, error) {
	n := curve().Params().N
	for range 16 {
		// Sample 32 random bytes, map mod n; reject zero.
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return nil, fmt.Errorf("spake2: rand.Int: %w", err)
		}
		if k.Sign() != 0 {
			return k, nil
		}
	}
	return nil, errors.New("spake2: failed to sample non-zero scalar")
}

// unmarshalAndValidate decodes an uncompressed P-256 point and
// rejects the identity element / off-curve points. SPAKE2+ requires
// the peer's contribution to be a valid non-identity point or the
// session is trivially attackable.
func unmarshalAndValidate(b []byte) (*ecdsa.PublicKey, error) {
	if len(b) != PointSize || b[0] != 0x04 {
		return nil, fmt.Errorf("%w: bad encoding", ErrInvalidPoint)
	}
	x, y := elliptic.Unmarshal(curve(), b) //nolint:staticcheck // SA1019: see curve()
	if x == nil || y == nil {
		return nil, fmt.Errorf("%w: not on curve", ErrInvalidPoint)
	}
	if x.Sign() == 0 && y.Sign() == 0 {
		return nil, fmt.Errorf("%w: identity element", ErrInvalidPoint)
	}
	return &ecdsa.PublicKey{Curve: curve(), X: x, Y: y}, nil
}

// hmacSHA256 returns the full 32-byte HMAC-SHA-256 over data. Callers
// truncate to [ConfirmTagSize] when needed.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
