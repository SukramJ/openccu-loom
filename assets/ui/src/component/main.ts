// The component bundle's entry. install-host comes first: the editor's
// stores read their host while their modules evaluate.
import "./install-host";
import { LoomDeviceEditor } from "./editor-element.svelte";

export { LoomDeviceEditor };
export type { EditorTransport } from "./install-host";

if (!customElements.get("loom-device-editor")) {
  customElements.define("loom-device-editor", LoomDeviceEditor);
}
