import { describe, expect, it, vi, beforeEach } from "vitest";
import type { SurfacesResponse } from "$lib/api/surface-types";

const getUISurfaces = vi.fn();
const putUISurfaces = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getUISurfaces: (...args: unknown[]) => getUISurfaces(...args),
    putUISurfaces: (...args: unknown[]) => putUISurfaces(...args),
  },
}));

const { surfacesStore } = await import("./surfaces.svelte");

/** A small registry that covers every row state the editor renders. */
function response(over: Partial<SurfacesResponse> = {}): SurfacesResponse {
  return {
    embedded: false,
    profile: "standalone",
    profiles: {},
    centrals: 1,
    effective: {
      "nav.devices": true,
      "nav.alarm": true,
      "nav.matter": false,
      "device.configure": true,
      "device.configure.links": true,
    },
    surfaces: [
      {
        id: "nav.devices",
        group: "overview",
        defaults: { standalone: true, embedded: true },
        floor: "always",
      },
      {
        id: "nav.alarm",
        group: "overview",
        defaults: { standalone: true, embedded: true },
        warn: "alarm_armed",
      },
      {
        id: "nav.matter",
        group: "bridges",
        defaults: { standalone: true, embedded: false },
        gate: "matter",
        write_gated: true,
        ha_owns: true,
      },
      {
        id: "settings.users",
        group: "settings",
        defaults: { standalone: true, embedded: false },
        floor: "standalone",
        write_gated: true,
      },
      {
        id: "device.configure",
        group: "device",
        defaults: { standalone: true, embedded: false },
        write_gated: true,
      },
      {
        id: "device.configure.links",
        group: "device",
        defaults: { standalone: true, embedded: false },
        parent: "device.configure",
        write_gated: true,
      },
    ],
    ...over,
  };
}

beforeEach(async () => {
  getUISurfaces.mockReset();
  putUISurfaces.mockReset();
  getUISurfaces.mockResolvedValue(response());
  await surfacesStore.load();
  surfacesStore.discard();
  surfacesStore.setEditing("standalone");
});

describe("surfacesStore.visible", () => {
  it("answers from the daemon's resolved map", () => {
    expect(surfacesStore.visible("nav.devices")).toBe(true);
    expect(surfacesStore.visible("nav.matter")).toBe(false);
  });

  it("treats an unknown id as visible", () => {
    // A view this build knows but the daemon does not (a downgrade, or a
    // newer SPA against an older daemon) must render, not vanish.
    expect(surfacesStore.visible("nav.from_the_future")).toBe(true);
  });
});

describe("surfacesStore editing", () => {
  it("stores only deviations from the shipped default", async () => {
    putUISurfaces.mockResolvedValue(response());
    surfacesStore.toggle("nav.alarm"); // visible → hidden, a real deviation
    surfacesStore.toggle("nav.alarm"); // back to the default
    expect(surfacesStore.deviationCount()).toBe(0);
    expect(surfacesStore.hasChanges()).toBe(false);
  });

  it("refuses to hide a floor surface", () => {
    surfacesStore.toggle("nav.devices");
    expect(surfacesStore.draftVisible("nav.devices")).toBe(true);
    expect(surfacesStore.isChanged("nav.devices")).toBe(false);
  });

  it("scopes the standalone floor to the standalone profile", () => {
    expect(surfacesStore.isFloor("settings.users", "standalone")).toBe(true);
    expect(surfacesStore.isFloor("settings.users", "embedded")).toBe(false);
  });

  it("keeps a child no more visible than its parent", () => {
    surfacesStore.set("device.configure", false);
    expect(surfacesStore.draftVisible("device.configure.links")).toBe(false);
  });

  it("reports the shipped default per profile", () => {
    expect(surfacesStore.defaultOf("device.configure", "standalone")).toBe(true);
    expect(surfacesStore.defaultOf("device.configure", "embedded")).toBe(false);
  });

  it("resets one row and the whole profile", () => {
    surfacesStore.toggle("nav.alarm");
    expect(surfacesStore.deviationCount()).toBe(1);
    surfacesStore.resetSurface("nav.alarm");
    expect(surfacesStore.deviationCount()).toBe(0);

    surfacesStore.toggle("nav.alarm");
    surfacesStore.resetProfile();
    expect(surfacesStore.deviationCount()).toBe(0);
  });

  it("discards back to the saved state", () => {
    surfacesStore.toggle("nav.alarm");
    expect(surfacesStore.hasChanges()).toBe(true);
    surfacesStore.discard();
    expect(surfacesStore.hasChanges()).toBe(false);
    expect(surfacesStore.draftVisible("nav.alarm")).toBe(true);
  });

  it("edits the inactive profile without touching the live one", () => {
    surfacesStore.setEditing("embedded");
    surfacesStore.set("nav.matter", true);
    expect(surfacesStore.draftVisible("nav.matter", "embedded")).toBe(true);
    // The live profile is standalone; its resolution is untouched.
    expect(surfacesStore.visible("nav.matter")).toBe(false);
  });

  it("sends the working copy on save and adopts the daemon's answer", async () => {
    putUISurfaces.mockResolvedValue(
      response({ profiles: { standalone: { "nav.alarm": "hidden" } } }),
    );
    surfacesStore.toggle("nav.alarm");
    await surfacesStore.save();
    expect(putUISurfaces).toHaveBeenCalledWith({
      profiles: expect.objectContaining({
        standalone: { "nav.alarm": "hidden" },
      }),
    });
    // Adopting the response is what makes the sparse normalisation the
    // daemon applies visible to the operator immediately.
    expect(surfacesStore.hasChanges()).toBe(false);
  });

  it("saves the master toggle separately from the row edits", async () => {
    putUISurfaces.mockResolvedValue(response({ embedded: true, profile: "embedded" }));
    await surfacesStore.setEmbedded(true);
    expect(putUISurfaces).toHaveBeenCalledWith({ embedded: true });
    expect(surfacesStore.embedded).toBe(true);
    expect(surfacesStore.profile).toBe("embedded");
  });
});

describe("surfacesStore.load failure", () => {
  it("keeps every surface visible so the navigation never blanks", async () => {
    getUISurfaces.mockRejectedValue(new Error("boom"));
    // A fresh module instance, so `loaded` is false as on a cold start.
    vi.resetModules();
    const fresh = (await import("./surfaces.svelte")).surfacesStore;
    await fresh.load();
    expect(fresh.error).toContain("boom");
    expect(fresh.visible("nav.devices")).toBe(true);
    expect(fresh.visible("nav.matter")).toBe(true);
  });
});
