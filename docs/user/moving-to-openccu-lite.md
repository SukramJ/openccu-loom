# Moving a Central to openccu-lite

!!! info "Who this page is for"
    Administrators running OpenCCU-Loom as an add-on on a box that was
    updated from OpenCCU to openccu-lite. For connecting an openccu-lite
    box from scratch, and for what the box can and cannot do, see
    [Connecting openccu-lite](../admin/openccu-lite.md).

A box that moves from OpenCCU to openccu-lite keeps the add-on's data,
and with it a CCU central pointing at the box itself. That central can
never come up as a CCU: the readiness marker it waits for is ReGa's, and
openccu-lite has no ReGa. This page covers how the central is switched
to openccu-lite — by itself at the add-on's start, or with one
confirmation from you — what the switch changes, and what to do when
neither works.

The design is recorded in
[ADR 0077](../adr/0077-lite-addon-token-onboarding.md).

---

## 1. After the system update: the automatic move

openccu-lite mints the add-on an API token at every start and writes it
to a file only the add-on can read. When the add-on starts, it moves the
central over by itself if that token file is readable and **exactly
one** stored central is a CCU central whose host is this machine (a
loopback address, or an address one of the machine's interfaces
carries).

The central is switched to openccu-lite in place:

- It keeps its **name** and what is stored under it: channel flags,
  visibility un-ignore patterns, recording overrides, Matter exposures
  and its Matter partition.
- The CCU account, the RPC ports and the interface list go. The
  interfaces are read from the box, and the token comes from the
  add-on's token file (the central's `api_token_file` field).
- The switch is one save, which restarts the running central.

Nothing is moved when a central already uses the add-on token (the box
is configured; a leftover CCU central is yours to delete).

### When the add-on cannot move the central

The central then stands on the operator warning list as
`lite-move:<central>`, instead of waiting for a CCU forever. The warning
list is mirrored onto the box's Status page, so the notice also appears
where you look after the system update. The warning names one of three
reasons and the way out:

| Reason | What the warning says |
|---|---|
| No token | The system runs openccu-lite and gave this add-on no API token. Reinstall OpenCCU-Loom from the system's add-on page; it then moves the central by itself. |
| Several centrals | Several centrals point at this system, so none was moved automatically: switch the right one to openccu-lite in its settings. |
| Did not complete | The automatic switch did not complete: restart OpenCCU-Loom, or switch the central to openccu-lite in its settings. |

The warning clears by itself once the central is no longer stored as a
CCU central.

---

## 2. The guided move

An administrator finds a **Move to openccu-lite** button on the warning
in **Diagnostics**. The same dialog opens from
`#/settings?tab=ccus&lite_move=<central>`.

1. **Check.** The dialog loads the stored central and asks its host what
   it is. It stops with a message when the system does not answer as
   openccu-lite, when the box is still starting (try again in a moment),
   or when the box does not offer pairing (reinstall OpenCCU-Loom from
   the system's add-on page instead).
2. **What the move leaves behind.** Before anything happens, the dialog
   names the interfaces the box does not offer, with their devices, and
   that openccu-lite has no programs or system variables.
3. **Pair.** **Start pairing** shows a code. Open the box's web interface
   (the dialog links to it) and enter the code there to approve
   OpenCCU-Loom. This approval is the one confirmation the move needs.
4. **Switch.** Once the box approved, the central is switched to
   openccu-lite with the approved pairing. The dialog then shows the
   central's state; a failed switch shows the error and can be retried.

Closing the dialog withdraws a pairing still waiting on the box.

The MCP server does not offer this move, because it ends in an approval
a person gives on the box.

---

## 3. What changes

- **CUxD and its devices go.** CUxD needs ReGaHss and does not exist on
  openccu-lite. The devices of an interface the box does not offer are
  removed as by a removal, so their channel flags, recording overrides,
  cache rows and Matter exposures go with them.
- **Programs and system variables are no longer shown.** openccu-lite
  has no ReGaHss and no script interpreter, so it has no system
  variables, programs or HM-Script. The web UI hides those views when no
  central offers them.
- **Backups and reboots need a token with those scopes.** System
  management on openccu-lite is available wherever the API token carries
  the scope for it. `backup` and `power` are never part of an add-on
  token, and a token from pairing can never carry them either; use a
  manual token created on the box when you need them (see
  [Connecting openccu-lite](../admin/openccu-lite.md#adding-an-openccu-lite-box)).
  A box from 2026-11-10 on restores only a backup of openccu-lite; a
  CCU's backup is refused — import its devices on the box instead.
- **Home Assistant entities stay.** A restart of a central keeps its MQTT
  discovery configs; once the central's devices are loaded again, only
  what is really gone is removed — such as the devices of a dropped
  interface. While the box is not reachable nothing is removed, and the
  devices read unavailable.

---

## 4. When neither works

- **Reinstall the add-on.** When the box gave the add-on no API token,
  reinstall OpenCCU-Loom from the system's add-on page; it then moves
  the central by itself.
- **Edit the central by hand.** Switch the central to openccu-lite in
  its settings (**Settings → CCUs**).

---

## 5. Home Assistant: OpenCCU-Loom Remote without port 8119

On an openccu-lite box the daemon is reached through the box's own web
server at `https://<box>/addons/loom`, without a separate port. The
OpenCCU-Loom Remote add-on can point there:

```yaml
instances:
  - name: box
    url: "https://192.168.1.20/addons/loom"
```

The box's login stands in front of that path. Two ways through it:

- **Pairing (operator).** Leave `token` empty and press **Pair with
  box** on the instance's tile. The panel shows a six-digit code;
  approve it in the box's web interface. The add-on keeps the token in
  `/data/paired.json` and pins the box's certificate. The token opens
  the OpenCCU-Loom add-on with the **operator** role, never admin. After
  a new box certificate, pair again.
- **Manual token (admin).** Create an API token with **Full access** in
  the box's web interface and put it into `token`. It maps to the
  **admin** role and always wins over a pairing.

The box accepts pairing requests only from its own local networks. When
Home Assistant is elsewhere (another subnet, a VPN), the panel says so;
use the manual token then.

The design is recorded in
[ADR 0091](../adr/0091-remote-addon-box-pairing.md).

---

## See also

- [Connecting openccu-lite](../admin/openccu-lite.md) — what the box
  offers, feature keys and their scopes, running Loom on the box.
- [Backup & Restore](../admin/backup.md#backing-up-and-restoring-an-openccu-lite-system)
- [Home Assistant — Choosing a Path](home-assistant.md)
