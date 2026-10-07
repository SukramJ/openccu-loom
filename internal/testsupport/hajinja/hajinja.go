// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package hajinja renders the Jinja2 subset this daemon's Home Assistant
// discovery templates use, with Jinja2's own semantics, so a test can feed a
// template the payload the daemon really publishes and see what Home
// Assistant would see.
//
// It is a test helper, not a template engine: anything outside the subset is
// an error rather than a guess, so a template that grows a construct the
// helper does not know fails loudly instead of rendering something
// plausible. The semantics follow Jinja2 3.1 as Home Assistant runs it
// (homeassistant/helpers/template/__init__.py, `async_render_with_possible_json_value`):
//
//   - `value` is the raw payload, `value_json` its JSON decoding, or
//     undefined when the payload is not JSON;
//   - an undefined value prints as "", is false, iterates as empty and has
//     length 0, but an attribute of an undefined value is an error
//     (jinja2.Undefined._fail_with_undefined_error);
//   - an attribute of a mapping that lacks the key is undefined;
//   - `dict(x, k=v)` raises for an undefined or `None` x, as Jinja2's
//     sandbox does (verified against jinja2 3.1.6);
//   - the rendered result is whitespace-stripped.
//
// Supported: `{{ … }}`, `{% if %}/{% elif %}/{% else %}/{% endif %}`,
// `{% set name = … %}`; literals (strings, integers, floats, true/false/none
// in either case, dict literals); attribute access, `.get(k, d)` on a
// mapping, `dict(mapping, key=value)`; `a if c else b`, `or`, `and`, `not`,
// comparisons, `*`, `/`; the tests `defined`, `none`; the filters `lower`,
// `upper`, `int`, `float`, `tojson`, `default`, `length`.
package hajinja

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// undefinedValue is Jinja's undefined value.
type undefinedValue struct{ name string }

// errTemplate wraps every render failure.
var errTemplate = errors.New("hajinja")

// RenderValue renders template the way Home Assistant renders an MQTT value
// template against payload.
//
// loom:reachable:reason="test-support package: its only callers are the template round-trip tests in internal/north/mqtt and tests/contract (renderJinja), which pin discovery templates against Jinja2 semantics; no production code renders Jinja"
func RenderValue(template, payload string) (string, error) {
	vars := map[string]any{"value": payload}
	if v, err := decodeJSON(payload); err == nil {
		vars["value_json"] = v
	}
	return Render(template, vars)
}

// Render renders template with the given variables, which must be values of
// the JSON model (nil, bool, int64, float64, string, map[string]any, []any).
//
// loom:reachable:reason="test-support package: its only callers are internal/north/mqtt tests (the text command_template round trip) and RenderValue; no production code renders Jinja"
func Render(template string, vars map[string]any) (string, error) {
	nodes, err := parseTemplate(template)
	if err != nil {
		return "", err
	}
	scope := map[string]any{}
	for k, v := range vars {
		scope[k] = v
	}
	var out strings.Builder
	if err := execNodes(nodes, scope, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	return normalize(v), nil
}

func normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if !strings.ContainsAny(s, ".eE") {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i
			}
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
		return x
	}
	return v
}

// --- template structure ------------------------------------------------

type node interface{}

type textNode string

type exprNode struct{ e expr }

type setNode struct {
	name string
	e    expr
}

type ifNode struct {
	conds  []expr
	bodies [][]node
	els    []node
}

type token struct {
	kind string // "text", "expr", "stmt"
	val  string
}

func lex(t string) ([]token, error) {
	var out []token
	for t != "" {
		i := strings.IndexAny(t, "{")
		for i >= 0 && i+1 < len(t) && t[i+1] != '{' && t[i+1] != '%' {
			j := strings.IndexAny(t[i+1:], "{")
			if j < 0 {
				i = -1
				break
			}
			i += 1 + j
		}
		if i < 0 || i+1 >= len(t) {
			out = append(out, token{"text", t})
			break
		}
		if i > 0 {
			out = append(out, token{"text", t[:i]})
		}
		closer := "}}"
		kind := "expr"
		if t[i+1] == '%' {
			closer, kind = "%}", "stmt"
		}
		end := strings.Index(t[i+2:], closer)
		if end < 0 {
			return nil, fmt.Errorf("%w: unterminated %q", errTemplate, t[i:])
		}
		inner := strings.TrimSpace(t[i+2 : i+2+end])
		if strings.HasPrefix(inner, "-") || strings.HasSuffix(inner, "-") {
			return nil, fmt.Errorf("%w: whitespace control is not supported", errTemplate)
		}
		out = append(out, token{kind, inner})
		t = t[i+2+end+2:]
	}
	return out, nil
}

func parseTemplate(t string) ([]node, error) {
	toks, err := lex(t)
	if err != nil {
		return nil, err
	}
	nodes, rest, stop, err := parseNodes(toks)
	if err != nil {
		return nil, err
	}
	if stop != "" || len(rest) > 0 {
		return nil, fmt.Errorf("%w: unexpected {%% %s %%}", errTemplate, stop)
	}
	return nodes, nil
}

// parseNodes parses until an elif/else/endif statement, which it returns.
func parseNodes(toks []token) (nodes []node, rest []token, stop string, err error) {
	for len(toks) > 0 {
		tk := toks[0]
		toks = toks[1:]
		switch tk.kind {
		case "text":
			nodes = append(nodes, textNode(tk.val))
		case "expr":
			e, err := parseExpr(tk.val)
			if err != nil {
				return nil, nil, "", err
			}
			nodes = append(nodes, exprNode{e})
		case "stmt":
			word, arg, _ := strings.Cut(tk.val, " ")
			switch word {
			case "if":
				n, rem, err := parseIf(arg, toks)
				if err != nil {
					return nil, nil, "", err
				}
				nodes = append(nodes, n)
				toks = rem
			case "set":
				name, rhs, ok := strings.Cut(arg, "=")
				if !ok {
					return nil, nil, "", fmt.Errorf("%w: bad set %q", errTemplate, tk.val)
				}
				e, err := parseExpr(rhs)
				if err != nil {
					return nil, nil, "", err
				}
				nodes = append(nodes, setNode{strings.TrimSpace(name), e})
			case "elif", "else", "endif":
				return nodes, toks, tk.val, nil
			default:
				return nil, nil, "", fmt.Errorf("%w: unsupported statement %q", errTemplate, tk.val)
			}
		}
	}
	return nodes, nil, "", nil
}

func parseIf(cond string, toks []token) (ifNode, []token, error) {
	var n ifNode
	for {
		e, err := parseExpr(cond)
		if err != nil {
			return n, nil, err
		}
		body, rest, stop, err := parseNodes(toks)
		if err != nil {
			return n, nil, err
		}
		n.conds = append(n.conds, e)
		n.bodies = append(n.bodies, body)
		toks = rest
		switch {
		case strings.HasPrefix(stop, "elif "):
			cond = strings.TrimPrefix(stop, "elif ")
		case stop == "else":
			els, rest2, stop2, err := parseNodes(toks)
			if err != nil {
				return n, nil, err
			}
			if stop2 != "endif" {
				return n, nil, fmt.Errorf("%w: else without endif", errTemplate)
			}
			n.els = els
			return n, rest2, nil
		case stop == "endif":
			return n, toks, nil
		default:
			return n, nil, fmt.Errorf("%w: if without endif", errTemplate)
		}
	}
}

func execNodes(nodes []node, scope map[string]any, out *strings.Builder) error {
	for _, n := range nodes {
		switch x := n.(type) {
		case textNode:
			out.WriteString(string(x))
		case exprNode:
			v, err := x.e.eval(scope)
			if err != nil {
				return err
			}
			s, err := str(v)
			if err != nil {
				return err
			}
			out.WriteString(s)
		case setNode:
			v, err := x.e.eval(scope)
			if err != nil {
				return err
			}
			scope[x.name] = v
		case ifNode:
			done := false
			for i, c := range x.conds {
				v, err := c.eval(scope)
				if err != nil {
					return err
				}
				if truthy(v) {
					if err := execNodes(x.bodies[i], scope, out); err != nil {
						return err
					}
					done = true
					break
				}
			}
			if !done {
				if err := execNodes(x.els, scope, out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// --- value semantics -----------------------------------------------------

func truthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedValue:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return true
}

// str is Python's str() of a value, as Jinja prints it.
func str(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "None", nil
	case undefinedValue:
		return "", nil
	case bool:
		if x {
			return "True", nil
		}
		return "False", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return pyFloat(x), nil
	case string:
		return x, nil
	}
	return "", fmt.Errorf("%w: printing a %T has no stable Python repr here; use tojson", errTemplate, v)
}

func pyFloat(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func toJSON(v any) (string, error) {
	if err := noUndefined(v); err != nil {
		return "", err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("%w: tojson: %w", errTemplate, err)
	}
	return strings.TrimSpace(b.String()), nil
}

func noUndefined(v any) error {
	switch x := v.(type) {
	case undefinedValue:
		return fmt.Errorf("%w: tojson of an undefined value %q", errTemplate, x.name)
	case map[string]any:
		for _, e := range x {
			if err := noUndefined(e); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := noUndefined(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func getattr(obj any, name string) (any, error) {
	switch x := obj.(type) {
	case undefinedValue:
		return nil, fmt.Errorf("%w: %q is undefined, so it has no attribute %q", errTemplate, x.name, name)
	case map[string]any:
		if v, ok := x[name]; ok {
			return v, nil
		}
		return undefinedValue{name: name}, nil
	}
	return undefinedValue{name: name}, nil
}

// --- expressions ---------------------------------------------------------

type expr interface {
	eval(scope map[string]any) (any, error)
}

type lit struct{ v any }

func (l lit) eval(map[string]any) (any, error) { return l.v, nil }

type name struct{ n string }

func (n name) eval(s map[string]any) (any, error) {
	if v, ok := s[n.n]; ok {
		return v, nil
	}
	return undefinedValue{name: n.n}, nil
}

type attr struct {
	obj  expr
	name string
}

func (a attr) eval(s map[string]any) (any, error) {
	o, err := a.obj.eval(s)
	if err != nil {
		return nil, err
	}
	return getattr(o, a.name)
}

type dictLit struct{ keys, vals []expr }

func (d dictLit) eval(s map[string]any) (any, error) {
	out := map[string]any{}
	for i := range d.keys {
		k, err := d.keys[i].eval(s)
		if err != nil {
			return nil, err
		}
		ks, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("%w: non-string dict key", errTemplate)
		}
		v, err := d.vals[i].eval(s)
		if err != nil {
			return nil, err
		}
		out[ks] = v
	}
	return out, nil
}

type call struct {
	fn     expr
	args   []expr
	kwKeys []string
	kwVals []expr
}

func (c call) eval(s map[string]any) (any, error) {
	args := make([]any, len(c.args))
	for i, a := range c.args {
		v, err := a.eval(s)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	kws := map[string]any{}
	for i, k := range c.kwKeys {
		v, err := c.kwVals[i].eval(s)
		if err != nil {
			return nil, err
		}
		kws[k] = v
	}
	switch f := c.fn.(type) {
	case name:
		if f.n != "dict" {
			return nil, fmt.Errorf("%w: unsupported function %q", errTemplate, f.n)
		}
		out := map[string]any{}
		if len(args) > 1 {
			return nil, fmt.Errorf("%w: dict() takes one positional argument", errTemplate)
		}
		if len(args) == 1 {
			switch m := args[0].(type) {
			case map[string]any:
				for k, v := range m {
					out[k] = v
				}
			case undefinedValue:
				return nil, fmt.Errorf("%w: dict() of the undefined %q raises UndefinedError", errTemplate, m.name)
			default:
				return nil, fmt.Errorf("%w: dict() of a %T is a TypeError", errTemplate, args[0])
			}
		}
		for k, v := range kws {
			out[k] = v
		}
		return out, nil
	case attr:
		if f.name != "get" {
			return nil, fmt.Errorf("%w: unsupported method %q", errTemplate, f.name)
		}
		o, err := f.obj.eval(s)
		if err != nil {
			return nil, err
		}
		m, ok := o.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: .get on a %T", errTemplate, o)
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("%w: .get without a key", errTemplate)
		}
		key, ok := args[0].(string)
		if v, found := m[key]; ok && found {
			return v, nil
		}
		if len(args) > 1 {
			return args[1], nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: unsupported call", errTemplate)
}

type filter struct {
	in   expr
	name string
	args []expr
}

func (f filter) eval(s map[string]any) (any, error) {
	v, err := f.in.eval(s)
	if err != nil {
		return nil, err
	}
	args := make([]any, len(f.args))
	for i, a := range f.args {
		if args[i], err = a.eval(s); err != nil {
			return nil, err
		}
	}
	switch f.name {
	case "lower", "upper":
		sv, err := str(v)
		if err != nil {
			return nil, err
		}
		if f.name == "lower" {
			return strings.ToLower(sv), nil
		}
		return strings.ToUpper(sv), nil
	case "int":
		def := any(int64(0))
		if len(args) > 0 {
			def = args[0]
		}
		return toInt(v, def), nil
	case "float":
		def := any(0.0)
		if len(args) > 0 {
			def = args[0]
		}
		return toFloat(v, def), nil
	case "tojson":
		js, err := toJSON(v)
		if err != nil {
			return nil, err
		}
		return js, nil
	case "default":
		var def any = ""
		if len(args) > 0 {
			def = args[0]
		}
		boolean := len(args) > 1 && truthy(args[1])
		if _, und := v.(undefinedValue); und || (boolean && !truthy(v)) {
			return def, nil
		}
		return v, nil
	case "length":
		switch x := v.(type) {
		case undefinedValue:
			return int64(0), nil
		case string:
			return int64(len([]rune(x))), nil
		case map[string]any:
			return int64(len(x)), nil
		case []any:
			return int64(len(x)), nil
		}
		return nil, fmt.Errorf("%w: length of a %T", errTemplate, v)
	}
	return nil, fmt.Errorf("%w: unsupported filter %q", errTemplate, f.name)
}

func toInt(v, def any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return int64(f)
		}
	}
	return def
}

func toFloat(v, def any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return 1.0
		}
		return 0.0
	case int64:
		return float64(x)
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return def
}

type test struct {
	in   expr
	name string
	neg  bool
}

func (t test) eval(s map[string]any) (any, error) {
	v, err := t.in.eval(s)
	if err != nil {
		return nil, err
	}
	var r bool
	switch t.name {
	case "defined":
		_, und := v.(undefinedValue)
		r = !und
	case "none":
		r = v == nil
	default:
		return nil, fmt.Errorf("%w: unsupported test %q", errTemplate, t.name)
	}
	return r != t.neg, nil
}

type unary struct {
	op string
	e  expr
}

func (u unary) eval(s map[string]any) (any, error) {
	v, err := u.e.eval(s)
	if err != nil {
		return nil, err
	}
	switch u.op {
	case "not":
		return !truthy(v), nil
	case "-":
		switch x := v.(type) {
		case int64:
			return -x, nil
		case float64:
			return -x, nil
		}
	}
	return nil, fmt.Errorf("%w: bad unary %s", errTemplate, u.op)
}

type binary struct {
	op   string
	l, r expr
}

func (b binary) eval(s map[string]any) (any, error) {
	l, err := b.l.eval(s)
	if err != nil {
		return nil, err
	}
	switch b.op {
	case "and":
		if !truthy(l) {
			return l, nil
		}
		return b.r.eval(s)
	case "or":
		if truthy(l) {
			return l, nil
		}
		return b.r.eval(s)
	}
	r, err := b.r.eval(s)
	if err != nil {
		return nil, err
	}
	switch b.op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	}
	lf, lok := num(l)
	rf, rok := num(r)
	if !lok || !rok {
		return nil, fmt.Errorf("%w: %s between %T and %T is a TypeError", errTemplate, b.op, l, r)
	}
	switch b.op {
	case ">=":
		return lf >= rf, nil
	case "<=":
		return lf <= rf, nil
	case ">":
		return lf > rf, nil
	case "<":
		return lf < rf, nil
	case "*":
		li, lInt := l.(int64)
		ri, rInt := r.(int64)
		if lInt && rInt {
			return li * ri, nil
		}
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("%w: division by zero", errTemplate)
		}
		return lf / rf, nil
	}
	return nil, fmt.Errorf("%w: unsupported operator %s", errTemplate, b.op)
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func equal(a, b any) bool {
	af, aok := num(a)
	bf, bok := num(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b) && fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b)
}

type cond struct{ yes, c, no expr }

func (c cond) eval(s map[string]any) (any, error) {
	v, err := c.c.eval(s)
	if err != nil {
		return nil, err
	}
	if truthy(v) {
		return c.yes.eval(s)
	}
	if c.no == nil {
		return undefinedValue{}, nil
	}
	return c.no.eval(s)
}

// --- expression parser ---------------------------------------------------

type parser struct {
	toks []string
	pos  int
}

func parseExpr(src string) (expr, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.conditional()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("%w: trailing %q in %q", errTemplate, p.toks[p.pos:], src)
	}
	return e, nil
}

func tokenize(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		if c := s[i]; c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		tok, err := scanToken(s, i)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		i += len(tok)
	}
	return out, nil
}

func isIdentByte(c byte, first bool) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || !first && c >= '0' && c <= '9'
}

// scanToken returns the token starting at s[i], which is not blank.
func scanToken(s string, i int) (string, error) {
	c := s[i]
	switch {
	case c == '\'' || c == '"':
		j := i + 1
		for j < len(s) && s[j] != c {
			if s[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(s) {
			return "", fmt.Errorf("%w: unterminated string", errTemplate)
		}
		return s[i : j+1], nil
	case strings.ContainsRune(">=<!", rune(c)) && i+1 < len(s) && s[i+1] == '=':
		return s[i : i+2], nil
	case strings.ContainsRune("(){},:|.*/<>-=", rune(c)):
		return string(c), nil
	case c >= '0' && c <= '9':
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		return s[i:j], nil
	case isIdentByte(c, true):
		j := i
		for j < len(s) && isIdentByte(s[j], false) {
			j++
		}
		return s[i:j], nil
	}
	return "", fmt.Errorf("%w: unexpected %q", errTemplate, c)
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *parser) next() string { t := p.peek(); p.pos++; return t }

func (p *parser) expect(t string) error {
	if p.next() != t {
		return fmt.Errorf("%w: expected %q", errTemplate, t)
	}
	return nil
}

func (p *parser) conditional() (expr, error) {
	yes, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.peek() != "if" {
		return yes, nil
	}
	p.next()
	c, err := p.or()
	if err != nil {
		return nil, err
	}
	var no expr
	if p.peek() == "else" {
		p.next()
		if no, err = p.conditional(); err != nil {
			return nil, err
		}
	}
	return cond{yes: yes, c: c, no: no}, nil
}

func (p *parser) or() (expr, error) {
	l, err := p.and()
	for err == nil && p.peek() == "or" {
		p.next()
		var r expr
		if r, err = p.and(); err == nil {
			l = binary{"or", l, r}
		}
	}
	return l, err
}

func (p *parser) and() (expr, error) {
	l, err := p.not()
	for err == nil && p.peek() == "and" {
		p.next()
		var r expr
		if r, err = p.not(); err == nil {
			l = binary{"and", l, r}
		}
	}
	return l, err
}

func (p *parser) not() (expr, error) {
	if p.peek() == "not" {
		p.next()
		e, err := p.not()
		return unary{"not", e}, err
	}
	return p.compare()
}

func (p *parser) compare() (expr, error) {
	l, err := p.math()
	for err == nil {
		op := p.peek()
		if op != "==" && op != "!=" && op != ">=" && op != "<=" && op != ">" && op != "<" {
			break
		}
		p.next()
		var r expr
		if r, err = p.math(); err == nil {
			l = binary{op, l, r}
		}
	}
	return l, err
}

func (p *parser) math() (expr, error) {
	l, err := p.unaryExpr()
	for err == nil && (p.peek() == "*" || p.peek() == "/") {
		op := p.next()
		var r expr
		if r, err = p.unaryExpr(); err == nil {
			l = binary{op, l, r}
		}
	}
	return l, err
}

func (p *parser) unaryExpr() (expr, error) {
	if p.peek() == "-" {
		p.next()
		e, err := p.unaryExpr()
		return unary{"-", e}, err
	}
	e, err := p.postfix()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case "|":
			p.next()
			fname := p.next()
			f := filter{in: e, name: fname}
			if p.peek() == "(" {
				args, _, _, err := p.callArgs()
				if err != nil {
					return nil, err
				}
				f.args = args
			}
			e = f
		case "is":
			p.next()
			neg := false
			if p.peek() == "not" {
				p.next()
				neg = true
			}
			e = test{in: e, name: strings.ToLower(p.next()), neg: neg}
		default:
			return e, nil
		}
	}
}

func (p *parser) callArgs() (args []expr, kwKeys []string, kwVals []expr, err error) {
	if err := p.expect("("); err != nil {
		return nil, nil, nil, err
	}
	for p.peek() != ")" {
		if p.pos+1 < len(p.toks) && p.toks[p.pos+1] == "=" {
			k := p.next()
			p.next()
			v, err := p.conditional()
			if err != nil {
				return nil, nil, nil, err
			}
			kwKeys, kwVals = append(kwKeys, k), append(kwVals, v)
		} else {
			v, err := p.conditional()
			if err != nil {
				return nil, nil, nil, err
			}
			args = append(args, v)
		}
		if p.peek() == "," {
			p.next()
		} else if p.peek() != ")" {
			return nil, nil, nil, fmt.Errorf("%w: bad argument list", errTemplate)
		}
	}
	p.next()
	return args, kwKeys, kwVals, nil
}

func (p *parser) postfix() (expr, error) {
	e, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case ".":
			p.next()
			e = attr{obj: e, name: p.next()}
		case "(":
			args, keys, vals, err := p.callArgs()
			if err != nil {
				return nil, err
			}
			e = call{fn: e, args: args, kwKeys: keys, kwVals: vals}
		default:
			return e, nil
		}
	}
}

func (p *parser) primary() (expr, error) {
	t := p.next()
	switch {
	case t == "":
		return nil, fmt.Errorf("%w: unexpected end", errTemplate)
	case t == "(":
		e, err := p.conditional()
		if err != nil {
			return nil, err
		}
		return e, p.expect(")")
	case t == "{":
		d := dictLit{}
		for p.peek() != "}" {
			k, err := p.conditional()
			if err != nil {
				return nil, err
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			v, err := p.conditional()
			if err != nil {
				return nil, err
			}
			d.keys, d.vals = append(d.keys, k), append(d.vals, v)
			if p.peek() == "," {
				p.next()
			}
		}
		p.next()
		return d, nil
	case t[0] == '\'' || t[0] == '"':
		return lit{unquote(t[1 : len(t)-1])}, nil
	case t[0] >= '0' && t[0] <= '9':
		if strings.Contains(t, ".") {
			f, err := strconv.ParseFloat(t, 64)
			return lit{f}, err
		}
		i, err := strconv.ParseInt(t, 10, 64)
		return lit{i}, err
	}
	switch strings.ToLower(t) {
	case "true":
		return lit{true}, nil
	case "false":
		return lit{false}, nil
	case "none":
		return lit{nil}, nil
	}
	return name{t}, nil
}

func unquote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
