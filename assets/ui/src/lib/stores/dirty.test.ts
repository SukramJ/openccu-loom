// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { dirty } from "./dirty.svelte";

afterEach(() => {
  dirty.discardAll();
});

describe("dirty — discarding registered editors", () => {
  it("rolls back every editor that registered a rollback and clears the set", () => {
    const rollbackA = vi.fn();
    const rollbackB = vi.fn();
    dirty.set("surfaces:profiles", true, rollbackA);
    dirty.set("visibility:unignore", true, rollbackB);
    expect(dirty.any()).toBe(true);

    dirty.discardAll();

    expect(rollbackA).toHaveBeenCalledTimes(1);
    expect(rollbackB).toHaveBeenCalledTimes(1);
    // The flag has to go with the draft: leaving it raised is what made
    // the leave-confirm dialog re-appear on every later navigation.
    expect(dirty.any()).toBe(false);
  });

  it("clears an editor whose component owns the draft and registered none", () => {
    dirty.set("channel:ABC123:1", true);

    dirty.discardAll();

    expect(dirty.any()).toBe(false);
  });

  it("forgets the rollback once the editor reports itself clean", () => {
    const rollback = vi.fn();
    dirty.set("surfaces:profiles", true, rollback);
    dirty.set("surfaces:profiles", false);

    dirty.discardAll();

    expect(rollback).not.toHaveBeenCalled();
  });
});
