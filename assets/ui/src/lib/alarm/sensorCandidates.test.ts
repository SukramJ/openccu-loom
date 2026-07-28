import { describe, it, expect } from "vitest";
import type { AlarmOutputCandidate, DeviceSummary } from "$lib/api/types";
import {
  isSecurityDevice,
  guessSensorType,
  guessSensorParameter,
  guessSensorBinding,
  buildCandidates,
  filterOutputCandidates,
  distinctValues,
  sortPickerRows,
  type PickerSortKey,
} from "./sensorCandidates";

function makeDevice(overrides: Partial<DeviceSummary> = {}): DeviceSummary {
  return {
    address: "ABC123",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF-ABC123",
    model: "HmIP-PSM",
    name: "Some device",
    available: true,
    channels_count: 1,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...overrides,
  };
}

describe("isSecurityDevice", () => {
  it.each([
    ["HmIP-SWDO", "Kitchen window"],
    ["HmIP-SCI", "Contact interface"],
    ["HmIP-SMO", "Smoke detector"],
    ["HmIP-SMI", "Motion indoor"],
    ["HmIP-SPI", "Presence indoor"],
    ["HM-RC-4", "Remote control"],
    ["HmIP-KRCA", "Key ring remote"],
    ["HmIP-WRC6", "Wall remote"],
    ["HmIP-WGC", "Wall glass control"],
    ["HmIP-SWSD", "Smoke detector siren"],
  ])("matches model %s (%s)", (model, name) => {
    expect(isSecurityDevice(makeDevice({ model, name }))).toBe(true);
  });

  it.each([
    "Bewegungsmelder Flur",
    "Fenster Küche",
    "Kontakt Terrassentür",
    "Rauchmelder",
    "Wassermelder Keller",
    "Water leak sensor",
    "Gas warner",
    "Presence sensor",
    // Vendor label carries this exact misspelling — locked in deliberately,
    // not a typo in the test.
    "Prescence sensor",
    "Sabotagealarm",
    "Tamper switch",
    "Door sensor",
    "Window contact",
    "PIR motion detector",
  ])("matches name %s", (name) => {
    expect(isSecurityDevice(makeDevice({ model: "HmIP-XYZ", name }))).toBe(true);
  });

  it("matches via model_label when model and name are generic", () => {
    expect(
      isSecurityDevice(
        makeDevice({ model: "HmIP-XYZ", model_label: "Fensterkontakt", name: "Küche" }),
      ),
    ).toBe(true);
  });

  it.each([
    ["HmIP-BSL", "Brand Switch/dimmer"],
    ["HmIP-PSM", "Power switch/meter"],
    ["HmIP-BROLL", "Blind actuator"],
    ["HmIP-eTRV", "Thermostat"],
  ])("rejects model %s (%s)", (model, name) => {
    expect(isSecurityDevice(makeDevice({ model, name }))).toBe(false);
  });

  it.each(["Kitchen Lamp", "Living Room Dimmer", "Hallway Thermostat"])(
    "rejects plain name %s",
    (name) => {
      expect(isSecurityDevice(makeDevice({ model: "HmIP-BSL", name }))).toBe(false);
    },
  );
});

describe("guessSensorType", () => {
  it.each([
    ["HmIP-STHD", "Sabotagealarm"],
    ["HmIP-SWDO", "Tamper switch"],
  ])("model %s / name %s guesses tamper", (model, name) => {
    expect(guessSensorType(makeDevice({ model, name }))).toBe("tamper");
  });

  it.each([
    ["HmIP-SWSD", "Smoke detector"],
    ["HmIP-XYZ", "Rauchmelder"],
    ["HmIP-XYZ", "Wassermelder"],
    ["HmIP-XYZ", "Water leak sensor"],
    ["HmIP-XYZ", "CO2 sensor"],
    ["HmIP-XYZ", "Gas warner"],
  ])("model %s / name %s guesses hazard", (model, name) => {
    expect(guessSensorType(makeDevice({ model, name }))).toBe("hazard");
  });

  it.each([
    ["HmIP-SMI", "Motion indoor"],
    ["HmIP-SPI", "Presence indoor"],
    ["HmIP-XYZ", "Bewegungsmelder Flur"],
    ["HmIP-XYZ", "PIR sensor"],
  ])("model %s / name %s guesses motion", (model, name) => {
    expect(guessSensorType(makeDevice({ model, name }))).toBe("motion");
  });

  it.each([
    ["HmIP-SWDO", "Kitchen window"],
    ["HmIP-XYZ", "Fenstergriff"],
    ["HmIP-XYZ", "Rotary handle"],
  ])("model %s / name %s guesses window", (model, name) => {
    expect(guessSensorType(makeDevice({ model, name }))).toBe("window");
  });

  it.each([
    ["HM-RC-4", "Remote control"],
    ["HmIP-KRCA", "Key ring remote"],
    ["HmIP-XYZ", "Panic button"],
    ["HmIP-XYZ", "Taster"],
  ])("model %s / name %s guesses panic", (model, name) => {
    expect(guessSensorType(makeDevice({ model, name }))).toBe("panic");
  });

  it("falls back to door for a device that matches no keyword", () => {
    // "sci"/"kontakt" hit isSecurityDevice's broader net but neither word
    // is one of guessSensorType's own keywords, so this is the documented
    // fallback case rather than an accidental match.
    expect(
      guessSensorType(makeDevice({ model: "HmIP-SCI", name: "Kontakt Schnittstelle" })),
    ).toBe("door");
  });

  it("falls back to door for a plain door-style name", () => {
    expect(guessSensorType(makeDevice({ model: "HmIP-XYZ", name: "Front door" }))).toBe(
      "door",
    );
  });
});

describe("guessSensorParameter", () => {
  it.each([
    ["motion", "MOTION"],
    ["tamper", "SABOTAGE"],
    ["hazard", "SMOKE_DETECTOR_ALARM_STATUS"],
    ["panic", "PRESS_SHORT"],
    ["door", "STATE"],
    ["window", "STATE"],
  ] as const)("%s guesses parameter %s", (type, parameter) => {
    expect(guessSensorParameter(type)).toBe(parameter);
  });
});

describe("guessSensorBinding", () => {
  it("derives channel :1, the type-matched parameter, and the guessed type", () => {
    const device = makeDevice({ address: "ABC123", model: "HmIP-SWDO", name: "Kitchen window" });
    expect(guessSensorBinding(device)).toEqual({
      channel: "ABC123:1",
      parameter: "STATE",
      type: "window",
    });
  });

  it("binds a motion sensor to the MOTION parameter", () => {
    const device = makeDevice({ address: "DEF456", model: "HmIP-SMI", name: "Hallway motion" });
    expect(guessSensorBinding(device)).toEqual({
      channel: "DEF456:1",
      parameter: "MOTION",
      type: "motion",
    });
  });

  it("returns null for a device with no usable address", () => {
    expect(guessSensorBinding(makeDevice({ address: "" }))).toBeNull();
  });
});

describe("buildCandidates", () => {
  const doorContact = makeDevice({
    address: "SEC1",
    model: "HmIP-SWDO",
    name: "Kitchen window",
  });
  const motionSensor = makeDevice({
    address: "SEC2",
    model: "HmIP-SMI",
    name: "Hallway motion",
  });
  const lamp = makeDevice({ address: "OTH1", model: "HmIP-BSL", name: "Kitchen lamp" });
  const dimmer = makeDevice({ address: "OTH2", model: "HmIP-BDT", name: "Living room dimmer" });

  it("defaults to security devices only, in original order", () => {
    const result = buildCandidates([lamp, doorContact, dimmer, motionSensor]);
    expect(result).toEqual([doorContact, motionSensor]);
  });

  it("widens the pool to every device when showAll is set", () => {
    const result = buildCandidates([lamp, doorContact, dimmer, motionSensor], {
      showAll: true,
    });
    expect(result).toEqual([lamp, doorContact, dimmer, motionSensor]);
  });

  it("free-text search narrows the security-only pool by name", () => {
    const result = buildCandidates([doorContact, motionSensor], { query: "kitchen" });
    expect(result).toEqual([doorContact]);
  });

  it("free-text search also matches address, model, and model_label", () => {
    const labeled = makeDevice({
      address: "SEC3",
      model: "HmIP-SWDO",
      model_label: "Fensterkontakt",
      name: "Garage",
    });
    expect(buildCandidates([doorContact, motionSensor, labeled], { query: "SEC2" })).toEqual([
      motionSensor,
    ]);
    expect(buildCandidates([doorContact, motionSensor, labeled], { query: "SWDO" })).toEqual([
      doorContact,
      labeled,
    ]);
    expect(
      buildCandidates([doorContact, motionSensor, labeled], { query: "Fensterkontakt" }),
    ).toEqual([labeled]);
  });

  it("does not let a matching query override the security filter", () => {
    // "lamp" matches the non-security device's name, but showAll stays
    // false — the security gate is unconditional, not just a default.
    const result = buildCandidates([lamp, doorContact], { query: "lamp" });
    expect(result).toEqual([]);
  });

  it("combines showAll with a query over the widened pool", () => {
    const result = buildCandidates([lamp, doorContact, dimmer, motionSensor], {
      showAll: true,
      query: "kitchen",
    });
    expect(result).toEqual([lamp, doorContact]);
  });

  it("caps the result at the default limit of 60", () => {
    const many = Array.from({ length: 65 }, (_, i) =>
      makeDevice({ address: `SEC${i}`, model: "HmIP-SWDO", name: `Contact ${i}` }),
    );
    const result = buildCandidates(many);
    expect(result).toHaveLength(60);
    expect(result[0].address).toBe("SEC0");
    expect(result[59].address).toBe("SEC59");
  });

  it("honors a custom limit", () => {
    const result = buildCandidates([doorContact, motionSensor], { limit: 1 });
    expect(result).toEqual([doorContact]);
  });

  it("returns an empty list for an empty device pool", () => {
    expect(buildCandidates([])).toEqual([]);
  });

  it("room filter narrows to devices whose rooms array includes the exact room", () => {
    const kitchenDoor = makeDevice({
      address: "SEC10",
      model: "HmIP-SWDO",
      name: "Kitchen contact",
      rooms: ["Kitchen"],
    });
    const hallwayMotion = makeDevice({
      address: "SEC11",
      model: "HmIP-SMI",
      name: "Hallway motion",
      rooms: ["Hallway"],
    });
    const noRoomContact = makeDevice({
      address: "SEC12",
      model: "HmIP-SWDO",
      name: "Unassigned contact",
    });
    const result = buildCandidates([kitchenDoor, hallwayMotion, noRoomContact], {
      room: "Kitchen",
    });
    expect(result).toEqual([kitchenDoor]);
  });

  it("func filter narrows to devices whose functions array includes the exact function", () => {
    const securityFuncDoor = makeDevice({
      address: "SEC13",
      model: "HmIP-SWDO",
      name: "Security door",
      functions: ["Security"],
    });
    const climateFuncMotion = makeDevice({
      address: "SEC14",
      model: "HmIP-SMI",
      name: "Climate motion",
      functions: ["Climate"],
    });
    const noFuncContact = makeDevice({
      address: "SEC15",
      model: "HmIP-SWDO",
      name: "Unassigned contact",
    });
    const result = buildCandidates([securityFuncDoor, climateFuncMotion, noFuncContact], {
      func: "Security",
    });
    expect(result).toEqual([securityFuncDoor]);
  });

  it("free-text search also matches a room or function assignment", () => {
    const atticContact = makeDevice({
      address: "SEC16",
      model: "HmIP-SWDO",
      name: "Contact",
      rooms: ["Attic"],
    });
    const perimeterContact = makeDevice({
      address: "SEC17",
      model: "HmIP-SWDO",
      name: "Contact",
      functions: ["Perimeter"],
    });
    expect(buildCandidates([doorContact, atticContact], { query: "Attic" })).toEqual([
      atticContact,
    ]);
    expect(buildCandidates([doorContact, perimeterContact], { query: "Perimeter" })).toEqual([
      perimeterContact,
    ]);
  });

  it("room filter narrows further but does not widen past the security gate", () => {
    const kitchenLamp = makeDevice({
      address: "OTH3",
      model: "HmIP-BSL",
      name: "Kitchen lamp",
      rooms: ["Kitchen"],
    });
    const kitchenDoor = makeDevice({
      address: "SEC18",
      model: "HmIP-SWDO",
      name: "Kitchen door",
      rooms: ["Kitchen"],
    });
    // Non-security device sharing the same room stays excluded; showAll false.
    const result = buildCandidates([kitchenLamp, kitchenDoor], { room: "Kitchen" });
    expect(result).toEqual([kitchenDoor]);

    // With showAll, the room filter still narrows the widened pool.
    const otherRoomDoor = makeDevice({
      address: "SEC19",
      model: "HmIP-SWDO",
      name: "Other door",
      rooms: ["Hallway"],
    });
    const widened = buildCandidates([kitchenLamp, kitchenDoor, otherRoomDoor], {
      showAll: true,
      room: "Kitchen",
    });
    expect(widened).toEqual([kitchenLamp, kitchenDoor]);
  });

  it("room and query combine with AND semantics", () => {
    const kitchenDoor = makeDevice({
      address: "SEC20",
      model: "HmIP-SWDO",
      name: "Kitchen door",
      rooms: ["Kitchen"],
    });
    const kitchenMotion = makeDevice({
      address: "SEC21",
      model: "HmIP-SMI",
      name: "Kitchen motion",
      rooms: ["Kitchen"],
    });
    const result = buildCandidates([kitchenDoor, kitchenMotion], {
      room: "Kitchen",
      query: "door",
    });
    expect(result).toEqual([kitchenDoor]);
  });
});

function outputCandidate(overrides: Partial<AlarmOutputCandidate> = {}): AlarmOutputCandidate {
  return {
    central: "ccu1",
    device_address: "OUT1",
    device_name: "Siren",
    model: "HmIP-ASIR",
    channel_address: "OUT1:1",
    channel_no: 1,
    channel_name: "Siren channel",
    classes: [],
    kind: "",
    ...overrides,
  };
}

describe("filterOutputCandidates", () => {
  const kitchenSiren = outputCandidate({
    device_address: "SIR1",
    device_name: "Kitchen siren",
    model: "HmIP-ASIR",
    channel_address: "SIR1:3",
    channel_no: 3,
    channel_name: "Alarm output",
  });
  const hallwayLight = outputCandidate({
    device_address: "LGT1",
    device_name: "Hallway light",
    model: "HmIP-BSL",
    channel_address: "LGT1:1",
    channel_no: 1,
    channel_name: "Optical alarm",
  });

  it("returns every candidate unchanged and in order when no filters are given", () => {
    expect(filterOutputCandidates([kitchenSiren, hallwayLight])).toEqual([
      kitchenSiren,
      hallwayLight,
    ]);
  });

  it("query matches channel_name, device_name, device_address, channel_address, and model", () => {
    expect(
      filterOutputCandidates([kitchenSiren, hallwayLight], { query: "Alarm output" }),
    ).toEqual([kitchenSiren]);
    expect(
      filterOutputCandidates([kitchenSiren, hallwayLight], { query: "Kitchen siren" }),
    ).toEqual([kitchenSiren]);
    expect(filterOutputCandidates([kitchenSiren, hallwayLight], { query: "SIR1" })).toEqual([
      kitchenSiren,
    ]);
    expect(filterOutputCandidates([kitchenSiren, hallwayLight], { query: "SIR1:3" })).toEqual([
      kitchenSiren,
    ]);
    expect(filterOutputCandidates([kitchenSiren, hallwayLight], { query: "ASIR" })).toEqual([
      kitchenSiren,
    ]);
  });

  it("query matches a room or function assignment", () => {
    const atticSiren = outputCandidate({
      device_address: "SIR2",
      channel_address: "SIR2:1",
      rooms: ["Attic"],
    });
    const perimeterSiren = outputCandidate({
      device_address: "SIR3",
      channel_address: "SIR3:1",
      functions: ["Perimeter"],
    });
    expect(filterOutputCandidates([kitchenSiren, atticSiren], { query: "Attic" })).toEqual([
      atticSiren,
    ]);
    expect(filterOutputCandidates([kitchenSiren, perimeterSiren], { query: "Perimeter" })).toEqual(
      [perimeterSiren],
    );
  });

  it("room filter narrows to candidates whose rooms array includes the exact room", () => {
    const kitchenRoomSiren = outputCandidate({
      device_address: "SIR4",
      channel_address: "SIR4:1",
      rooms: ["Kitchen"],
    });
    const hallwayRoomSiren = outputCandidate({
      device_address: "SIR5",
      channel_address: "SIR5:1",
      rooms: ["Hallway"],
    });
    const result = filterOutputCandidates([kitchenRoomSiren, hallwayRoomSiren, kitchenSiren], {
      room: "Kitchen",
    });
    expect(result).toEqual([kitchenRoomSiren]);
  });

  it("func filter narrows to candidates whose functions array includes the exact function", () => {
    const securityFuncSiren = outputCandidate({
      device_address: "SIR6",
      channel_address: "SIR6:1",
      functions: ["Security"],
    });
    const climateFuncSiren = outputCandidate({
      device_address: "SIR7",
      channel_address: "SIR7:1",
      functions: ["Climate"],
    });
    const result = filterOutputCandidates([securityFuncSiren, climateFuncSiren, kitchenSiren], {
      func: "Security",
    });
    expect(result).toEqual([securityFuncSiren]);
  });

  it("returns an empty list for an empty candidate pool", () => {
    expect(filterOutputCandidates([])).toEqual([]);
  });
});

describe("distinctValues", () => {
  it("dedupes exact-string duplicates across items", () => {
    const devices = [
      makeDevice({ rooms: ["Kitchen", "Hallway"] }),
      makeDevice({ rooms: ["Kitchen"] }),
      makeDevice({ rooms: ["Hallway", "Attic"] }),
    ];
    expect(distinctValues(devices, (d) => d.rooms)).toEqual(["Attic", "Hallway", "Kitchen"]);
  });

  it("sorts distinct values case-insensitively rather than by raw character code", () => {
    // Plain character-code sort would put "Banana" (capital B, code 66)
    // before "apple" (lowercase a, code 97); case-insensitive sort must not.
    const devices = [makeDevice({ rooms: ["Banana"] }), makeDevice({ rooms: ["apple"] })];
    expect(distinctValues(devices, (d) => d.rooms)).toEqual(["apple", "Banana"]);
  });

  it("skips items whose accessor returns undefined or an empty array without crashing", () => {
    const devices = [
      makeDevice({ rooms: undefined }),
      makeDevice({ rooms: [] }),
      makeDevice({ rooms: ["Kitchen"] }),
    ];
    expect(distinctValues(devices, (d) => d.rooms)).toEqual(["Kitchen"]);
  });

  it("returns an empty array for an empty items list", () => {
    expect(distinctValues([], (d: DeviceSummary) => d.rooms)).toEqual([]);
  });

  it("is shape-agnostic: works over any array-valued facet, not just DeviceSummary", () => {
    const tagged = [{ tags: ["b", "a"] }, { tags: ["c"] }, { tags: [] as string[] }];
    expect(distinctValues(tagged, (t) => t.tags)).toEqual(["a", "b", "c"]);
  });
});

describe("sortPickerRows", () => {
  type Row = { id: string; name: string; room: string; model: string };
  const keyOf = (row: Row): PickerSortKey => ({
    name: row.name,
    room: row.room,
    model: row.model,
  });

  const rowA: Row = { id: "A", name: "Zebra", room: "Attic", model: "A-Model" };
  const rowB: Row = { id: "B", name: "Apple", room: "Basement", model: "Z-Model" };
  const rowC: Row = { id: "C", name: "Mango", room: "Cellar", model: "M-Model" };

  it("sorts ascending by name", () => {
    const result = sortPickerRows([rowA, rowB, rowC], "name", keyOf);
    expect(result.map((r) => r.id)).toEqual(["B", "C", "A"]);
  });

  it("sorts ascending by room", () => {
    const result = sortPickerRows([rowA, rowB, rowC], "room", keyOf);
    expect(result.map((r) => r.id)).toEqual(["A", "B", "C"]);
  });

  it("sorts ascending by model", () => {
    const result = sortPickerRows([rowA, rowB, rowC], "model", keyOf);
    expect(result.map((r) => r.id)).toEqual(["A", "C", "B"]);
  });

  it("sorts case-insensitively", () => {
    const lower: Row = { id: "lower", name: "banana", room: "", model: "" };
    const upper: Row = { id: "upper", name: "Apple", room: "", model: "" };
    const result = sortPickerRows([lower, upper], "name", keyOf);
    expect(result.map((r) => r.id)).toEqual(["upper", "lower"]);
  });

  it("sorts numerically rather than lexicographically", () => {
    const ten: Row = { id: "ten", name: "Item 10", room: "", model: "" };
    const two: Row = { id: "two", name: "Item 2", room: "", model: "" };
    const result = sortPickerRows([ten, two], "name", keyOf);
    expect(result.map((r) => r.id)).toEqual(["two", "ten"]);
  });

  it("does not mutate the input array", () => {
    const rows = [rowA, rowB, rowC];
    const snapshot = rows.slice();
    sortPickerRows(rows, "name", keyOf);
    expect(rows).toEqual(snapshot);
    expect(rows[0]).toBe(rowA);
    expect(rows[1]).toBe(rowB);
    expect(rows[2]).toBe(rowC);
  });
});
