// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import {
  render,
  cleanup,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params
      ? Object.entries(params).reduce(
          (s, [k, v]) => s.replace(`{${k}}`, String(v)),
          key,
        )
      : key,
}));

import RoomFunctionSelect from "./RoomFunctionSelect.svelte";

afterEach(() => cleanup());

const baseProps = {
  options: ["Wohnzimmer", "Küche", "Bad"],
  placeholder: "search…",
  createLabel: (v: string) => `create ${v}`,
  removeLabel: (n: string) => `remove ${n}`,
};

describe("RoomFunctionSelect", () => {
  it("renders the selected entries as chips", () => {
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: ["Wohnzimmer", "Bad"], onChange: vi.fn() },
    });
    expect(screen.getByText("Wohnzimmer")).toBeInTheDocument();
    expect(screen.getByText("Bad")).toBeInTheDocument();
  });

  it("adds a filtered catalogue option on click", async () => {
    const onChange = vi.fn();
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: ["Wohnzimmer"], onChange },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Küche" } });
    await fireEvent.click(await screen.findByRole("option", { name: "Küche" }));
    expect(onChange).toHaveBeenCalledWith(["Wohnzimmer", "Küche"]);
  });

  it("does not offer an already-selected entry as an option", async () => {
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: ["Küche"], onChange: vi.fn() },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Küche" } });
    // "Küche" is selected → not repeated as an option; a create is offered
    // instead is NOT the case here (exact match exists), so no_matches shows.
    await waitFor(() =>
      expect(screen.queryByRole("option", { name: "Küche" })).toBeNull(),
    );
  });

  it("removes a chip via its ✕ button", async () => {
    const onChange = vi.fn();
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: ["Wohnzimmer", "Bad"], onChange },
    });
    await fireEvent.click(
      screen.getByRole("button", { name: "remove Wohnzimmer" }),
    );
    expect(onChange).toHaveBeenCalledWith(["Bad"]);
  });

  it("offers to create a novel value, then assigns it", async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: [], onChange, onCreate },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Flur" } });
    await fireEvent.click(await screen.findByText("create Flur"));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith("Flur"));
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(["Flur"]));
  });

  it("shows no-matches (and no create) when the value is unknown and no creator is wired", async () => {
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: [], onChange: vi.fn() },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Zzz" } });
    expect(await screen.findByText("roomfn.no_matches")).toBeInTheDocument();
    expect(screen.queryByText(/^create /)).toBeNull();
  });

  it("adds the first filtered option on Enter", async () => {
    const onChange = vi.fn();
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: [], onChange },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Ba" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith(["Bad"]);
  });

  it("does not open the dropdown when disabled", async () => {
    render(RoomFunctionSelect, {
      props: { ...baseProps, selected: [], onChange: vi.fn(), disabled: true },
    });
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await fireEvent.focus(input);
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});
