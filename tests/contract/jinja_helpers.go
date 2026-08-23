// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// renderJinja is a minimal Jinja2 template renderer covering exactly
// the filter and test subset that openccu-loom's HA-Discovery templates
// use. It is NOT a full Jinja2 implementation — complex control flow,
// inheritance, macros, and most filters are intentionally absent.
//
// Supported constructs:
//
//   - `{% if value_json is defined %} … {% endif %}` and its
//     `{% if value_json is defined and value_json.value is not none %}`
//     variant — guard against undefined value_json (empty or non-JSON
//     input → empty output)
//   - `{{ expr }}` — variable/filter expression output
//   - Filters: `lower`, `int`, `float`, `tojson`, `default(x)`, `default(x, true)`
//   - `value_json.<field>` — JSON field access
//   - Arithmetic: `(value_json.field | float * N)` — multiply after float cast
//
// envelope is the raw JSON string published on the state topic. An empty
// string or non-JSON string is treated as "value_json undefined".
//
// renderJinja never fails the test directly — it returns the rendered
// string and leaves assertion to the caller. When a construct is
// unrecognised it returns a descriptive placeholder so callers can
// detect and report unexpected template shapes.
func renderJinja(t *testing.T, template, envelope string) string {
	t.Helper()

	// Parse envelope into a generic map. An empty or non-JSON envelope
	// is represented as nil (value_json undefined).
	var valueJSON map[string]any
	if envelope != "" {
		if err := json.Unmarshal([]byte(envelope), &valueJSON); err != nil {
			// Non-JSON envelope → value_json undefined.
			valueJSON = nil
		}
	}

	return evalTemplate(template, valueJSON)
}

// evalTemplate is the recursive evaluator. valueJSON is nil when the
// input envelope was empty or non-JSON (value_json undefined).
func evalTemplate(tmpl string, valueJSON map[string]any) string {
	// Strip outer whitespace from template.
	tmpl = strings.TrimSpace(tmpl)

	// --- Handle {% if value_json is defined [and ... is not none] %} … {% endif %}
	// This is the guard pattern used by valueJSONValueTemplate and
	// valueJSONValueLowerTemplate. Both halves are evaluated:
	//   - valueJSON == nil → return ""  (`is defined` fires)
	//   - the `is not none` clause present AND value_json.value is JSON
	//     null → return "" as well
	//   - otherwise evaluate the inner block
	//
	// Evaluating the second clause rather than merely tolerating it is
	// what makes it load-bearing here. While the regex only skipped over
	// it, deleting `and value_json.value is not none` from production left
	// every guard in this package green — and the real Jinja renders
	// `none | lower` as the literal string "none", which Home Assistant
	// matches against neither payload_on nor payload_off.
	ifDefinedRe := regexp.MustCompile(`(?s)\{%[-\s]*if\s+value_json\s+is\s+defined(\s+and\s+value_json\.value\s+is\s+not\s+none)?\s*[-\s]*%\}(.*?)\{%[-\s]*endif\s*[-\s]*%\}`)
	if loc := ifDefinedRe.FindStringSubmatchIndex(tmpl); loc != nil {
		if valueJSON == nil {
			return ""
		}
		notNoneClause := loc[2] >= 0
		if notNoneClause {
			if v, ok := valueJSON["value"]; !ok || v == nil {
				return ""
			}
		}
		inner := tmpl[loc[4]:loc[5]]
		return evalTemplate(inner, valueJSON)
	}

	// --- Handle {{ expr }} --------------------------------------------------
	exprRe := regexp.MustCompile(`\{\{(.*?)\}\}`)
	result := exprRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		inner := strings.TrimSpace(match[2 : len(match)-2])
		return evalExpr(inner, valueJSON)
	})

	// Strip remaining Jinja block tags ({% ... %}) — they were not matched
	// above; replace with empty to avoid leaking raw syntax.
	blockRe := regexp.MustCompile(`\{%-?\s*.*?\s*-?%\}`)
	result = blockRe.ReplaceAllString(result, "")

	return strings.TrimSpace(result)
}

// evalExpr evaluates a single `{{ expr }}` expression.
// Handles the filter pipeline: `value_json.field | filter1 | filter2 ...`
// and arithmetic: `(value_json.field | float * 0.5)`.
func evalExpr(expr string, valueJSON map[string]any) string {
	expr = strings.TrimSpace(expr)

	// Arithmetic expression: `(base_expr * multiplier)` or
	// `(base_expr / divisor)`.
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		inner := expr[1 : len(expr)-1]
		return evalArith(inner, valueJSON)
	}

	// Filter pipeline: split on `|`.
	parts := splitPipeline(expr)
	if len(parts) == 0 {
		return ""
	}

	// Resolve the base value.
	base := strings.TrimSpace(parts[0])
	val := resolveValue(base, valueJSON)

	// Apply filters in order.
	for _, filter := range parts[1:] {
		val = applyFilter(strings.TrimSpace(filter), val)
	}

	return fmt.Sprintf("%v", val)
}

// splitPipeline splits a Jinja filter pipeline on `|` while respecting
// parentheses (so `default(none, true)` is not split mid-argument).
func splitPipeline(expr string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, c := range expr {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				parts = append(parts, expr[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, expr[start:])
	return parts
}

// resolveValue resolves a base expression (field access or literal) to
// its Go value.
func resolveValue(expr string, valueJSON map[string]any) any {
	expr = strings.TrimSpace(expr)
	switch {
	case expr == "value_json":
		return valueJSON
	case strings.HasPrefix(expr, "value_json."):
		field := strings.TrimPrefix(expr, "value_json.")
		if valueJSON == nil {
			return nil
		}
		// Support nested access: value_json.a.b (rare in our templates).
		parts := strings.SplitN(field, ".", 2)
		v, ok := valueJSON[parts[0]]
		if !ok {
			return nil
		}
		if len(parts) == 2 {
			if sub, ok := v.(map[string]any); ok {
				return sub[parts[1]]
			}
			return nil
		}
		return v
	case expr == "value":
		// bare `value` without `value_json.` prefix — used in some
		// arithmetic templates (e.g. `{{ value | float / 100 }}`).
		// In the round-trip context this means the raw payload scalar;
		// we return nil to signal "not applicable here".
		return nil
	default:
		return expr // literal string
	}
}

// applyFilter applies a single Jinja filter (with optional args) to val.
func applyFilter(filter string, val any) any {
	// Parse filter name and args: `filtername(arg1, arg2)` or just `filtername`.
	name, args := parseFilter(filter)
	switch name {
	case "lower":
		return strings.ToLower(fmt.Sprintf("%v", val))
	case "upper":
		return strings.ToUpper(fmt.Sprintf("%v", val))
	case "int":
		s := fmt.Sprintf("%v", val)
		var f float64
		if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
			return int(f)
		}
		return 0
	case "float":
		s := fmt.Sprintf("%v", val)
		var f float64
		if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
			return f
		}
		return 0.0
	case "default":
		// `| default(fallback)` or `| default(fallback, true)`.
		// The second `true` arg coerces falsy values (empty string,
		// 0, false) to the fallback as well.
		if val == nil {
			if len(args) > 0 {
				a := strings.TrimSpace(args[0])
				if a == "none" || a == "None" {
					return nil
				}
				return a
			}
			return nil
		}
		if len(args) >= 2 && strings.TrimSpace(args[1]) == "true" {
			// Coerce falsy.
			s := fmt.Sprintf("%v", val)
			if s == "" || s == "0" || s == "false" || s == "<nil>" {
				if len(args) > 0 {
					a := strings.TrimSpace(args[0])
					if a == "none" || a == "None" {
						return nil
					}
					return a
				}
				return nil
			}
		}
		return val
	case "tojson":
		// Serialize val back to JSON.
		if val == nil {
			return "null"
		}
		b, err := json.Marshal(val)
		if err != nil {
			return "null"
		}
		return string(b)
	case "string":
		return fmt.Sprintf("%v", val)
	default:
		// Unknown filter — return value unchanged to avoid masking bugs.
		return val
	}
}

// parseFilter parses `filtername(arg1, arg2, ...)` into name and args.
func parseFilter(filter string) (name string, args []string) {
	filter = strings.TrimSpace(filter)
	before, after, ok := strings.Cut(filter, "(")
	if !ok {
		return filter, nil
	}
	name = before
	rest := after
	rest = strings.TrimSuffix(rest, ")")
	if rest == "" {
		return name, nil
	}
	for a := range strings.SplitSeq(rest, ",") {
		args = append(args, strings.TrimSpace(a))
	}
	return name, args
}

// evalArith evaluates simple arithmetic: `<base_expr> * <number>` or
// `<base_expr> / <number>`. Used for multiplier templates like
// `(value_json.value | float * 0.1)`.
func evalArith(expr string, valueJSON map[string]any) string {
	expr = strings.TrimSpace(expr)
	// Try `<pipeline> * <num>` or `<pipeline> / <num>`.
	for _, op := range []string{" * ", " / "} {
		before, after, ok := strings.Cut(expr, op)
		if !ok {
			continue
		}
		lhs := strings.TrimSpace(before)
		rhs := strings.TrimSpace(after)

		// Evaluate the left-hand pipeline.
		parts := splitPipeline(lhs)
		var lhsVal any
		if len(parts) > 0 {
			lhsVal = resolveValue(strings.TrimSpace(parts[0]), valueJSON)
			for _, f := range parts[1:] {
				lhsVal = applyFilter(strings.TrimSpace(f), lhsVal)
			}
		}

		// Parse rhs as float.
		var rhsF float64
		if _, err := fmt.Sscanf(rhs, "%f", &rhsF); err != nil {
			return fmt.Sprintf("arith-err:%v", err)
		}

		// Coerce lhsVal to float.
		lhsF := toFloat(lhsVal)
		var result float64
		if op == " * " {
			result = lhsF * rhsF
		} else {
			if rhsF == 0 {
				return "arith-div-zero"
			}
			result = lhsF / rhsF
		}
		// Format without trailing zeros (mirrors formatMultiplier).
		return fmt.Sprintf("%g", result)
	}
	// No arithmetic operator found — evaluate as plain expression.
	return evalExpr(expr, valueJSON)
}

// toFloat coerces any to float64 for arithmetic.
func toFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
			return f
		}
	}
	return 0
}
