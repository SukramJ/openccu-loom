# Interface-process write semantics — why the paramset write path is strict

Audience: contributors changing `internal/central/adapter/paramsets.go`,
`internal/parameter`, or any surface that accepts configuration values
(REST, WS, MCP, config import). This note records the behavioural model of
the CCU interface processes that the write path is built against, and which
parts of that model are measured versus assumed defensively.

## The behavioural model

The two interface-process families do not validate configuration writes,
and they fail in opposite directions:

1. **The HmIP family persists what it rejects.** A `putParamset` answered
   with a fault has already stored the struct in the process's own
   configuration store for that channel. A parameter name the channel's
   description does not carry is stored permanently: it survives restarts,
   no RPC method removes it, and from then on every `putParamset` on that
   channel — including an empty one — faults. Only deleting and re-pairing
   the device clears it. A value of the wrong *type* additionally leaves
   `CONFIG_PENDING` latched until a valid full MASTER write of the channel
   replaces the stored values.
2. **The BidCos family answers ok to what it discards.** `rfd` drops
   unknown parameter names, ignores type-mismatched values, coerces
   strings to numbers, and clamps out-of-range values to `MIN`/`MAX` — and
   answers ok to all of it. On this family a fault-free answer proves
   nothing about what was applied; the only reliable acknowledgement is
   re-reading the stored paramset and comparing.
3. **Neither family range-checks.** An out-of-range integer is either
   stored as sent (HmIP) or silently clamped (BidCos). If the user is to
   see a range error, the daemon has to produce it.

## What the code does about it

- `coerceParamsetValuesWithDescriptions`
  (`internal/central/adapter/paramsets.go`) refuses any parameter that is
  not in the channel's own paramset description, and refuses the whole
  write when the description cannot be fetched. The model path enforces
  the same through `Channel.SetMany` (`ErrUnknownParameter`,
  `validateForSet`).
- Every MASTER and LINK write re-reads the stored paramset afterwards and
  reports sent-vs-stored divergences (`ParamsetWriteReport`), because of
  (2) above.
- ENUM values go out as the **index**: both families accept name and
  index, but reads return the index, so index-out keeps the changed-value
  diff and the read-back comparison stable
  (`internal/parameter/coerce.go`, `coerceEnum`).
- FLOAT values go out as an explicit XML-RPC `<double>`
  (`internal/client/transport/xmlrpc/value.go`); requests are encoded
  ISO-8859-1 end to end (`message.go`), matching what the CCU stores.
- `SPECIAL` values pass validation although they lie outside
  `MIN`..`MAX` (`internal/parameter/validate.go`) — clamping them would
  break device semantics such as a valve's fixed OPEN/CLOSED codes.
- Multi-apply (`ParamsetApplyDomain`) gates on identity of the full
  stored wire description, never on channel type: equal channel types
  across device types or firmware versions routinely carry different
  MASTER parameter sets, and per (1) a mismatched write is not a
  recoverable mistake.
- Caller-supplied paramset keys are parsed strictly against the three
  known keys before any RPC: `rfd` treats an unrecognised paramset key in
  a `getParamset`/`getParamsetDescription` call as a **peer address** and
  answers with LINK-paramset defaults instead of a fault, so a free-form
  key must never reach the wire.

## CONFIG_PENDING means two different things

- **BidCos**: a configuration is queued for a device that has not picked
  it up yet. On battery devices it clears when the device next wakes;
  nothing the daemon does can shorten it. It is a normal, expected,
  self-clearing state.
- **HmIP**: either a transient during transfer, or — when latched — the
  stored configuration cannot be transferred (wrong type, poisoned
  store). Only this case warrants the repair action (a valid full MASTER
  rewrite per channel); `DeviceSummary.master_pushes_config_pending`
  tells the SPA which semantics apply.

`clearConfigCache` and `restoreConfigToDevice` are implemented by the
BidCos daemons (`rfd`, `hs485d` — see the firmware sources,
`src/rfd/XmlRpcMethods.cpp`, `src/hs485d/XmlRpcMethods.cpp`); the HmIP
process lists similarly named methods but does not implement them for its
devices, so the repair flow offers them on BidCos interfaces only.

## Provenance

- Measured in this repository's own tests: the strict-write behaviour, the
  read-back comparison, ENUM/FLOAT encoding, ISO-8859-1 framing (unit and
  contract tests next to the named files).
- Read from the CCU firmware sources: the `rssiInfo` response shape and
  tuple direction (`src/rfd/RFManager.cpp`, `GetRSSIInfo`),
  `setBidcosInterface` argument order (`src/rfd/XmlRpcMethods.cpp`), and
  the `clearConfigCache`/`restoreConfigToDevice` implementations named
  above.
- The persistence-of-rejected-writes behaviour (1), the silent-discard
  behaviour (2) and the peer-address key quirk are **defensive
  assumptions about live firmware behaviour**: they have not been
  reproduced by this repository's own instrumentation against hardware.
  They are treated as true because the cost asymmetry is extreme — if (1)
  is true and the daemon guesses wrong once, a customer channel is
  permanently unwritable. Reproducing them requires deliberate
  fault-injection writes against lab hardware and must never be run
  against a production CCU.

The fault-code catalogue itself, with per-family wording and retryability,
lives in `pkg/hmerr/errors.go` (`XMLRPCFaultCode`).
