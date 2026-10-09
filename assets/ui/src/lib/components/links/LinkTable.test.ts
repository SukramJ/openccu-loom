// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/svelte";
import type { Link } from "$lib/api/types";

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import LinkTable from "./LinkTable.svelte";

function link(sender: string, receiver: string, name: string): Link {
  return {
    sender_address: sender,
    receiver_address: receiver,
    name,
    sender_channel_name: `ch ${sender}`,
    receiver_channel_name: `ch ${receiver}`,
    peer_address: receiver,
    direction: "outgoing",
  };
}

const LINKS = [
  link("KEY:1", "LAMP:4", "one"),
  link("KEY:1", "LAMP2:4", "two"),
  link("KEY:2", "LAMP:4", "three"),
];

function mount(extra: Record<string, unknown> = {}) {
  return render(LinkTable, {
    props: { links: LINKS, persistKey: "t", emptyMessage: "empty", onDelete: vi.fn(), ...extra },
  });
}

beforeEach(() => {
  localStorage.clear();
  location.hash = "";
});
afterEach(() => cleanup());

describe("LinkTable", () => {
  it("heads the columns with Sender | Link | Receiver", () => {
    mount();
    const cells = Array.from(screen.getByTestId("column-groups").querySelectorAll("th")).map(
      (th) => [th.textContent?.trim(), th.getAttribute("colspan")],
    );
    expect(cells).toEqual([
      ["links.sender", "2"],
      ["links.editor.link", "3"],
      ["links.receiver", "2"],
    ]);
  });

  it("groups by sender, drops the sender columns and offers to add a receiver", async () => {
    mount();
    await fireEvent.click(screen.getByText("links.group.sender"));
    const groups = screen.getByTestId("column-groups").textContent ?? "";
    expect(groups).not.toContain("links.sender");
    // One header per sender channel.
    expect(screen.getAllByText("links.add_receiver")).toHaveLength(2);
    await fireEvent.click(screen.getAllByText("links.add_receiver")[0]);
    expect(location.hash).toMatch(/^#\/links\/new\?sender=KEY%3A[12]$/);
  });

  it("groups by receiver and offers to add a sender", async () => {
    mount();
    await fireEvent.click(screen.getByText("links.group.receiver"));
    expect(screen.getAllByText("links.add_sender")).toHaveLength(2);
    expect(screen.getByTestId("column-groups").textContent).not.toContain("links.receiver");
  });

  it("remembers the grouping per list", async () => {
    mount();
    await fireEvent.click(screen.getByText("links.group.receiver"));
    cleanup();
    mount();
    expect(screen.getAllByText("links.add_sender")).toHaveLength(2);
  });

  it("offers no actions where the editor is hidden", () => {
    mount({ editable: false });
    expect(screen.queryByText("links.edit")).toBeNull();
    expect(screen.queryByText("common.delete")).toBeNull();
  });

  it("hands the row to onDelete", async () => {
    const onDelete = vi.fn();
    mount({ onDelete });
    await fireEvent.click(screen.getAllByText("common.delete")[0]);
    expect(onDelete).toHaveBeenCalledWith(expect.objectContaining({ sender_address: expect.any(String) }));
  });
});
