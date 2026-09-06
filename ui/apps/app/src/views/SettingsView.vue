<script setup lang="ts">
import { computed } from "vue";
import { RouterLink } from "vue-router";
import { useSessionStore } from "../stores/session";
import { useWorkspacePreferences } from "../stores/workspacePreferences";
const preferences = useWorkspacePreferences();
const session = useSessionStore();
const groups = [
  { name: "Business", links: [{ to: "/app/baseline", label: "Business profile" }, { to: "/app/account", label: "People & access" }, { to: "/app/agents", label: "Agents & tools", package: "agents" }, { to: "/app/integrations", label: "Connected services", package: "integrations" }] },
  { name: "Account", links: [{ to: "/app/billing", label: "Billing & plan" }, { to: "/app/security", label: "Security" }, { to: "/app/affiliate", label: "Referral program" }] },
  { name: "Privacy & data", links: [{ to: "/app/privacy", label: "Privacy preferences" }, { to: "/app/account-exports", label: "Export your data" }, { to: "/app/account-closures", label: "Close account" }] }
];
const restrictedPaths = ["/app/billing", "/app/security", "/app/privacy", "/app/account-exports"];
const visibleGroups = computed(() => groups.map(group => ({ ...group, links: group.links.filter(link =>
  session.selected?.account_state === "restricted" ? restrictedPaths.includes(link.to) :
    !("package" in link) || session.selected?.entitlements.packages.some(p => p.code === link.package && p.mode !== "suspended")
)})).filter(group => group.links.length));
</script>
<template>
  <section class="page settings-page">
    <header class="page-heading"><h1>Settings</h1></header>
    <section v-for="group in visibleGroups" :key="group.name" class="settings-group"><h2>{{ group.name }}</h2><nav :aria-label="group.name"><RouterLink v-for="link in group.links" :key="link.to" :to="link.to">{{ link.label }}<span aria-hidden="true">→</span></RouterLink></nav></section>
    <details v-if="session.selected?.account_state !== 'restricted'" class="settings-group"><summary>Workspace preferences</summary><fieldset><legend>Keep these views in the toolbar</legend><label v-for="item in [{path: '/app/schedules', label: 'Schedules'}, {path: '/app/finance', label: 'Finance'}, {path: '/app/marketing', label: 'Marketing'}]" :key="item.path"><input v-model="preferences.pinned" type="checkbox" :value="item.path">{{ item.label }}</label></fieldset></details>
  </section>
</template>
