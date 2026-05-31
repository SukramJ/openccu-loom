import { describe, it, expect } from "vitest";
import { resolveTileColor } from "./state-color";

const NEUTRAL = "var(--ha-secondary-text-color)";
const DISABLED =
  "var(--ha-disabled-text-color, var(--ha-secondary-text-color))";

describe("resolveTileColor — unobserved", () => {
  it("returns the disabled token regardless of family or value", () => {
    expect(resolveTileColor("SWITCH", true, false)).toBe(DISABLED);
    expect(resolveTileColor("DIMMER", 0.5, false)).toBe(DISABLED);
    expect(resolveTileColor("HEATING_CONTROL_HMIP", 1, false)).toBe(DISABLED);
  });
});

describe("resolveTileColor — SWITCH / DOOROPENER", () => {
  it("returns the switch-active token when truthy", () => {
    expect(resolveTileColor("SWITCH", true, true)).toContain(
      "--state-switch-active-color",
    );
    expect(resolveTileColor("DOOROPENER", 1, true)).toContain(
      "--state-switch-active-color",
    );
  });

  it("returns the neutral token when falsy", () => {
    expect(resolveTileColor("SWITCH", false, true)).toBe(NEUTRAL);
    expect(resolveTileColor("DOOROPENER", 0, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — DIMMER family", () => {
  it("uses the light-active token when LEVEL > 0", () => {
    for (const fam of [
      "DIMMER",
      "DIMMER_REAL",
      "DUAL_WHITE_BRIGHTNESS",
      "RGBW_AUTOMATIC",
      "RGBW_COLOR",
      "RGB_COLOR",
      "UNIVERSAL_LIGHT_RECEIVER",
    ] as const) {
      expect(resolveTileColor(fam, 0.42, true)).toContain(
        "--state-light-active-color",
      );
    }
  });

  it("falls back to neutral when LEVEL == 0", () => {
    expect(resolveTileColor("DIMMER", 0, true)).toBe(NEUTRAL);
  });

  it("coerces stringified numbers", () => {
    expect(resolveTileColor("DIMMER", "0.5", true)).toContain(
      "--state-light-active-color",
    );
    expect(resolveTileColor("DIMMER", "0", true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — BLIND/JALOUSIE/SHUTTER family", () => {
  it("returns cover-active only while partially open (0 < LEVEL < 1)", () => {
    for (const fam of [
      "BLIND",
      "JALOUSIE",
      "SHUTTER_TRANSMITTER",
      "WINDOW",
    ] as const) {
      expect(resolveTileColor(fam, 0.5, true)).toContain(
        "--state-cover-active-color",
      );
    }
  });

  it("returns neutral when fully open or fully closed", () => {
    expect(resolveTileColor("BLIND", 0, true)).toBe(NEUTRAL);
    expect(resolveTileColor("BLIND", 1, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — HEATING_CONTROL family", () => {
  it("returns the climate-auto token without slot-context resolution", () => {
    expect(resolveTileColor("HEATING_CONTROL", 0, true)).toContain(
      "--state-climate-auto-color",
    );
    expect(resolveTileColor("HEATING_CONTROL_HMIP", 1, true)).toContain(
      "--state-climate-auto-color",
    );
  });
});

describe("resolveTileColor — LOCK", () => {
  it("uses the lock-active token when STATE is truthy (locked)", () => {
    expect(resolveTileColor("LOCK", true, true)).toContain(
      "--state-lock-active-color",
    );
  });

  it("returns neutral when unlocked", () => {
    expect(resolveTileColor("LOCK", false, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — DANGER / SMOKE / WATER", () => {
  it("uses the siren-active token on alarm", () => {
    for (const fam of [
      "DANGER",
      "SMOKE_DETECTOR",
      "WATER_DETECTION_TRANSMITTER",
    ] as const) {
      expect(resolveTileColor(fam, true, true)).toContain(
        "--state-siren-active-color",
      );
    }
  });

  it("returns neutral when quiet", () => {
    expect(resolveTileColor("DANGER", 0, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — binary sensors (WIN_SC / DOOR / RHS)", () => {
  it("uses the binary_sensor-door-on token when triggered", () => {
    for (const fam of [
      "WIN_SC",
      "WIN_SC_SENSOR",
      "WIN_SC_SECURE",
      "DOOR_SENSOR",
      "DOOR_STATE_TRANSCEIVER",
      "RHS",
    ] as const) {
      expect(resolveTileColor(fam, 1, true)).toContain(
        "--state-binary_sensor-door-on-color",
      );
    }
  });

  it("returns neutral when closed", () => {
    expect(resolveTileColor("WIN_SC", 0, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — DOOR_RECEIVER (garage)", () => {
  it("uses cover-active token when the door is open or venting", () => {
    expect(resolveTileColor("DOOR_RECEIVER", true, true)).toContain(
      "--state-cover-active-color",
    );
    expect(resolveTileColor("DOOR_RECEIVER", "OPEN", true)).toContain(
      "--state-cover-active-color",
    );
  });

  it("returns neutral when closed", () => {
    expect(resolveTileColor("DOOR_RECEIVER", false, true)).toBe(NEUTRAL);
    expect(resolveTileColor("DOOR_RECEIVER", "", true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — POWERMETER", () => {
  it("uses the fan-active token while drawing load", () => {
    for (const fam of [
      "POWERMETER",
      "POWERMETER_IEC",
      "POWERMETER_IGL",
      "POWERMETER_PSM",
    ] as const) {
      expect(resolveTileColor(fam, 42, true)).toContain(
        "--state-fan-active-color",
      );
    }
  });

  it("returns neutral at zero load", () => {
    expect(resolveTileColor("POWERMETER", 0, true)).toBe(NEUTRAL);
  });

  it("returns neutral for negative reverse-feed reading", () => {
    expect(resolveTileColor("POWERMETER", -3, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — default branch (unknown family)", () => {
  it("falls back to primary-color when truthy", () => {
    expect(resolveTileColor("BATTERIE", true, true)).toContain(
      "--ha-primary-color",
    );
  });

  it("falls back to neutral when falsy", () => {
    expect(resolveTileColor("BATTERIE", false, true)).toBe(NEUTRAL);
  });
});

describe("resolveTileColor — value coercion", () => {
  it("treats boolean true as truthy", () => {
    expect(resolveTileColor("SWITCH", true, true)).toContain(
      "--state-switch-active-color",
    );
  });

  it("treats empty string as falsy", () => {
    expect(resolveTileColor("SWITCH", "", true)).toBe(NEUTRAL);
  });

  it('treats "0" / "false" / nullish as falsy', () => {
    expect(resolveTileColor("SWITCH", "0", true)).toBe(NEUTRAL);
    expect(resolveTileColor("SWITCH", "false", true)).toBe(NEUTRAL);
    expect(resolveTileColor("SWITCH", null, true)).toBe(NEUTRAL);
    expect(resolveTileColor("SWITCH", undefined, true)).toBe(NEUTRAL);
  });

  it("treats non-empty arbitrary strings as truthy", () => {
    expect(resolveTileColor("SWITCH", "on", true)).toContain(
      "--state-switch-active-color",
    );
  });
});
