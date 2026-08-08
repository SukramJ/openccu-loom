// Generates wire-byte fixtures for the openccu-loom TLV encoder to match
// against. Pipe to /tmp/tlv-fixtures.json. Each fixture pairs a JSON
// description (label + builder steps) with the hex-encoded matter.js
// TlvCodec.encode() output — openccu-loom's encoder must produce the
// same bytes for the same shape.
const { TlvCodec, TlvType } = require("@matter/types/tlv");

interface Fixture {
    label: string;
    description: string;
    bytesHex: string;
}

function bytesToHex(b: Uint8Array): string {
    return Array.from(b).map(x => x.toString(16).padStart(2, "0")).join("");
}

const out: Fixture[] = [];

function add(label: string, description: string, build: (w: any) => void) {
    const writer = new (require("@matter/types/tlv").TlvByteArrayWriter)();
    build(writer);
    out.push({ label, description, bytesHex: bytesToHex(writer.toByteArray()) });
}

// Direct encoder access — TlvCodec.writeTag + writeAtomicValue
const Tag = require("@matter/types/tlv").TlvTag;
const codec = require("@matter/types/tlv");

// matter.js exposes the wire-level codec as `TlvCodec.encode(<TlvElement>)`
// where the element is `{ typeLength: { type, length }, tag, value }`.
// We assemble shapes by hand so the fixtures cover individual tag /
// type combinations openccu-loom's encoder must produce.
function encodeOne(typeLength: any, tag: any, value: any): Uint8Array {
    const writer = new codec.TlvByteArrayWriter();
    writer.writeTag(typeLength, tag);
    // Null + Boolean encode the value into the control byte; only
    // primitives with payload need writePrimitive.
    if (typeLength.type !== TlvType.Null && typeLength.type !== TlvType.Boolean) {
        writer.writePrimitive(typeLength, value);
    }
    return writer.toByteArray();
}

const ANON = { id: undefined };
function ctx(n: number): any { return { id: n }; }

// --- unsigned ints, magnitude-driven width selection ---
out.push({
    label: "uint_anon_0",
    description: "PutUint(AnonymousTag, 0) → TypeUnsignedInt1, control=0x04, payload=00",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, ANON, 0)),
});
out.push({
    label: "uint_anon_255",
    description: "PutUint(AnonymousTag, 0xFF) → TypeUnsignedInt1, control=0x04, payload=FF",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, ANON, 0xFF)),
});
out.push({
    label: "uint_anon_256",
    description: "PutUint(AnonymousTag, 0x0100) → TypeUnsignedInt2, control=0x05, payload=00 01 (LE)",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 1 }, ANON, 0x0100)),
});
out.push({
    label: "uint_anon_65535",
    description: "PutUint(AnonymousTag, 0xFFFF) → TypeUnsignedInt2, control=0x05, payload=FF FF",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 1 }, ANON, 0xFFFF)),
});
out.push({
    label: "uint_anon_65536",
    description: "PutUint(AnonymousTag, 0x10000) → TypeUnsignedInt4, control=0x06, payload=00 00 01 00 (LE)",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 2 }, ANON, 0x10000)),
});
out.push({
    label: "uint_anon_specversion",
    description: "PutUint(AnonymousTag, 0x01050100) — Matter SpecificationVersion field",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 2 }, ANON, 0x01050100)),
});
out.push({
    label: "uint_anon_max32",
    description: "PutUint(AnonymousTag, 0xFFFFFFFF) → TypeUnsignedInt4 boundary",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 2 }, ANON, 0xFFFFFFFF)),
});
out.push({
    label: "uint64_anon_2_to_32",
    description: "PutUint64(AnonymousTag, 4294967296) → TypeUnsignedInt8 — least value that needs 8 bytes",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 3 }, ANON, BigInt(0x100000000))),
});

// --- explicit-width writers (PutUint16 / PutUint32 / PutUint64) ---
out.push({
    label: "uint16_explicit_1",
    description: "PutUint16(AnonymousTag, 1) — explicit 2-byte width regardless of magnitude",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 1 }, ANON, 1)),
});
out.push({
    label: "uint32_explicit_1",
    description: "PutUint32(AnonymousTag, 1) — explicit 4-byte width regardless of magnitude",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 2 }, ANON, 1)),
});
out.push({
    label: "uint64_explicit_1",
    description: "PutUint64(AnonymousTag, 1) — explicit 8-byte width regardless of magnitude",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 3 }, ANON, BigInt(1))),
});

// --- signed ints ---
out.push({
    label: "int_anon_minus1",
    description: "PutInt(AnonymousTag, -1) → TypeSignedInt1, payload=FF (two's complement)",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.SignedInt, length: 0 }, ANON, -1)),
});
out.push({
    label: "int_anon_minus128",
    description: "PutInt(AnonymousTag, -128) → TypeSignedInt1, payload=80",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.SignedInt, length: 0 }, ANON, -128)),
});
out.push({
    label: "int_anon_minus129",
    description: "PutInt(AnonymousTag, -129) → TypeSignedInt2",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.SignedInt, length: 1 }, ANON, -129)),
});

// --- bool / null ---
// matter.js encodes Boolean via the typeLength.value field (not length):
// `typeLength = type + (value ? 1 : 0)` → 0x09 for true, 0x08 for false.
out.push({
    label: "bool_anon_true",
    description: "PutBool(AnonymousTag, true) → control=0x09",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.Boolean, value: true }, ANON, true)),
});
out.push({
    label: "bool_anon_false",
    description: "PutBool(AnonymousTag, false) → control=0x08",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.Boolean, value: false }, ANON, false)),
});
out.push({
    label: "null_anon",
    description: "PutNull(AnonymousTag) → TypeNull, control=0x14",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.Null, length: 0 }, ANON, null)),
});

// --- UTF-8 strings ---
out.push({
    label: "utf8_anon_empty",
    description: "PutUTF8(AnonymousTag, '') → length-prefixed empty string, control=0x0C 00",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.Utf8String, length: 0 }, ANON, "")),
});
out.push({
    label: "utf8_anon_openccu-loom",
    description: "PutUTF8(AnonymousTag, 'openccu-loom')",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.Utf8String, length: 0 }, ANON, "openccu-loom")),
});

// --- octet strings ---
out.push({
    label: "octets_anon_empty",
    description: "PutOctets(AnonymousTag, []) → empty length-prefixed octet string",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.ByteString, length: 0 }, ANON, new Uint8Array())),
});
out.push({
    label: "octets_anon_3bytes",
    description: "PutOctets(AnonymousTag, 0x010203)",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.ByteString, length: 0 }, ANON, new Uint8Array([1, 2, 3]))),
});

// --- context tagged ---
out.push({
    label: "uint_ctx0_1",
    description: "PutUint(ContextTag(0), 1) — single-octet context tag, payload=01",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, ctx(0), 1)),
});
out.push({
    label: "uint_ctx7_42",
    description: "PutUint(ContextTag(7), 42) — single-octet context tag form",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, ctx(7), 42)),
});

// --- FullyQualified tags (matter.js TlvCodec.ts:271-282) ---
// 48-bit form: profile uint32 (LE) + id uint16 (LE).
// 64-bit form: profile uint32 (LE) + id uint32 (LE).
// Used by Matter for cluster-specific extension tags that need a full
// vendor + profile + id tuple on the wire (e.g. manufacturer-specific
// attribute extensions, vendor-extension events).
function fq(profile: number, id: number): any { return { profile, id }; }
out.push({
    label: "fq6_profile_12345678_id_abcd_uint8_42",
    description: "FullyQualifiedTag(profile=0x12345678,id=0xABCD) PutUint(_, 42) → control=0xC4, profile uint32 LE + id uint16 LE",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, fq(0x12345678, 0xABCD), 42)),
});
out.push({
    label: "fq8_profile_12345678_id_deadbeef_uint8_42",
    description: "FullyQualifiedTag(profile=0x12345678,id=0xDEADBEEF) PutUint(_, 42) → control=0xE4, profile uint32 LE + id uint32 LE",
    bytesHex: bytesToHex(encodeOne({ type: TlvType.UnsignedInt, length: 0 }, fq(0x12345678, 0xDEADBEEF), 42)),
});

// --- ImplicitProfile tags (Matter Core §A.7.2 control bytes 0x80/0xA0) ---
// matter.js's TlvCodec encoder does NOT emit ImplicitProfile tags
// (writeTag only handles Anonymous, ContextSpecific, CommonProfile16/32,
// FullyQualified48/64); see matter.js TlvCodec.ts:254-283. The spec
// however defines tag-controls 4 + 5 and Apple Home, chip-tool, and
// CHIP-Tool decode them. openccu-loom's encoder DOES emit them via
// `ImplicitTag(n)` for the "implicit profile is the current message
// context" use-case. The bytes below are derived from the spec
// (control = tagControl<<5 | typeLength) so the openccu-loom encoder
// stays wire-correct against any conforming Matter consumer, not just
// matter.js. matter.js's decoder is happy to read them per its
// NotImplementedError-but-only-on-unknown-control-flow at
// TlvCodec.js:120-122 (which throws only when no profile is set, not
// when a known ImplicitProfile control byte arrives).
//
// Hand-encoded — replace with `bytesToHex(...)` once matter.js gains
// an Implicit-Profile encoder.
out.push({
    label: "implicit2_id_1234_uint8_42",
    description: "ImplicitTag(0x1234) PutUint(_, 42) → control=0x84, id uint16 LE (0x1234), value 0x2A. matter.js encoder does not emit; spec-derived bytes per Matter Core §A.7.2.",
    bytesHex: "8434122a",
});
out.push({
    label: "implicit4_id_12345678_uint8_42",
    description: "ImplicitTag(0x12345678) PutUint(_, 42) → control=0xA4, id uint32 LE (0x12345678), value 0x2A. matter.js encoder does not emit; spec-derived bytes per Matter Core §A.7.2.",
    bytesHex: "a4785634122a",
});

console.log(JSON.stringify(out, null, 2));
