// @vitest-environment happy-dom
import { describe, it, expect, vi } from "vitest";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import { ROLES, roleBadgeVariant, roleLabel, roleOptions } from "./roles";

describe("roles — vocabulary", () => {
  it("offers exactly viewer, operator and admin, least-privileged first", () => {
    expect([...ROLES]).toEqual(["viewer", "operator", "admin"]);
  });

  it("renders every role as a translated option", () => {
    expect(roleOptions()).toEqual([
      { value: "viewer", label: "role.viewer" },
      { value: "operator", label: "role.operator" },
      { value: "admin", label: "role.admin" },
    ]);
  });
});

describe("roles — labels", () => {
  it("translates every known role", () => {
    expect(roleLabel("viewer")).toBe("role.viewer");
    expect(roleLabel("operator")).toBe("role.operator");
    expect(roleLabel("admin")).toBe("role.admin");
  });

  // The wire type is a plain string. A role this build does not know must
  // read as itself, not as a bare `role.<x>` translation key.
  it("falls back to the raw value for an unknown role", () => {
    expect(roleLabel("maintainer")).toBe("maintainer");
    expect(roleLabel("")).toBe("");
  });
});

describe("roles — badge colour", () => {
  it("escalates admin to danger and operator to warning", () => {
    expect(roleBadgeVariant("admin")).toBe("danger");
    expect(roleBadgeVariant("operator")).toBe("warning");
  });

  it("keeps viewer and unknown roles neutral", () => {
    expect(roleBadgeVariant("viewer")).toBe("muted");
    expect(roleBadgeVariant("maintainer")).toBe("muted");
  });
});
