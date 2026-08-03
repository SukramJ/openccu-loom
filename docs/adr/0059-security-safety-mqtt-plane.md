# ADR 0059 — The Security & Safety MQTT plane

Status: accepted
Date: 2026-08-03
Extends: [ADR 0052 — daemon-level alarm MQTT topics](./0052-daemon-level-alarm-mqtt-topics.md)

## Context

ADR 0052 established that alarm zones are daemon-level and therefore
carry no `<central>` segment in their topics, making `<base>/alarm/**`
the second daemon-level tree beside `<base>/bridge/**`.

The Security & Safety domain (`docs/security-safety-concept.md`) has the
same property for the same reason: a hazard class aggregates across
every configured CCU, so scoping its topics to one central would be a
lie. It needs a third tree.

It also needs an answer to a question the alarm plane never had to face:
the domain emits both *state* (what is active now) and *reports* (what
just happened, in a sentence). Those two have opposite retention needs.

## Decision

**A third daemon-level tree, `<base>/security/**`,** with discovery node
id `security` and its own device card `openccu-loom_security`.

**The card is separate from `openccu-loom_alarm`.** The alarm block is
rewritten on every panel discovery and every broker reconnect; two
publishers writing different blocks under one identifier set make the
card name flap and would rename every existing alarm entity. Two cards
cost nothing.

**Retention splits by kind, not by convenience:**

| Topic | Retained | Why |
|---|---|---|
| `security/state`, `alarm`, `problem`, `health` | yes | A consumer connecting later must see the truth immediately, not wait for the next change. |
| `security/class/<class>`, `security/zone/<slug>` | yes | Same. |
| `security/availability` | yes | Availability is only useful if it survives a reconnect. |
| `security/event`, `security/fault` | **no** | A consumer ignores retained payloads on an event topic, and a retained alarm event would re-fire every automation on every broker restart. |
| `security/last_alarm`, `security/last_fault` | yes | Precisely *because* the event topics are not: after a consumer restart these are the only record of what happened. |

Event topics additionally publish at QoS 0. An event is a moment; a
re-delivered alarm event re-fires every automation subscribed to it.

**A class with no known source is not declared at all**, rather than
declared and reported inactive. An installation without gas detectors
should not carry a permanently-off gas alarm in its entity list.

**`OnBrokerConnect` re-seeds only the retained half.** Events are never
replayed — an alarm that fired an hour ago must not fire again because
the broker restarted. This is the reason the two halves are separated at
all.

## Consequences

- The discovery orphan sweep filtered on the `<central>_` node prefix,
  which neither daemon-level plane carries. Both node ids are now named
  explicitly (`daemonLevelNodeIDs`); without that, a retracted zone panel
  or a class that lost its last source would keep a retained discovery
  config alive in every consumer forever, unreachable by any cleanup.
- Attribute payloads are bounded (30 entries) and say so when they
  truncate. A consumer's recorder discards attributes past a size limit,
  so an unbounded list on a fleet-wide fault would silently lose the
  whole attribute set. The full list stays one REST call away via `link`.
- The duress-visibility policy is applied once, in the domain, and each
  plane honours the resulting `Retainable` flag rather than re-deriving
  the rule. A rule implemented in three planes is a rule that will
  eventually disagree with itself, and the plane that gets it wrong
  exposes the person the feature protects.
