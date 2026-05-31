import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Tailwind class composition helper — pattern lifted from shadcn.
 * Allows conditional classes with `clsx` and resolves conflicts
 * (e.g. `bg-red-500 bg-blue-500` → `bg-blue-500`) via tailwind-merge.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
