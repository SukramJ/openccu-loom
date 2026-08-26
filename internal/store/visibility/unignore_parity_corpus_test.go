// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file is a reference-cross-referenced PARITY CORPUS for the un_ignore
// decider, distinct from the per-function unit tests. Each case states the
// un_ignore lines an operator writes, a set of (model, channel, parameter)
// probes, and the EXPECTED ignore decision derived from the Python reference
// grammar (store/visibility/parser.py + parameter_decider.py) — NOT
// recorded from OpenCCU-Loom's own output. That asymmetry is the point: a
// self-recorded golden would freeze whatever the code does (including a bug);
// reference-derived expectations lock actual parity and fail when behaviour
// drifts away from the reference.
//
// The flagship regression guard is the concrete-channel case below: the
// pre-rewrite parser split the whole line on ':' and mis-parsed
// `BOOST_TIME:VALUES@HmIP-eTRV:1` into a garbage model/channel, so the
// parameter was never actually un-ignored. This corpus would have failed
// against that parser; it now pins the corrected grammar.
//
// Where OpenCCU-Loom deliberately diverges from the reference (documented in
// notes/parity/by_design.md BD-Visibility-UnIgnoreMatchingEdges) the expected
// value encodes the divergence and the case comment says so.

// loadUnIgnoreLines parses raw un_ignore lines and loads the resulting entries
// into a fresh decider. Unparseable lines are skipped (mirrors the file
// loader); the test asserts decisions, not parse errors.
func loadUnIgnoreLines(t *testing.T, required []hmenum.Parameter, lines ...string) *ParameterDecider {
	t.Helper()
	d := NewParameterDecider(nil)
	if len(required) > 0 {
		d.SetRequiredParameters(required)
	}
	entries := make([]UnIgnoreEntry, 0, len(lines))
	for _, line := range lines {
		parsed := ParseUnIgnoreLine(line)
		if parsed.Entry != nil {
			entries = append(entries, *parsed.Entry)
		}
	}
	d.LoadUnIgnore(entries)
	return d
}

func TestUnIgnoreParityCorpus(t *testing.T) {
	t.Parallel()

	const boost = hmenum.Parameter("BOOST_TIME") // a member of the static ignoredParameters set
	values := hmenum.ParamsetKeyValues

	type probe struct {
		name      string
		model     string
		channelNo int
		param     hmenum.Parameter
		paramset  hmenum.ParamsetKey
		want      bool // expected IsParameterIgnored
	}
	cases := []struct {
		name     string
		lines    []string
		required []hmenum.Parameter
		probes   []probe
	}{
		{
			// Baseline: with no un_ignore, a statically-ignored parameter is
			// ignored everywhere. Reference: parameter_decider.py:378-380.
			name: "baseline static ignore",
			probes: []probe{
				{"eTRV ch1", "HmIP-eTRV", 1, boost, values, true},
				{"other model", "HmIP-BROLL", 3, boost, values, true},
			},
		},
		{
			// Simple form (bare parameter) un-ignores on every VALUES lookup.
			// Reference: parser.py simple_parameter + parameter_decider.py:370.
			name:  "simple un-ignore flips everywhere",
			lines: []string{"BOOST_TIME"},
			probes: []probe{
				{"eTRV ch1", "HmIP-eTRV", 1, boost, values, false},
				{"eTRV ch7", "HmIP-eTRV", 7, boost, values, false},
				{"other model", "HmIP-BROLL", 3, boost, values, false},
			},
		},
		{
			// The grammar-rewrite regression guard. A complex concrete-channel
			// entry un-ignores ONLY on the named model + channel. The old
			// colon-split parser mis-read this line and never un-ignored it.
			// Reference grammar: PARAMETER:PARAMSET@MODEL:CHANNEL_NO.
			name:  "complex concrete-channel un-ignore (grammar-rewrite guard)",
			lines: []string{"BOOST_TIME:VALUES@HmIP-eTRV:1"},
			probes: []probe{
				{"eTRV ch1 (un-ignored)", "HmIP-eTRV", 1, boost, values, false},
				{"eTRV ch2 (still ignored)", "HmIP-eTRV", 2, boost, values, true},
				{"other model ch1 (still ignored)", "HmIP-BWTH", 1, boost, values, true},
			},
		},
		{
			// Any-model wildcard with a concrete channel: the reference search
			// matrix's any-model point is (UN_IGNORE_WILDCARD, channel.no), so
			// a `*` model un-ignores the parameter on that channel for EVERY
			// model. The parser lower-cases `*`; the matrix treats the literal
			// "all" token as the wildcard — see by_design note. OpenCCU-Loom
			// matches the reference on the any-model+concrete-channel point.
			name:  "any-model wildcard, concrete channel",
			lines: []string{"BOOST_TIME:VALUES@all:1"},
			probes: []probe{
				{"modelA ch1", "HmIP-eTRV", 1, boost, values, false},
				{"modelB ch1", "HmIP-BWTH", 1, boost, values, false},
				{"modelA ch2 (not un-ignored)", "HmIP-eTRV", 2, boost, values, true},
			},
		},
		{
			// Documented divergence (BD-Visibility-UnIgnoreMatchingEdges): a
			// `*` / empty channel is a LIVE any-channel wildcard in
			// OpenCCU-Loom, honouring the operator's evident "all channels"
			// intent, where the reference's own matrix leaves it inert.
			name:  "channel wildcard is live (documented divergence)",
			lines: []string{"BOOST_TIME:VALUES@HmIP-eTRV:*"},
			probes: []probe{
				{"eTRV ch1", "HmIP-eTRV", 1, boost, values, false},
				{"eTRV ch9", "HmIP-eTRV", 9, boost, values, false},
				{"other model ch1 (model still scoped)", "HmIP-BWTH", 1, boost, values, true},
			},
		},
		{
			// Documented behaviour (BD-Visibility-UnIgnoreMatchingEdges): a
			// required parameter always surfaces, regardless of the static
			// ignore list — OpenCCU-Loom's deliberate "required wins" choice.
			name:     "required parameter always surfaces",
			required: []hmenum.Parameter{boost},
			probes: []probe{
				{"eTRV ch1", "HmIP-eTRV", 1, boost, values, false},
				{"other model", "HmIP-BROLL", 3, boost, values, false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := loadUnIgnoreLines(t, tc.required, tc.lines...)
			for _, p := range tc.probes {
				got := d.IsParameterIgnored(p.model, "", p.channelNo, p.paramset, p.param)
				if got != p.want {
					t.Errorf("%s: IsParameterIgnored(%s, ch=%d, %s, %s) = %v, want %v",
						p.name, p.model, p.channelNo, p.param, p.paramset, got, p.want)
				}
			}
		})
	}
}
