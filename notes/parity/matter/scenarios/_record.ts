// SPDX-License-Identifier: Apache-2.0
//
// Scenario-fixture recorder. Walks every *.json scenario in this
// directory, identifies the bridge.expect_tx steps that ship
// ReportData, and emits a matter.js-canonical sidecar
// `<scenario>__matter_js_reference.json` capturing the TLV wire
// bytes matter.js HEAD would produce for the same envelope shape.
//
// The sidecar pins the deterministic envelope fields the bridge
// controls (suppressResponse, interactionModelRevision, the
// presence/absence of attributeReports) so a later phase can extend
// the Go harness with byte-exact envelope comparison against the
// matter.js reference. Fields the bridge picks dynamically
// (subscriptionId, message counter, fresh exchange ID) stay
// placeholder-encoded — those bind via $variables in the scenario
// step list and are excluded from any byte comparison.
//
// Run from any directory; the script resolves ../matter.js via its
// own __dirname. Regen workflow:
//
//   make scenarios-regen-sidecars
//
// Phase-G scope: enumerate scenarios, emit one sidecar per scenario
// with ≥1 ReportData step. Per-cluster AttributeData pre-encoding
// (so inner data bytes are also pinned) is a future enhancement.

const path = require("path");
const fs = require("fs");

const scriptDir = __dirname;
const matterJsRoot = path.resolve(scriptDir, "../../../../../matter.js");
const typesMain = path.join(matterJsRoot, "node_modules/@matter/types/dist/cjs/index.js");

if (!fs.existsSync(typesMain)) {
    console.error(
        `[scenario-recorder] @matter/types not found at ${typesMain}\n` +
            `Run \`npm install\` in ${matterJsRoot} first, or update the matter.js pin.`,
    );
    process.exit(1);
}

const { TlvDataReportForSend } = require(typesMain);

interface ReportDataRecord {
    type: "ReportData";
    description: string;
    input: any;
    bytesHex: string;
}

interface ScenarioReference {
    scenario: string;
    matter_js_pinned_at: string;
    references: { [stepIndex: number]: ReportDataRecord };
}

interface ScenarioStep {
    actor: string;
    kind: string;
    opcode?: string;
}

interface Scenario {
    name: string;
    given: {
        subscription?: { endpoint: number; cluster: number; attribute: number };
        subscriptions?: { subscription: { endpoint: number; cluster: number; attribute: number } }[];
    };
    steps: ScenarioStep[];
}

function hex(buf: Uint8Array): string {
    return Array.from(buf)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

function matterJsRev(): string {
    try {
        const out = require("child_process").execSync("git rev-parse HEAD", { cwd: matterJsRoot });
        return out.toString().trim();
    } catch {
        return "unknown";
    }
}

function isReportDataExpect(step: ScenarioStep): boolean {
    return step.actor === "bridge" && step.kind === "expect_tx" && (!step.opcode || step.opcode === "ReportData");
}

const pinned = matterJsRev();

const entries = fs
    .readdirSync(scriptDir)
    .filter(
        (n: string) =>
            n.endsWith(".json") &&
            !n.startsWith("_") &&
            !n.includes("__matter_js_reference."),
    );

let emitted = 0;
for (const filename of entries) {
    const filepath = path.join(scriptDir, filename);
    let scenario: Scenario;
    try {
        scenario = JSON.parse(fs.readFileSync(filepath, "utf8"));
    } catch (err) {
        console.error(`[scenario-recorder] skip ${filename}: parse error ${err}`);
        continue;
    }

    const references: { [stepIndex: number]: ReportDataRecord } = {};
    for (let i = 0; i < scenario.steps.length; i++) {
        const step = scenario.steps[i];
        if (!isReportDataExpect(step)) continue;

        // Pin the envelope shape only. Bridge fills subscriptionId at
        // runtime (per-subscription); the placeholder marks that field
        // as runtime-bound.
        const input = {
            subscriptionId: 0xffffffff,
            attributeReports: [],
            suppressResponse: false,
            interactionModelRevision: 11,
        };
        const bytes = TlvDataReportForSend.encode(input);
        references[i] = {
            type: "ReportData",
            description: `matter.js DataReport envelope for ${scenario.name} step[${i}]. Bridge-runtime fields (subscriptionId, attributeReports payload, counter, exchange ID) are bound via $variables in the scenario.`,
            input,
            bytesHex: hex(bytes),
        };
    }

    if (Object.keys(references).length === 0) {
        continue;
    }

    const fixture: ScenarioReference = {
        scenario: scenario.name,
        matter_js_pinned_at: pinned,
        references,
    };
    const outPath = path.join(scriptDir, `${scenario.name}__matter_js_reference.json`);
    fs.writeFileSync(outPath, JSON.stringify(fixture, null, 2) + "\n");
    emitted++;
}

console.log(`[scenario-recorder] emitted ${emitted} sidecar(s) for matter.js @ ${pinned.slice(0, 8)}`);
