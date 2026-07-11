<script lang="ts">
  import { cn } from "$lib/utils";

  // Abstract "loom" backdrop: parallel weft threads spanning the full width,
  // with one localized band where a few warp threads interlace with them.
  // The over/under is drawn the way a single-colour woven cloth actually
  // reads — the thread that passes underneath is broken with a small gap at
  // that crossing. Purely decorative (aria-hidden, pointer-events-none),
  // meant to sit behind content at very low opacity as a reusable signature
  // element for otherwise-empty surfaces (login, empty states, …).
  type Props = {
    class?: string;
    // Set false to skip the one-shot weave-in (it is skipped under
    // prefers-reduced-motion regardless).
    animate?: boolean;
  };

  let { class: className, animate = true }: Props = $props();

  const W = 480;
  const H = 320;
  const stroke = 3;
  const gap = stroke + 2.5; // half-length removed around an under-crossing

  // Weft: horizontal threads across the whole width.
  const weftYs: number[] = [];
  for (let y = 18; y < H; y += 22) weftYs.push(y);

  // Warp: vertical threads, only inside a single band → the "one spot" where
  // the cloth is woven. They run the full height so they cross every weft.
  const warpXs: number[] = [];
  for (let x = 150; x <= 330; x += 26) warpXs.push(x);

  type Seg = { x1: number; y1: number; x2: number; y2: number };

  // Cut a 1-D line [a,b] at the given crossing centres, removing ±g around
  // each so the thread visibly dips "under" at that intersection.
  function cut(
    a: number,
    b: number,
    centres: number[],
    g: number,
  ): [number, number][] {
    const segs: [number, number][] = [];
    let start = a;
    for (const c of centres) {
      const lo = c - g;
      const hi = c + g;
      if (lo > start) segs.push([start, Math.min(lo, b)]);
      start = Math.max(start, hi);
    }
    if (start < b) segs.push([start, b]);
    return segs;
  }

  const segments: Seg[] = (() => {
    const out: Seg[] = [];
    // Wefts: break where the warp passes over (checkerboard parity).
    weftYs.forEach((y, ri) => {
      const overWarps = warpXs.filter((_, wi) => (wi + ri) % 2 === 0);
      for (const [x1, x2] of cut(0, W, overWarps, gap)) {
        out.push({ x1, y1: y, x2, y2: y });
      }
    });
    // Warps: break at the complementary crossings, where the weft passes over.
    warpXs.forEach((x, wi) => {
      const overWefts = weftYs.filter((_, ri) => (wi + ri) % 2 === 1);
      for (const [y1, y2] of cut(0, H, overWefts, gap)) {
        out.push({ x1: x, y1, x2: x, y2 });
      }
    });
    return out;
  })();
</script>

<div
  class={cn(
    "pointer-events-none absolute inset-0 overflow-hidden text-brand-500 opacity-[0.055] dark:text-brand-400 dark:opacity-[0.09]",
    className,
  )}
  aria-hidden="true"
>
  <svg
    class="h-full w-full"
    class:weave-in={animate}
    viewBox="0 0 {W} {H}"
    preserveAspectRatio="xMidYMid slice"
    fill="none"
    stroke="currentColor"
    stroke-width={stroke}
    stroke-linecap="round"
  >
    {#each segments as s (s.x1 + ":" + s.y1 + ":" + s.x2 + ":" + s.y2)}
      <line x1={s.x1} y1={s.y1} x2={s.x2} y2={s.y2} />
    {/each}
  </svg>
</div>

<style>
  /* One-shot weave-in: the pattern fades + settles from a slight offset.
     Opacity animates on the SVG only (0 → 1); the low base opacity lives on
     the wrapper, so the two compose to the intended faint tint. */
  .weave-in {
    animation: weave-in 560ms ease-out both;
  }
  @keyframes weave-in {
    from {
      opacity: 0;
      transform: translateX(10px);
    }
    to {
      opacity: 1;
      transform: none;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .weave-in {
      animation: none;
    }
  }
</style>
