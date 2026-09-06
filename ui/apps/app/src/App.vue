<script setup lang="ts">
import { APIProblem, emitAnalytics, getPrivacyConsent, listAttentionQueue, logout, type CatalogPackageCode } from "@spyglass/api";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute, useRouter } from "vue-router";
import { applicationEntryPoint } from "./applicationEntry";
import { useSessionStore } from "./stores/session";
import { useConversationStore } from "./stores/conversation";
import BaselineView from "./views/BaselineView.vue";
import { useWorkspacePreferences } from "./stores/workspacePreferences";
import WorkspaceChat from "./components/WorkspaceChat.vue";
const route = useRoute(), router = useRouter(), session = useSessionStore(), chat = useConversationStore();
const main = ref<HTMLElement>();
const attentionCount = ref<number>();
let attentionSequence = 0;
let attentionTimer: ReturnType<typeof setTimeout> | undefined;
async function refreshAttention(): Promise<void> {
  const ticket = ++attentionSequence, accountID = session.selectedID, userID = session.userID;
  if (attentionTimer) clearTimeout(attentionTimer);
  if (!accountID || !userID || session.selected?.account_state === "restricted") { attentionCount.value = undefined; return; }
  try { const items = await listAttentionQueue(accountID, userID, session.attentionAccess); if (ticket === attentionSequence) attentionCount.value = items.length; }
  catch { if (ticket === attentionSequence) attentionCount.value = undefined; }
  if (ticket === attentionSequence) attentionTimer = setTimeout(() => void refreshAttention(), 30000);
}
watch(() => [session.selectedID, session.userID, chat.run?.state, route.fullPath], () => { attentionCount.value = undefined; void refreshAttention(); }, { immediate: true });
onBeforeUnmount(() => { attentionSequence++; if (attentionTimer) clearTimeout(attentionTimer); });
const signingOut = ref(false), signOutError = ref("");
const preferences = useWorkspacePreferences();
const businessRoute = computed(() => route.name === "baseline" || route.name === "baseline-detail");
watch([businessRoute, () => session.selectedID], ([active]) => { if (active) { chat.businessVisited = true; chat.surface = "business"; } }, { immediate: true });
const setupActive = computed(() => route.name === "setup");
const setupRequired = computed(() => Boolean(session.selected?.owner_enrollment_required));
const restricted = computed(() => session.selected?.account_state === "restricted");
const restrictedNavigation = new Set(["/app/settings", "/app/billing", "/app/security", "/app/account-exports", "/app/privacy", "/app/checkout"]);
const panelOpen = computed(() => !["workspace", "agent-conversation"].includes(String(route.name)));
const settingsPaths = ["/app/settings", "/app/account", "/app/billing", "/app/security", "/app/account-exports", "/app/account-closures", "/app/affiliate", "/app/privacy", "/app/agents", "/app/integrations", "/app/baseline", "/app/checkout"];
const inSettings = computed(() => settingsPaths.some(p => route.path === p || route.path.startsWith(p + "/")) && route.name !== "agent-conversation");
interface NavigationItem { to: string; label: string; packageCode?: CatalogPackageCode }
const primary: NavigationItem[] = [
  { to: "/app/work", label: "Work", packageCode: "work" },
  { to: "/app/knowledge", label: "Knowledge", packageCode: "knowledge" },
  { to: "/app/documents", label: "Documents", packageCode: "knowledge" }
];
const secondary: NavigationItem[] = [
  { to: "/app/schedules", label: "Schedules", packageCode: "agents" },
  { to: "/app/finance", label: "Finance", packageCode: "finance" },
  { to: "/app/marketing", label: "Marketing", packageCode: "marketing" }
];
const enabled = (item: NavigationItem) => !restricted.value && (!item.packageCode || session.selected?.entitlements.packages.some(p => p.code === item.packageCode && p.mode !== "suspended"));
const visiblePrimary = computed(() => [...primary, ...secondary.filter(item => preferences.pinned.includes(item.to))].filter(enabled)), visibleSecondary = computed(() => secondary.filter(enabled));
function setupReturnTo(value: string): string {
  return value.startsWith("/") && !value.startsWith("//") && !value.startsWith("/app/setup") ? value : "/app/workspace";
}
watch(() => route.fullPath, async () => {
  chat.mobileChat = !panelOpen.value;
  await nextTick(); if (panelOpen.value) main.value?.focus();
});
onMounted(async () => {
  await session.load();
  if (!session.userID || sessionStorage.getItem("spyglass_application_entered") === "1") return;
  try {
    const consent = await getPrivacyConsent();
    if (await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, { name: "application_entered", fields: { entry_point: applicationEntryPoint(route.name) } })) sessionStorage.setItem("spyglass_application_entered", "1");
  } catch { /* Optional analytics never interrupts the workspace. */ }
});
watch([restricted, () => route.path], ([isRestricted, path]) => {
  if (isRestricted && !setupRequired.value && !restrictedNavigation.has(path)) void router.replace("/app/billing");
}, { immediate: true });
watch([() => session.loaded, setupRequired, () => route.fullPath], ([loaded, required, fullPath]) => {
  if (!loaded) return;
  if (required && !setupActive.value) void router.replace({ name: "setup", query: { return_to: setupReturnTo(fullPath) } });
  else if (!required && setupActive.value) {
    const requested = Array.isArray(route.query.return_to) ? route.query.return_to[0] : route.query.return_to;
    void router.replace(setupReturnTo(typeof requested === "string" ? requested : ""));
  }
}, { immediate: true });
async function selectAccount(event: Event): Promise<void> {
  const target = event.target as HTMLSelectElement, next = target.value, previous = session.selectedID ?? "";
  try {
    const failure = await router.push("/app/workspace");
    if (failure && route.path !== "/app/workspace") { target.value = previous; return; }
    await session.select(next);
  } catch { target.value = previous; }
}
async function signOut(): Promise<void> {
  if (signingOut.value) return;
  signingOut.value = true; signOutError.value = "";
  try { await logout(); chat.forget(); window.location.assign("/login?status=signed_out"); }
  catch (cause) { signOutError.value = cause instanceof APIProblem ? cause.message : "We could not sign you out. Please try again."; signingOut.value = false; }
}
async function toggleChat(): Promise<void> {
  chat.mobileChat = !chat.mobileChat;
  await nextTick();
  if (chat.mobileChat) document.querySelector<HTMLElement>((chat.surface === "business" ? "#workspace-business-chat" : "#workspace-chat") + " textarea")?.focus();
  else main.value?.focus();
}
async function openMore(event: Event): Promise<void> {
  const target = event.target as HTMLSelectElement;
  if (target.value === "business-chat") { chat.surface = "business"; chat.mobileChat = true; }
  else if (target.value === "agent-chat") { chat.surface = "agents"; chat.mobileChat = true; }
  else if (target.value) await router.push(target.value);
  target.value = "";
}
</script>

<template>
  <a class="skip-link" :href="panelOpen ? '#main' : chat.surface === 'business' ? '#workspace-business-chat' : '#workspace-chat'">Skip to content</a>
  <div class="workspace-shell" :class="{ 'workspace-setup': setupActive }">
    <header v-if="!setupActive" class="workspace-top">
      <RouterLink class="workspace-brand" to="/app/workspace">Spyglass</RouterLink>
      <label class="workspace-account"><span class="sr-only">Account</span><select id="account" :value="session.selectedID" :disabled="session.loading || session.selecting || !session.accounts.length" @change="selectAccount"><option v-if="!session.accounts.length" value="">{{ session.loading ? "Loading…" : "No account" }}</option><option v-for="account in session.accounts" :key="account.account_id" :value="account.account_id">{{ account.display_name }}</option></select></label>
      <RouterLink class="workspace-settings-link" to="/app/settings">Settings</RouterLink>
    </header>
    <nav v-if="!setupActive" class="workspace-navigation" aria-label="Workspace views">
      <RouterLink v-for="item in visiblePrimary" :key="item.to" :to="item.to">{{ item.label }}</RouterLink>
      <RouterLink v-if="!restricted" class="workspace-attention" to="/app/your-turn">Needs you<span v-if="attentionCount"> · {{ attentionCount }}</span></RouterLink>
      <label v-if="visibleSecondary.length" class="workspace-more"><span class="sr-only">More workspace views</span><select value="" @change="openMore"><option value="">More…</option><option value="agent-chat">Agent conversations</option><option v-if="chat.businessVisited" value="business-chat">Business interview</option><option v-for="item in visibleSecondary" :key="item.to" :value="item.to">{{ item.label }}</option></select></label>
      <button class="workspace-chat-toggle" type="button" :aria-pressed="chat.mobileChat || !panelOpen" :aria-controls="chat.surface === 'business' ? 'workspace-business-chat' : 'workspace-chat'" @click="toggleChat">{{ chat.mobileChat && panelOpen ? "Back to view" : "Chat" }}<span v-if="chat.running"> · working</span></button>
    </nav>
    <p v-if="signOutError" role="alert" class="session-notice">{{ signOutError }}</p>
    <div v-if="session.unavailable && !setupActive" class="session-notice" role="status">We could not load your account. <a href="/login?return_to=%2Fapp">Sign in again</a></div>
    <div v-else-if="session.selectionError && !setupActive" class="session-notice" role="alert">{{ session.selectionError }}</div>
    <div v-else-if="restricted && !setupActive" class="session-notice" role="status">This account is restricted. Billing, security, privacy, and data export remain available.</div>
    <div class="workspace-body" :class="{ 'workspace-body-chat-only': !panelOpen, 'workspace-mobile-chat': chat.mobileChat }">
      <main v-if="panelOpen || setupActive" id="main" ref="main" tabindex="-1" class="workspace-view">
        <div v-if="!setupActive" class="workspace-view-controls"><RouterLink v-if="inSettings && route.path !== '/app/settings'" to="/app/settings">← Settings</RouterLink><span></span><RouterLink v-if="!restricted" to="/app/workspace" aria-label="Close working view">Close</RouterLink></div>
        <RouterView />
        <div v-if="route.name === 'settings'" class="settings-sign-out"><button type="button" :disabled="signingOut" @click="signOut">{{ signingOut ? "Signing out…" : "Sign out" }}</button></div>
      </main>
      <BaselineView v-if="!setupActive && chat.businessVisited" v-show="chat.surface === 'business'" id="workspace-business-chat" :key="'business-' + session.selectedID" :workspace-mode="true" />
      <WorkspaceChat v-if="!setupActive" v-show="chat.surface === 'agents'" id="workspace-chat" :key="session.selectedID ?? 'no-account'" :role="!panelOpen ? 'main' : undefined" />
    </div>
    <button v-if="setupActive" class="setup-sign-out" type="button" :disabled="signingOut" @click="signOut">{{ signingOut ? "Signing out…" : "Sign out" }}</button>
    <p v-if="setupActive && signOutError" class="setup-sign-out-error" role="alert">{{ signOutError }}</p>
  </div>
</template>
