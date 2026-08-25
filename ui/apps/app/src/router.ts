import { createRouter, createWebHistory } from "vue-router";
import PrivacyView from "./views/PrivacyView.vue";
import FeaturePlaceholderView from "./views/FeaturePlaceholderView.vue";
import YourTurnView from "./views/YourTurnView.vue";
import YourTurnDetailView from "./views/YourTurnDetailView.vue";
import CheckoutView from "./views/CheckoutView.vue";
import AffiliateView from "./views/AffiliateView.vue";
import WorkView from "./views/WorkView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", redirect: "/app/your-turn" },
    { path: "/app", redirect: "/app/your-turn" },
    { path: "/app/your-turn", name: "your-turn", component: YourTurnView, meta: { title: "Your Turn" } },
	{ path: "/app/your-turn/:kind/:id", name: "your-turn-detail", component: YourTurnDetailView, meta: { title: "Your Turn detail" } },
	{ path: "/app/work", name: "work", component: WorkView, meta: { title: "Work" } },
	{ path: "/app/work/:itemID", name: "work-detail", component: WorkView, meta: { title: "Work detail" } },
	{ path: "/app/knowledge", name: "knowledge", component: FeaturePlaceholderView, meta: { title: "Knowledge" } },
	{ path: "/app/agents", name: "agents", component: FeaturePlaceholderView, meta: { title: "Agents" } },
    { path: "/app/checkout", name: "checkout", component: CheckoutView, meta: { title: "Checkout" } },
    { path: "/app/affiliate", name: "affiliate", component: AffiliateView, meta: { title: "Affiliate" } },
    { path: "/app/privacy", name: "privacy", component: PrivacyView, meta: { title: "Privacy" } },
    { path: "/:pathMatch(.*)*", redirect: "/app/your-turn" }
  ],
  scrollBehavior: () => ({ top: 0 })
});

router.afterEach((route) => {
  document.title = `${String(route.meta.title ?? "Spyglass")} · Infinite Ocean`;
});
