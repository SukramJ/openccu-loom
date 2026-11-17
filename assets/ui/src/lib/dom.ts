/**
 * The element that holds keyboard focus, followed into shadow roots.
 *
 * `document.activeElement` stops at the outermost shadow host: when the
 * editor runs as a web component inside Home Assistant, it names the
 * component's host, never the field inside it. A focus trap comparing
 * against that value sees every Tab as "focus left the dialog", and a
 * focus return restores the host instead of the opener. Descending
 * through each open shadow root's own `activeElement` yields the element
 * the user actually focused, in the SPA (no shadow root, one step) and in
 * any nesting depth of hosts alike.
 */
export function deepActiveElement(doc: Document = document): HTMLElement | null {
  let active: Element | null = doc.activeElement;
  while (active?.shadowRoot?.activeElement) {
    active = active.shadowRoot.activeElement;
  }
  return active as HTMLElement | null;
}
