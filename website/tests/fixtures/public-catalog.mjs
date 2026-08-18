export const publicCatalogFixture = {
  version: 2,
  published_at: "2026-01-01T00:00:00Z",
  packages: [
    { code: "knowledge", version: 1, name: "Knowledge", description: "Source-attributed business facts.", features: ["knowledge.read"], default_limits: { documents: 25 } },
    { code: "work", version: 1, name: "Work", description: "Accountable work across people and agents.", features: ["work.read", "work.manage"], default_limits: { active_items: 100 } },
  ],
  limits: [
    { code: "documents", package_code: "knowledge", name: "Documents", unit: "document", kind: "capacity", combine: "maximum" },
    { code: "active_items", package_code: "work", name: "Active work items", unit: "work_item", kind: "capacity", combine: "maximum" },
  ],
  plans: [
    { code: "free", version: 1, name: "Free", description: "Explore Spyglass.", packages: { knowledge: "enabled" } },
    { code: "team", version: 1, name: "Team", description: "Operate as a growing team.", packages: { knowledge: "enabled", work: "enabled" } },
  ],
  offers: [
    { code: "free-v1", plan_code: "free", plan_version: 1, currency: "USD", amount_minor: 0, billing_interval: "none", effective_from: "2026-01-01T00:00:00Z" },
    { code: "team-monthly-v1", plan_code: "team", plan_version: 1, currency: "USD", amount_minor: 4900, billing_interval: "month", effective_from: "2026-01-01T00:00:00Z" },
  ],
};
