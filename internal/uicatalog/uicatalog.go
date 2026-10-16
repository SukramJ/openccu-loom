// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package uicatalog reads the SPA's translation catalogues out of
// assets/ui/src/lib/i18n.ts. It is the one parser the contract tests and
// the reference-page generator share, so a change to the catalogue's
// syntax breaks both in the same way instead of letting them drift.
//
// The parser understands the subset of TypeScript the catalogue file uses
// for its object literals: line and block comments, double-quoted,
// single-quoted and placeholder-free template string literals, string
// concatenation with `+`, and trailing commas. Anything else inside a
// catalogue is reported as an error rather than skipped.
package uicatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Path is the catalogue file, relative to the repository root.
const Path = "assets/ui/src/lib/i18n.ts"

// Catalogs holds the parsed English and German catalogues, keyed by
// translation key.
type Catalogs struct {
	EN map[string]string
	DE map[string]string
}

// Load reads and parses the catalogue file below the repository root.
func Load(root string) (Catalogs, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path))) //nolint:gosec // G304: a fixed asset path below the repository root
	if err != nil {
		return Catalogs{}, fmt.Errorf("uicatalog: read %s: %w", Path, err)
	}
	return Parse(string(raw))
}

// Parse extracts the `const EN` and `const DE` object literals from the
// catalogue source.
func Parse(src string) (Catalogs, error) {
	en, err := parseCatalog(src, "const EN")
	if err != nil {
		return Catalogs{}, err
	}
	de, err := parseCatalog(src, "const DE")
	if err != nil {
		return Catalogs{}, err
	}
	return Catalogs{EN: en, DE: de}, nil
}

func parseCatalog(src, marker string) (map[string]string, error) {
	start := strings.Index(src, marker)
	if start < 0 {
		return nil, fmt.Errorf("uicatalog: %q catalogue marker not found", marker)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		return nil, fmt.Errorf("uicatalog: %q has no object literal", marker)
	}
	p := &parser{src: src, pos: start + open + 1, name: marker}
	return p.object()
}

type parser struct {
	src  string
	pos  int
	name string
}

func (p *parser) errorf(format string, args ...any) error {
	line := 1 + strings.Count(p.src[:min(p.pos, len(p.src))], "\n")
	return fmt.Errorf("uicatalog: %s, line %d: %s", p.name, line, fmt.Sprintf(format, args...))
}

// skip advances over whitespace and comments.
func (p *parser) skip() error {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case strings.HasPrefix(p.src[p.pos:], "//"):
			nl := strings.IndexByte(p.src[p.pos:], '\n')
			if nl < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += nl + 1
			}
		case strings.HasPrefix(p.src[p.pos:], "/*"):
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				return p.errorf("unterminated block comment")
			}
			p.pos += 2 + end + 2
		default:
			return nil
		}
	}
	return nil
}

func (p *parser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) object() (map[string]string, error) {
	out := make(map[string]string)
	for {
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.peek() == '}' {
			p.pos++
			return out, nil
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		if err := p.skip(); err != nil {
			return nil, err
		}
		if p.peek() != ':' {
			return nil, p.errorf("expected ':' after key %q", key)
		}
		p.pos++
		val, err := p.value()
		if err != nil {
			return nil, fmt.Errorf("%w (key %q)", err, key)
		}
		// A repeated key overrides the earlier one, as in a JS object literal.
		out[key] = val
		if err := p.skip(); err != nil {
			return nil, err
		}
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
		default:
			return nil, p.errorf("expected ',' or '}' after value of %q", key)
		}
	}
}

func (p *parser) key() (string, error) {
	switch p.peek() {
	case '"', '\'', '`':
		return p.str()
	}
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return "", p.errorf("expected a key, found %q", string(p.peek()))
	}
	return p.src[start:p.pos], nil
}

// value parses one string literal or a `+`-joined chain of them.
func (p *parser) value() (string, error) {
	var b strings.Builder
	for {
		if err := p.skip(); err != nil {
			return "", err
		}
		s, err := p.str()
		if err != nil {
			return "", err
		}
		b.WriteString(s)
		if err := p.skip(); err != nil {
			return "", err
		}
		if p.peek() != '+' {
			return b.String(), nil
		}
		p.pos++
	}
}

func (p *parser) str() (string, error) {
	quote := p.peek()
	if quote != '"' && quote != '\'' && quote != '`' {
		return "", p.errorf("expected a string literal, found %q", string(quote))
	}
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return b.String(), nil
		case c == '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		case quote == '`' && strings.HasPrefix(p.src[p.pos:], "${"):
			return "", p.errorf("template literal with a placeholder is not a static string")
		case c == '\n' && quote != '`':
			return "", p.errorf("unterminated string literal")
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", p.errorf("unterminated string literal")
}

func (p *parser) escape(b *strings.Builder) error {
	p.pos++ // backslash
	if p.pos >= len(p.src) {
		return p.errorf("dangling escape")
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		b.WriteByte('\r')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'v':
		b.WriteByte('\v')
	case '0':
		b.WriteByte(0)
	case '\n':
		// Line continuation contributes nothing.
	case 'u':
		return p.unicodeEscape(b)
	case 'x':
		if p.pos+2 > len(p.src) {
			return p.errorf("short \\x escape")
		}
		n, err := strconv.ParseUint(p.src[p.pos:p.pos+2], 16, 8)
		if err != nil {
			return p.errorf("bad \\x escape: %v", err)
		}
		p.pos += 2
		b.WriteRune(rune(n))
	default:
		// \" \' \` \\ and any other character stand for themselves.
		r, size := utf8.DecodeRuneInString(p.src[p.pos-1:])
		b.WriteRune(r)
		p.pos += size - 1
	}
	return nil
}

func (p *parser) unicodeEscape(b *strings.Builder) error {
	if p.peek() == '{' {
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 0 {
			return p.errorf("unterminated \\u{} escape")
		}
		n, err := strconv.ParseUint(p.src[p.pos+1:p.pos+end], 16, 32)
		if err != nil {
			return p.errorf("bad \\u{} escape: %v", err)
		}
		if n > unicode.MaxRune {
			return p.errorf("\\u{} escape out of range: %x", n)
		}
		p.pos += end + 1
		b.WriteRune(rune(n)) //nolint:gosec // G115: n is at most unicode.MaxRune, checked above
		return nil
	}
	if p.pos+4 > len(p.src) {
		return p.errorf("short \\u escape")
	}
	n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 16)
	if err != nil {
		return p.errorf("bad \\u escape: %v", err)
	}
	p.pos += 4
	r := rune(n)
	// A UTF-16 surrogate pair spells one code point across two escapes.
	if r >= 0xD800 && r <= 0xDBFF && strings.HasPrefix(p.src[p.pos:], "\\u") && p.pos+6 <= len(p.src) {
		lo, err := strconv.ParseUint(p.src[p.pos+2:p.pos+6], 16, 16)
		if err == nil && lo >= 0xDC00 && lo <= 0xDFFF {
			p.pos += 6
			r = 0x10000 + (r-0xD800)<<10 + (rune(lo) - 0xDC00)
		}
	}
	b.WriteRune(r)
	return nil
}
