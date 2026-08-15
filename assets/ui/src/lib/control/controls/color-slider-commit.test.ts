// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";

// Every colour slider drives a CCU write per `onChange`, so a drag that
// emitted one callback per pointermove burned dozens of radio writes against
// the HmIP duty-cycle budget. They follow ControlSlider's contract instead:
// `onInput` tracks the drag, `onChange` commits once on release.

import ControlHueSlider from "./ControlHueSlider.svelte";
import ControlSaturationSlider from "./ControlSaturationSlider.svelte";
import ControlColorTempSlider from "./ControlColorTempSlider.svelte";

type SliderCase = {
  name: string;
  component: typeof ControlHueSlider;
  props: { value: number } & Record<string, unknown>;
  /** Value the slider must commit for a release at 50 % of the track. */
  midpoint: number;
  /** Thumb offset for that release — quantized, so not always 50 %. */
  thumbPercent: string;
};

const CASES: SliderCase[] = [
  {
    name: "ControlHueSlider",
    component: ControlHueSlider,
    props: { value: 0, min: 0, max: 360 },
    midpoint: 180,
    thumbPercent: "left: 50%",
  },
  {
    name: "ControlSaturationSlider",
    component: ControlSaturationSlider as typeof ControlHueSlider,
    props: { value: 0 },
    midpoint: 0.5,
    thumbPercent: "left: 50%",
  },
  {
    name: "ControlColorTempSlider",
    component: ControlColorTempSlider as typeof ControlHueSlider,
    props: { value: 2000, min: 2000, max: 6500, step: 100 },
    midpoint: 4300,
    thumbPercent: "left: 51.1",
  },
];

// `globals: false` keeps testing-library from registering its own cleanup.
afterEach(cleanup);

// happy-dom implements neither pointer capture nor layout, so the track needs
// both stubbed before a pointer sequence can be interpreted.
function trackAt(): HTMLElement {
  const track = document.querySelector<HTMLElement>('[role="slider"]')!;
  track.setPointerCapture = () => {};
  track.releasePointerCapture = () => {};
  track.getBoundingClientRect = () =>
    ({ left: 0, width: 200, top: 0, height: 40 }) as DOMRect;
  return track;
}

function pointer(type: string, clientX: number): Event {
  const ev = new MouseEvent(type, { bubbles: true, clientX });
  Object.defineProperty(ev, "pointerId", { value: 1 });
  return ev;
}

describe.each(CASES)("$name — commit on release", (c) => {
  it("writes once per drag rather than once per pointermove", async () => {
    const onChange = vi.fn();
    const onInput = vi.fn();
    render(c.component, { ...c.props, onChange, onInput });
    const track = trackAt();

    track.dispatchEvent(pointer("pointerdown", 10));
    for (const x of [40, 70, 100, 130, 160]) {
      track.dispatchEvent(pointer("pointermove", x));
    }
    track.dispatchEvent(pointer("pointerup", 100));
    await tick();

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(c.midpoint);
    // The drag is still reported continuously so a caller can preview it.
    expect(onInput.mock.calls.length).toBeGreaterThan(1);
  });

  it("ignores pointermove when no drag is in progress", async () => {
    const onChange = vi.fn();
    render(c.component, { ...c.props, onChange });
    const track = trackAt();

    track.dispatchEvent(pointer("pointermove", 100));
    await tick();

    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not write while disabled", async () => {
    const onChange = vi.fn();
    render(c.component, { ...c.props, disabled: true, onChange });
    const track = trackAt();

    track.dispatchEvent(pointer("pointerdown", 10));
    track.dispatchEvent(pointer("pointermove", 100));
    track.dispatchEvent(pointer("pointerup", 100));
    await tick();

    expect(onChange).not.toHaveBeenCalled();
  });

  it("tracks the drag position before the value prop catches up", async () => {
    const onChange = vi.fn();
    render(c.component, { ...c.props, onChange });
    const track = trackAt();

    track.dispatchEvent(pointer("pointerdown", 100));
    await tick();

    const thumb = track.querySelector<HTMLElement>("div");
    expect(thumb?.getAttribute("style")).toContain(c.thumbPercent);

    track.dispatchEvent(pointer("pointerup", 100));
  });
});
