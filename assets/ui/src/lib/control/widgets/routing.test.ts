import { describe, it, expect } from "vitest";
import type { DataPointSummary } from "$lib/api/types";
import type { ResolvedChannel } from "../resolver";
import { widgetFor, widgetForResolved } from "./index";
import Dimmer from "./Dimmer.svelte";
import FixedColorLight from "./FixedColorLight.svelte";
import ColorTempLight from "./ColorTempLight.svelte";
import UniversalLight from "./UniversalLight.svelte";
import Garage from "./Garage.svelte";
import SimpleRfThermostat from "./SimpleRfThermostat.svelte";

function dp(parameter: string, control?: string): DataPointSummary {
  return {
    parameter,
    value: 0,
    observed: true,
    operations: { read: true, write: true, event: true },
    control,
    unique_id: `dp-${parameter}`,
  };
}

function channel(
  family: ResolvedChannel["family"],
  slots: Record<string, DataPointSummary>,
  siblings: ResolvedChannel["siblings"] = {},
): ResolvedChannel {
  return { family, slots, siblings };
}

describe("widgetForResolved — DIMMER family slot upgrades", () => {
  it("plain DIMMER with only LEVEL resolves to the Dimmer widget", () => {
    const ch = channel("DIMMER", { LEVEL: dp("LEVEL", "DIMMER.LEVEL") });
    expect(widgetForResolved(ch)).toBe(Dimmer);
  });

  it("DIMMER with a COLOR slot upgrades to FixedColorLight (HmIP-BSL)", () => {
    const ch = channel("DIMMER", {
      LEVEL: dp("LEVEL", "DIMMER.LEVEL"),
      COLOR: dp("COLOR", "DIMMER.COLOR"),
    });
    expect(widgetForResolved(ch)).toBe(FixedColorLight);
  });

  it("DIMMER with a COLOR_TEMPERATURE slot upgrades to ColorTempLight", () => {
    const ch = channel("DIMMER", {
      LEVEL: dp("LEVEL", "DIMMER.LEVEL"),
      COLOR_TEMPERATURE: dp("COLOR_TEMPERATURE", "DIMMER.COLOR_TEMPERATURE"),
    });
    expect(widgetForResolved(ch)).toBe(ColorTempLight);
  });

  it("DIMMER_REAL with COLOR also upgrades", () => {
    const ch = channel("DIMMER_REAL", {
      LEVEL: dp("LEVEL", "DIMMER_REAL.LEVEL"),
      COLOR: dp("COLOR", "DIMMER_REAL.COLOR"),
    });
    expect(widgetForResolved(ch)).toBe(FixedColorLight);
  });

  it("DIMMER_REAL with COLOR_TEMPERATURE also upgrades", () => {
    const ch = channel("DIMMER_REAL", {
      LEVEL: dp("LEVEL", "DIMMER_REAL.LEVEL"),
      COLOR_TEMPERATURE: dp("COLOR_TEMPERATURE", "DIMMER_REAL.COLOR_TEMPERATURE"),
    });
    expect(widgetForResolved(ch)).toBe(ColorTempLight);
  });
});

describe("widgetForResolved — UNIVERSAL_LIGHT_RECEIVER", () => {
  it("routes UNIVERSAL_LIGHT_RECEIVER to UniversalLight (HmIP-RGBW)", () => {
    const ch = channel("UNIVERSAL_LIGHT_RECEIVER", {
      LEVEL: dp("LEVEL", "UNIVERSAL_LIGHT_RECEIVER.LEVEL"),
      HUE: dp("HUE", "UNIVERSAL_LIGHT_RECEIVER.HUE"),
      SATURATION: dp("SATURATION", "UNIVERSAL_LIGHT_RECEIVER.SATURATION"),
      COLOR_TEMPERATURE: dp("COLOR_TEMPERATURE", "UNIVERSAL_LIGHT_RECEIVER.COLOR_TEMPERATURE"),
      EFFECT: dp("EFFECT", "UNIVERSAL_LIGHT_RECEIVER.EFFECT"),
    });
    expect(widgetForResolved(ch)).toBe(UniversalLight);
  });

  it("routes plain LEVEL-only UNIVERSAL_LIGHT_RECEIVER to UniversalLight (HmIP-DRG-DALI minimal)", () => {
    const ch = channel("UNIVERSAL_LIGHT_RECEIVER", {
      LEVEL: dp("LEVEL", "UNIVERSAL_LIGHT_RECEIVER.LEVEL"),
    });
    expect(widgetForResolved(ch)).toBe(UniversalLight);
  });
});

describe("widgetForResolved — multi-family SimpleRfThermostat", () => {
  it("routes SWITCH+TEMP sibling pair to SimpleRfThermostat (HM-CC-TC)", () => {
    const ch = channel(
      "SWITCH",
      { STATE: dp("STATE", "SWITCH.STATE") },
      { TEMP: { SETPOINT: dp("SETPOINT", "TEMP.SETPOINT") } },
    );
    expect(widgetForResolved(ch)).toBe(SimpleRfThermostat);
  });

  it("routes TEMP+SWITCH sibling pair (reverse dominance) to SimpleRfThermostat", () => {
    const ch = channel(
      "TEMP",
      { SETPOINT: dp("SETPOINT", "TEMP.SETPOINT") },
      { SWITCH: { STATE: dp("STATE", "SWITCH.STATE") } },
    );
    expect(widgetForResolved(ch)).toBe(SimpleRfThermostat);
  });

  it("plain SWITCH without TEMP sibling stays a regular Switch", () => {
    const ch = channel("SWITCH", { STATE: dp("STATE", "SWITCH.STATE") });
    expect(widgetForResolved(ch)).not.toBe(SimpleRfThermostat);
  });
});

describe("widgetForResolved — OPTICAL_SIGNAL_RECEIVER (HmIP-WRC6-230 LED)", () => {
  it("routes OPTICAL_SIGNAL_RECEIVER to FixedColorLight, not Siren", () => {
    const ch = channel("OPTICAL_SIGNAL_RECEIVER", {
      LEVEL: dp("LEVEL", "OPTICAL_SIGNAL_RECEIVER.LEVEL"),
      COLOR: dp("COLOR", "OPTICAL_SIGNAL_RECEIVER.COLOR"),
      COLOR_BEHAVIOUR: dp("COLOR_BEHAVIOUR", "OPTICAL_SIGNAL_RECEIVER.COLOR_BEHAVIOUR"),
    });
    expect(widgetForResolved(ch)).toBe(FixedColorLight);
  });
});

describe("widgetForResolved — DOOR_RECEIVER", () => {
  it("routes DOOR_RECEIVER (HmIP-MOD-HO) to Garage", () => {
    const ch = channel("DOOR_RECEIVER", {
      DOOR_COMMAND: dp("DOOR_COMMAND", "DOOR_RECEIVER.DOOR_COMMAND"),
      DOOR_STATE: dp("DOOR_STATE", "DOOR_RECEIVER.DOOR_STATE"),
    });
    expect(widgetForResolved(ch)).toBe(Garage);
  });
});

describe("widgetForResolved — non-DIMMER families pass through", () => {
  it("SWITCH falls through to the family registry", () => {
    const ch = channel("SWITCH", { STATE: dp("STATE", "SWITCH.STATE") });
    expect(widgetForResolved(ch)).toBe(widgetFor("SWITCH"));
  });

  it("HEATING_CONTROL_HMIP falls through unchanged", () => {
    const ch = channel("HEATING_CONTROL_HMIP", {
      SETPOINT: dp("SETPOINT", "HEATING_CONTROL_HMIP.SETPOINT"),
    });
    expect(widgetForResolved(ch)).toBe(widgetFor("HEATING_CONTROL_HMIP"));
  });

  it("HEATING_CONTROL (RF) shares the Climate widget with HmIP", () => {
    const ch = channel("HEATING_CONTROL", {
      SETPOINT: dp("SETPOINT", "HEATING_CONTROL.SETPOINT"),
      AUTO: dp("AUTO_MODE", "HEATING_CONTROL.AUTO"),
    });
    expect(widgetForResolved(ch)).toBe(widgetFor("HEATING_CONTROL_HMIP"));
  });
});
