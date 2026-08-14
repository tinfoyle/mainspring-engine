import type { AgentsPayload, AppConfig, BaselinePayload, BoardroomPayload, ConversationPayload, DocumentPayload, DocumentsPayload, FinanceEntryPayload, FinancePayload, HomePayload, InboxPayload, TicketPayload, WorkQueuePayload, YourTurnPayload } from "./types";

export class APIError extends Error {
  constructor(message: string, public status: number) {
    super(message);
  }
}

async function problem(response: Response): Promise<string> {
  try {
    const body = await response.json();
    return body.error?.detail || body.detail || body.title || `Request failed (${response.status})`;
  } catch {
    return `Request failed (${response.status})`;
  }
}

async function request<T>(url: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    ...init,
    headers: { Accept: "application/json", ...init.headers },
  });
  if (!response.ok) throw new APIError(await problem(response), response.status);
  return response.json() as Promise<T>;
}

function encoded(form: FormData) {
  const body = new URLSearchParams();
  form.forEach((value, key) => { if (typeof value === "string") body.append(key, value); });
  return body;
}

export const api = {
  home: () => request<HomePayload>("/api/v2/home"),
  workQueue: (search: string) => request<WorkQueuePayload>(`/api/v2/work${search}`),
  ticket: (id: string) => request<TicketPayload>(`/api/v2/work/${encodeURIComponent(id)}`),
  createWork: (config: AppConfig, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<{ item: TicketPayload["item"] }>("/api/v2/work", { method: "POST", body: encoded(form) });
  },
  updateStatus: (config: AppConfig, id: string, status: string) => {
    const form = new FormData();
    form.set("csrf_token", config.csrf);
    form.set("status", status);
    return request<TicketPayload>(`/api/v2/work/${encodeURIComponent(id)}/status`, { method: "POST", body: encoded(form) });
  },
  sendTicketMessage: (config: AppConfig, id: string, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<TicketPayload>(`/api/v2/work/${encodeURIComponent(id)}/messages`, { method: "POST", body: form });
  },
  yourTurn: (search: string) => request<YourTurnPayload>(`/api/v2/your-turn${search}`),
  answerCoordinator: (config: AppConfig, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<unknown>("/api/v2/your-turn/coordinator/answer", { method: "POST", body: form });
  },
  decideApproval: (config: AppConfig, id: string, decision: "approve" | "reject") => {
    const body = new URLSearchParams({ csrf_token: config.csrf });
    return request<YourTurnPayload>(`/api/v2/your-turn/approvals/${encodeURIComponent(id)}/${decision}`, { method: "POST", body });
  },
  documents: () => request<DocumentsPayload>("/api/v2/documents"),
  document: (id: string) => request<DocumentPayload>(`/api/v2/documents/${encodeURIComponent(id)}`),
  uploadDocument: (config: AppConfig, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<DocumentPayload>("/api/v2/documents", { method: "POST", body: form });
  },
  boardroom: (id: string) => request<BoardroomPayload>(`/api/v2/boardrooms/${encodeURIComponent(id)}`),
  createConversation: (config: AppConfig, id: string, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<ConversationPayload>(`/api/v2/boardrooms/${encodeURIComponent(id)}/conversations`, { method: "POST", body: encoded(form) });
  },
  conversation: (id: string) => request<ConversationPayload>(`/api/v2/conversations/${encodeURIComponent(id)}`),
  followUp: (config: AppConfig, id: string, form: FormData) => {
    form.set("csrf_token", config.csrf);
    return request<ConversationPayload>(`/api/v2/conversations/${encodeURIComponent(id)}/runs`, { method: "POST", body: encoded(form) });
  },
  agents: () => request<AgentsPayload>("/api/v2/agents"),
  finance: (ledger?: string) => request<FinancePayload>(`/api/v2/finance${ledger ? `?ledger=${encodeURIComponent(ledger)}` : ""}`),
  createLedger: (config: AppConfig, form: FormData) => { form.set("csrf_token", config.csrf); return request<FinancePayload>("/api/v2/finance/ledgers", { method: "POST", body: encoded(form) }); },
  createAccount: (config: AppConfig, ledger: string, form: FormData) => { form.set("csrf_token", config.csrf); return request<FinancePayload>(`/api/v2/finance/ledgers/${encodeURIComponent(ledger)}/accounts`, { method: "POST", body: encoded(form) }); },
  createEntry: (config: AppConfig, ledger: string, form: FormData) => { form.set("csrf_token", config.csrf); return request<FinanceEntryPayload>(`/api/v2/finance/ledgers/${encodeURIComponent(ledger)}/entries`, { method: "POST", body: encoded(form) }); },
  financeEntry: (id: string) => request<FinanceEntryPayload>(`/api/v2/finance/entries/${encodeURIComponent(id)}`),
  financeEntryAction: (config: AppConfig, id: string, action: "post" | "void") => request<FinancePayload>(`/api/v2/finance/entries/${encodeURIComponent(id)}/${action}`, { method: "POST", body: new URLSearchParams({ csrf_token: config.csrf }) }),
  baseline: () => request<BaselinePayload>("/api/v2/baseline"),
  baselineMutation: (config: AppConfig, path: string, values: Record<string,string>) => request<BaselinePayload>(`/api/v2/baseline${path}`, { method: "POST", body: new URLSearchParams({ csrf_token: config.csrf, ...values }) }),
  inbox: () => request<InboxPayload>("/api/v2/inbox"),
  readAnnouncement: (config: AppConfig, id: string) => request<InboxPayload>(`/api/v2/inbox/${encodeURIComponent(id)}/read`, { method: "POST", body: new URLSearchParams({csrf_token:config.csrf}) }),
};
