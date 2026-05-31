// Walks @matter/model's MatterDefinition tree and emits a parity-friendly
// snapshot for openccu-loom's matter-side code: device types with revisions,
// clusters with revisions + featureMap + per-attribute IDs/types/conformance/
// constraints + commands + events. Output is JSON on stdout — pipe to
// docs/parity/matter/matter-schema-snapshot.json for in-repo persistence.
import { MatterDefinition } from "@matter/model";

interface DeviceTypeOut {
    id: number;
    name: string;
    classification?: string;
    revision: number;
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
    },
    deviceTypes: [] as DeviceTypeOut[],
    clusters: [] as ClusterOut[],
};

try {
    const spec = require("@matter/model/dist/cjs/common/Specification.js");
    if (spec.Specification) {
        out.matter.revision = spec.Specification.REVISION;
        out.matter.specificationVersion = spec.Specification.SPECIFICATION_VERSION;
        out.matter.interactionModelRevision = spec.Specification.INTERACTION_MODEL_REVISION;
        out.matter.dataModelRevision = spec.Specification.DATA_MODEL_REVISION;
    }
} catch (e) {
    console.error("spec import:", (e as Error).message);
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

for (const c of children) {
    if (c.tag === "deviceType" && typeof c.id === "number") {
        let rev = 1;
        const desc = c.children?.find((ch: any) => ch.tag === "requirement" && ch.name === "Descriptor");
        if (desc) {
            const dtList = desc.children?.find((ch: any) => ch.name === "DeviceTypeList");
            if (dtList?.default?.[0]?.revision) rev = dtList.default[0].revision;
        }
        out.deviceTypes.push({ id: c.id, name: c.name, classification: c.classification, revision: rev });
    } else if (c.tag === "cluster" && typeof c.id === "number") {
        let rev = 1;
        let featureMap = 0;
        const attributes: AttrOut[] = [];
        const commands: CmdOut[] = [];
        const events: EvtOut[] = [];
        const features: { name: string; conformance?: string; description?: string }[] = [];
        for (const ch of (c.children ?? [])) {
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
//   docs/parity/matter/matter-schema-snapshot.json  (master / audit reference)
//   internal/north/matter/parity/schema.json         (embed source for parity_matterjs_test.go)
//
// Example:
//   npx ts-node docs/parity/matter/extract-from-matter-js.ts \
//       > docs/parity/matter/matter-schema-snapshot.json
//   cp docs/parity/matter/matter-schema-snapshot.json \
//       internal/north/matter/parity/schema.json
