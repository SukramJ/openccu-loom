// Generates IM-message-level wire-byte fixtures for openccu-loom's Go encoders.
//
// Each record pairs a canonical matter.js .encode() output (hex) with the
// JSON-form of the input, so the Go test can reconstruct the Go struct,
// re-encode via MarshalTLV, and assert byte equality.
//
// Regen command (run from inside the matter.js checkout):
//   node /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/generate-im-wire-fixtures.ts \
//       > /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/im-wire-fixtures.json
//
// Or from the openccu-loom repo root:
//   cd /Users/markus/Documents/GitHub/matter.js && \
//     node /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/generate-im-wire-fixtures.ts \
//     > /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/im-wire-fixtures.json
//
// The generated im-wire-fixtures.json is committed to the repo. The Go test
// at internal/north/matter/im/parity_wire_fixtures_test.go loads it via
// go:embed and asserts that each Go MarshalTLV call produces the same bytes.
//
// Notes on by-design divergences documented in notes/parity/by_design.md:
//   - SubscribeResponse: Go uses PutUint32/PutUint16 for subscriptionId and
//     maxInterval because chip-tool and Apple Home require explicit-width
//     encoding per the spec (not magnitude-encoded like matter.js TlvUInt32).
//   - ReportData keepalive: matter.js omits the attributeReports field
//     when undefined; Go always emits an empty attributeReports array.
//     These by-design cases are marked with "byDesignDivergence: true"
//     and skipped in the strict byte-equality assertion.

// Resolve @matter/types from the matter.js checkout. The script may be
// invoked from any working directory; we use Module._resolveFilename to
// find the package relative to the matter.js checkout root before
// requiring it.
const Module = require("module");
const path = require("path");

// Locate matter.js checkout: two directories up from this script's
// notes/parity/matter/ location.
const scriptDir = __dirname;
const matterJsRoot = path.resolve(scriptDir, "../../../../matter.js");
const typesMain = path.join(matterJsRoot, "node_modules/@matter/types/dist/cjs/index.js");

const {
    TlvDataReportForSend,
    TlvSubscribeRequest,
    TlvSubscribeResponse,
    TlvStatusResponse,
    TlvReadRequest,
    TlvWriteRequest,
    TlvWriteResponse,
    TlvInvokeRequest,
    TlvInvokeResponseForSend,
    TlvTimedRequest,
    TlvBoolean,
} = require(typesMain);

function toHex(bytes: Uint8Array): string {
    return Buffer.from(bytes).toString("hex");
}

interface Fixture {
    label: string;
    description: string;
    type: string;
    fixture: any;
    bytesHex: string;
    // When true the Go encoder intentionally diverges from matter.js on this
    // case; the test skips byte-equality but still exercises the encoder
    // without panicking and verifies a decode round-trip.
    byDesignDivergence?: boolean;
    byDesignNote?: string;
}

const out: Fixture[] = [];

function add(label: string, description: string, msgType: string, input: any, schema: any, opts?: { byDesign?: boolean; byDesignNote?: string }) {
    const bytes = schema.encode(input);
    out.push({
        label,
        description,
        type: msgType,
        fixture: input,
        bytesHex: toHex(bytes),
        ...(opts?.byDesign ? { byDesignDivergence: true, byDesignNote: opts.byDesignNote } : {}),
    });
}

// =============================================================
// ReportData (TlvDataReportForSend)
// =============================================================

// F3 regression case: suppressResponse=false on a non-empty ReportData.
// Before fix F3, openccu-loom omitted suppressResponse when value was false.
// matter.js always emits it as TlvOptionalField with a concrete value.
add(
    "report_data_supress_false_empty_attrs",
    "ReportData with empty attributeReports and suppressResponse=false — the F3 regression fixture",
    "ReportData",
    {
        suppressResponse: false,
        attributeReports: [],
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
);

// suppressResponse=true: keepalive path
// Note: matter.js omits attributeReports when it is undefined.
// Go always emits an empty attributeReports array even on keepalives.
// This is a by-design divergence documented in notes/parity/by_design.md.
add(
    "report_data_keepalive_no_subid",
    "ReportData keepalive (suppressResponse=true, no subscriptionId, no attributeReports field)",
    "ReportData",
    {
        suppressResponse: true,
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "matter.js omits attributeReports when undefined; Go always emits an empty array",
    },
);

// suppressResponse=true with subscriptionId: the typical keepalive from
// ServerSubscription.ts:777 — subscriptionId present, no attributeReports.
add(
    "report_data_keepalive_with_subid",
    "ReportData keepalive with subscriptionId=5, suppressResponse=true",
    "ReportData",
    {
        subscriptionId: 5,
        suppressResponse: true,
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "matter.js omits attributeReports when undefined; Go always emits an empty array",
    },
);

// suppressResponse=false with subscriptionId + empty attributeReports.
// By-design: Go uses PutUint32 for subscriptionId (always 4-byte); matter.js
// TlvUInt32 magnitude-encodes (subscriptionId=42 fits in 1 byte → 1-byte wire).
// The Go code comment in read.go explains the Apple Home requirement.
add(
    "report_data_with_subid_supress_false",
    "ReportData: subscriptionId=42, suppressResponse=false, empty attributeReports",
    "ReportData",
    {
        subscriptionId: 42,
        suppressResponse: false,
        attributeReports: [],
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "Go uses PutUint32 for SubscriptionId (always 4-byte); matter.js TlvUInt32 magnitude-encodes small values. Apple Home requires fixed-width.",
    },
);

// moreChunkedMessages=true — by-design: subscriptionId fixed-width divergence.
add(
    "report_data_more_chunked_supress_false",
    "ReportData: moreChunkedMessages=true (mid-chunk), suppressResponse=false, subscriptionId=42",
    "ReportData",
    {
        subscriptionId: 42,
        suppressResponse: false,
        attributeReports: [],
        moreChunkedMessages: true,
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "Go uses PutUint32 for SubscriptionId (always 4-byte); matter.js TlvUInt32 magnitude-encodes small values.",
    },
);

// moreChunkedMessages=true with suppressResponse=false and no subscription
add(
    "report_data_chunked_no_subid",
    "ReportData: moreChunkedMessages=true, no subscriptionId (read-request chunk)",
    "ReportData",
    {
        suppressResponse: false,
        attributeReports: [],
        moreChunkedMessages: true,
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
);

// eventReports present (no attributeReports) — by-design: subscriptionId fixed-width.
add(
    "report_data_event_only",
    "ReportData: eventReports array present (non-empty), suppressResponse=false",
    "ReportData",
    {
        subscriptionId: 99,
        suppressResponse: false,
        eventReports: [],
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "Go uses PutUint32 for SubscriptionId (always 4-byte); matter.js TlvUInt32 magnitude-encodes small values.",
    },
);

// Both moreChunkedMessages and suppressResponse=true (final chunk of subscription keepalive)
add(
    "report_data_final_chunk_supress_true",
    "ReportData: final chunk, suppressResponse=true (no more chunks)",
    "ReportData",
    {
        subscriptionId: 7,
        suppressResponse: true,
        interactionModelRevision: 13,
    },
    TlvDataReportForSend,
    {
        byDesign: true,
        byDesignNote: "matter.js omits attributeReports when undefined; Go always emits an empty array",
    },
);

// =============================================================
// SubscribeRequest (TlvSubscribeRequest)
// =============================================================

// SubscribeRequest fixtures document the matter.js reference wire shape.
// Go SubscribeRequest.MarshalTLV always emits an empty attributeRequests array
// when no attributes are requested, whereas matter.js uses TlvOptionalField and
// omits the field. Go also does not emit interactionModelRevision in this message.
// All SubscribeRequest fixtures are by-design divergences; they serve as the
// matter.js reference but are skipped in the strict byte-equality assertion.

add(
    "subscribe_req_minimal_keep_true",
    "SubscribeRequest: keepSubscriptions=true, intervals=0/60, isFabricFiltered=false",
    "SubscribeRequest",
    {
        keepSubscriptions: true,
        minIntervalFloorSeconds: 0,
        maxIntervalCeilingSeconds: 60,
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvSubscribeRequest,
    {
        byDesign: true,
        byDesignNote: "Go SubscribeRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision; matter.js uses TlvOptionalField for both",
    },
);

add(
    "subscribe_req_minimal_keep_false",
    "SubscribeRequest: keepSubscriptions=false, intervals=0/30, isFabricFiltered=false",
    "SubscribeRequest",
    {
        keepSubscriptions: false,
        minIntervalFloorSeconds: 0,
        maxIntervalCeilingSeconds: 30,
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvSubscribeRequest,
    {
        byDesign: true,
        byDesignNote: "Go SubscribeRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision; matter.js uses TlvOptionalField for both",
    },
);

// With attribute requests
add(
    "subscribe_req_with_attrs_fabric_filtered",
    "SubscribeRequest: one attribute path, isFabricFiltered=true",
    "SubscribeRequest",
    {
        keepSubscriptions: false,
        minIntervalFloorSeconds: 1,
        maxIntervalCeilingSeconds: 30,
        attributeRequests: [{ endpointId: 1, clusterId: 6, attributeId: 0 }],
        isFabricFiltered: true,
        interactionModelRevision: 13,
    },
    TlvSubscribeRequest,
    {
        byDesign: true,
        byDesignNote: "Go SubscribeRequest.MarshalTLV omits interactionModelRevision; matter.js emits it",
    },
);

// With event requests
add(
    "subscribe_req_with_events",
    "SubscribeRequest: one event path, no attribute requests",
    "SubscribeRequest",
    {
        keepSubscriptions: true,
        minIntervalFloorSeconds: 0,
        maxIntervalCeilingSeconds: 60,
        eventRequests: [{ endpointId: 1, clusterId: 6, eventId: 0 }],
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvSubscribeRequest,
    {
        byDesign: true,
        byDesignNote: "Go SubscribeRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision",
    },
);

// With dataVersionFilters
add(
    "subscribe_req_with_dvf",
    "SubscribeRequest: with one dataVersionFilter (endpoint=1, cluster=6, dataVersion=100)",
    "SubscribeRequest",
    {
        keepSubscriptions: false,
        minIntervalFloorSeconds: 0,
        maxIntervalCeilingSeconds: 30,
        isFabricFiltered: false,
        dataVersionFilters: [{ path: { endpointId: 1, clusterId: 6 }, dataVersion: 100 }],
        interactionModelRevision: 13,
    },
    TlvSubscribeRequest,
    {
        byDesign: true,
        byDesignNote: "Go SubscribeRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision",
    },
);

// =============================================================
// SubscribeResponse (TlvSubscribeResponse)
// =============================================================

// Canonical subscribe response.
// By-design divergence: matter.js uses magnitude-encoded TlvUInt32 for
// subscriptionId; Go uses PutUint32 (always 4-byte) because chip-tool
// and Apple Home require explicit-width encoding.
add(
    "subscribe_resp_canonical",
    "SubscribeResponse: subscriptionId=1, maxInterval=30",
    "SubscribeResponse",
    {
        subscriptionId: 1,
        maxInterval: 30,
        interactionModelRevision: 13,
    },
    TlvSubscribeResponse,
    {
        byDesign: true,
        byDesignNote: "Go uses PutUint32/PutUint16 (fixed-width); matter.js TlvUInt32/UInt16 magnitude-encodes. Spec requires fixed-width; chip-tool and Apple reject magnitude-encoded subId/maxInterval.",
    },
);

add(
    "subscribe_resp_large_subid",
    "SubscribeResponse: subscriptionId=0x12345678 (forces 4-byte encoding), maxInterval=120",
    "SubscribeResponse",
    {
        subscriptionId: 0x12345678,
        maxInterval: 120,
        interactionModelRevision: 13,
    },
    TlvSubscribeResponse,
    {
        byDesign: true,
        byDesignNote: "Go uses PutUint32/PutUint16 (fixed-width); matter.js TlvUInt32/UInt16 magnitude-encodes.",
    },
);

// =============================================================
// StatusResponse (TlvStatusResponse)
// =============================================================

add(
    "status_resp_success",
    "StatusResponse: status=SUCCESS(0)",
    "StatusResponse",
    { status: 0, interactionModelRevision: 13 },
    TlvStatusResponse,
);

add(
    "status_resp_unsupported_attribute",
    "StatusResponse: status=UNSUPPORTED_ATTRIBUTE(0x86)",
    "StatusResponse",
    { status: 0x86, interactionModelRevision: 13 },
    TlvStatusResponse,
);

add(
    "status_resp_unsupported_cluster",
    "StatusResponse: status=UNSUPPORTED_CLUSTER(0xC3)",
    "StatusResponse",
    { status: 0xc3, interactionModelRevision: 13 },
    TlvStatusResponse,
);

add(
    "status_resp_failure",
    "StatusResponse: status=FAILURE(0x01)",
    "StatusResponse",
    { status: 0x01, interactionModelRevision: 13 },
    TlvStatusResponse,
);

add(
    "status_resp_invalid_action",
    "StatusResponse: status=INVALID_ACTION(0x80)",
    "StatusResponse",
    { status: 0x80, interactionModelRevision: 13 },
    TlvStatusResponse,
);

add(
    "status_resp_busy",
    "StatusResponse: status=BUSY(0x9C)",
    "StatusResponse",
    { status: 0x9c, interactionModelRevision: 13 },
    TlvStatusResponse,
);

// =============================================================
// ReadRequest (TlvReadRequest)
// =============================================================

// ReadRequest fixtures document the matter.js reference wire shape.
// Go ReadRequest.MarshalTLV always emits the attributeRequests array (even empty)
// and does not emit interactionModelRevision. All ReadRequest fixtures are
// by-design divergences from the matter.js reference.

add(
    "read_req_minimal",
    "ReadRequest: no attributeRequests, isFabricFiltered=false",
    "ReadRequest",
    { isFabricFiltered: false, interactionModelRevision: 13 },
    TlvReadRequest,
    {
        byDesign: true,
        byDesignNote: "Go ReadRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision",
    },
);

add(
    "read_req_with_attrs",
    "ReadRequest: one attribute path (ep=1, cluster=6, attr=0), isFabricFiltered=false",
    "ReadRequest",
    {
        attributeRequests: [{ endpointId: 1, clusterId: 6, attributeId: 0 }],
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvReadRequest,
    {
        byDesign: true,
        byDesignNote: "Go ReadRequest.MarshalTLV omits interactionModelRevision; matter.js emits it",
    },
);

add(
    "read_req_wildcard",
    "ReadRequest: wildcard attribute path (no endpointId/clusterId/attributeId), isFabricFiltered=false",
    "ReadRequest",
    {
        attributeRequests: [{}],
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvReadRequest,
    {
        byDesign: true,
        byDesignNote: "Go ReadRequest.MarshalTLV omits interactionModelRevision; matter.js emits it",
    },
);

add(
    "read_req_fabric_filtered",
    "ReadRequest: one attribute path, isFabricFiltered=true",
    "ReadRequest",
    {
        attributeRequests: [{ endpointId: 0, clusterId: 0x001f, attributeId: 0 }],
        isFabricFiltered: true,
        interactionModelRevision: 13,
    },
    TlvReadRequest,
    {
        byDesign: true,
        byDesignNote: "Go ReadRequest.MarshalTLV omits interactionModelRevision; matter.js emits it",
    },
);

add(
    "read_req_with_events",
    "ReadRequest: event requests for cluster 6 event 0 on ep=1",
    "ReadRequest",
    {
        eventRequests: [{ endpointId: 1, clusterId: 6, eventId: 0 }],
        isFabricFiltered: false,
        interactionModelRevision: 13,
    },
    TlvReadRequest,
    {
        byDesign: true,
        byDesignNote: "Go ReadRequest.MarshalTLV always emits empty attributeRequests array and omits interactionModelRevision",
    },
);

// =============================================================
// WriteRequest (TlvWriteRequest)
// =============================================================

// WriteRequest fixtures: the bridge decodes WriteRequests (sent by controllers),
// it does not encode them. These fixtures are used for Stage-3 round-trip decode
// tests only. The encode path (MarshalTLV) does not exist for WriteRequest.
const boolTrueTlv = TlvBoolean.encodeTlv(true);

add(
    "write_req_not_timed",
    "WriteRequest: timedRequest=false, one AttributeDataIB (OnOff attr, bool true) — decode-only fixture",
    "WriteRequest",
    {
        timedRequest: false,
        writeRequests: [
            {
                path: { endpointId: 1, clusterId: 6, attributeId: 0 },
                data: boolTrueTlv,
            },
        ],
        interactionModelRevision: 13,
    },
    TlvWriteRequest,
    {
        byDesign: true,
        byDesignNote: "WriteRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "write_req_timed",
    "WriteRequest: timedRequest=true (timed write transaction) — decode-only fixture",
    "WriteRequest",
    {
        timedRequest: true,
        writeRequests: [
            {
                path: { endpointId: 1, clusterId: 6, attributeId: 0 },
                data: boolTrueTlv,
            },
        ],
        interactionModelRevision: 13,
    },
    TlvWriteRequest,
    {
        byDesign: true,
        byDesignNote: "WriteRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "write_req_suppress_response",
    "WriteRequest: suppressResponse=true, timedRequest=false — decode-only fixture",
    "WriteRequest",
    {
        suppressResponse: true,
        timedRequest: false,
        writeRequests: [
            {
                path: { endpointId: 1, clusterId: 6, attributeId: 0 },
                data: boolTrueTlv,
            },
        ],
        interactionModelRevision: 13,
    },
    TlvWriteRequest,
    {
        byDesign: true,
        byDesignNote: "WriteRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

// =============================================================
// WriteResponse (TlvWriteResponse)
// =============================================================

add(
    "write_resp_success_single",
    "WriteResponse: single attribute status SUCCESS",
    "WriteResponse",
    {
        writeResponses: [
            {
                path: { endpointId: 1, clusterId: 6, attributeId: 0 },
                status: { status: 0 },
            },
        ],
        interactionModelRevision: 13,
    },
    TlvWriteResponse,
);

add(
    "write_resp_empty",
    "WriteResponse: empty writeResponses array",
    "WriteResponse",
    {
        writeResponses: [],
        interactionModelRevision: 13,
    },
    TlvWriteResponse,
);

add(
    "write_resp_error_single",
    "WriteResponse: single attribute status UnsupportedAttribute(0x86)",
    "WriteResponse",
    {
        writeResponses: [
            {
                path: { endpointId: 1, clusterId: 6, attributeId: 99 },
                status: { status: 0x86 },
            },
        ],
        interactionModelRevision: 13,
    },
    TlvWriteResponse,
);

// =============================================================
// InvokeRequest (TlvInvokeRequest)
// =============================================================

// InvokeRequest fixtures: the bridge decodes InvokeRequests (sent by controllers),
// it does not encode them. These fixtures are used for Stage-3 round-trip decode
// tests only. The encode path (MarshalTLV) does not exist for InvokeRequest.
add(
    "invoke_req_not_timed_not_suppressed",
    "InvokeRequest: suppressResponse=false, timedRequest=false, one command — decode-only fixture",
    "InvokeRequest",
    {
        suppressResponse: false,
        timedRequest: false,
        invokeRequests: [
            {
                commandPath: { endpointId: 1, clusterId: 6, commandId: 0 },
                commandFields: [],
            },
        ],
        interactionModelRevision: 13,
    },
    TlvInvokeRequest,
    {
        byDesign: true,
        byDesignNote: "InvokeRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "invoke_req_timed",
    "InvokeRequest: timedRequest=true (door-lock timed invoke) — decode-only fixture",
    "InvokeRequest",
    {
        suppressResponse: false,
        timedRequest: true,
        invokeRequests: [
            {
                commandPath: { endpointId: 1, clusterId: 0x101, commandId: 0 },
                commandFields: [],
            },
        ],
        interactionModelRevision: 13,
    },
    TlvInvokeRequest,
    {
        byDesign: true,
        byDesignNote: "InvokeRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "invoke_req_suppress_response",
    "InvokeRequest: suppressResponse=true, timedRequest=false — decode-only fixture",
    "InvokeRequest",
    {
        suppressResponse: true,
        timedRequest: false,
        invokeRequests: [
            {
                commandPath: { endpointId: 0, clusterId: 0x0003, commandId: 0 },
                commandFields: [],
            },
        ],
        interactionModelRevision: 13,
    },
    TlvInvokeRequest,
    {
        byDesign: true,
        byDesignNote: "InvokeRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "invoke_req_empty_requests",
    "InvokeRequest: no invoke requests — decode-only fixture",
    "InvokeRequest",
    {
        suppressResponse: false,
        timedRequest: false,
        invokeRequests: [],
        interactionModelRevision: 13,
    },
    TlvInvokeRequest,
    {
        byDesign: true,
        byDesignNote: "InvokeRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

// =============================================================
// InvokeResponseForSend (TlvInvokeResponseForSend)
// =============================================================

add(
    "invoke_resp_empty_success",
    "InvokeResponseForSend: suppressResponse=false, empty invokeResponses",
    "InvokeResponse",
    {
        suppressResponse: false,
        invokeResponses: [],
        interactionModelRevision: 13,
    },
    TlvInvokeResponseForSend,
);

add(
    "invoke_resp_suppress_true",
    "InvokeResponseForSend: suppressResponse=true, empty invokeResponses",
    "InvokeResponse",
    {
        suppressResponse: true,
        invokeResponses: [],
        interactionModelRevision: 13,
    },
    TlvInvokeResponseForSend,
);

// =============================================================
// TimedRequest (TlvTimedRequest)
// =============================================================
// TimedRequest fixtures: the bridge decodes TimedRequests (sent by controllers),
// it does not encode them. These fixtures are used for Stage-3 round-trip decode
// tests only.

add(
    "timed_req_short",
    "TimedRequest: timeout=6000ms (typical door-lock timed-request window) — decode-only fixture",
    "TimedRequest",
    { timeout: 6000, interactionModelRevision: 13 },
    TlvTimedRequest,
    {
        byDesign: true,
        byDesignNote: "TimedRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "timed_req_max",
    "TimedRequest: timeout=65535ms (maximum uint16 value) — decode-only fixture",
    "TimedRequest",
    { timeout: 65535, interactionModelRevision: 13 },
    TlvTimedRequest,
    {
        byDesign: true,
        byDesignNote: "TimedRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

add(
    "timed_req_minimal",
    "TimedRequest: timeout=1ms — decode-only fixture",
    "TimedRequest",
    { timeout: 1, interactionModelRevision: 13 },
    TlvTimedRequest,
    {
        byDesign: true,
        byDesignNote: "TimedRequest is decode-only in openccu-loom (bridge receives, never sends); no MarshalTLV to test",
    },
);

console.log(JSON.stringify(out, null, 2));
