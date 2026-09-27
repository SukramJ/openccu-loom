// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, waitFor, cleanup } from "@testing-library/svelte";

const create = vi.fn();
const update = vi.fn();
const del = vi.fn();
const ask = vi.fn();
const success = vi.fn();
const failure = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    createTaxonomyNode: (...a: unknown[]) => create(...a),
    updateTaxonomyNode: (...a: unknown[]) => update(...a),
    deleteTaxonomyNode: (...a: unknown[]) => del(...a),
  },
  ApiError: class ApiError extends Error {},
}));
vi.mock("$lib/stores/centrals.svelte", () => ({ centralStore: { byName: () => undefined } }));
vi.mock("$lib/stores/confirm.svelte", () => ({ confirmStore: { ask: (...a: unknown[]) => ask(...a) } }));
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: (...a: unknown[]) => success(...a), error: (...a: unknown[]) => failure(...a) },
}));
vi.mock("$lib/stores/preferences.svelte", () => ({ prefs: { locale: "en" } }));
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => (vars ? `${key} ${Object.values(vars).join(" ")}` : key),
}));

import TaxonomyTreeEditor from "./TaxonomyTreeEditor.svelte";

function central(writable: boolean, tree: boolean) {
  return {
    central: "box",
    revision: 3,
    writable,
    tree,
    enums: [
      {
        id: "room",
        names: { en: "Rooms" },
        nodes: [
          { id: "eg", path: "eg", name: "Erdgeschoss", children: [{ id: "kueche", path: "eg/kueche", name: "Küche" }] },
        ],
      },
    ],
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  create.mockResolvedValue({ path: "eg/bad" });
  update.mockResolvedValue(undefined);
  del.mockResolvedValue(undefined);
  ask.mockResolvedValue(true);
});
afterEach(() => cleanup());

describe("TaxonomyTreeEditor", () => {
  it("renders the tree indented by depth", () => {
    const { container } = render(TaxonomyTreeEditor, { props: { central: central(true, true), onChanged: vi.fn() } });
    const rows = Array.from(container.querySelectorAll("li")).map((li) => [
      li.querySelector("span.text-sm")?.textContent,
      li.getAttribute("style"),
    ]);
    expect(rows).toEqual([
      ["Erdgeschoss", "padding-left: 0rem;"],
      ["Küche", "padding-left: 1.25rem;"],
    ]);
  });

  it("creates a node below another and reloads", async () => {
    const onChanged = vi.fn();
    const { getByLabelText, getByRole } = render(TaxonomyTreeEditor, {
      props: { central: central(true, true), onChanged },
    });
    await fireEvent.click(getByLabelText("taxonomy.add_child_named Erdgeschoss"));
    await fireEvent.input(getByRole("textbox"), { target: { value: "Bad" } });
    await fireEvent.submit(getByRole("textbox").closest("form")!);
    await waitFor(() => expect(create).toHaveBeenCalledWith("box", "room", "Bad", "eg"));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(success).toHaveBeenCalledWith("taxonomy.created");
  });

  it("renames a node", async () => {
    const { getByLabelText, getByRole } = render(TaxonomyTreeEditor, {
      props: { central: central(true, true), onChanged: vi.fn() },
    });
    await fireEvent.click(getByLabelText("taxonomy.rename_named Küche"));
    await fireEvent.input(getByRole("textbox"), { target: { value: "Wohnküche" } });
    await fireEvent.submit(getByRole("textbox").closest("form")!);
    await waitFor(() => expect(update).toHaveBeenCalledWith("box", "room", "eg/kueche", { name: "Wohnküche" }));
  });

  it("deletes a node only after the confirmation", async () => {
    ask.mockResolvedValueOnce(false);
    const { getByLabelText } = render(TaxonomyTreeEditor, { props: { central: central(true, true), onChanged: vi.fn() } });
    await fireEvent.click(getByLabelText("taxonomy.delete_named Küche"));
    await waitFor(() => expect(ask).toHaveBeenCalledTimes(1));
    expect(del).not.toHaveBeenCalled();
    await fireEvent.click(getByLabelText("taxonomy.delete_named Küche"));
    await waitFor(() => expect(del).toHaveBeenCalledWith("box", "room", "eg/kueche"));
    expect(ask.mock.calls[0][0]).toMatchObject({ destructive: true });
  });

  it("offers no nesting or moving on a flat taxonomy, and nothing when read-only", () => {
    const flat = render(TaxonomyTreeEditor, { props: { central: central(true, false), onChanged: vi.fn() } });
    expect(flat.queryByLabelText("taxonomy.add_child_named Erdgeschoss")).toBeNull();
    expect(flat.queryByLabelText("taxonomy.move_named Erdgeschoss")).toBeNull();
    expect(flat.queryByLabelText("taxonomy.rename_named Erdgeschoss")).toBeTruthy();
    cleanup();

    const ro = render(TaxonomyTreeEditor, { props: { central: central(false, true), onChanged: vi.fn() } });
    expect(ro.queryByLabelText("taxonomy.rename_named Erdgeschoss")).toBeNull();
    expect(ro.queryByText("taxonomy.add_root")).toBeNull();
    expect(ro.getByText("taxonomy.readonly")).toBeTruthy();
  });

  it("surfaces a refused edit", async () => {
    create.mockRejectedValueOnce(new Error("name taken"));
    const { getByText, getByRole } = render(TaxonomyTreeEditor, {
      props: { central: central(true, true), onChanged: vi.fn() },
    });
    await fireEvent.click(getByText("taxonomy.add_root"));
    await fireEvent.input(getByRole("textbox"), { target: { value: "Keller" } });
    await fireEvent.submit(getByRole("textbox").closest("form")!);
    await waitFor(() => expect(failure).toHaveBeenCalledWith("name taken"));
  });
});
