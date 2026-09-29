// @vitest-environment happy-dom
//
// The device picture loads from the icon route through apiBase(), so the
// Home Assistant Ingress prefix is kept, and swaps to the device-type
// glyph when the image fails — the route answers 404 for a device it has
// no picture for.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => `${key}:${vars?.model ?? ""}`,
}));

vi.mock("$lib/api/base", () => ({
  apiBase: () => "/api/hassio_ingress/tok/api/v1",
}));

import DeviceImage from "./DeviceImage.svelte";

afterEach(() => cleanup());

describe("DeviceImage", () => {
  it("requests the icon route under the Ingress-aware API base", () => {
    const { container } = render(DeviceImage, {
      props: { address: "0001D3C99B4E2F", model: "HmIP-WTH-2" },
    });
    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    expect(img!.getAttribute("src")).toBe(
      "/api/hassio_ingress/tok/api/v1/devices/0001D3C99B4E2F/icon",
    );
    expect(img!.getAttribute("alt")).toBe("device.image_alt:HmIP-WTH-2");
  });

  it("falls back to the glyph when the image fails to load", async () => {
    const { container } = render(DeviceImage, {
      props: { address: "0001D3C99B4E2F", model: "HmIP-WTH-2" },
    });
    await fireEvent.error(container.querySelector("img")!);
    const tile = container.querySelector('[data-testid="device-image"]')!;
    expect(tile.getAttribute("data-state")).toBe("fallback");
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("retries the picture for another device after a fallback", async () => {
    const { container, rerender } = render(DeviceImage, {
      props: { address: "AAA", model: "HmIP-WTH-2" },
    });
    await fireEvent.error(container.querySelector("img")!);
    expect(container.querySelector("img")).toBeNull();
    await rerender({ address: "BBB", model: "HmIP-PSM" });
    expect(container.querySelector("img")?.getAttribute("src")).toBe(
      "/api/hassio_ingress/tok/api/v1/devices/BBB/icon",
    );
  });
});
