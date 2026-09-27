// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => (vars ? `${key} ${Object.values(vars).join(" ")}` : key),
}));

import TaxonomyPicker from "./TaxonomyPicker.svelte";

afterEach(() => cleanup());

const rooms = {
  id: "room",
  nodes: [
    { id: "eg", path: "eg", name: "EG", children: [{ id: "kueche", path: "eg/kueche", name: "Küche" }] },
    { id: "og", path: "og", name: "OG", children: [{ id: "kueche", path: "og/kueche", name: "Küche" }] },
  ],
};

describe("TaxonomyPicker", () => {
  it("shows each assignment by its whole path and removes by path", async () => {
    const onChange = vi.fn();
    const { getByText, getByLabelText } = render(TaxonomyPicker, {
      props: { taxonomy: rooms, selected: ["og/kueche"], onChange },
    });
    expect(getByText("OG › Küche")).toBeTruthy();
    await fireEvent.click(getByLabelText("taxonomy.picker.remove OG › Küche"));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("offers nothing to change when disabled", () => {
    const { queryByLabelText, queryByText } = render(TaxonomyPicker, {
      props: { taxonomy: rooms, selected: ["eg/kueche"], onChange: vi.fn(), disabled: true },
    });
    expect(queryByText("EG › Küche")).toBeTruthy();
    expect(queryByLabelText("taxonomy.picker.remove EG › Küche")).toBeNull();
    expect(queryByLabelText("taxonomy.picker.add")).toBeNull();
  });
});
