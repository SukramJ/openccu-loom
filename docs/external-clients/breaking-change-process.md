# Breaking a published identity — what has to be written down

!!! info "Who this page is for"
    Maintainers about to change something a consumer has already stored:
    an entity `unique_id`, an MQTT topic, a routing key. Operators do not
    need this page; what they need is the release note it produces.

A consumer that has stored an identity cannot be asked to notice that it
moved. Home Assistant binds an entity to its `unique_id` in the registry, and
that binding carries the entity's history, its area, its customisations and
every automation and dashboard card that names it. Change the identity and the
binding does not follow.

That is not a reason never to change one — an identity built on something
renameable breaks by itself, every time somebody tidies up a name. It is a
reason to make the change **once**, deliberately, with the loss stated before
it happens rather than discovered afterwards.

## The two planes differ, and only in what a consumer can do about it

[ADR 0068](../adr/0068-unique-id-stability-per-plane.md) sets the rule. In
short:

- **REST/WS** — a consumer owns its registry and can rewrite it. A re-key
  ships with the consumer-side migration in the same release, and the old key
  must be derivable from data the consumer already receives.
- **MQTT discovery** — Home Assistant's MQTT integration has **no**
  `unique_id` migration path at all: no second field, no `async_migrate_entries`
  in `homeassistant/components/mqtt/`, and the entity takes the value straight
  from the discovery payload (`entity.py`, `self._attr_unique_id =
  config.get(CONF_UNIQUE_ID)`). Nor can another integration repair it, since
  registry migration is scoped to one config entry and MQTT-discovered
  entities belong to the MQTT entry.

So on MQTT there is no migration, only a break. It may still be made — what it
may not be is silent.

## What a break on the MQTT plane must carry

**1. The old and new identity, side by side, with a real example.**
Not the rule that produces them. A reader matches strings.

**2. What is lost, named.** History, area, customisations, and every
automation or dashboard entry that refers to the entity by id. Say it plainly;
"entities re-key once" reads like a formality and this is not one.

**3. The orphan swept, not left behind.** The daemon retracts the retained
discovery config of the old identity by publishing an empty payload at its
topic, so Home Assistant removes the old entity instead of leaving a
permanently unavailable one beside the new. `RunDiscoveryOrphanCleanupOnce`
(`internal/north/mqtt/retain_cleanup.go`) is the mechanism; a break that
changes the discovery topic must extend it, and a break that keeps the topic
and changes only the payload gets this for free.

This is the whole of the mitigation, and it is worth being exact about what it
achieves: it does not save the history. It converts a silent zombie into a
clean disappearance, so the operator sees what happened and acts once.

**4. What the operator has to do, as steps.** Re-point automations and
dashboards; optionally give the new entity the old entity's `entity_id` so
references keep resolving. Where a re-point cannot be avoided, say so.

**5. How to see the blast radius before upgrading.** Which entities are
affected, and how to list them — a topic filter, an id prefix, a template
query.

**6. Announced where the affected operator actually reads.** The root
`CHANGELOG.md` is not enough: an add-on user sees
`packaging/ha-addon/*/CHANGELOG.md` in the Home Assistant add-on store, and
that is where the notice belongs too.

## What makes it a break rather than a fix

Both of these are breaks and both need this page:

- the identity changes for an object that stayed the same;
- the shape changes such that a stored id no longer parses the way a consumer
  expects.

A new entity appearing, or one disappearing because the underlying CCU object
is gone, is neither — nothing stored moved.

## Where it is written

One section per break in
[`ha-unique-id-migration.md`](./ha-unique-id-migration.md), added **before**
the change is released rather than after, plus the release-note entries from
point 6. The ADR records the rule; that page records each application of it.
