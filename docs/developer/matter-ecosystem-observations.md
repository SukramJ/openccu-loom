# Matter Ecosystem Observations

!!! info "Who this page is for"
    Contributors and AI agents working on the Matter bridge. The
    operator-facing guide is [Matter](../user/matter.md); the parity rules are
    [Matter Behavioral-Parity Contract](../matter-parity-contract.md).

Matter controllers do not implement the specification uniformly, and the ways
they diverge are not discoverable from the specification. What is known about
them is scattered across the code as comments next to the line each finding
forced — valuable where it sits, but impossible to answer "what does this
project know about Apple Home?" from. This page is that answer.

**Every entry names its source.** An entry backed by a code comment cites the
file; an entry inherited from matter.js cites its document; an entry nobody
here has reproduced says so in those words. Nothing on this page is inferred
from how a controller "probably" behaves — when the mechanism is unknown, the
entry records the observation and stops.

---

## How to read the confidence column

| Level | Meaning |
| --- | --- |
| **Reproduced** | Observed against a real controller during this project's own bring-up, with the wire evidence named in the cited code. |
| **Inherited** | Taken from matter.js, which encodes it after their own interop testing. Not independently reproduced here. |
| **Reported** | Third-party report we act on but have not reproduced. |

A finding does not become more true by being acted on. Several entries below
are Inherited or Reported and shape real code; that is a deliberate choice to
follow the gold standard rather than re-derive it, not a claim of measurement.

---

## Apple Home

Apple's HAP-service mapper is the strictest consumer of the bridge's structure.
It rebuilds a HomeKit service graph from the Matter topology at pairing time,
and it aborts the pairing rather than degrading when the graph does not fit.

| Observation | Consequence for the bridge | Confidence | Source |
| --- | --- | --- | --- |
| The HAP-service mapper keys its service lookup on the **first** entry of `Descriptor.DeviceTypeList`. A list starting with BridgedNode drops the endpoint into an "Other" fallback and the bridge renders as unsupported. | `DeviceTypeList` is always emitted primary-first, BridgedNode last. | Reproduced | `internal/north/matter/endpoint/materialize.go` |
| The mapper validates the `(deviceType, revision)` tuple against an internal whitelist. A mismatch surfaces as "Attribute report is not parsed into a known struct" and aborts the rebuild with `HAPErrorDomain Code=14`. | Device-type revisions are codegen'd from the matter.js snapshot, never hand-written. | Reproduced | `internal/north/matter/endpoint/helpers.go` |
| An endpoint without `Identify` in its `ServerList` fails the rebuild with `HAPErrorDomain Code=24`, regardless of how complete the rest of the surface is. | Identify is mounted on every bridged endpoint before the source's own clusters. | Reproduced | `internal/north/matter/endpoint/materialize.go` |
| An endpoint advertising a cluster ID matter.js does not know (the draft Schedules cluster `0x0024`) is rejected with `HAPErrorDomain Code=24`, killing the pair after Subscribe-Initial. Verified by decoding the outbound Subscribe-Initial: four Thermostat endpoints carried `0x24` with `Status_0x84`, and Apple sent RemoveFabric immediately after. | The Schedules infrastructure stays unmounted until a canonical cluster ships. | Reproduced | `internal/model/custom/climate/matter.go` |
| Missing `BridgedDeviceBasicInformation` or `Descriptor` on a bridged endpoint aborts the pair with `HMMTRErrorDomain Code 9` ("no topology / no link layer") roughly 9 s after CommissioningComplete. Empty fields inside `BridgedDeviceBasicInformation` surface as `HAPErrorDomain Code=24`. | Both clusters are mounted unconditionally, with VendorName / ProductName / SerialNumber / UniqueID populated. | Reproduced | `internal/north/matter/endpoint/materialize.go` |
| The post-CommissioningComplete flow writes `AccessControl.ACL` to install HomePod / Apple TV edge controllers as additional Administer subjects. Without a decoder for that structured write the WriteResponse never lands, Apple times out after 10 s and tears down the fabric via RemoveFabric. | Every writable list/struct attribute needs an explicit decoder case; a fall-through reaches `MatterWrite` as nil. | Reproduced | `internal/north/matter/bridge/attribute_value_reader.go` |
| A `CountryCode` silently decoded as `""` had Apple sending RemoveFabric about 80 s into the pair. | UTF-8 TLV fields are read from `el.String`, never `el.Octets`. | Reproduced | `internal/north/matter/bridge/fields_reader.go` |
| Cancelling a pairing in Apple Home and retrying returns BUSY for up to 900 s when the aborted attempt left the fail-safe armed. | `RevokeCommissioning` expires the fail-safe unconditionally, before the window-state check. | Reproduced | `internal/north/matter/bridge/commissioning_window.go` |
| Parallel CASE sessions are opened from one controller, and Sigma3 is retransmitted by the MRP layer. | Sigma2 replies are deduplicated per exchange ID. | Reproduced | `internal/north/matter/bridge/bridge.go`, `case_provider.go` |
| Per-boot rotation of `UniqueID` breaks the HAP-service mapper's device identity across restarts. | UniqueID is stable across restarts; rotation exists only as a deliberate debug escape. | Reproduced | `internal/north/matter/bootid/bootid.go` |
| Names for the **bridge itself** are ignored at pairing; devices show as "Matter Accessory". Per-device names from `BridgedDeviceBasicInformation` do apply. | Nothing to do — the per-device path is the one that works. | Inherited | matter.js `docs/KNOWN_ISSUES` |
| The root endpoint is queried for `BridgedDeviceBasicInformation` (0x39) and OTA (0x2a), neither of which exists there per the specification. The resulting Status 195 in the log is expected. | Do not "fix" these by mounting the clusters on the root endpoint. | Inherited | matter.js `docs/KNOWN_ISSUES` |
| Deleting a device from Apple Home does not remove every fabric; the leftover may need clearing from Apple system settings. | Nothing to do; relevant when diagnosing a stale fabric. | Inherited | matter.js `docs/KNOWN_ISSUES` |

## Amazon Alexa

Alexa is the least tolerant of structural surprises and the slowest to adopt
new device types. Two of its constraints shape the bridge's topology.

| Observation | Consequence for the bridge | Confidence | Source |
| --- | --- | --- | --- |
| A bridged endpoint is recognised only by the clusters its device type specifies. An end device carrying a cluster the Device Library does not name for it as mandatory or optional is recognised as the reduced type — a temperature sensor with an added humidity cluster registers as temperature only. | The device-type conformance guards. See [below](#the-guards-that-hold-this). | Inherited | matter.js `docs/ECOSYSTEMS`, "Composed Devices" |
| Power-source information belongs at the bridged-node level, not on an endpoint whose device type does not specify it. | `PowerSource` rides on one of the device's own endpoints, where BridgedNode specifies it. | Inherited | matter.js `docs/ECOSYSTEMS` |
| Devices are discovered only on UDP port 5540. | 5540 is the default; nothing special is done for Alexa. | Inherited | matter.js `docs/ECOSYSTEMS` and `KNOWN_ISSUES` |
| An Endpoint 1 must exist beside the root endpoint, either as the main device or an aggregator. | Endpoint 1 is always the Aggregator. | Inherited | matter.js `docs/ECOSYSTEMS` |
| Device types introduced after Alexa's bridge support — the Matter 1.3 Water Leak Detector (0x0043) among them — can render an entire bridge unresponsive: every accessory on it stops reacting. | The leak measurement class materialises as Contact Sensor (0x0015) instead. | Reported | `pkg/interfaces/matter.go`; third-party report via `docs/user/matter.md` |
| A bridge exposing too many accessories cannot be paired; the practical ceiling is reported around 80–100. | Nothing enforced; operators trim the exposure selection. | Reported | `docs/user/matter.md` |
| The WaterValve device type is unsupported. | The irrigation valve is exposed as a plain on/off endpoint (ADR 0049). | Reported | `docs/user/matter.md` |

## Google Home

| Observation | Consequence for the bridge | Confidence | Source |
| --- | --- | --- | --- |
| Setpoint writes echo the read `SystemMode` back on state changes; a stale wire value made every Apple/Google setpoint write fail with an IM error. | The thermostat projection keeps the mode value current. | Reproduced | `internal/model/custom/climate/matter.go` |
| Slider drags emit 5–10 position commands in quick succession. | Cover positions are debounced. | Reproduced | `internal/model/custom/cover/matter_debounce.go` |
| Window Covering is supported for lift only. | Nothing to do; tilt is exposed and simply not surfaced there. | Inherited | matter.js `docs/ECOSYSTEMS` |

## Other ecosystems

| Ecosystem | Observation | Confidence | Source |
| --- | --- | --- | --- |
| Samsung SmartThings | Commissioning may report a timeout in the app even when pairing completed successfully. | Inherited | matter.js `docs/KNOWN_ISSUES` |
| Tuya | Bridges are not supported at all. | Inherited | matter.js `docs/ECOSYSTEMS` and `KNOWN_ISSUES` |
| Home Assistant | Prefers `TemperatureMeasurement` over the Thermostat cluster's `LocalTemperature` for chart history. The reading is available either way: a thermostat channel's `ACTUAL_TEMPERATURE` materialises as its own TemperatureSensor endpoint. | Reported | `internal/model/custom/climate/matter.go` (recorded when the duplicate cluster was removed) |

---

## The guards that hold this

Ecosystem behaviour is not testable from here — no controller is in the loop.
What *is* testable is conformance to the specification those controllers read,
and that is where the guards sit:

| Guard | What it holds |
| --- | --- |
| `TestBridgedEndpointClustersConformToTheirDeviceType` | Every endpoint source's clusters against its own device type, over the whole hydrated fleet. |
| `TestEveryMandatoryAttributeIsAnswered` | Every conformance-`M` attribute of every mounted cluster answers a read. |
| `TestFeatureGatedAttributesAreAnsweredWhenAdvertised` | An attribute gated on a feature answers a read when the server advertises that feature's bit. |
| `TestEveryMandatoryCommandIsAccepted` | Every conformance-`M` command appears in `AcceptedCommandList`. |
| `TestMeasurementClassProjectsOntoAConformantDeviceType` | Every measurement class names a device type whose definition permits its cluster. |

All five read the same oracle: the matter.js HEAD schema snapshot, which the
go-fabric module generates and this repo pins (`make sync-matter-schema`). They
live under `tests/integration/` and `tests/contract/`.

The one thing they cannot do is confirm a real controller's reaction. That
needs the chip-tool suite (`.github/workflows/chiptool.yml`, `needs-chiptool`
label), which is the only place a real commissioner is in the loop.

## Adding to this page

Add an entry when a controller's behaviour forces a decision in the code. Cite
the file the decision lives in, and pick the confidence level honestly — an
Inherited entry acted on is useful, an Inherited entry labelled Reproduced is
a trap for whoever revisits it. If the mechanism behind an observation is
unknown, record the observation and leave the mechanism blank rather than
supplying a plausible one.
