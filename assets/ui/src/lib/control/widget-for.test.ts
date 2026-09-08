import { describe, it, expect } from "vitest";
import { widgetFor, type WidgetKind } from "./resolver";

// The cases below are the slot-semantics table of
// notes/reference/control-inventory.md, one row per CONTROL family the
// editor renders differently from its own heuristics — plus the two
// same-suffix disambiguations the inventory calls out, which are exactly
// where a naive "*.LEVEL is a slider" rule goes wrong.
describe("widgetFor", () => {
  const cases: {
    name: string;
    param: Parameters<typeof widgetFor>[0];
    want: WidgetKind;
  }[] = [
    // ACTION wins over everything: there is no value to show.
    {
      name: "an ACTION parameter is a trigger even with no CONTROL",
      param: { type: "ACTION" },
      want: "trigger",
    },
    {
      name: "BUTTON.SHORT typed ACTION is a trigger",
      param: { type: "ACTION", control: "BUTTON.SHORT" },
      want: "trigger",
    },

    // STATE — a toggle only where it is writable and BOOL.
    {
      name: "SWITCH.STATE is a switch",
      param: { type: "BOOL", control: "SWITCH.STATE" },
      want: "switch",
    },
    {
      name: "SIMPLE_SWITCH_RECEIVER.STATE is a switch",
      param: { type: "BOOL", control: "SIMPLE_SWITCH_RECEIVER.STATE" },
      want: "switch",
    },
    {
      name: "DOOR_SENSOR.STATE is read-only, so not a switch",
      param: { type: "BOOL", control: "DOOR_SENSOR.STATE", operations: { write: false } },
      want: "auto",
    },
    {
      name: "DANGER.STATE typed ENUM is not a switch",
      param: { type: "ENUM", control: "DANGER.STATE" },
      want: "auto",
    },

    // LEVEL — a position, except where the CCU gives it choices.
    {
      name: "DIMMER.LEVEL is a level",
      param: { type: "FLOAT", control: "DIMMER.LEVEL" },
      want: "level",
    },
    {
      name: "BLIND.LEVEL is a level",
      param: { type: "FLOAT", control: "BLIND.LEVEL" },
      want: "level",
    },
    {
      name: "JALOUSIE.LEVEL_SLATS is a level",
      param: { type: "FLOAT", control: "JALOUSIE.LEVEL_SLATS" },
      want: "level",
    },
    {
      name: "DIMMER.LEVEL_REAL is a level",
      param: { type: "FLOAT", control: "DIMMER.LEVEL_REAL" },
      want: "level",
    },
    {
      // The inventory's own disambiguation row: WIN_SC.LEVEL is an
      // enumerated handle position, not a 0-100 slider.
      name: "WIN_SC.LEVEL with a value list is a selector, not a level",
      param: {
        type: "ENUM",
        control: "WIN_SC.LEVEL",
        value_list: [{ value: 0 }, { value: 1 }, { value: 2 }],
      },
      want: "auto",
    },
    {
      name: "a read-only LEVEL is not a level widget",
      param: { type: "FLOAT", control: "SHUTTER_TRANSMITTER.LEVEL", operations: { write: false } },
      want: "auto",
    },

    // Everything the table does not cover falls through to the heuristics.
    {
      name: "HEATING_CONTROL_HMIP.SETPOINT falls through to auto",
      param: { type: "FLOAT", control: "HEATING_CONTROL_HMIP.SETPOINT" },
      want: "auto",
    },
    {
      name: "POWERMETER.ENERGY_COUNTER falls through to auto",
      param: { type: "FLOAT", control: "POWERMETER.ENERGY_COUNTER" },
      want: "auto",
    },
    {
      name: "an unknown family falls through to auto",
      param: { type: "INTEGER", control: "SOMETHING_NEW.WHATEVER" },
      want: "auto",
    },
    {
      name: "no CONTROL falls through to auto",
      param: { type: "INTEGER" },
      want: "auto",
    },
    {
      name: "a malformed CONTROL falls through to auto",
      param: { type: "INTEGER", control: "NONE" },
      want: "auto",
    },
    {
      name: "an empty CONTROL falls through to auto",
      param: { type: "INTEGER", control: "" },
      want: "auto",
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(widgetFor(c.param)).toBe(c.want);
    });
  }
});
