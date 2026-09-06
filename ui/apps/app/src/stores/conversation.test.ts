// @vitest-environment happy-dom
import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AccountChoice, AgentBoardroom, AgentConversation, AgentMessage, AgentPersona, AgentRun } from "@spyglass/api";
import { useSessionStore } from "./session";
import { useConversationStore } from "./conversation";
import WorkspaceChat from "../components/WorkspaceChat.vue";

const api = vi.hoisted(() => ({
  listAgentBoardrooms: vi.fn(), listAgentPersonas: vi.fn(), listAgentConversations: vi.fn(),
  getAgentConversation: vi.fn(), listAgentMessages: vi.fn(), getAgentRun: vi.fn(),
  startAgentRun: vi.fn(), resolveAgentRun: vi.fn(), getWorkItem: vi.fn(), getKnowledgeClaim: vi.fn(), getDocumentCitation: vi.fn()
}));
vi.mock("@spyglass/api", async original => ({ ...await original<typeof import("@spyglass/api")>(), ...api }));
const account = { account_id: "account-a", account_state: "active", role: "owner", owner_enrollment_required: false,
  entitlements: { packages: [{ code: "agents", mode: "enabled" }] } } as unknown as AccountChoice;
const room = { id: "room-a", name: "Operations", state: "active" } as AgentBoardroom;
const person = { id: "person-a", persona_version_id: "version-a", name: "Coordinator", state: "active" } as AgentPersona;
const conversation = { id: "conversation-a", boardroom_id: room.id, subject: "Supplier report", state: "open" } as AgentConversation;
const message = { id: "message-a", body: "Working on the report", role: "persona", created_at: "2026-09-06T12:00:00Z", run_id: "run-a", persona_version_id: "version-a" } as AgentMessage;
const run = { id: "run-a", conversation_id: conversation.id, boardroom_id: room.id, state: "running", resolutions: [] } as unknown as AgentRun;
let chat: ReturnType<typeof useConversationStore>;
beforeEach(() => {
  sessionStorage.clear(); setActivePinia(createPinia());
  Object.values(api).forEach(mock => mock.mockReset());
  api.listAgentBoardrooms.mockResolvedValue([room]); api.listAgentPersonas.mockResolvedValue([person]);
  api.listAgentConversations.mockResolvedValue([conversation]); api.getAgentConversation.mockResolvedValue(conversation);
  api.listAgentMessages.mockResolvedValue([message]); api.getAgentRun.mockResolvedValue(run); api.startAgentRun.mockResolvedValue(run);
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "owner-a";
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  chat = useConversationStore();
});
afterEach(() => { chat.forget(); chat.$dispose(); vi.useRealTimers(); });

describe("persistent workspace conversation", () => {
  it("keeps a draft while changing working views and restores it after reload", async () => {
    await flushPromises();
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: "/app/documents", component: { template: "<div>Documents</div>" } },
      { path: "/app/work", component: { template: "<div>Work</div>" } }, { path: "/app/knowledge", component: { template: "<div>Knowledge</div>" } }
    ] });
    await router.push("/app/work");
    const wrapper = mount({ components: { WorkspaceChat }, template: "<WorkspaceChat/><RouterView/>" }, { global: { plugins: [router] } });
    await wrapper.get("textarea").setValue("Keep my question");
    await router.push("/app/knowledge");
    expect(wrapper.get("textarea").element.value).toBe("Keep my question");
    wrapper.unmount(); chat.$dispose();
    setActivePinia(createPinia());
    const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = "owner-a";
    chat = useConversationStore(); await flushPromises();
    expect(chat.prompt).toBe("Keep my question");
  });

  it("refreshes published agent configuration without losing the active run or draft", async () => {
    await flushPromises(); await chat.open(room.id, conversation.id);
    chat.prompt = "Keep this next question";
    api.listAgentPersonas.mockResolvedValue([{ ...person, persona_version_id: "version-b" }]);
    await chat.refreshTeam(room.id);
    expect(chat.personas[0]?.persona_version_id).toBe("version-b");
    expect(chat.conversationID).toBe(conversation.id);
    expect(chat.prompt).toBe("Keep this next question");
    expect(chat.running).toBe(true);
    api.getAgentRun.mockResolvedValue({ ...run, state: "succeeded" });
    await vi.advanceTimersByTimeAsync(2500);
    expect(chat.running).toBe(false);
  });

  it("resumes following a running conversation loaded from a deep link", async () => {
    await flushPromises(); await chat.open(room.id, conversation.id);
    expect(chat.running).toBe(true);
    api.getAgentRun.mockResolvedValue({ ...run, state: "succeeded" });
    api.listAgentMessages.mockResolvedValue([{ ...message, body: "Report complete" }]);
    await vi.advanceTimersByTimeAsync(2500);
    expect(chat.running).toBe(false); expect(chat.messages[0]?.body).toBe("Report complete");
  });

  it("rejects late old-account results and keeps drafts isolated", async () => {
    await flushPromises(); chat.prompt = "Private account A draft";
    let release: (value: AgentMessage[]) => void = () => {};
    api.listAgentMessages.mockReturnValueOnce(new Promise(resolve => { release = resolve; }));
    const opening = chat.open(room.id, conversation.id); await flushPromises();
    const session = useSessionStore();
    api.listAgentBoardrooms.mockResolvedValue([]);
    session.accounts = [account, { ...account, account_id: "account-b" }];
    session.selectedID = "account-b"; await flushPromises();
    release([message]); await opening;
    expect(chat.messages).toEqual([]); expect(chat.prompt).toBe(""); expect(chat.roomID).toBe("");
    api.listAgentBoardrooms.mockResolvedValue([room]);
    session.selectedID = account.account_id; await flushPromises();
    await chat.open(room.id);
    expect(chat.prompt).toBe("Private account A draft");
  });

  it("rechecks context before sending and refuses changed records", async () => {
    await flushPromises();
    chat.attach({ kind: "work", id: "work-a", title: "Job", version: "version 2", text: "old text" });
    chat.prompt = "Summarize this"; api.getWorkItem.mockResolvedValue({ id: "work-a", version: 3 });
    await chat.send();
    expect(api.startAgentRun).not.toHaveBeenCalled();
    expect(chat.error).toContain("task changed"); expect(chat.prompt).toBe("Summarize this");
  });

  it("uses freshly authorized reference content and prevents duplicate in-flight sends", async () => {
    await flushPromises();
    chat.attach({ kind: "work", id: "work-a", title: "Job", version: "version 2", text: "tampered cached content" });
    chat.prompt = "Summarize this";
    let release: (value: unknown) => void = () => {};
    api.getWorkItem.mockReturnValue(new Promise(resolve => { release = resolve; }));
    const sending = chat.send(); await chat.send();
    release({ id: "work-a", version: 2, title: "Job", description: "Verified source content", state: "open" }); await sending;
    expect(api.startAgentRun).toHaveBeenCalledTimes(1);
    const input = api.startAgentRun.mock.calls[0]![2];
    expect(input.prompt).toContain("Verified source content");
    expect(input.prompt).not.toContain("tampered cached content");
    expect(chat.context).toEqual([]); expect(chat.prompt).toBe("");
  });

  it("registers an explicitly attached document in the server frozen context", async () => {
    await flushPromises();
    chat.attach({ kind: "document", id: "doc-a", title: "Delivery details", version: "revision 1 · whole document",
      text: JSON.stringify({ document_id: "doc-a", revision_id: "rev-a", chunk_id: "chunk-a" }) });
    api.getDocumentCitation.mockResolvedValue({ document_id: "doc-a", revision_id: "rev-a", chunk_id: "chunk-a", content: "Checked text" });
    chat.prompt = "What is the delivery window?";
    await chat.send();
    expect(api.startAgentRun.mock.calls[0]![2].context).toEqual({ knowledge_document_ids: ["doc-a"] });
    expect(api.getDocumentCitation).toHaveBeenCalledWith(account.account_id, "doc-a", "rev-a", "chunk-a");
  });

  it("never dispatches a queued message if accounts change during context authorization", async () => {
    await flushPromises();
    chat.attach({ kind: "work", id: "work-a", title: "Job", version: "version 2", text: "" });
    chat.prompt = "Summarize";
    let release: (value: unknown) => void = () => {};
    api.getWorkItem.mockReturnValue(new Promise(resolve => { release = resolve; }));
    const sending = chat.send();
    const session = useSessionStore(); api.listAgentBoardrooms.mockResolvedValue([]);
    session.accounts = [account, { ...account, account_id: "account-b" }]; session.selectedID = "account-b";
    release({ id: "work-a", version: 2 }); await sending; await flushPromises();
    expect(api.startAgentRun).not.toHaveBeenCalled(); expect(chat.prompt).toBe("");
  });
  it("keeps the summary agent out of selected turns", async () => {
    api.listAgentBoardrooms.mockResolvedValue([{ ...room, manager_persona_id: person.id }]);
    api.listAgentPersonas.mockResolvedValue([person, { ...person, id: "researcher", name: "Researcher" }]);
    await chat.initialize(); await flushPromises();
    chat.mode = "manager_led"; await flushPromises();
    expect(chat.summaryAvailable).toBe(true);
    expect(chat.selectedPersonaIDs).toEqual(["researcher"]);
  });

  it("keeps raw action payloads out of chat and links to approval review", async () => {
    await flushPromises(); await chat.open(room.id, conversation.id);
    chat.messages = [{ ...message, result: { contribution: message.body, findings: ["A useful finding"], recommendations: [], questions: [],
      citations: [], confidence: "high", delegations: [], proposed_actions: [{ kind: "schedules.create", reason: "Daily report", payload: { secret_internal_field: "not for display" }, evidence: [] }] } }];
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: "/app/work", component: { template: "<div>Work</div>" } }, { path: "/app/your-turn", component: { template: "<div>Approval</div>" } },
      { path: "/app/documents", component: { template: "<div>Documents</div>" } }
    ] });
    await router.push("/app/work");
    const wrapper = mount(WorkspaceChat, { global: { plugins: [router] } });
    expect(wrapper.text()).toContain("A useful finding");
    expect(wrapper.text()).not.toContain("secret_internal_field");
    expect(wrapper.get('a[href="/app/your-turn"]').text()).toBe("Review proposed actions");
    wrapper.unmount();
  });

});
