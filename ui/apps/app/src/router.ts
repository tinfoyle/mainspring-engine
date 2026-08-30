import { createRouter, createWebHistory } from "vue-router";
import PrivacyView from "./views/PrivacyView.vue";
import YourTurnView from "./views/YourTurnView.vue";
import YourTurnDetailView from "./views/YourTurnDetailView.vue";
import CheckoutView from "./views/CheckoutView.vue";
import AffiliateView from "./views/AffiliateView.vue";
import WorkView from "./views/WorkView.vue";
import KnowledgeView from "./views/KnowledgeView.vue";
import AgentsView from "./views/AgentsView.vue";
import SchedulesView from "./views/SchedulesView.vue";
import AccountView from "./views/AccountView.vue";
import SecurityView from "./views/SecurityView.vue";
import ExportsView from "./views/ExportsView.vue";
import LifecycleView from "./views/LifecycleView.vue";
import BillingView from "./views/BillingView.vue";
import FinanceView from "./views/FinanceView.vue";
import IntegrationsView from "./views/IntegrationsView.vue";
import BaselineView from "./views/BaselineView.vue";
import MarketingView from "./views/MarketingView.vue";
import SetupView from "./views/SetupView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", redirect: "/app/your-turn" },
    { path: "/app", redirect: "/app/your-turn" },
    { path: "/app/setup", name: "setup", component: SetupView, meta: { title: "Account setup" } },
    { path: "/app/your-turn", name: "your-turn", component: YourTurnView, meta: { title: "Your Turn" } },
	{ path: "/app/your-turn/:kind/:id", name: "your-turn-detail", component: YourTurnDetailView, meta: { title: "Your Turn detail" } },
	{ path: "/app/work", name: "work", component: WorkView, meta: { title: "Work" } },
	{ path: "/app/work/:itemID", name: "work-detail", component: WorkView, meta: { title: "Work detail" } },
	{ path: "/app/knowledge", name: "knowledge", component: KnowledgeView, meta: { title: "Knowledge" } },
	{ path: "/app/knowledge/claims/:claimID", name: "knowledge-claim", component: KnowledgeView, meta: { title: "Knowledge claim" } },
	{ path: "/app/baseline", name: "baseline", component: BaselineView, meta: { title: "Business Baseline" } },
	{ path: "/app/baseline/:assessmentID", name: "baseline-detail", component: BaselineView, meta: { title: "Business Baseline" } },
	{ path: "/app/agents", name: "agents", component: AgentsView, meta: { title: "Agents" } },
	{ path: "/app/agents/boardrooms/:roomID", name: "agent-boardroom", component: AgentsView, meta: { title: "Agent Boardroom" } },
	{ path: "/app/agents/boardrooms/:roomID/conversations/:conversationID", name: "agent-conversation", component: AgentsView, meta: { title: "Agent conversation" } },
    { path: "/app/schedules", name: "schedules", component: SchedulesView, meta: { title: "Schedules" } },
    { path: "/app/schedules/:scheduleID", name: "schedule-detail", component: SchedulesView, meta: { title: "Schedule detail" } },
    { path: "/app/account", name: "account", component: AccountView, meta: { title: "Account" } },
    { path: "/app/security", name: "security", component: SecurityView, meta: { title: "Security" } },
    { path: "/app/account-exports", name: "account-exports", component: ExportsView, meta: { title: "Account exports" } },
    { path: "/app/account-closures", name: "account-closures", component: LifecycleView, meta: { title: "Account lifecycle" } },
    { path: "/app/billing", name: "billing", component: BillingView, meta: { title: "Billing" } },
    { path: "/app/finance", name: "finance", component: FinanceView, meta: { title: "Finance" } },
    { path: "/app/finance/ledgers/:ledgerID", name: "finance-ledger", component: FinanceView, meta: { title: "Finance ledger" } },
    { path: "/app/finance/accounts/:postingAccountID", name: "finance-account", component: FinanceView, meta: { title: "Finance account" } },
    { path: "/app/finance/entries/:entryID", name: "finance-entry", component: FinanceView, meta: { title: "Finance entry" } },
    { path: "/app/finance/reconciliations/:reconciliationID", name: "finance-reconciliation", component: FinanceView, meta: { title: "Finance reconciliation" } },
    { path: "/app/integrations", name: "integrations", component: IntegrationsView, meta: { title: "Integrations" } },
    { path: "/app/integrations/connections/:connectionID", name: "integration-connection", component: IntegrationsView, meta: { title: "Integration connection" } },
    { path: "/app/integrations/executions/:executionID", name: "integration-execution", component: IntegrationsView, meta: { title: "Integration execution" } },
    { path: "/app/integrations/authorizations/:authorizationID", name: "integration-authorization", component: IntegrationsView, meta: { title: "Integration authorization" } },
    { path: "/app/marketing", name: "marketing", component: MarketingView, meta: { title: "Marketing" } },
    { path: "/app/marketing/campaigns/:campaignID", name: "marketing-campaign", component: MarketingView, meta: { title: "Marketing campaign" } },
    { path: "/app/marketing/releases/:releaseID", name: "marketing-release", component: MarketingView, meta: { title: "Marketing release" } },
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
