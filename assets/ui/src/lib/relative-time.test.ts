import { describe, it, expect } from "vitest";
import {
  formatAbsoluteTime,
  formatRelativeTime,
  isAbsentTimestamp,
  timestampSortKey,
} from "./relative-time";

const now = new Date("2030-05-01T12:00:00Z");

describe("isAbsentTimestamp", () => {
  it("treats undefined, empty, Go zero time and garbage as absent", () => {
    expect(isAbsentTimestamp(undefined)).toBe(true);
    expect(isAbsentTimestamp("")).toBe(true);
    expect(isAbsentTimestamp("0001-01-01T00:00:00Z")).toBe(true);
    expect(isAbsentTimestamp("not a date")).toBe(true);
    expect(isAbsentTimestamp("2030-05-01T11:59:00Z")).toBe(false);
  });
});

describe("formatRelativeTime", () => {
  it("renders null for an absent timestamp so the cell shows a dash", () => {
    expect(formatRelativeTime("0001-01-01T00:00:00Z", now, "en")).toBeNull();
    expect(formatRelativeTime(undefined, now, "de")).toBeNull();
  });

  it("picks the unit from the distance, in the given locale", () => {
    expect(formatRelativeTime("2030-05-01T11:59:30Z", now, "en")).toBe("30 seconds ago");
    expect(formatRelativeTime("2030-05-01T11:57:00Z", now, "en")).toBe("3 minutes ago");
    expect(formatRelativeTime("2030-05-01T11:57:00Z", now, "de")).toBe("vor 3 Minuten");
    expect(formatRelativeTime("2030-05-01T09:00:00Z", now, "de")).toBe("vor 3 Stunden");
    expect(formatRelativeTime("2030-04-29T12:00:00Z", now, "en")).toBe("2 days ago");
  });

  it("clamps a timestamp slightly in the future to now (clock skew)", () => {
    expect(formatRelativeTime("2030-05-01T12:00:02Z", now, "en")).toBe("now");
  });
});

describe("timestampSortKey", () => {
  it("sorts absent below every observed timestamp", () => {
    expect(timestampSortKey("0001-01-01T00:00:00Z")).toBe(0);
    expect(timestampSortKey("2030-05-01T11:59:00Z")).toBeGreaterThan(0);
  });
});

describe("formatAbsoluteTime", () => {
  it("is empty for absent and non-empty for a real timestamp", () => {
    expect(formatAbsoluteTime("0001-01-01T00:00:00Z", "de-DE")).toBe("");
    expect(formatAbsoluteTime("2030-05-01T11:59:00Z", "de-DE")).not.toBe("");
  });
});
