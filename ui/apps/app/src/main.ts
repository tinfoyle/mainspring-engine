import "@spyglass/design-system/tokens.css";
import { setUnauthorizedHandler } from "@spyglass/api";
import { createPinia } from "pinia";
import { createApp } from "vue";
import App from "./App.vue";
import { installDialogFocus } from "./dialogFocus";
import { router } from "./router";
import "./styles.css";
import "./workspace.css";

const root = document.querySelector<HTMLElement>("#app");
if (!root) throw new Error("Spyglass application root is missing.");
let redirectingToLogin = false;
setUnauthorizedHandler(() => {
  if (redirectingToLogin || window.location.pathname === "/login") return;
  redirectingToLogin = true;
  const returnTo = `${window.location.pathname}${window.location.search}`;
  window.location.assign(`/login?return_to=${encodeURIComponent(returnTo)}`);
});
createApp(App).use(createPinia()).use(router).mount(root);
installDialogFocus(root);
