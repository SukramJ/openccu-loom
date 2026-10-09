// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/svelte";

import OverflowMenu from "./OverflowMenu.svelte";

afterEach(() => cleanup());

function mount(onExport = vi.fn(), disabled = false) {
  render(OverflowMenu, {
    props: {
      ariaLabel: "More",
      items: [
        { label: "Export", onSelect: onExport },
        { label: "Import", onSelect: vi.fn(), disabled },
      ],
    },
  });
  return onExport;
}

describe("OverflowMenu", () => {
  it("stays closed until the button is pressed", async () => {
    mount();
    expect(screen.queryByRole("menu")).toBeNull();
    const button = screen.getByRole("button", { name: "More" });
    expect(button.getAttribute("aria-expanded")).toBe("false");
    await fireEvent.click(button);
    expect(screen.getByRole("menu")).toBeTruthy();
    expect(button.getAttribute("aria-expanded")).toBe("true");
  });

  it("runs the chosen item and closes", async () => {
    const onExport = mount();
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Export" }));
    expect(onExport).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("closes on Escape and on a click elsewhere", async () => {
    mount();
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("disables an item that cannot run", async () => {
    mount(vi.fn(), true);
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    expect((screen.getByRole("menuitem", { name: "Import" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
