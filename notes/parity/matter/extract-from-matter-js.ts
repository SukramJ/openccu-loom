// Walks @matter/model's MatterDefinition tree and emits a parity-friendly
// snapshot for openccu-loom's matter-side code: device types with revisions
// and their cluster requirements (server/client + conformance),
// clusters with revisions + featureMap + per-attribute IDs/types/conformance/
// constraints + commands + events. Output is JSON on stdout — pipe to
// notes/parity/matter/matter-schema-snapshot.json for in-repo persistence.
import { MatterDefinition, Specification } from "@matter/model";
import { execSync } from "node:child_process";

// ReqOut is one cluster requirement of a device type: which cluster the
// Device Library specifies for the type, on which side (server/client), and
// under which conformance. openccu-loom's device-type conformance guard
// (tests/contract/matter_devicetype_conformance_test.go) reads these to
// decide whether a bridged endpoint may mount a given cluster as a server.
interface ReqOut {
    id: number;
    name: string;
    element: string;
    conformance?: string;
}

interface DeviceTypeOut {
    id: number;
    name: string;
    classification?: string;
    revision: number;
    requirements: ReqOut[];
}

interface AttrOut {
    id: number;
    name: string;
    type?: string;
    conformance?: string;
    access?: string;
    constraint?: string;
    quality?: string;
    default?: any;
}

interface CmdOut {
    id: number;
    name: string;
    direction?: string;
    conformance?: string;
    response?: string;
}

interface EvtOut {
    id: number;
    name: string;
    priority?: string;
    conformance?: string;
}

interface ClusterOut {
    id: number;
    name: string;
    revision: number;
    featureMap: number;
    attributes: AttrOut[];
    commands: CmdOut[];
    events: EvtOut[];
    features?: { name: string; conformance?: string; description?: string }[];
}

const out = {
    matter: {
        revision: undefined as string | undefined,
        specificationVersion: undefined as number | undefined,
        interactionModelRevision: undefined as number | undefined,
        dataModelRevision: undefined as number | undefined,
        sourceCommit: undefined as string | undefined,
    },
    deviceTypes: [] as DeviceTypeOut[],
    clusters: [] as ClusterOut[],
};

// Matter spec metadata, straight off the @matter/model package export so the
// snapshot records which spec revision the schema was extracted at.
out.matter.revision = Specification.REVISION;
out.matter.specificationVersion = Specification.SPECIFICATION_VERSION;
out.matter.interactionModelRevision = Specification.INTERACTION_MODEL_REVISION;
out.matter.dataModelRevision = Specification.DATA_MODEL_REVISION;

try {
    // Record the matter.js HEAD commit the snapshot was extracted from, so the
    // pinned reference is traceable. This script runs inside the matter.js
    // checkout, so HEAD is matter.js's. Deterministic: changes only when the
    // matter.js source does.
    out.matter.sourceCommit = execSync("git rev-parse HEAD", {
        encoding: "utf8",
    }).trim();
} catch (e) {
    console.error("source commit:", (e as Error).message);
}

function jsonable(v: any): any {
    if (typeof v === "bigint") return v.toString();
    if (Array.isArray(v)) return v.map(jsonable);
    if (typeof v === "object" && v !== null) {
        const o: any = {};
        for (const k of Object.keys(v)) o[k] = jsonable(v[k]);
        return o;
    }
    return v;
}

const root: any = MatterDefinition;
const children: any[] = root.children ?? [];

// Index every named top-level element so a cluster declared as
// `{ name, id, type: "Base" }` (e.g. the ConcentrationMeasurement /
// ResourceMonitoring families) resolves its inherited ClusterRevision +
// members from the base. Without this, type-inheriting clusters collapse to
// revision 1 with empty attribute lists when matter.js HEAD declares them by
// reference instead of inline.
const byName = new Map<string, any>();
for (const c of children) if (c.name && !byName.has(c.name)) byName.set(c.name, c);

// Merge inherited children: base first, then own overrides keyed by (tag, id|name).
function resolvedChildren(node: any, seen: Set<string> = new Set()): any[] {
    let base: any[] = [];
    if (node.type && byName.has(node.type) && !seen.has(node.type)) {
        seen.add(node.type);
        base = resolvedChildren(byName.get(node.type), seen);
    }
    const keyOf = (ch: any) => `${ch.tag}:${typeof ch.id === "number" ? ch.id : ch.name}`;
    const merged = new Map<string, any>();
    for (const ch of base) merged.set(keyOf(ch), ch);
    for (const ch of (node.children ?? [])) merged.set(keyOf(ch), ch); // own wins
    return [...merged.values()];
}

for (const c of children) {
    if (c.tag === "deviceType" && typeof c.id === "number") {
        let rev = 1;
        const kids = resolvedChildren(c);
        const desc = kids.find((ch: any) => ch.tag === "requirement" && ch.name === "Descriptor");
        if (desc) {
            const dtList = desc.children?.find((ch: any) => ch.name === "DeviceTypeList");
            if (dtList?.default?.[0]?.revision) rev = dtList.default[0].revision;
        }
        // Cluster requirements carry a numeric id; the nested per-attribute /
        // per-command / per-feature requirements do not and are skipped.
        const requirements: ReqOut[] = [];
        for (const ch of kids) {
            if (ch.tag !== "requirement" || typeof ch.id !== "number" || !ch.element) continue;
            requirements.push({ id: ch.id, name: ch.name, element: ch.element, conformance: ch.conformance });
        }
        requirements.sort((a, b) => a.id - b.id || a.element.localeCompare(b.element));
        out.deviceTypes.push({
            id: c.id,
            name: c.name,
            classification: c.classification,
            revision: rev,
            requirements,
        });
    } else if (c.tag === "cluster" && typeof c.id === "number") {
        let rev = 1;
        let featureMap = 0;
        const attributes: AttrOut[] = [];
        const commands: CmdOut[] = [];
        const events: EvtOut[] = [];
        const features: { name: string; conformance?: string; description?: string }[] = [];
        for (const ch of resolvedChildren(c)) {
            if (ch.tag === "attribute") {
                if (ch.id === 0xFFFD || ch.name === "ClusterRevision") {
                    if (ch.default !== undefined) rev = ch.default;
                }
                if (ch.id === 0xFFFC || ch.name === "FeatureMap") {
                    if (typeof ch.default === "number") featureMap = ch.default;
                    for (const f of (ch.children ?? [])) {
                        if (f.tag === "field") features.push({ name: f.name, conformance: f.conformance, description: f.description });
                    }
                }
                if (typeof ch.id === "number") {
                    attributes.push({
                        id: ch.id,
                        name: ch.name,
                        type: ch.type,
                        conformance: ch.conformance,
                        access: ch.access,
                        constraint: ch.constraint,
                        quality: ch.quality,
                        default: ch.default !== undefined ? jsonable(ch.default) : undefined,
                    });
                }
            } else if (ch.tag === "command" && typeof ch.id === "number") {
                commands.push({ id: ch.id, name: ch.name, direction: ch.direction, conformance: ch.conformance, response: ch.response });
            } else if (ch.tag === "event" && typeof ch.id === "number") {
                events.push({ id: ch.id, name: ch.name, priority: ch.priority, conformance: ch.conformance });
            }
        }
        attributes.sort((a, b) => a.id - b.id);
        commands.sort((a, b) => a.id - b.id);
        events.sort((a, b) => a.id - b.id);
        out.clusters.push({ id: c.id, name: c.name, revision: rev, featureMap, attributes, commands, events, features });
    }
}

out.deviceTypes.sort((a, b) => a.id - b.id);
out.clusters.sort((a, b) => a.id - b.id);

console.log(JSON.stringify(out, null, 2));

// After running this script, copy the output to BOTH:
//   notes/parity/matter/matter-schema-snapshot.json  (master / audit reference)
//   internal/north/matter/parity/schema.json         (embed source for parity_matterjs_test.go)
//
// Example:
//   npx ts-node notes/parity/matter/extract-from-matter-js.ts \
//       > notes/parity/matter/matter-schema-snapshot.json
//   cp notes/parity/matter/matter-schema-snapshot.json \
//       internal/north/matter/parity/schema.json
