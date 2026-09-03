// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// unIgnoreLinePattern is the regex for the complex un-ignore format.
// Format: PARAMETER:PARAMSET_KEY@MODEL:CHANNEL_NO
// Mirrors the pattern in store/visibility/parser.py.
var unIgnoreLinePattern = regexp.MustCompile(
	`^(?P<parameter>[^:@]+):(?P<paramset_key>[^@]+)@(?P<model>[^:]+):(?P<channel_no>.*)$`,
)

// unIgnoreWildcard is the sentinel string that represents "all models" or
// "all channels" in the complex un-ignore grammar. It is used both as the
// model field value and as the channel_no field value to express a
// fully-open wildcard entry. The concrete value ("all") matches
// UN_IGNORE_WILDCARD defined by the upstream Python reference implementation.
const unIgnoreWildcard = "all"

// UnIgnoreEntry is one parsed line of an `un_ignore` file.
// The grammar has two forms:
//   - Simple: a bare parameter name; matches any model/channel for the VALUES paramset.
//   - Complex: PARAMETER:PARAMSET_KEY@MODEL:CHANNEL_NO; fully-specified entry.
//
// Simple entries have IsSimple=true. Complex entries carry Model,
// ChannelNo/*ChannelNoIsWildcard, and ParamsetKey.
type UnIgnoreEntry struct {
	// Parameter is the parameter name to un-ignore (always upper-case wire form).
	Parameter hmenum.Parameter
	// IsSimple is true for bare parameter-only entries that apply to all
	// VALUES paramsets on any model/channel. These entries are the fast-path
	// in matchesUnIgnoreLocked.
	IsSimple bool
	// Model restricts the entry to a device model (lower-cased, as parsed).
	// The special value unIgnoreWildcard ("all") means any model.
	// Empty only for simple entries.
	Model string
	// ChannelNo restricts the entry to a specific channel number when non-nil.
	// Nil means the Python None (no specific channel constraint).
	// For simple entries ChannelNo is always nil.
	ChannelNo *int
	// ChannelNoIsWildcard is true when the parsed channel_no was a non-numeric
	// string (e.g. "*"). Python stores it as the raw string; Go uses this flag
	// instead so ChannelNo can stay typed.
	ChannelNoIsWildcard bool
	// ParamsetKey restricts the entry to a paramset.
	// Empty for simple entries.
	ParamsetKey hmenum.ParamsetKey
	// Comment carries any inline `# …` text from the source line.
	Comment string
	// Central scopes the entry to one CCU (central.Unit.Name). Empty means
	// the entry is global and matches every central — the default for
	// entries built by [ParseUnIgnore], which has no central context of its
	// own.
	//
	// No production caller sets this field today. The daemon persists
	// un-ignore patterns per central (one SQLite row set per central, one
	// REST PUT per central) but the composition root then newline-joins
	// every central's patterns into one stream and parses it here, so every
	// live entry carries Central=="" and the scoping below never fires: an
	// un-ignore requested for one CCU applies to the whole fleet. That
	// behaviour is deliberate on the composition-root side
	// (cmd/openccu-loom/visibility_adapter.go documents the decider as
	// fleet-wide) and it contradicts the per-central shape of the operator
	// surface; which of the two is right is an open decision, not something
	// this field settles. Until it is settled, treat the scoping as an
	// unused seam rather than as a live guarantee — the only writers of this
	// field are tests.
	Central string
}

// ParsedUnIgnoreLine is the result of parsing one un-ignore line. It wraps
// either a successfully parsed entry or a parse error string.
type ParsedUnIgnoreLine struct {
	// Entry is the parsed entry; nil when the line had a parse error.
	Entry *UnIgnoreEntry
	// Err is a human-readable error description; empty on success.
	Err string
	// OriginalLine is the raw source line before any trimming.
	OriginalLine string
}

// ParseUnIgnoreLine parses a single un-ignore line and returns a
// [ParsedUnIgnoreLine] result. Blank lines and pure-comment lines return a
// result with Entry == nil and Err == "".
//
// Grammar (one entry per line):
//
//   - Simple: PARAMETER — global un-ignore for VALUES paramset, any model/channel.
//   - Complex: PARAMETER:PARAMSET_KEY@MODEL:CHANNEL_NO — fully-qualified entry.
//
// Mirrors parse_un_ignore_line in store/visibility/parser.py.
func ParseUnIgnoreLine(line string) ParsedUnIgnoreLine {
	result := ParsedUnIgnoreLine{OriginalLine: line}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return result // blank / comment — no entry, no error
	}
	// Split off inline comment.
	comment := ""
	if idx := strings.Index(trimmed, "#"); idx >= 0 {
		comment = strings.TrimSpace(trimmed[idx+1:])
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	if trimmed == "" {
		return result
	}

	if !strings.Contains(trimmed, "@") {
		// Simple format: bare parameter name; no ':' allowed.
		if strings.Contains(trimmed, ":") {
			result.Err = "invalid format: ':' without '@' in '" + trimmed + "'"
			return result
		}
		result.Entry = &UnIgnoreEntry{
			Parameter: hmenum.Parameter(trimmed),
			IsSimple:  true,
			Comment:   comment,
		}
		return result
	}

	// Complex format — match the regex.
	match := unIgnoreLinePattern.FindStringSubmatch(trimmed)
	if match == nil {
		result.Err = "invalid complex format: '" + trimmed + "'; expected 'PARAMETER:PARAMSET@MODEL:CHANNEL'"
		return result
	}
	parameter := match[1]
	paramsetKeyStr := match[2]
	model := strings.ToLower(match[3])
	channelNoStr := match[4]

	// Validate paramset key.
	paramsetKey := hmenum.ParamsetKey(paramsetKeyStr)
	switch paramsetKey {
	case hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster, hmenum.ParamsetKeyLink:
		// accepted
	default:
		result.Err = "invalid paramset key '" + paramsetKeyStr + "' in '" + trimmed + "'"
		return result
	}

	// Parse channel number.
	var channelNo *int
	channelNoIsWildcard := false
	switch {
	case channelNoStr == "":
		// Python None: no specific channel.
		channelNo = nil
	case isNumericString(channelNoStr):
		n, _ := strconv.Atoi(channelNoStr)
		channelNo = &n
	default:
		// Wildcard string (e.g. "*") — keep nil, mark wildcard.
		channelNo = nil
		channelNoIsWildcard = true
	}

	// Simple-wildcard collapse: model=="all" && channel_no=="all" && paramset==VALUES
	// → simple entry (matches any model/channel for VALUES).
	if model == unIgnoreWildcard && channelNoIsWildcard && channelNoStr == unIgnoreWildcard &&
		paramsetKey == hmenum.ParamsetKeyValues {
		result.Entry = &UnIgnoreEntry{
			Parameter: hmenum.Parameter(parameter),
			IsSimple:  true,
			Comment:   comment,
		}
		return result
	}

	// MASTER paramset constraints: channel must be numeric or empty (not a
	// wildcard string), and model must not be a wildcard.
	if paramsetKey == hmenum.ParamsetKeyMaster {
		if channelNoIsWildcard {
			result.Err = "channel must be numeric or empty for MASTER paramset in '" + trimmed + "'"
			return result
		}
		if model == unIgnoreWildcard {
			result.Err = "model must be specified for MASTER paramset in '" + trimmed + "'"
			return result
		}
	}

	result.Entry = &UnIgnoreEntry{
		Parameter:           hmenum.Parameter(parameter),
		IsSimple:            false,
		Model:               model,
		ChannelNo:           channelNo,
		ChannelNoIsWildcard: channelNoIsWildcard,
		ParamsetKey:         paramsetKey,
		Comment:             comment,
	}
	return result
}

// isNumericString reports whether s consists entirely of ASCII decimal digits.
func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParseUnIgnore parses an `un_ignore` file.
// Mirrors parse_un_ignore_line (store/visibility/parser.py) and
// the UnIgnoreRuleParser (store/visibility/parser_handler.py).
//
// Lines that begin with `#` are skipped. Inline comments start at the
// first `#` and are attached to the returned entry's Comment field.
//
// Unparseable lines are skipped silently — caller can re-run with a
// linter to surface them. Returning a parser error per line would
// make the file fragile in the face of CCU firmware churn.
func ParseUnIgnore(r io.Reader) ([]UnIgnoreEntry, error) {
	rules, err := ParseUnIgnoreRules(r)
	return rules.Entries, err
}

// ParseUnIgnoreRules parses an `un_ignore` file and
// returns a [ParsedUnIgnoreRules] that includes both the successfully parsed
// Entries and a count of skipped lines.
// Mirrors UnIgnoreRuleParser.parse_entries (store/visibility/parser_handler.py).
func ParseUnIgnoreRules(r io.Reader) (ParsedUnIgnoreRules, error) {
	var result ParsedUnIgnoreRules
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parsed := ParseUnIgnoreLine(line)
		switch {
		case parsed.Entry != nil:
			result.Entries = append(result.Entries, *parsed.Entry)
		case parsed.Err != "":
			result.SkippedLines++
		}
		// blank / comment lines: neither entry nor error — skip silently
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}
