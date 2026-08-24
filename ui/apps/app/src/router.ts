import { createRouter, createWebHistory } from "vue-router";
import PrivacyView from "./views/PrivacyView.vue";
import FeaturePlaceholderView from "./views/FeaturePlaceholderView.vue";
import YourTurnView from "./views/YourTurnView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", redirect: "/app/your-turn" },
    { path: "/app", redirect: "/app/your-turn" },
    { path: "/app/your-turn", name: "your-turn", component: YourTurnView, meta: { title: "Your Turn" } },
	{ path: "/app/work", name: "work", component: FeaturePlaceholderView, meta: { title: "Work" } },
	{ path: "/app/knowledge", name: "knowledge", component: FeaturePlaceholderView, meta: { title: "Knowledge" } },
	{ path: "/app/agents", name: "agents", component: FeaturePlaceholderView, meta: { title: "Agents" } },
    { path: "/app/privacy", name: "privacy", component: PrivacyView, meta: { title: "Privacy" } },
    { path: "/:pathMatch(.*)*", redirect: "/app/your-turn" }
  ],
  scrollBehavior: () => ({ top: 0 })
});

router.afterEach((route) => {
  document.title = `${String(route.meta.title ?? "Spyglass")} · Infinite Ocean`;
});
