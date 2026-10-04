# ADR 0082 — Device onboarding is the daemon's own, on every system

- **Status**: accepted (2026-10-04)
- **Related**: [ADR 0002 — multi-CCU as a first-class concept](./0002-multi-ccu-first-class.md),
  [ADR 0071 — south profiles](./0071-south-profiles.md),
  [ADR 0074 — per-central features](./0074-per-central-features.md)

## Context

A newly paired device has to be named and assigned to rooms and functions
before it is useful. The moment that happens matters: MQTT discovery, the
Matter bridge and the outbound webhook publish a device as soon as the
daemon knows it, and the name it carries at that moment is the one the
receiving ecosystem keeps. A device published as `HmIP-SWDO 0001D3C99B4E2F`
and renamed afterwards leaves a Home Assistant entity id that does not
follow the rename.

A CCU answers this with its inbox, a ReGa concept: a paired device waits
there until an operator names it. An openccu-lite system has no ReGa and
therefore no inbox. Building the operator's first contact with a device on
the CCU inbox gives two different experiences, and none at all on the
system that needs it most.

The daemon already has its own mechanism, added as an opt-in
(`delay_new_device_creation`, default off). It holds a device in two
phases that survive a restart:

1. **Waiting to be accepted** — the device is paired on the system and
   announced, but the daemon has not built it: no channels, no data
   points. The descriptions are parked.
2. **Waiting to be released** — the operator accepted it. The device is
   built and configurable in the daemon's own surfaces, and it is withheld
   from the ecosystems (MQTT, Matter, outbound webhook) until the operator
   releases it.

The mechanism is independent of the system type: it parks descriptions the
daemon receives, whichever event source delivered them. What made it feel
like a CCU feature is everything around it. It was off unless an operator
found an expert switch. Its surface was the "Inbox" view, whose navigation
entry was gated on the per-central feature `hub.inbox`, which an
openccu-lite system never has; the view listed held devices on such a
system only once one was already held, behind a navigation entry that was
not there. And with the switch off, a new device went
straight to every bridge under its factory name.

## Decision

**Onboarding is a construct of the daemon, the same on every system.**

1. **Every newly paired device is held, by default.** The setting keeps its
   name and its place (`centrals[].behavior.delay_new_device_creation`) and
   becomes default **on**. Switching it off releases what is held and
   returns to immediate creation; it is an ordinary setting, not an expert
   one.
2. **The two phases are the model.** Accepting builds the device; releasing
   publishes it. A client may offer both as one action ("accept and
   release"), which is the common case once the name and the assignments
   are entered. The phases stay separate underneath, because configuring a
   device needs it built, and publishing it must be the last step.
3. **The system's own inbox is an input, not the surface.** On a CCU,
   accepting in the daemon also accepts the device in the CCU inbox, and
   entries that only the CCU inbox holds are listed alongside. A system
   without an inbox loses nothing: the daemon's hold covers it.
4. **The operator surface belongs to the daemon.** The view that lists
   waiting devices does not depend on `hub.inbox`; it is offered wherever a
   device can be paired or held. Pairing itself starts from the add-device
   dialog, which also accepts and names a device as it joins.

### What does not change

- **Devices the daemon already knows are never held.** A hold exists only
  for an address the daemon has not built. The bring-up pulls the
  inventory and builds it before it announces itself for events, and a
  re-announcement of a known device is skipped, so neither an upgrade nor
  the first start of a fresh installation parks an existing fleet. A hold
  also needs a baseline: until the daemon has taken stock of an
  interface's inventory in this process — its pull succeeded, or an
  announcement built it — an announcement on it is built, not held, so an
  interface whose boot pull failed recovers with its fleet built instead
  of waiting to be accepted; devices already held stay held. Absence
  of a hold means released: an existing installation publishes exactly
  what it published before.
- **A hold starts with an announcement the daemon receives.** The boot
  pull honours holds already recorded and never adds one, because it
  cannot tell a device paired a moment ago from one paired years ago. A
  device paired while the daemon is not running — or before its boot pull
  read the system's inventory — is therefore built by that pull without a
  hold. On openccu-lite the event stream can attach after the boot pull,
  and a box replays nothing to a first attach; the daemon re-reads the
  inventory once the stream is attached and treats a device it does not
  know as announced, so a pairing in that gap is held like any other.
- **The daemon's own surfaces show a held device.** REST, WebSocket and the
  Config UI list it, with its phase, because they are where it gets
  configured. Only the ecosystems are withheld.
- **Removal clears a hold.** A device deleted on the system while it waits
  leaves the queue.

## Consequences

- **Behaviour change on upgrade.** A device paired after the upgrade does
  not appear in Home Assistant, Matter or a webhook consumer until it is
  released. An installation that relies on new devices appearing on their
  own sets the switch off. The changelog says so prominently.
- **Consistent with the Home Assistant integration.** `homematicip_local`
  already defers new devices unconditionally and asks for a name through a
  repair issue before it creates them. The daemon's default now matches
  the behaviour its largest consumer has settled on.
- **One experience across systems.** The same list, the same two steps and
  the same dialog on a CCU, an OpenCCU and an openccu-lite box.
- **The name is right the first time.** Discovery payloads, Matter node
  labels and webhook events carry the operator's name and assignments from
  their first publication.

## Rollout

Three changes, each shippable on its own:

1. The default and this record: the setting defaults on and is no longer
   an expert setting; an end-to-end test boots a fresh installation in
   production order and proves the inventory is built while a device
   paired afterwards waits.
2. The view and its navigation entry are no longer gated on `hub.inbox`
   and are named for what they hold.
3. The add-device dialog takes the name, rooms and functions and offers
   accept-and-release as one action.

## Alternatives considered

- **Keep it opt-in and only make it visible.** Rejected: the default
  decides what most installations experience, and the default was the
  factory name reaching every ecosystem.
- **Default on for new centrals only.** Rejected: two behaviours in the
  field for the same version, and the existing installations are the ones
  whose operators already know the cost of a wrong first name.
- **Emulate an inbox on openccu-lite.** Rejected: the inbox is the CCU's
  answer to the same question; the daemon's hold answers it for every
  system without pretending a ReGa concept exists where it does not.
