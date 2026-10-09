// Shared state of ChannelPanelStub.svelte. Each stub instance registers
// under its link role so a test can make that side dirty, decide what its
// save() resolves to, and count the calls.
export type StubSide = {
  saveResult: boolean;
  saves: number;
  discards: number;
  paramCount: number;
  markDirty?: (count: number) => void;
};

export const stubSides: Record<string, StubSide> = {};

export function resetStubSides(): void {
  for (const k of Object.keys(stubSides)) delete stubSides[k];
}
