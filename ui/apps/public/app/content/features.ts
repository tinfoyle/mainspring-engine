export interface PublicFeature {
  slug: string;
  name: string;
  title: string;
  summary: string;
  purpose: string;
  workflows: ReadonlyArray<string>;
  boundaries: ReadonlyArray<string>;
  packageCode?: "knowledge" | "work" | "agents" | "finance" | "marketing" | "integrations";
}

export const publicFeatures: ReadonlyArray<PublicFeature> = [
  {
    slug: "your-turn",
    name: "Your Turn",
    title: "Handle what needs you",
    summary: "Questions, reviews, approvals and problems that need your call—all in one list.",
    purpose: "Your Turn puts the decisions only you can make in one place. Each item tells you what happened, what it needs and what happens after you answer.",
    workflows: ["Answer a question that is holding up the work", "Approve finished work or send it back with changes", "Review an important action suggested by an AI agent", "Sort out an outside action when the result is unclear"],
    boundaries: ["You can see exactly what you are approving", "Changed or out-of-date items must be reviewed again", "The person who asks for a sensitive recovery action cannot approve it alone"]
  },
  {
    slug: "work",
    name: "Work",
    title: "Keep every job moving",
    summary: "Clear tasks, owners, priorities and history for your team and AI agents.",
    purpose: "Work gives every job a clear owner, status and next step. You can see what is moving, what is stuck and what needs another look.",
    workflows: ["Create and prioritize work", "Assign it to a person or AI agent", "Review the result and ask for changes", "See why the work was created in the first place"],
    boundaries: ["People can only make changes allowed by their role", "If someone else changes an item, you review the latest version", "Important results come back through Your Turn"],
    packageCode: "work"
  },
  {
    slug: "knowledge",
    name: "Knowledge",
    title: "Keep answers and documents together",
    summary: "Save the facts your business runs on, along with where they came from.",
    purpose: "Knowledge gives your team one dependable place for documents, answers and business rules. Old versions stay available, and unreviewed guesses do not quietly become fact.",
    workflows: ["Save useful facts and the source behind them", "Update documents without losing the old version", "Review information before publishing it", "Use a saved answer to unblock work"],
    boundaries: ["Drafts stay separate from approved information", "Private information keeps its access limits", "Answers point back to the exact information used"],
    packageCode: "knowledge"
  },
  {
    slug: "baseline",
    name: "Baseline",
    title: "Get the business out of your head",
    summary: "Answer a few questions, bring in what you have and see what is missing.",
    purpose: "Baseline helps Spyglass learn how your business works today. It finds missing information and turns the gaps you care about into a plan you can review.",
    workflows: ["List the information and records you already have", "Review what the business needs", "See missing items and exceptions", "Approve the work Spyglass suggests"],
    boundaries: ["You choose which sources Spyglass can use", "You can see who made each decision", "The final plan does not start without your approval"]
  },
  {
    slug: "agents",
    name: "Agents",
    title: "Give AI agents real jobs",
    summary: "Give each agent a job, the tools it needs and clear limits.",
    purpose: "AI agents can handle routine work without getting free rein over your business. You decide their responsibilities, tools and limits, and you can inspect what they did.",
    workflows: ["Set up an agent for a specific job", "Bring several agents together to work through a problem", "Let agents use only the tools you approve", "Review their work, results and problems"],
    boundaries: ["An agent cannot give itself more access", "Important actions still need your approval", "If an outside action has an unclear result, Spyglass stops instead of guessing"],
    packageCode: "agents"
  },
  {
    slug: "schedules",
    name: "Schedules",
    title: "Stay ahead of recurring work",
    summary: "Set up repeat work once and see exactly when it will run again.",
    purpose: "Schedules keep inspections, follow-ups, reports and other repeat work from slipping through the cracks.",
    workflows: ["Create or change a schedule", "See what will run next", "Pause and restart repeat work", "Follow scheduled work through to the result"],
    boundaries: ["Only authorized team members can change schedules", "Pausing a schedule does not erase its history", "Scheduled work still follows the same access and approval rules"]
  },
  {
    slug: "finance",
    name: "Finance",
    title: "Keep financial records organized",
    summary: "Track entries, accounts and reconciliations without mixing them with your Spyglass bill.",
    purpose: "Finance helps you keep clear internal records, balance entries and compare them with statements. Your Spyglass subscription stays separate.",
    workflows: ["Set up ledgers and accounts", "Draft balanced entries", "Compare records with statements", "Send important entries for approval"],
    boundaries: ["Every entry must balance in one supported currency", "Posted and reversed entries keep a permanent history", "Your Spyglass subscription is not mixed into your business records"],
    packageCode: "finance"
  },
  {
    slug: "marketing",
    name: "Marketing",
    title: "Plan it, review it, send it",
    summary: "Keep campaigns, drafts, channels and approvals together from idea to release.",
    purpose: "Marketing gives your team one place to plan a campaign, prepare the material and review it before anything goes out.",
    workflows: ["Plan campaigns and update drafts", "Prepare releases for each channel", "Send a release for review", "Start, pause and finish campaigns"],
    boundaries: ["Publishing requires the right approval", "Every draft and release keeps its version history", "If a channel gives an unclear result, Spyglass checks before trying again"],
    packageCode: "marketing"
  },
  {
    slug: "integrations",
    name: "Integrations",
    title: "Connect the tools you already use",
    summary: "Choose what Spyglass can access, check connection health and disconnect at any time.",
    purpose: "Integrations let Spyglass work with outside services without handing over more access than the job requires.",
    workflows: ["Connect and approve an outside service", "Update or remove its credentials", "Check whether the connection is working", "Review an outside action when the result is unclear"],
    boundaries: ["Passwords and access tokens are never shown back in the browser", "Each connection gets only the access it needs", "Disconnecting or fixing a connection keeps a useful history"],
    packageCode: "integrations"
  },
  {
    slug: "account-administration",
    name: "Team administration",
    title: "Manage your team and access",
    summary: "Invite people, choose their roles and keep ownership and billing access clear.",
    purpose: "Team administration makes it easy to see who belongs to the team, what they can do and who is responsible for ownership and billing.",
    workflows: ["Switch between teams you can access", "Invite or remove team members", "Choose roles and responsibilities", "Check plan access and billing status"],
    boundaries: ["The server checks access on every action", "Business ownership and billing access can be assigned separately", "Unavailable features stay closed without exposing their data"]
  },
  {
    slug: "security",
    name: "Security and recovery",
    title: "Protect the important stuff",
    summary: "Use passkeys, control active sessions and add extra checks to sensitive actions.",
    purpose: "Spyglass asks for stronger proof when the action carries more risk. Signing in once does not give anyone unlimited authority forever.",
    workflows: ["Set up passkeys and recovery codes", "Confirm a sensitive action", "Review and sign out active sessions", "Recover access safely"],
    boundaries: ["Owners must finish security setup", "Sensitive changes require a recent security check", "High-risk recovery and approvals require another person when needed"]
  },
  {
    slug: "export-lifecycle",
    name: "Data and account closure",
    title: "Get your data when you need it",
    summary: "Download your business data, review account closure and manage privacy requests.",
    purpose: "Your data should not be trapped. Spyglass gives you a clear way to request an export, close an account and understand what must be kept for legal or security reasons.",
    workflows: ["Request and download a team data export", "See what must be handled before closure", "Cancel or finish an account closure", "Request access to or deletion of personal data"],
    boundaries: ["Spyglass clearly explains records that must be kept", "An account closes only after required steps and waiting periods", "Affiliate privacy requests do not expose referred customers"]
  }
];

export const featureBySlug = (slug: string): PublicFeature | undefined => publicFeatures.find((feature) => feature.slug === slug);
