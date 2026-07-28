import { describe, it, expect } from "vitest";
import type { DeviceSummary } from "$lib/api/types";
import {
  isSecurityDevice,
  guessSensorType,
  guessSensorParameter,
  guessSensorBinding,
  buildCandidates,
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
});
