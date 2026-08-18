export const packages = [
  {
    code: "work", name: "Work", mark: "W", kicker: "Keep motion visible", tone: "green",
    summary: "Turn operating gaps, commitments, and agent discoveries into work with a clear owner and durable history.",
    highlights: ["Shared work queue", "Human and agent ownership", "Reviews, dependencies, and provenance"],
  },
  {
    code: "agents", name: "Agents", mark: "A", kicker: "Specialists with boundaries", tone: "blue",
    summary: "Configure coordinated specialists that work from your business context and use only the capabilities you grant.",
    highlights: ["Versioned agent roles", "Governed tools and budgets", "Boardroom collaboration"],
  },
  {
    code: "finance", name: "Finance", mark: "F", kicker: "Know the operating numbers", tone: "gold",
    summary: "Keep a trustworthy operational view of accounts, transactions, performance, and the work behind each change.",
    highlights: ["Balanced operating ledger", "Account and category views", "Auditable posting and reversal"],
  },
  {
    code: "marketing", name: "Marketing", mark: "M", kicker: "Make the story consistent", tone: "coral",
    summary: "Connect brand knowledge, research, campaigns, content, and follow-through in one accountable system.",
    highlights: ["Brand and audience memory", "Campaign planning", "Research-to-work workflows"],
  },
  {
    code: "knowledge", name: "Knowledge", mark: "K", kicker: "A source-attributed memory", tone: "sage",
    summary: "Preserve the facts, records, evidence, and citations that help people and agents understand the business.",
    highlights: ["Documents and revisions", "Business facts", "Evidence and citations"],
  },
  {
    code: "integrations", name: "Integrations", mark: "I", kicker: "Bring trusted sources closer", tone: "slate",
    summary: "Connect approved sources and delivery channels without handing credentials or unrestricted access to agents.",
    highlights: ["Scoped connectors", "Credential isolation", "Health and revocation"],
  },
] as const;

export const plans = [
  { name: "Free", offerCode: undefined, price: "$0", cadence: "forever", description: "A real Spyglass Account for exploring the operating model.", featured: false, features: ["1 Account", "Core business baseline", "Knowledge starter", "Package previews", "No card required"] },
  { name: "Team", offerCode: "team-monthly-v1", price: "$49", cadence: "per month", description: "For a small team ready to make operating work visible.", featured: true, features: ["Everything in Free", "Work package", "Up to 5 members", "Schedules and exports", "Email support"] },
  { name: "Operating", offerCode: "operating-monthly-v1", price: "$149", cadence: "per month", description: "For businesses coordinating people, agents, and deeper workflows.", featured: false, features: ["Everything in Team", "Agents package", "Finance or Marketing", "Up to 20 members", "Higher automation limits"] },
] as const;
