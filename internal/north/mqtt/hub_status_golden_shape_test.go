// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// hubGoldenPreCCUGateDigests is every pinned hub discovery payload as it
// stood immediately BEFORE the per-CCU availability gate was added, hashed
// with its `availability` key removed.
//
// It is the mechanical proof that the golden move was one-dimensional. The
// gate is a byte-moving change: it rewrites 45 of the 48 pinned payloads,
// and a reviewer reading that diff cannot tell an added availability entry
// from a `state_topic` that shifted underneath it, a `unique_id` that got
// re-derived, a `device` block that lost a field or a platform that changed
// — all of which are unmigratable in Home Assistant, which keys the entity
// registry on `unique_id` and the device registry on `identifiers` with no
// migration path for either.
//
// So the claim is pinned instead of eyeballed: strike `availability` out of
// every payload and NOTHING may have moved. A digest per entry makes the
// claim exact (it covers fields nobody thought to list, including ones added
// later) and makes a violation point at the entry that broke it. The
// digests are a frozen artefact of a specific commit, not a regenerable
// golden — there is no `-update` flag for them and there must not be one,
// because regenerating them would delete the very statement they make.
//
// A legitimate later change to a hub payload — a new field, a corrected
// name — breaks this test on the entries it touches. That is the intended
// cost: it is then deleted along with the availability-only claim it
// records, in the commit that makes the change, deliberately.
//
// One such change has landed, and rather than delete the claim for 48
// entries to admit a delta in two, the delta is carried explicitly. The
// discovery-slug unification moved the `object_id` segment of exactly two
// topics ([hubGoldenSlugUnificationTopicMoves]), and nothing else anywhere
// — so those two entries are checked by substituting the pre-unification
// topic back in and requiring the ORIGINAL pre-gate digest. That is a
// stronger statement than a re-baselined digest would be: it says the
// payload did not move AND names the one byte range of the topic that did.
var hubGoldenPreCCUGateDigests = map[string]string{
	"aggregate/alarm-messages":                  "807cd762dc60a5ac5d8e9f86f516b858520792f2e3b9ecc6634e09303d501f88",
	"aggregate/hazard-second-central-inbox":     "c09a19a17ffa95d927066f2e9b6203230d882be3dfdc919089d6c50d89030b26",
	"aggregate/inbox":                           "3a0c094f53fe70c0f083197702204fc58f16b9f6d68d5f82fd6903b610d65532",
	"aggregate/service-messages":                "174b962d2e53e5fea6b0912985e7325adcaf40aae8cc9643e834593f5a0e7009",
	"connectivity/bidcos":                       "9a76a962d2b21311945fe92c732e227ea146db043d3b8b5c328a099d088346fa",
	"connectivity/hazard-second-central":        "0d47d90b66ec9d879078553354967891de331f7a4660eab871302ac2e2f12f08",
	"connectivity/hmip":                         "68faa5b28bfe2ff376249805ea1c40952540cccb12d09b0a3e259648e4f764ab",
	"install-mode/button-bidcos":                "60bfa9f49de16637c2d2055bdcdb3c7cc6ef35296dcf552991ef6bc634e5260d",
	"install-mode/button-bidcos-wired":          "6d25b4724fc4aeffbd450c631c9f4e45f2de1471d4da8155dc65bc770cacae74",
	"install-mode/button-hmip":                  "3856119de3d6a727345e8ae4e40a5e823c49cec5d0c98d2f427f552dfe1e284e",
	"install-mode/hazard-second-central-sensor": "b0154de7d67a82c4d2cec83979c08235772b6a5adc6aba89a01ee98417ba36a2",
	"install-mode/sensor-bidcos":                "28265686678140b69c6f8aed317a170fb324c604d6e4547feaf9d02f61416b8c",
	"install-mode/sensor-bidcos-wired":          "4e73799a3dddc4108c3db6452ec2dcf341aec8af600a17411af0352b45c61792",
	"install-mode/sensor-hmip":                  "939bfc9a1b0218528b17c42e4910b8e2cc7d11bb8adffd6f2d0a5cc6d52e74d9",
	"program/hazard-second-central":             "484df37acdce72d1cb8541840e7c7b0b2052cf7c940f5b925c30c05801acf2ef",
	"program/hazard-unnamed":                    "2cbe72941ffa77c033ff12b4d41753b3e71cc7d10d3b0facb60e6dd2dbbce66a",
	"program/legacy-single-switch":              "c267bd732e4936fde86a6283eeaabc80d5d2052ec902841e8f4c0759e35dc5bf",
	"program/role-execute-button":               "1f0e949e60dc895ea464563cf62706ef2010f23c79c518346e9aaf9763c1cf8c",
	"program/role-principal-switch":             "0f09abf661f1db70dc1fe83b75e2ea57402d298ef89121e725f827b1216f0cf2",
	"system/connection-latency":                 "e021d684d0e9f37dd397fec264b346c3c722dcb3ac14ceadb9f8d70153c9ddec",
	"system/daemon-status":                      "73b54f2336c9fac90494a4af20ff4bcb2fa2a0f49a2a3b92bb8324f132f1b5e4",
	"system/hazard-second-central-health":       "94d56a7d1144a2b32722dc300c8222b333112d087ecf65e11d5b95d56aa936ee",
	"system/health":                             "f54628d128285bae1d556e77ac62a08b897d4ff00a6530dc64575e0c6b8a675a",
	"system/hub-update":                         "25d63484474664512180451caa3d507e7540b404cc7cc629a146d9eafeecfa02",
	"system/last-event-age":                     "94ca79b4ceed2ce78ca0f9a38a70173d3724f4a6ee4ad3ffcd4532131e4d69e6",
	"sysvar/alarm-binary-sensor":                "10cbf3cba54d74380a175a15a636e39a1b5c816bbda4ab4cf9592076a03ef1a9",
	"sysvar/auto-energy-counter":                "c0d2b0e679881ec31540146a6f1f9e8d16741488a28bdd071d217baadab2a348",
	"sysvar/float-number-fallback-range":        "603bc1ca439cded1429a2142c9993221da72f3f5df5ae67a330f70fc9527d590",
	"sysvar/hazard-accent-twin-a":               "bb6903a9fd824db4d549d26264ae221691d7827108052089c2380f070291a43d",
	"sysvar/hazard-accent-twin-b":               "9d1e3093d50edf1002f01f01afea390cbca2415deaca9e7907d884201691532b",
	"sysvar/hazard-linked-to-channel":           "74660891a0de1452f4dedcc48b6779499ebeee37fdf0bb8cdd8716ece7f8ab6f",
	"sysvar/hazard-linked-to-device":            "d9860678e0935b4bf90b48174180814e54ab27ccdbe8dc3c8de15e26410a3651",
	"sysvar/hazard-linked-to-internal-device":   "c320708430e59171f40063c8b8b7ccc52b69f3136d160fe9f2e949281cf525dd",
	"sysvar/hazard-literal-double-underscore":   "3bfef3cf182e3c3be5ea761e3816390c02bd76427476c021f37ce7232453254c",
	"sysvar/hazard-punctuation-twin-a":          "dff38adf6a3af6e03c0451b237884947385a12904e2b263cbfe83dc836442177",
	"sysvar/hazard-punctuation-twin-b":          "ac7a117e6a65d223e877afd54eb2f2ee3ee09db71d382f012f9d57a553270650",
	"sysvar/hazard-second-central":              "5ab46cfec0a8987102ad0ae20417c59129ff39ee94b63de4a89ba42087bc9ce7",
	"sysvar/hazard-unresolved-vid":              "8c3869b0ac91872a20e65f6a3541dd49f7a1f61259d82e6cbbab721ff3ce62ed",
	"sysvar/integer-number-bounded":             "8bee43fc7dc35831e81e259dfaf38bf6e30c887d07534efd2d3bb4639f278eb5",
	"sysvar/list-extended-without-options":      "b683f8f59e04157e44246cc32cf0b359cde0184c5e0b2e5033fce3d3f1a227aa",
	"sysvar/list-select":                        "907b5e44caa851b3069617b4ed0fdcb01a56f07444880e354a0e6690b4acf33c",
	"sysvar/list-sensor-enum":                   "e376df0e9b9ca3ceab550d3bc2f9e777508377d67be189c47c6b8e0f75d68e2e",
	"sysvar/logic-binary-sensor":                "bec02452b28589c6f3d4b6803cce8448df895fe8eaa73b5cf39e9730219feaaf",
	"sysvar/logic-switch":                       "c927f1bb1a1bcd696257d8bbfecef55b20f9d5bb84eb3ce1a3662b4de0b4c310",
	"sysvar/number-sensor":                      "60f1f65b1952d31fd6df7c06cb9f5af77e96893cde07569dafa1a7cda9bf9f64",
	"sysvar/string-sensor":                      "9014939e57acd3ea5296d5608177a04f9d678fbcf3b2e30f535495d888202e91",
	"sysvar/string-text":                        "d8800f2edb04200d2b0238cad8765fa9c8781d569d75f098cfc79e22301ff5b2",
	"sysvar/unknown-type-sensor":                "8ae04e35f10286a5a4d3ac067ca74b0196ed4ba4f4b6352dd7ee02975ba38d16",
}

// hubGoldenSlugUnificationTopicMoves is every pinned hub entry whose
// discovery TOPIC moved when `naming.DiscoverySlug` was unified onto the
// shared `topic.Slug` rule, against the topic it carried before.
//
// Both are hazard rows added for exactly this step, and both are a fixed
// defect rather than drift: `Café Terrasse` used to slug to `caf_terrasse`
// — the same object id as a sibling named `Caf Terrasse`, so two system
// variables shared one retained config — and `Watchdog:_CCU-Jack` used to
// keep a literal double underscore. No payload field of either entry moved,
// and no `unique_id`, `identifiers`, `default_entity_id` or state topic
// moved anywhere on this plane; [TestHubGoldenChangedOnlyInAvailability] is
// what proves the second half of that sentence.
var hubGoldenSlugUnificationTopicMoves = map[string]string{
	"sysvar/hazard-accent-twin-a":             "homeassistant/binary_sensor/ccu-01_sysvars/caf_terrasse/config",
	"sysvar/hazard-literal-double-underscore": "homeassistant/binary_sensor/ccu-01_sysvars/watchdog__ccu-jack/config",
}

// TestHubGoldenChangedOnlyInAvailability strips `availability` from every
// pinned hub payload and requires the remainder to hash to what it hashed to
// before the gate was added.
func TestHubGoldenChangedOnlyInAvailability(t *testing.T) {
	t.Parallel()
	golden := renderedHubEntries(t)

	if len(golden) != len(hubGoldenPreCCUGateDigests) {
		t.Fatalf("pinned %d hub payloads, %d pre-gate digests: an entity was added or removed, "+
			"which this test cannot distinguish from a payload that moved",
			len(golden), len(hubGoldenPreCCUGateDigests))
	}
	for name, entry := range golden {
		want, ok := hubGoldenPreCCUGateDigests[name]
		if !ok {
			t.Errorf("%s: no pre-gate digest — a new hub entity, or a renamed one", name)
			continue
		}
		if was, moved := hubGoldenSlugUnificationTopicMoves[name]; moved {
			// The slug unification moved this topic's object-id segment.
			// Put the old segment back: if the digest then matches the
			// original pre-gate one, the topic move is the whole delta and
			// the payload is untouched — which is the claim. If the topic
			// did NOT in fact move, `was` equals the current topic and this
			// still holds, so the entry has to be removed from the map by
			// hand rather than silently passing under a stale exemption.
			if entry.Topic == was {
				t.Errorf("%s: listed as a slug-unification topic move but the topic is still %q — "+
					"remove it from hubGoldenSlugUnificationTopicMoves", name, was)
			}
			entry.Topic = was
		}
		if got := hubEntryDigestWithoutAvailability(t, entry); got != want {
			t.Errorf("%s: payload changed outside `availability` (digest %s, want %s). "+
				"The per-CCU gate adds one availability entry and nothing else; anything "+
				"else in this diff is a defect in the change that produced it.", name, got, want)
		}
	}
}

// TestHubGoldenAvailabilityGainedOnlyTheCCUGate is the other half: the
// availability lists themselves may have grown by exactly one entry, that
// entry is the CCU gate, and it is APPENDED rather than substituted.
//
// Appending is the whole semantics. Home Assistant's default
// `availability_mode: "all"` is a conjunction over the list, so an entity
// stays gated on `bridge/status` (daemon up) and gains a gate on
// `hub/status` (CCU on the bus). Replacing the bridge entry instead would
// have traded one blind spot for another: a dead daemon publishes nothing
// about its CCUs.
func TestHubGoldenAvailabilityGainedOnlyTheCCUGate(t *testing.T) {
	t.Parallel()

	for name, entry := range renderedHubEntries(t) {
		list, mode := hubAvailabilityOf(t, entry)
		if len(list) == 0 {
			// The daemon-status sensor: no availability block at all, and
			// `availability_mode` must be absent with it.
			if mode != "" {
				t.Errorf("%s: no availability list but availability_mode=%q", name, mode)
			}
			continue
		}
		if mode != "all" {
			t.Errorf("%s: availability_mode=%q — the CCU gate is a conjunction with the "+
				"bridge entry and means nothing under any other mode", name, mode)
		}
		if first := list[0]["topic"]; first != hubGoldenBase+"/bridge/status" {
			t.Errorf("%s: first availability entry is %v, want the bridge status topic — "+
				"the CCU gate is added alongside it, never instead of it", name, first)
		}
		for i, e := range list {
			topic, _ := e["topic"].(string)
			isGate := strings.HasSuffix(topic, "/hub/status")
			if isGate && i != len(list)-1 {
				t.Errorf("%s: CCU gate at position %d of %d, want last", name, i, len(list))
			}
			if !isGate {
				continue
			}
			if e["payload_available"] != "online" || e["payload_not_available"] != "offline" {
				t.Errorf("%s: CCU gate payloads %v/%v, want online/offline — Home Assistant "+
					"ignores an availability payload it does not recognise and leaves the "+
					"entity permanently unavailable with nothing on the wire to show why",
					name, e["payload_available"], e["payload_not_available"])
			}
		}
	}
}

// hubEntryDigestWithoutAvailability hashes one pinned entry with its
// `availability` key removed. `availability_mode` stays in: it is not part
// of the added entry, and a change to it would be a real behaviour change
// this test should catch.
func hubEntryDigestWithoutAvailability(t *testing.T, entry goldenEntry) string {
	t.Helper()
	stripped := make(map[string]any, len(entry.Payload))
	for k, v := range entry.Payload {
		if k == "availability" {
			continue
		}
		stripped[k] = v
	}
	// Marshalled through the same canonical form the golden comparison uses,
	// so the digest is over the bytes the pin is over.
	raw, err := json.Marshal(map[string]any{"topic": entry.Topic, "payload": stripped})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hubAvailabilityOf(t *testing.T, entry goldenEntry) (list []map[string]any, mode string) {
	t.Helper()
	mode, _ = entry.Payload["availability_mode"].(string)
	raw, ok := entry.Payload["availability"].([]any)
	if !ok {
		return nil, mode
	}
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("availability entry is %T, not an object", e)
		}
		list = append(list, m)
	}
	return list, mode
}

// TestTheFoldsOwnInputsAreNotGatedOnTheFold pins the two deliberate
// exemptions, which exist for one reason stated twice: an entity whose job
// is to report that something went quiet must not be made unavailable by the
// thing it reports.
//
// The per-interface connectivity sensors are the fold's inputs. The
// daemon-status sensor is the same argument one level up, against
// `bridge/status`, and already carries no availability block at all — which
// is why "an entity that declares no gate acquires none" is the rule rather
// than a second exemption list.
func TestTheFoldsOwnInputsAreNotGatedOnTheFold(t *testing.T) {
	t.Parallel()

	golden := renderedHubEntries(t)
	exempt := []string{
		"connectivity/bidcos",
		"connectivity/hmip",
		"connectivity/hazard-second-central",
		"system/daemon-status",
	}
	for _, name := range exempt {
		entry, ok := golden[name]
		if !ok {
			t.Fatalf("%s is not in the pin — the exemption it records may have moved", name)
		}
		list, _ := hubAvailabilityOf(t, entry)
		for _, e := range list {
			if topic, _ := e["topic"].(string); strings.HasSuffix(topic, "/hub/status") {
				t.Errorf("%s lists the CCU gate %q — it reports the signal the gate is "+
					"folded from, so gating it makes it unavailable in exactly the "+
					"situation it exists for", name, topic)
			}
		}
	}
}

// TestEveryOtherCCUScopedHubEntityIsGated is the positive half: the
// exemptions above are the ONLY ones. A hub entity that quietly acquires no
// gate is the defect this change fixes, reintroduced for one entity.
func TestEveryOtherCCUScopedHubEntityIsGated(t *testing.T) {
	t.Parallel()

	exempt := map[string]bool{
		"connectivity/bidcos": true, "connectivity/hmip": true,
		"connectivity/hazard-second-central": true, "system/daemon-status": true,
	}
	for name, entry := range renderedHubEntries(t) {
		if exempt[name] {
			continue
		}
		list, _ := hubAvailabilityOf(t, entry)
		gated := false
		for _, e := range list {
			if topic, _ := e["topic"].(string); strings.HasSuffix(topic, "/hub/status") {
				gated = true
			}
		}
		if !gated {
			t.Errorf("%s takes availability from the daemon alone: an unreachable CCU leaves "+
				"it 'available' with a stale value", name)
		}
	}
}

// TestTheCCUGateIsScopedToTheEntitysOwnCentral: with two CCUs pinned, an
// entity of one must not be gated on the other's reachability. A gate
// rendered from the wrong central is worse than none — it makes an entity
// unavailable for a fault on a machine it has nothing to do with.
func TestTheCCUGateIsScopedToTheEntitysOwnCentral(t *testing.T) {
	t.Parallel()

	for name, entry := range renderedHubEntries(t) {
		list, _ := hubAvailabilityOf(t, entry)
		for _, e := range list {
			topic, _ := e["topic"].(string)
			if !strings.HasSuffix(topic, "/hub/status") {
				continue
			}
			// `<base>/<central>/hub/status`, and the same `<central>` the
			// entity's own state topic carries.
			want := strings.TrimSuffix(topic, "hub/status")
			if state, ok := entry.Payload["state_topic"].(string); ok &&
				strings.Contains(state, "/hub/") && !strings.HasPrefix(state, want) {
				t.Errorf("%s: gate %q but state topic %q — different centrals", name, topic, state)
			}
			if !strings.HasPrefix(topic, hubGoldenBase+"/") {
				t.Errorf("%s: gate %q is not under the configured base", name, topic)
			}
		}
	}
}

// renderedHubEntries is every hub discovery payload as the BUILDERS produce
// it, right now — not as the golden file records it.
//
// The distinction is the difference between a test and a tautology. A shape
// test that reads the pinned file passes for any production change, because
// the file is regenerated from the production code: the byte pin next door
// catches the change and this test says nothing. Reading the builders makes
// each claim here stand on its own.
func renderedHubEntries(t *testing.T) map[string]goldenEntry {
	t.Helper()
	out := map[string]goldenEntry{}
	tb := &TopicBuilder{Base: hubGoldenBase}
	for _, c := range hubGoldenCases() {
		var body map[string]any
		if err := json.Unmarshal(c.item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		out[c.name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(c.item.Component, c.item.NodeID, c.item.ObjectID),
			Payload: body,
		}
	}
	return out
}
