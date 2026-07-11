<script lang="ts">
  import { cn } from "$lib/utils";

  // Shared loom-style loader — three horizontal warp threads with a weft
  // "shuttle" that runs across each in turn, echoing the product's weave
  // signature. Decorative by design: the surrounding context (e.g.
  // LoadingState) owns the status/live region and the accessible label, so
  // this is aria-hidden to avoid duplicate status roles. CSS-only
  // (transform/opacity), so it stays cheap; prefers-reduced-motion collapses
  // it to a static three-thread mark. Colour tracks the neutral slate token in
  // both themes. `size` sets the height; the loader is ~1.5× as wide as tall.
  type Props = {
    size?: number;
    class?: string;
  };

  let { size = 18, class: className }: Props = $props();
</script>

<span
  class={cn("loom-loader text-slate-400 dark:text-slate-500", className)}
  style="--loom-size:{size}px"
  aria-hidden="true"
>
  <span class="loom-thread"></span>
  <span class="loom-thread"></span>
  <span class="loom-thread"></span>
</span>

<style>
  .loom-loader {
    display: inline-block;
    position: relative;
    width: calc(var(--loom-size) * 1.5);
    height: var(--loom-size);
    flex: none;
  }

  .loom-thread {
    position: absolute;
    left: 0;
    right: 0;
    height: max(1.5px, calc(var(--loom-size) / 9));
    border-radius: 9999px;
    background-color: currentColor;
    opacity: 0.25;
    overflow: hidden;
  }
  .loom-thread:nth-child(1) {
    top: 12%;
  }
  .loom-thread:nth-child(2) {
    top: 44%;
  }
  .loom-thread:nth-child(3) {
    top: 76%;
  }

  /* The weft shuttle: a short, brighter segment that sweeps left → right,
     staggered per thread so the three fill in sequence. translateX is
     relative to the shuttle's own width, and the thread clips it. */
  .loom-thread::after {
    content: "";
    position: absolute;
    top: 0;
    bottom: 0;
    width: 40%;
    border-radius: inherit;
    background-color: currentColor;
    opacity: 0.9;
    animation: loom-weft 1.1s ease-in-out infinite;
  }
  .loom-thread:nth-child(2)::after {
    animation-delay: 0.16s;
  }
  .loom-thread:nth-child(3)::after {
    animation-delay: 0.32s;
  }

  @keyframes loom-weft {
    0% {
      transform: translateX(-110%);
    }
    55%,
    100% {
      transform: translateX(360%);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .loom-thread {
      opacity: 0.5;
    }
    .loom-thread::after {
      animation: none;
      opacity: 0;
    }
  }
</style>
