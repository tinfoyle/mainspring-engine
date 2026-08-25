import type {
  AgentBoardroom,
  AgentBoardrooms,
  AgentConversation,
  AgentConversations,
  AgentMessage,
  AgentMessages,
  AgentPersona,
  AgentPersonas,
  AgentRun,
  AgentRunResolution,
  ConfigureAgentBoardroomManagerRequest,
  CreateAgentBoardroomRequest,
  PublishAgentPersonaRequest,
  ResolveAgentRunRequest,
  StartAgentRunRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

const pendingOperations = new Map<string, string>();

function base(accountID: string): string { return `/api/v1/accounts/${encodeURIComponent(accountID)}`; }

function collectionPath(accountID: string, roomID: string, suffix: string): string {
  return `${base(accountID)}/agent-boardrooms/${encodeURIComponent(roomID)}/${suffix}`;
}

function operation(method: string, path: string, body: string): { fingerprint: string; key: string } {
  const fingerprint = `${method} ${path} ${body}`;
  let key = pendingOperations.get(fingerprint);
  if (!key) { key = crypto.randomUUID(); pendingOperations.set(fingerprint, key); }
  return { fingerprint, key };
}

async function command<T>(method: "POST" | "PUT", path: string, payload: unknown): Promise<T> {
  const body = JSON.stringify(payload);
  const pending = operation(method, path, body);
  const result = await requestJSON<T>(path, { method, headers: { "Idempotency-Key": pending.key }, body });
  pendingOperations.delete(pending.fingerprint);
  return result;
}

async function allPages<T>(path: string): Promise<ReadonlyArray<T>> {
  const items: T[] = [];
  let next: string | undefined = path;
  for (let index = 0; next && index < 20; index += 1) {
    const page = await requestJSON<{ readonly items: ReadonlyArray<T>; readonly next_cursor?: string }>(next);
    items.push(...page.items);
    if (!page.next_cursor) return items;
    const cursorURL: URL = new URL(next, "https://spyglass.invalid");
    cursorURL.searchParams.set("cursor", page.next_cursor);
    next = `${cursorURL.pathname}${cursorURL.search}`;
  }
  throw new Error("Agents returned too many pages to load safely.");
}

export async function listAgentBoardrooms(accountID: string): Promise<ReadonlyArray<AgentBoardroom>> {
  return (await requestJSON<AgentBoardrooms>(`${base(accountID)}/agent-boardrooms`)).items;
}

export function createAgentBoardroom(accountID: string, input: CreateAgentBoardroomRequest): Promise<AgentBoardroom> {
  return command("POST", `${base(accountID)}/agent-boardrooms`, input);
}

export async function listAgentPersonas(accountID: string, roomID: string): Promise<ReadonlyArray<AgentPersona>> {
  return (await requestJSON<AgentPersonas>(collectionPath(accountID, roomID, "personas"))).items;
}

export function configureAgentManager(accountID: string, roomID: string, input: ConfigureAgentBoardroomManagerRequest): Promise<AgentBoardroom> {
  return command("PUT", collectionPath(accountID, roomID, "manager"), input);
}

export function publishAgentPersona(accountID: string, roomID: string, input: PublishAgentPersonaRequest): Promise<AgentPersona> {
  return command("POST", collectionPath(accountID, roomID, "personas"), input);
}

export function listAgentConversations(accountID: string, roomID: string): Promise<ReadonlyArray<AgentConversation>> {
  return allPages<AgentConversation>(`${collectionPath(accountID, roomID, "conversations")}?limit=100`);
}

export function getAgentConversation(accountID: string, conversationID: string): Promise<AgentConversation> {
  return requestJSON<AgentConversation>(`${base(accountID)}/agent-conversations/${encodeURIComponent(conversationID)}`);
}

export function listAgentMessages(accountID: string, conversationID: string): Promise<ReadonlyArray<AgentMessage>> {
  return allPages<AgentMessage>(`${base(accountID)}/agent-conversations/${encodeURIComponent(conversationID)}/messages?limit=100`);
}

export function startAgentRun(accountID: string, roomID: string, input: StartAgentRunRequest): Promise<AgentRun> {
  return command("POST", collectionPath(accountID, roomID, "runs"), input);
}

export function getAgentRun(accountID: string, runID: string): Promise<AgentRun> {
  return requestJSON<AgentRun>(`${base(accountID)}/agent-runs/${encodeURIComponent(runID)}`);
}

export function resolveAgentRun(accountID: string, runID: string, input: ResolveAgentRunRequest): Promise<AgentRunResolution> {
  return command("POST", `${base(accountID)}/agent-runs/${encodeURIComponent(runID)}/resolutions`, input);
}

export type { AgentConversations, AgentMessages };
