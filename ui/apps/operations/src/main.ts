import "@spyglass/design-system/tokens.css";
import { createApp } from "vue";
import App from "./App.vue";
import "./styles.css";

const root = document.querySelector<HTMLElement>("#operations");
if (!root) throw new Error("Operations console root is missing.");
createApp(App).mount(root);
