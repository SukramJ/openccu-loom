# ADR 0069 — The master-profile surface reads a link-paramset archive

- Status: accepted
- Date: 2026-08-31

## Context

A domain-core duplication audit found the `profiles/` archive decoded two
incompatible ways: `internal/store/masterprofile` and
`internal/store/linkprofile` disagree on both the file key grammar and the
constraint grammar. The fold looked mechanical. It is not, and the reason only
surfaced while implementing it.

Three measurements against the shipped data rather than against prose:

1. **The archive is keyed by channel type, not by device model.**
   `masterprofile` keys its lookup by `Device.Model` (`HmIP-eTRV`), the archive
   files by receiver channel type (`ACTOR_WINDOW`, `ACCESS_RECEIVER`, …). Every
   current caller therefore receives an empty list or `ErrNotFound` — the
   surface has never matched anything.

2. **`masterprofile` models only `value`.** Of 38,572 constraints across the
   65 archives, 10,551 are `list` or `range`; all of them decode to nil and
   disqualify the profile they belong to.

3. **The carrier the surface is named for is empty.** The shipped
   `ccudata.ChannelMetadata.MasterProfile` has no content.

Re-keying the store would make it *hit* entries. The question was whether those
entries are the right ones.

## Decision

**They are not. The archive is link-paramset data, and the master-profile
surface is a link surface under a wrong name.**

The generator is decisive, because it is the only source that knows what bytes
it wrote. `openccu-data/openccu_data/profiles/extractor.py:512` reads
`receiver_dir = base / receiver_type` and `:519` `sender_type = tcl_file.stem`,
over `www/config/easymodes/<RECEIVER>/<SENDER>.tcl`. Its `_SKIP_DIRS` at `:71`
deliberately excludes the firmware's MASTER easymode tree (`hmip`, `etc`). Its
sibling extractor draws the same line in the same vocabulary:
`easymodes/extractor.py:914` `# Parse channel type directories (LINK easymode
profiles)` against `:942` `# Parse hmipChannelConfigDialogs.tcl for MASTER
paramset metadata`. The script that produced `profiles/*.json.gz` reads the
LINK tree and skips the MASTER tree.

The firmware corroborates. The pair-keyed easymode tree is reached only from
link renderers (`*_ch_link.tcl`, `linkHmIP_*.tcl`, `ic_neweasymode.cgi`), never
from `ic_deviceparameters.cgi`'s MASTER path, which sources flat
`*_ch_master.tcl`. The write ends at `occu/WebUI/www/config/ic_common.tcl:332`
`xmlrpc $url putParamset [list string $address] [list string $peer] …` — the
paramset key is the **peer channel address**, which is the LINK paramset.

The Python reference writes the same shape:
`aiohomematic/client/interface_client.py:896-897` sets `is_link_call` when the
paramset key is a channel address, and `:933` resolves it to
`ParamsetKey.LINK`. `aiohomematic-config` keeps two stores for the two things —
`profile_store.py` ("easymode profile definitions", fed from
`profiles/*.json.gz`) and `master_profile_store.py` ("MASTER paramset easymode
profiles", fed from `easymode_extract.json.gz`). OpenCCU-Loom feeds its
*master*-profile store from the file that feeds the reference's *link* store.

One source disagrees with itself and is how this arose: `go-openccu-data`'s own
prose (`README.md:5`, `CLAUDE.md:19`, `openccudata.go:6`) calls
`data/profiles/*.json.gz` "MASTER-paramset profiles", while its own generator
package calls itself an "Easymode link-profile extractor". The generator wins;
the prose is worth correcting upstream.

## Consequences

`master_profiles.apply` is a reachable defect, not merely a misnamed one. It
resolves `FixedParams()` from link-profile data and writes them into
`SessionKey{ChannelAddress: …, ParamsetKey: hmenum.ParamsetKeyMaster}` — a
link-derived constraint set written into a channel's own MASTER paramset on a
live device.

It is dead only for the input the field name invites. `device_type` is a
caller-supplied string that reaches `Store.load` unvalidated and becomes a
filename (`internal/store/masterprofile/store.go:232`
`path.Join(s.prefix, deviceType+".json.gz")`), so what the lookup rejects is
not "any input" but "a device model" — measured: none of the 65 archive
basenames matches any of the 529 device-model literals in this repository,
while all 65 are receiver channel types. A client that sends
`device_type: "ACTOR_WINDOW"` therefore loads a real archive, resolves a real
profile, and writes it. The path is gated by an edit lock on the target
channel (`commands_extended.go:890-896`), so it needs an authenticated session
holding that lock — it is not anonymous — but it is reachable today, on
`main`, against a live CCU.

Re-keying without changing the write would turn a defect that needs an
undocumented argument into one that fires on the documented one. That is why
the rebuild is one change, not two.

The rebuild, deliberately kept out of the fold that found it:

- `internal/store/masterprofile` is deleted rather than re-keyed. Everything it
  does correctly, `internal/store/linkprofile` already does correctly, and both
  already read the same `ccudata.ProfilesFS()`, so no data moves.
- `internal/store/linkprofile` becomes the single decoder of the archive.
- The `master_profiles.*` WebSocket category is re-keyed to
  `(receiver_channel_type after alias resolution, sender_channel_type)`, and
  `apply` takes a receiver *and* a sender channel address and writes the LINK
  paramset. The edit-lock key moves with it. This changes `assets/wsapi.json`
  and the REST `APIVersion`; no consumer depends on the current field meaning,
  because no current call returns anything.
- Whether OpenCCU-Loom wants a two-channel link-profile surface at all, or
  should drop the category outright, is a product decision the sources cannot
  make. The existing REST shape `/devices/{addr}/channels/{no}` cannot express
  a channel pair, so that question is answered before the endpoint is drawn.

Held in the meantime:
`tests/contract/profiles_archive_constraint_grammar_test.go` pins what the
archive actually contains — 38,572 constraints across 65 archives by type — so
a data refresh that changes the shape is caught even while the two stores
disagree about how to read it.

Not established here: the on-wire XML-RPC frame was never observed. Every
citation above is source code; no reader ran anything against a CCU. The LINK
identification rests on `ic_common.tcl:332` passing `$peer` as the paramset key
and on `interface_client.py:933` resolving that shape to `ParamsetKey.LINK`.
