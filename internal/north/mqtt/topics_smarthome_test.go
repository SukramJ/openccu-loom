// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"testing"

	hatopic "github.com/SukramJ/go-hamqtt/topic"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
)

// TestTopicBuilderSpellsTheSharedSmartHomeGrammar pins this daemon's own
// topic builder to go-hamqtt's mqtt-smarthome layout, byte for byte: the
// instance topics and the three item functions. The builder composes its
// strings itself — the naming package owns the item paths — so this is the
// guard that the function level and the instance topics cannot drift from
// the layout the five bridges use.
//
// Falsifiability: spell `naming.StatusTopic` with a `state` function, or
// [TopicBuilder.Connected] as `bridge/status`, and the matching row fails.
func TestTopicBuilderSpellsTheSharedSmartHomeGrammar(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"openccu-loom", "home/loom"} {
		sh, err := hatopic.NewSmartHomeMultiLevel(base)
		if err != nil {
			t.Fatalf("NewSmartHomeMultiLevel(%q): %v", base, err)
		}
		tb := NewTopicBuilder(base)
		item := []string{"ccu", "ccu-HmIP-RF", "0001ABCD", "1", "values", "STATE"}
		for _, c := range []struct{ name, got, want string }{
			{"connected", tb.Connected(), sh.Connected()},
			{"info", tb.Info(), sh.Info()},
			{"maintenance stats", tb.Maintenance("stats"), sh.Maintenance("stats")},
			{"maintenance restart", tb.Maintenance("set", "restart"), sh.Maintenance("set", "restart")},
			{"status", naming.StatusTopic(base, item...), sh.Status(item...)},
			{"set", naming.SetTopic(base, item...), sh.Set(item...)},
			{"meta", naming.MetaTopic(base, item...), sh.Meta(item...)},
			{"data point state", tb.DataPointState("ccu", "ccu-HmIP-RF", "0001ABCD", 1, "STATE"), sh.Status(item...)},
			{"data point set", tb.DataPointCommand("ccu", "ccu-HmIP-RF", "0001ABCD", 1, "STATE"), sh.Set(item...)},
			{"data point meta", tb.DataPointConfig("ccu", "ccu-HmIP-RF", "0001ABCD", 1, "STATE"), sh.Meta(item...)},
		} {
			if c.got != c.want {
				t.Errorf("base %q, %s: builder %q, shared layout %q", base, c.name, c.got, c.want)
			}
		}
	}
}

// TestTopicBaseConformance pins the start-up warning's predicate: one topic
// level is mqtt-smarthome conformant, several are accepted and are not.
func TestTopicBaseConformance(t *testing.T) {
	t.Parallel()
	for base, want := range map[string]bool{
		"openccu-loom": true, "loom_2": true, "/openccu-loom/": true,
		"home/loom": false, "a/b/c": false,
	} {
		if got := TopicBaseConformant(base); got != want {
			t.Errorf("TopicBaseConformant(%q) = %v, want %v", base, got, want)
		}
	}
}
