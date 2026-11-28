import { mount, unmount } from "svelte";
import css from "../app.css?inline";
import { authStore } from "$lib/stores/auth.svelte";
import { centralStore } from "$lib/stores/centrals.svelte";
import { prefs } from "$lib/stores/preferences.svelte";
import { configureHost, type EditorTransport } from "./install-host";
import EditorRoot from "./EditorRoot.svelte";

/**
 * Registers the `@property` rules of the stylesheet on the document, once.
 *
 * Tailwind 4 gives the variables several utilities read (`--tw-border-style`
 * for `border`, among others) their value only as the `initial-value` of an
 * `@property` rule, and a rule inside a shadow root's stylesheet registers
 * nothing: measured, the trigger of a select lost its border until the
 * rules sat on the document (#1067). The rules only declare custom
 * properties with Tailwind's own prefix, so they change nothing else on
 * the host page.
 */
function registerPropertyRules(): void {
  const marker = "loom-device-editor-properties";
  if ((document as Document & { [k: string]: unknown })[marker]) return;
  const rules = css.match(/@property\s+--[\w-]+\s*\{[^}]*\}/g) ?? [];
  const sheet = new CSSStyleSheet();
  sheet.replaceSync(rules.join("\n"));
  document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
  (document as Document & { [k: string]: unknown })[marker] = true;
}

/**
 * `<loom-device-editor>`: the daemon's device configuration editor for an
 * embedding page (ADR 0088 decision 3, ADR 0089).
 *
 * Properties: `transport` (required before the first render), `address`,
 * `channel`, `sub`, `locale` ("de" | "en"), `dark` and `skin` ("loom" |
 * "ha"). A move to another page of the editor (a link, a schedule, another
 * device) is reported as a `loom-navigate` event whose `detail.route` is
 * the SPA hash route; the host decides where it leads.
 *
 * One element per page: the editor's stores are module singletons.
 */
export class LoomDeviceEditor extends HTMLElement {
  #shadow: ShadowRoot;
  #frame: HTMLDivElement;
  #mountPoint: HTMLDivElement;
  #portal: HTMLDivElement;
  #app: Record<string, unknown> | null = null;
  #props = $state({ address: "", channel: undefined as number | undefined, sub: undefined as string | undefined, locale: "en" });
  #transport: EditorTransport | null = null;
  #dark = false;
  #skin: "loom" | "ha" = "ha";
  #constructed = false;

  constructor() {
    super();
    this.#shadow = this.attachShadow({ mode: "open" });
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(css);
    this.#shadow.adoptedStyleSheets = [sheet];
    // The frame carries the theme. `dark` and `data-skin` on a node inside
    // the shadow root let the stylesheet's `.dark *` and skin selectors
    // match without reaching across the boundary.
    this.#frame = document.createElement("div");
    this.#mountPoint = document.createElement("div");
    this.#portal = document.createElement("div");
    this.#frame.append(this.#mountPoint, this.#portal);
    this.#shadow.append(this.#frame);
    this.#applyLook();
    // An anchor with an SPA hash route would move Home Assistant's own
    // address; it goes to the host instead.
    this.#shadow.addEventListener("click", (e) => {
      const a = (e.target as Element | null)?.closest?.("a[href^='#/']");
      if (!a) return;
      e.preventDefault();
      this.#navigate(a.getAttribute("href") ?? "");
    });
    this.#constructed = true;
  }

  set transport(t: EditorTransport) {
    this.#transport = t;
    configureHost({ transport: t });
    if (this.isConnected) this.#start();
  }
  get transport(): EditorTransport | null {
    return this.#transport;
  }
  set address(v: string) {
    this.#props.address = v;
  }
  get address(): string {
    return this.#props.address;
  }
  set channel(v: number | undefined) {
    this.#props.channel = v;
  }
  set sub(v: string | undefined) {
    this.#props.sub = v;
  }
  set locale(v: string) {
    const loc = v === "de" ? "de" : "en";
    this.#props.locale = loc;
    prefs.locale = loc;
  }
  set dark(v: boolean) {
    this.#dark = v;
    this.#applyLook();
  }
  set skin(v: "loom" | "ha") {
    this.#skin = v === "loom" ? "loom" : "ha";
    this.#applyLook();
  }

  connectedCallback(): void {
    this.#applyLook();
    registerPropertyRules();
    configureHost({ root: this.#shadow, navigate: (r) => this.#navigate(r) });
    this.#start();
  }

  disconnectedCallback(): void {
    if (this.#app) {
      void unmount(this.#app);
      this.#app = null;
    }
  }

  /**
   * The look goes on two nodes. The theme tokens of @thingfabric/fabric-ui
   * select on the shadow host (`:host(.dark)`, `:host([data-skin="ha"])`),
   * so the element itself carries `dark` and `data-skin`; Tailwind's `dark:`
   * variant (`.dark *`) cannot see the host from inside the shadow root, so
   * the frame carries `dark` as well. A custom element must not give itself
   * attributes in its constructor, so the host's share waits until then.
   */
  #applyLook(): void {
    this.#frame.classList.toggle("dark", this.#dark);
    this.#frame.dataset.skin = this.#skin;
    this.#frame.style.colorScheme = this.#dark ? "dark" : "light";
    if (!this.#constructed) return;
    this.classList.toggle("dark", this.#dark);
    if (this.dataset.skin !== this.#skin) this.dataset.skin = this.#skin;
  }

  #navigate(route: string): void {
    this.dispatchEvent(new CustomEvent("loom-navigate", { detail: { route }, bubbles: true, composed: true }));
  }

  #start(): void {
    if (this.#app || !this.#transport) return;
    authStore.probe();
    void centralStore.refresh();
    centralStore.ensureStream();
    const props = this.#props;
    this.#app = mount(EditorRoot, {
      target: this.#mountPoint,
      props: {
        get address() {
          return props.address;
        },
        get channel() {
          return props.channel;
        },
        get sub() {
          return props.sub;
        },
        get locale() {
          return props.locale;
        },
        portal: this.#portal,
      },
    });
  }
}
