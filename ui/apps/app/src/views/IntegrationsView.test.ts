// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, IntegrationConnection, IntegrationConnectionDetail, IntegrationExecutionDetail } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import IntegrationsView from "./IntegrationsView.vue";

const api = vi.hoisted(() => ({
  listIntegrationConnections: vi.fn(), getIntegrationConnection: vi.fn(), listIntegrationHealth: vi.fn(), createIntegrationConnection: vi.fn(), reviseIntegrationConnection: vi.fn(), activateIntegrationCredential: vi.fn(), rotateIntegrationCredential: vi.fn(), disableIntegrationConnection: vi.fn(), enableIntegrationConnection: vi.fn(), revokeIntegrationConnection: vi.fn(),
  beginIntegrationAuthorization: vi.fn(), getIntegrationAuthorization: vi.fn(), revokeIntegrationCredential: vi.fn(), listIntegrationExecutions: vi.fn(), getIntegrationExecution: vi.fn(), prepareIntegrationExecution: vi.fn(), requestIntegrationResolution: vi.fn(), confirmIntegrationResolution: vi.fn(), searchIntegrationWeb: vi.fn(), readIntegrationWeb: vi.fn()
}));
vi.mock("@spyglass/api", async (original) => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));

const accountID = "10000000-0000-4000-8000-000000000001";
const userID = "20000000-0000-4000-8000-000000000002";
const connection = { id: "30000000-0000-4000-8000-000000000003", account_id: accountID, name: "Policy research", kind: "web_research", state: "active", current_revision_id: "40000000-0000-4000-8000-000000000004", current_revision: 2, credential_id: "50000000-0000-4000-8000-000000000005", credential_generation: 1, version: 4, created_by: { user_id: userID }, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" } satisfies IntegrationConnection;
const detail = { connection, revision: { id: connection.current_revision_id, account_id: accountID, connection_id: connection.id, revision: 2, capabilities: ["web.research"], scope: { https_origin: "https://example.com", path_prefix: "/policy" }, created_by: { user_id: userID }, created_at: "2026-08-24T00:00:00Z" }, latest_health: { id: "60000000-0000-4000-8000-000000000006", account_id: accountID, connection_id: connection.id, connection_revision_id: connection.current_revision_id, credential_id: connection.credential_id, state: "healthy", latency_milliseconds: 81, checked_at: "2026-08-24T00:00:00Z" } } satisfies IntegrationConnectionDetail;
const executionDetail = { execution: { id: "70000000-0000-4000-8000-000000000007", account_id: accountID, release_id: "80000000-0000-4000-8000-000000000008", release_version: 3, approval_id: "90000000-0000-4000-8000-000000000009", capability: "web.publish", connection_id: connection.id, connection_revision_id: connection.current_revision_id, connection_revision: 2, credential_id: connection.credential_id, credential_generation: 1, payload_sha256: "a".repeat(64), state: "manual_resolution", attempt_count: 1, created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z" }, attempts: [], resolution: { id: "a0000000-0000-4000-8000-00000000000a", execution_id: "70000000-0000-4000-8000-000000000007", requested_outcome: "succeeded", evidence_sha256: "b".repeat(64), requested_by_user_id: userID, requested_at: "2026-08-24T00:00:00Z", state: "pending" } } satisfies IntegrationExecutionDetail;
const account = { account_id: accountID, account_type: "paid", account_version: 2, cell_id: "cell-a", display_name: "Northstar", placement_generation: 1, role: "owner", slug: "northstar", owner_enrollment_required: false, entitlements: { account_id: accountID, catalog_version: 1, evaluated_at: "2026-08-24T00:00:00Z", version: 1, packages: [{ code: "integrations", mode: "enabled", sources: ["subscription"], version: 1 }, { code: "marketing", mode: "enabled", sources: ["subscription"], version: 1 }, { code: "knowledge", mode: "enabled", sources: ["subscription"], version: 1 }] } } satisfies AccountChoice;

beforeEach(() => {
  setActivePinia(createPinia()); Object.values(api).forEach((mock) => mock.mockReset());
  api.listIntegrationConnections.mockResolvedValue({ items: [connection] }); api.getIntegrationConnection.mockResolvedValue(detail); api.listIntegrationHealth.mockResolvedValue({ items: [detail.latest_health] });
  api.listIntegrationExecutions.mockResolvedValue({ items: [executionDetail.execution] }); api.getIntegrationExecution.mockResolvedValue(executionDetail);
  api.searchIntegrationWeb.mockResolvedValue({ query: "policy", items: [{ citation_id: "citation", title: "Policy", url: "https://example.com/policy/launch", description: "Launch controls", retrieved_at: "2026-08-24T00:00:00Z" }] });
  api.readIntegrationWeb.mockResolvedValue({ capture_id: "capture", citation_id: "citation", url: "https://example.com/policy/launch", title: "Policy", media_type: "text/html", content_sha256: "c".repeat(64), document_id: "document", document_revision_id: "revision", retrieved_at: "2026-08-24T00:00:00Z" });
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = accountID; session.userID = userID;
});
async function mountAt(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/integrations", component: IntegrationsView }, { path: "/app/integrations/connections/:connectionID", component: IntegrationsView }, { path: "/app/integrations/executions/:executionID", component: IntegrationsView }, { path: "/app/integrations/authorizations/:authorizationID", component: IntegrationsView }] });
  await router.push(path); await router.isReady(); const wrapper = mount(IntegrationsView, { global: { plugins: [router] } }); await flushPromises(); return wrapper;
}

describe("Integrations workspace", () => {
  it("loads a durable connection and removes management controls in read-only mode", async () => {
    const session = useSessionStore(); session.accounts = [{ ...account, entitlements: { ...account.entitlements, packages: account.entitlements.packages.map((item) => item.code === "integrations" ? { ...item, mode: "read_only" as const } : item) } }];
    const wrapper = await mountAt(`/app/integrations/connections/${connection.id}`);
    expect(api.getIntegrationConnection).toHaveBeenCalledWith(accountID, connection.id); expect(wrapper.text()).toContain("Read-only access"); expect(wrapper.text()).toContain("https://example.com/policy"); expect(wrapper.text()).not.toContain("Revise scope"); expect(wrapper.text()).not.toContain("New connection");
  });

  it("enforces different-manager confirmation for an uncertain external outcome", async () => {
    const wrapper = await mountAt(`/app/integrations/executions/${executionDetail.execution.id}`);
    expect(wrapper.text()).toContain("A different owner or administrator must confirm this exact outcome."); expect(wrapper.text()).not.toContain("Confirm exact outcome");
  });

  it("searches a reviewed scope and confirms Knowledge capture explicitly", async () => {
    const wrapper = await mountAt("/app/integrations"); await wrapper.findAll("[role=tab]").find((item) => item.text() === "Research")?.trigger("click"); await wrapper.get(".integration-search input").setValue("policy"); await wrapper.get(".integration-search").trigger("submit"); await flushPromises(); expect(wrapper.text()).toContain("Launch controls");
    await wrapper.findAll("button").find((item) => item.text() === "Capture to Knowledge")?.trigger("click"); await wrapper.get("[role=dialog] input").setValue("CAPTURE"); await wrapper.get("[role=dialog]").trigger("submit"); await flushPromises();
    expect(api.readIntegrationWeb).toHaveBeenCalledWith(accountID, { connection_id: connection.id, url: "https://example.com/policy/launch" }); expect(wrapper.text()).toContain("Knowledge evidence captured");
  });
});
