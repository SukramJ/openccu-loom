import { mount } from "svelte";
import App from "./App.svelte";
import "./app.css";

const target = document.getElementById("app");
if (!target) {
  throw new Error("OpenCCU-Loom SPA: #app mount point missing from index.html");
}

mount(App, { target });
