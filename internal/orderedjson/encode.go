// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package orderedjson

import (
	"fmt"
	"math"
	"strconv"
)

// indentUnit is orjson's OPT_INDENT_2 step: two spaces per nesting level.
const indentUnit = "  "

// Marshal serialises v exactly the way orjson does with
// `OPT_INDENT_2 | OPT_NON_STR_KEYS`: two-space indentation, ": " after
// each key, "," between members, compact "{}"/"[]" for empty containers,
// no HTML escaping, raw UTF-8 for non-ASCII, orjson's float repr, and no
// trailing newline. The byte stream is intended to match the Python reference's
// export so the result is corpus-compatible with godevccu.
func Marshal(v any) ([]byte, error) {
	e := &encoder{}
	if err := e.encode(v, 0); err != nil {
		return nil, err
	}
	return e.buf, nil
}

type encoder struct {
	buf []byte
}

func (e *encoder) encode(v any, depth int) error {
	switch x := v.(type) {
	case nil:
		e.buf = append(e.buf, "null"...)
	case bool:
		if x {
			e.buf = append(e.buf, "true"...)
		} else {
			e.buf = append(e.buf, "false"...)
		}
	case string:
		e.appendString(x)
	case *Object:
		return e.encodeObject(x, depth)
	case Array:
		return e.encodeArray(x, depth)
	case []any:
		return e.encodeArray(Array(x), depth)
	case float64:
		s, err := formatFloat(x)
		if err != nil {
			return err
		}
		e.buf = append(e.buf, s...)
	case float32:
		s, err := formatFloat(float64(x))
		if err != nil {
			return err
		}
		e.buf = append(e.buf, s...)
	case int:
		e.buf = strconv.AppendInt(e.buf, int64(x), 10)
	case int8:
		e.buf = strconv.AppendInt(e.buf, int64(x), 10)
	case int16:
		e.buf = strconv.AppendInt(e.buf, int64(x), 10)
	case int32:
		e.buf = strconv.AppendInt(e.buf, int64(x), 10)
	case int64:
		e.buf = strconv.AppendInt(e.buf, x, 10)
	case uint:
		e.buf = strconv.AppendUint(e.buf, uint64(x), 10)
	case uint8:
		e.buf = strconv.AppendUint(e.buf, uint64(x), 10)
	case uint16:
		e.buf = strconv.AppendUint(e.buf, uint64(x), 10)
	case uint32:
		e.buf = strconv.AppendUint(e.buf, uint64(x), 10)
	case uint64:
		e.buf = strconv.AppendUint(e.buf, x, 10)
	default:
		return fmt.Errorf("orderedjson: unsupported type %T", v)
	}
	return nil
}

func (e *encoder) encodeObject(o *Object, depth int) error {
	if o == nil || len(o.Members) == 0 {
		e.buf = append(e.buf, '{', '}')
		return nil
	}
	e.buf = append(e.buf, '{')
	inner := depth + 1
	for i := range o.Members {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newlineIndent(inner)
		e.appendString(o.Members[i].Key)
		e.buf = append(e.buf, ':', ' ')
		if err := e.encode(o.Members[i].Value, inner); err != nil {
			return err
		}
	}
	e.newlineIndent(depth)
	e.buf = append(e.buf, '}')
	return nil
}

func (e *encoder) encodeArray(a Array, depth int) error {
	if len(a) == 0 {
		e.buf = append(e.buf, '[', ']')
		return nil
	}
	e.buf = append(e.buf, '[')
	inner := depth + 1
	for i := range a {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newlineIndent(inner)
		if err := e.encode(a[i], inner); err != nil {
			return err
		}
	}
	e.newlineIndent(depth)
	e.buf = append(e.buf, ']')
	return nil
}

func (e *encoder) newlineIndent(depth int) {
	e.buf = append(e.buf, '\n')
	for range depth {
		e.buf = append(e.buf, indentUnit...)
	}
}

// hexDigits backs the \u00XX escape; orjson emits lowercase hex.
const hexDigits = "0123456789abcdef"

// appendString writes a JSON string literal using orjson's escape set:
// only `"`, `\`, and the C0 control range (0x00–0x1F) are escaped — the
// latter as \b \t \n \f \r where defined, otherwise \u00XX. Forward slash,
// DEL (0x7F), U+2028/U+2029 and all other non-ASCII bytes are passed through
// verbatim as UTF-8. This matches orjson (which never HTML-escapes and never
// \u-escapes non-ASCII) byte-for-byte.
func (e *encoder) appendString(s string) {
	e.buf = append(e.buf, '"')
	start := 0
	for i := range len(s) {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		if start < i {
			e.buf = append(e.buf, s[start:i]...)
		}
		switch c {
		case '"':
			e.buf = append(e.buf, '\\', '"')
		case '\\':
			e.buf = append(e.buf, '\\', '\\')
		case '\b':
			e.buf = append(e.buf, '\\', 'b')
		case '\t':
			e.buf = append(e.buf, '\\', 't')
		case '\n':
			e.buf = append(e.buf, '\\', 'n')
		case '\f':
			e.buf = append(e.buf, '\\', 'f')
		case '\r':
			e.buf = append(e.buf, '\\', 'r')
		default:
			e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		}
		start = i + 1
	}
	if start < len(s) {
		e.buf = append(e.buf, s[start:]...)
	}
	e.buf = append(e.buf, '"')
}

// formatFloat renders f the way orjson does (Rust ryū shortest-round-trip):
//   - integral magnitudes carry a ".0" suffix (1.0, 255.0, 1000000000000000.0);
//   - fixed notation is used when the leading significant digit sits at decimal
//     exponent e ∈ [-5, 15], scientific notation otherwise;
//   - the exponent is lowercase "e", carries a sign only when negative, and has
//     no leading zeros (1e-7, 1e16, 3.4028234663852886e38);
//   - signed zero is preserved (-0.0).
//
// Go's strconv produces the same shortest digit sequence but formats it
// differently (e.g. "1e-07", "0"); we reuse its digits and reshape them.
func formatFloat(f float64) (string, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("orderedjson: cannot encode non-finite float %v", f)
	}
	neg := math.Signbit(f)
	abs := math.Abs(f)

	if abs == 0 {
		if neg {
			return "-0.0", nil
		}
		return "0.0", nil
	}

	// Shortest scientific form from strconv: "d[.ddd]e±XX". Parse out the
	// significant digits and the decimal exponent of the leading digit.
	sci := strconv.AppendFloat(nil, abs, 'e', -1, 64)
	digits, exp10 := splitSci(sci)

	var out []byte
	if exp10 >= -5 && exp10 <= 15 {
		out = appendFixed(nil, digits, exp10)
	} else {
		out = appendScientific(nil, digits, exp10)
	}
	if neg {
		return "-" + string(out), nil
	}
	return string(out), nil
}

// splitSci parses strconv's 'e' shortest output ("1.234567e+06") into the
// concatenated significant digits ("1234567") and the leading-digit decimal
// exponent (6).
func splitSci(sci []byte) (digits string, exp10 int) {
	ePos := -1
	for i := range sci {
		if sci[i] == 'e' {
			ePos = i
			break
		}
	}
	mantissa := sci[:ePos]
	exp, _ := strconv.Atoi(string(sci[ePos+1:]))

	d := make([]byte, 0, len(mantissa))
	for i := range mantissa {
		if mantissa[i] != '.' {
			d = append(d, mantissa[i])
		}
	}
	return string(d), exp
}

// appendFixed renders digits in fixed notation, with the leading digit at
// 10^exp10. Integral results gain the orjson ".0" suffix.
func appendFixed(dst []byte, digits string, exp10 int) []byte {
	nd := len(digits)
	if exp10 >= 0 {
		intLen := exp10 + 1
		if nd <= intLen {
			dst = append(dst, digits...)
			for i := nd; i < intLen; i++ {
				dst = append(dst, '0')
			}
			return append(dst, '.', '0')
		}
		dst = append(dst, digits[:intLen]...)
		dst = append(dst, '.')
		return append(dst, digits[intLen:]...)
	}
	// exp10 in [-5, -1]: "0." then (-exp10-1) leading zeros then digits.
	dst = append(dst, '0', '.')
	for range -exp10 - 1 {
		dst = append(dst, '0')
	}
	return append(dst, digits...)
}

// appendScientific renders digits as "d[.ddd]e<exp>" with orjson's exponent
// style (lowercase e, sign only when negative, no leading zeros).
func appendScientific(dst []byte, digits string, exp10 int) []byte {
	dst = append(dst, digits[0])
	if len(digits) > 1 {
		dst = append(dst, '.')
		dst = append(dst, digits[1:]...)
	}
	dst = append(dst, 'e')
	if exp10 < 0 {
		dst = append(dst, '-')
		exp10 = -exp10
	}
	return strconv.AppendInt(dst, int64(exp10), 10)
}
