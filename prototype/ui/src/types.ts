export type User = {
  DisplayName: string;
  Email: string;
  Role: string;
  Development: boolean;
};

export type AppConfig = {
  tenant: string;
  csrf: string;
  user: User;
};

export type WorkItem = {
  ID: string;
  Number: number;
  Kind: string;
  Title: string;
  Description: string;
  Status: string;
  Priority: string;
  Source: string;
  CreatedByName: string;
  AssignedToName: string;
  AssignedToType: string;
  Responsibility: string;
  ParentID: string;
  ParentNumber: number;
  DueLabel: string;
  IsOverdue: boolean;
  CreatedAt: string;
  UpdatedAt: string;
};

export type WorkSummary = {
  Active: number;
  InProgress: number;
  Waiting: number;
  Urgent: number;
  Done: number;
};

export type WorkQueuePayload = {
  items: WorkItem[];
  summary: WorkSummary;
  filter: { Status: string; Kind: string; Query: string };
};

export type Persona = {
  ID: string;
  Name: string;
  Role: string;
  Tools: string[];
  CanCreateSubtasks: boolean;
};

export type ResearchResult = { Title: string; URL: string; Excerpt: string };
export type ResearchActivity = {
  Tool: string;
  Query: string;
  URL: string;
  Status: string;
  Results: ResearchResult[];
};

export type Message = {
  ID: string;
  PersonaName: string;
  PersonaRole: string;
  Role: string;
  Body: string;
  Sequence: number;
  CreatedAt: string;
  Research: ResearchActivity[];
  optimistic?: boolean;
};

export type DocumentOption = { ID: string; Name: string; MediaType: string; Selected: boolean };
export type Run = { ID: string; Status: string; Prompt: string; TurnCount: number; EventCursor: number; CreatedAt: string; Error: string };
export type Approval = {
  ID: string;
  PersonaName: string;
  PersonaRole: string;
  ActionType: string;
  Reason: string;
  Evidence: string[];
  Status: string;
  WorkItemID: string;
  WorkItemNumber: number;
  WorkItemTitle: string;
  ReviewSummary: string;
  Recommendations: string[];
};

export type TicketPayload = {
  item: WorkItem;
  subtasks: WorkItem[];
  personas: Persona[];
  messages: Message[];
  attachments: DocumentOption[];
  documents: DocumentOption[];
  run: Run;
  approvals: Approval[];
};

export type CoordinatorMessage = { Role: string; MessageKind: string; Body: string; CreatedAt: string };
export type CoordinatorTicket = { ID: string; Number: number; Title: string };
export type CoordinatorQuestion = {
  FactKey: string;
  Label: string;
  Prompt: string;
  TicketCount: number;
  QuestionCount: number;
  Tickets: CoordinatorTicket[];
};
export type KnowledgeFact = { Key: string; Label: string; Value: string; SourceType: string; UpdatedAt: string };
export type Coordinator = {
  Messages: CoordinatorMessage[];
  Current: CoordinatorQuestion | null;
  PendingQuestions: number;
  PendingRequests: number;
  RemainingTopics: number;
  KnownFacts: number;
  RecentFacts: KnowledgeFact[];
};
export type HumanInput = {
  ID: string;
  ParentWorkItemID: string;
  ParentNumber: number;
  ParentTitle: string;
  WorkItemID: string;
  WorkItemNumber: number;
  PersonaName: string;
  PersonaRole: string;
  Questions: string[];
  Status: string;
  RequestedAt: string;
};
export type YourTurnPayload = {
  tab: "input" | "reviews" | "approvals";
  counts: { Inputs: number; Reviews: number; Approvals: number };
  coordinator: Coordinator;
  inputs: HumanInput[];
  approvals: Approval[];
  documents: DocumentOption[];
};

export type Document = {
  ID: string;
  Name: string;
  MediaType: string;
  Status: string;
  ChunkCount: number;
  CharacterCount: number;
  UploadedBy: string;
  Revision: number;
  CreatedAt: string;
  UpdatedAt: string;
};

export type DocumentDetail = Document & { Content: string };
export type DocumentsPayload = { documents: Document[] };
export type DocumentPayload = { document: DocumentDetail };

export type Boardroom = {
  ID: string;
  Name: string;
  Description: string;
  PersonaCount: number;
  Status: string;
};

export type Conversation = {
  ID: string;
  Title: string;
  Source: string;
  LatestStatus: string;
  LatestPrompt: string;
  MessageCount: number;
  CreatedAt: string;
  UpdatedAt: string;
};

export type BoardroomPayload = {
  boardroom: Boardroom;
  personas: Persona[];
  conversations: Conversation[];
  documents: DocumentOption[];
  template: string;
};

export type ConversationPayload = {
  boardroom: Boardroom;
  personas: Persona[];
  conversation: Conversation;
  run: Run;
  messages: Message[];
  attachments: DocumentOption[];
  documents: DocumentOption[];
  approvals: Approval[];
};

export type HomePayload = {
  boardrooms: Boardroom[];
  work: WorkSummary;
  yourTurn: { Inputs: number; Reviews: number; Approvals: number };
  documents: number;
};

export type Agent = {
  ID: string;
  Name: string;
  Role: string;
  Description: string;
  BoardroomName: string;
  Provider: string;
  Model: string;
  ReasoningEffort: string;
  Position: number;
  MaxTurns: number;
  ToolCount: number;
  Enabled: boolean;
};
export type AgentsPayload = { agents: Agent[] };

export type FinanceLedger = { ID: string; Name: string; Code: string; Description: string; Currency: string; Status: string; AccountCount: number; DraftCount: number; Income: string; Expenses: string; Net: string };
export type FinanceAccount = { ID: string; ParentAccountID: string; Code: string; Name: string; Description: string; Type: string; Status: string; Balance: string; AllowPosting: boolean };
export type FinanceLine = { AccountID: string; AccountCode: string; AccountName: string; Memo: string; Debit: string; Credit: string };
export type FinanceEntry = { ID: string; LedgerID: string; Number: string; Date: string; Description: string; Reference: string; Status: string; Source: string; Total: string; ReversalOfID: string; Lines: FinanceLine[] };
export type FinancePayload = { ledgers: FinanceLedger[]; selected: FinanceLedger | null; accounts: FinanceAccount[]; entries: FinanceEntry[]; today: string };
export type FinanceEntryPayload = { ledger: FinanceLedger; entry: FinanceEntry; accounts: FinanceAccount[] };

export type BaselineMessage = { Role: string; Body: string };
export type BusinessFact = { Key: string; Label: string; Value: string; SourceLabel: string };
export type EvidenceLink = { Label: string; URL: string; Type: string };
export type BaselineResearchResult = { Title: string; URL: string; Description: string; CitationID: string };
export type BaselineResearch = { Query: string; Status: string; LastError: string; Results: BaselineResearchResult[] };
export type EvidenceRequirement = { ID: string; Domain: string; Label: string; Rationale: string; Status: string; Disposition: string; Responsibility: string; RenewalDue: string; OwnerAnswer: string; Interviewed: boolean; Evidence: EvidenceLink[]; Research: BaselineResearch[] };
export type Baseline = { ID: string; Status: string; Phase: string; CurrentQuestion: number; QuestionCount: number; CurrentPrompt: string; CurrentExplanation: string; Messages: BaselineMessage[]; Facts: BusinessFact[]; Requirements: EvidenceRequirement[]; Documents: DocumentOption[]; InventoryCurrent: EvidenceRequirement | null; InventoryAnswered: number; InventoryTotal: number; EvidenceScopeTitle: string; EvidenceScopeSummary: string; PlanParentWorkItemID: string; NextReassessment: string };
export type BaselinePayload = { baseline: Baseline };
export type Announcement = { ID: string; Title: string; Body: string; Category: string; PublishedAt: string; Read: boolean };
export type InboxPayload = { items: Announcement[]; unread: number };
