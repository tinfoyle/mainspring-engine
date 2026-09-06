import { expect, test, type Page } from "@playwright/test";
import { installSyntheticAPI, fulfillJSON, expectAccessible, expectNoHorizontalOverflow, accountID, agentRoom, agentConversation,
  agentRun, agentRunID, agentMessage, agentPersona, baseline, baselineID, workItem, knowledgeClaim, financeLedger, financeEntry, integrationConnection, marketingCampaign, marketingRelease, schedule } from "./private-fixtures";

import { baselinePersonaDescription, baselinePersonaInstructions, baselineRoomPurpose } from "../../apps/app/src/baseline-interviewer";

const documentID = "d1000000-0000-4000-8000-000000000001", revisionID = "d2000000-0000-4000-8000-000000000002", chunkID = "d3000000-0000-4000-8000-000000000003";
const document = { id: documentID, account_id: accountID, title: "Supplier requirements", state: "ready", sensitivity: "internal",
  current_revision: 1, current_revision_id: revisionID, version: 2, legal_hold: false, created_at: "2026-09-06T12:00:00Z", updated_at: "2026-09-06T12:00:00Z" };
const passage = { document_id: documentID, revision_id: revisionID, chunk_id: chunkID, document_title: document.title, account_id: accountID,
  content: "Compare equivalent bag sizes. Keep delivery charges separate.", revision: 1, chunk_index: 0, start_byte: 0, end_byte: 62,
  content_sha256: "a".repeat(64), sensitivity: "internal", index_generation: "knowledge-v1" };
const detail = { document, latest_revision: { id: revisionID, number: 1, filename: "requirements.txt", byte_size: 62,
  state: "ready", scan_state: "clean", extraction_state: "ready", index_state: "ready", chunk_count: 1, failure_code: "", created_at: document.created_at, updated_at: document.updated_at } };

async function openChat(page: Page): Promise<void> {
  await expect(page.locator("#workspace-message")).toBeAttached();
  if (!(await page.locator("#workspace-message").isVisible())) await page.getByRole("button", { name: /^Chat/ }).click();
  await expect(page.locator("#workspace-message")).toBeVisible();
}
test.beforeEach(async ({ page }) => {
  await installSyntheticAPI(page);
  await page.route("**/knowledge/documents**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.includes("/chunks/")) return fulfillJSON(route, passage);
    if (path.endsWith("/documents")) return fulfillJSON(route, { items: [document] });
    return fulfillJSON(route, detail);
  });
  await page.route("**/knowledge/retrieval", async route => {
    const input = route.request().postDataJSON();
    if (input.document_id) expect(input.revision_id).toBe(revisionID);
    return fulfillJSON(route, { items: [passage] });
  });
});
test("working views preserve the conversation and draft", async ({ page }, info) => {
  await page.goto("/app/work"); await openChat(page);
  await page.locator("#workspace-message").fill("Help me plan tomorrow");
  await page.getByRole("navigation", { name: "Workspace views" }).getByRole("link", { name: "Knowledge", exact: true }).click();
  await openChat(page); await expect(page.locator("#workspace-message")).toHaveValue("Help me plan tomorrow");
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await openChat(page); await expect(page.locator("#workspace-message")).toHaveValue("Help me plan tomorrow");
  await page.reload(); await openChat(page); await expect(page.locator("#workspace-message")).toHaveValue("Help me plan tomorrow");
  await expectNoHorizontalOverflow(page); await expectAccessible(page);
  await page.screenshot({ path: info.outputPath("workspace.png"), fullPage: true });
});
test("documents open as references and are attached only by an explicit action", async ({ page }) => {
  await page.goto("/app/documents/" + documentID);
  await expect(page.getByText(passage.content, { exact: true })).toBeVisible();
  await expect(page.locator(".chat-context")).toHaveCount(0);
  await page.getByRole("button", { name: "Use passage in chat", exact: true }).click();
  await expect(page.locator(".chat-context")).toContainText(document.title);
  await expect(page.locator(".chat-context")).toContainText("revision 1");
  await expectNoHorizontalOverflow(page); await expectAccessible(page);
});
test("existing conversation links resume a run while another view is open", async ({ page }) => {
  let finished = false;
  await page.route("**/agent-runs/" + agentRunID, route => fulfillJSON(route, { ...agentRun, state: finished ? "succeeded" : "running" }));
  await page.route("**/agent-conversations/" + agentConversation.id + "/messages?**", route => fulfillJSON(route, { items: [{ ...agentMessage, body: finished ? "The report is complete." : "The report is running." }] }));
  await page.goto("/app/agents/boardrooms/" + agentRoom.id + "/conversations/" + agentConversation.id);
  await expect(page.getByText("The agents are working…", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Settings", exact: true }).click(); finished = true;
  await openChat(page);
  await expect(page.getByText("The report is complete.", { exact: true })).toBeVisible({ timeout: 10000 });
  await expect(page.getByText("The agents are working…", { exact: true })).toHaveCount(0);
});
test("one message is sent while switching working views", async ({ page }) => {
  let sends = 0;
  await page.route("**/agent-boardrooms/" + agentRoom.id + "/runs", async route => {
    sends++; await new Promise(resolve => setTimeout(resolve, 400));
    return fulfillJSON(route, { ...agentRun, state: "running" }, 201);
  });
  await page.goto("/app/workspace"); await openChat(page);
  await page.locator("#workspace-message").fill("Review the work summary.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await expect.poll(() => sends).toBe(1);
  await openChat(page); await expect(page.locator("#workspace-message")).toHaveValue("");
  expect(sends).toBe(1);
});
test("existing record links still open the relevant working view", async ({ page }) => {
  for (const path of ["/app/work/" + workItem.id, "/app/knowledge/claims/" + knowledgeClaim.id,
    "/app/schedules/" + schedule.id, "/app/finance/ledgers/" + financeLedger.id,
    "/app/finance/entries/" + financeEntry.id, "/app/integrations/connections/" + integrationConnection.id,
    "/app/marketing/campaigns/" + marketingCampaign.id, "/app/marketing/releases/" + marketingRelease.id,
    "/app/security", "/app/privacy", "/app/account", "/app/billing", "/app/account-exports", "/app/account-closures", "/app/affiliate"]) {
    await page.goto(path);
    await expect(page).toHaveURL(new RegExp(path + "$"));
    await expect(page.locator("#main")).toBeVisible();
    await expect(page.getByRole("link", { name: "Close working view" })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  }
});

test("business interview stays in the chat area across working views and reload", async ({ page }) => {
  await page.route("**/baseline-assessments/current", route => fulfillJSON(route, baseline));
  await page.route("**/agent-boardrooms", route => fulfillJSON(route, { items: [{ ...agentRoom, purpose: baselineRoomPurpose, manager_persona_id: agentPersona.id }] }));
  await page.route("**/agent-boardrooms/" + agentRoom.id + "/personas", route => fulfillJSON(route, { items: [{ ...agentPersona, description: baselinePersonaDescription, system_instructions: baselinePersonaInstructions }] }));
  await page.route("**/agent-boardrooms/" + agentRoom.id + "/conversations?**", route => fulfillJSON(route, { items: [{ ...agentConversation, subject: "Business Baseline · " + baselineID }] }));
  await page.goto("/app/baseline/" + baselineID);
  await expect(page.locator("#baseline-reply")).toBeAttached();
  if (!(await page.locator("#baseline-reply").isVisible())) await page.getByRole("button", { name: /^Chat/ }).click();
  await expect(page.locator("#baseline-reply")).toBeVisible();
  await expect(page.locator("#workspace-message")).not.toBeVisible();
  await page.locator("#baseline-reply").fill("We serve Plymouth and nearby towns.");
  await page.getByRole("navigation", { name: "Workspace views" }).getByRole("link", { name: "Work", exact: true }).click();
  await expect(page.locator("#baseline-reply")).toBeAttached();
  if (!(await page.locator("#baseline-reply").isVisible())) await page.getByRole("button", { name: /^Chat/ }).click();
  await expect(page.locator("#baseline-reply")).toHaveValue("We serve Plymouth and nearby towns.");
  await page.reload();
  await expect(page.locator("#baseline-reply")).toBeAttached();
  if (!(await page.locator("#baseline-reply").isVisible())) await page.getByRole("button", { name: /^Chat/ }).click();
  await expect(page.locator("#baseline-reply")).toHaveValue("We serve Plymouth and nearby towns.");
  await expectNoHorizontalOverflow(page);
});

test("upload confirms one document and opens its published text", async ({ page }) => {
  let uploads = 0;
  await page.route("**/knowledge/documents", async route => {
    if (route.request().method() !== "POST") return route.fallback();
    uploads++;
    expect(route.request().headers()["idempotency-key"]).toBeTruthy();
    expect(route.request().postData()).toContain("Workspace upload check");
    await new Promise(resolve => setTimeout(resolve, 250));
    return fulfillJSON(route, detail, 201);
  });
  await page.goto("/app/documents");
  await page.getByRole("button", { name: "Upload document", exact: true }).click();
  await page.getByLabel("File", { exact: true }).setInputFiles({ name: "requirements.txt", mimeType: "text/plain", buffer: Buffer.from(passage.content) });
  await page.getByLabel("Title", { exact: true }).fill("Workspace upload check");
  await page.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByRole("button", { name: "Uploading…" })).toBeDisabled();
  await expect(page).toHaveURL(new RegExp("/app/documents/" + documentID + "$"));
  await expect(page.getByText(passage.content, { exact: true })).toBeVisible();
  expect(uploads).toBe(1);
  await expectAccessible(page); await expectNoHorizontalOverflow(page);
});

test("optional toolbar views persist after reload", async ({ page }) => {
  await page.goto("/app/settings");
  await page.getByText("Workspace preferences", { exact: true }).click();
  await page.getByRole("checkbox", { name: "Schedules", exact: true }).check();
  const navigation = page.getByRole("navigation", { name: "Workspace views" });
  await expect(navigation.getByRole("link", { name: "Schedules", exact: true })).toBeVisible();
  await page.reload();
  await navigation.getByRole("link", { name: "Schedules", exact: true }).click();
  await expect(page).toHaveURL(/\/app\/schedules$/);
  await expectNoHorizontalOverflow(page);
});
