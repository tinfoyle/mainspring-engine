import { defineAsyncComponent, type Component } from "vue";

export type ModuleID = "overview" | "lookup" | "traffic" | "analytics" | "billing" | "privacy" | "affiliate" | "help";
export interface OperationsModule { id: ModuleID; label: string; description: string; roles: readonly string[]; component?: Component }

// New modules declare navigation, required roles and their own component here.
// The server must independently enforce the same authority on every endpoint.
export const operationsModules: readonly OperationsModule[] = [
  { id: "overview", label: "Overview", description: "Choose an admin task.", roles: [] },
  { id: "lookup", label: "Customer lookup", description: "Find one customer and open a timed, read-only support view.", roles: ["support"] },
  { id: "traffic", label: "Traffic & logs", description: "See requests, unique IPs, response codes and recent request logs.", roles: ["operations_administrator"], component: defineAsyncComponent(() => import("./TrafficPanel.vue")) },
  { id: "analytics", label: "Analytics", description: "Review consented website, signup and in-app events.", roles: ["analytics"], component: defineAsyncComponent(() => import("./AnalyticsPanel.vue")) },
  { id: "billing", label: "Billing issues", description: "Inspect payment processing failures and retry a specific event.", roles: ["billing"] },
  { id: "privacy", label: "Privacy rights", description: "Review requests, deadlines and recorded outcomes.", roles: ["privacy"] },
  { id: "affiliate", label: "Affiliates", description: "Inspect an affiliate and manage its status.", roles: ["affiliate"] },
  { id: "help", label: "How to use this console", description: "Instructions for reports, logs and staff access.", roles: [], component: defineAsyncComponent(() => import("./HelpPanel.vue")) }
];

export function availableModules(roles: readonly string[]): readonly OperationsModule[] {
  return operationsModules.filter(module => module.roles.length === 0 || roles.includes("operations_administrator") || module.roles.some(role => roles.includes(role)));
}
