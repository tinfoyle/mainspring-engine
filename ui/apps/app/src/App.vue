<script setup lang="ts">
import { IoLogo } from "@spyglass/design-system";
import { emitAnalytics, getPrivacyConsent } from "@spyglass/api";
import { onMounted, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute } from "vue-router";
import { useSessionStore } from "./stores/session";

const route = useRoute();
const session = useSessionStore();
const menuOpen = ref(false);
watch(() => route.fullPath, () => { menuOpen.value = false; });
onMounted(async () => {
  await session.load();
  if (!session.userID || sessionStorage.getItem("spyglass_application_entered") === "1") return;
  try {
    const consent = await getPrivacyConsent();
    const emitted = await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, {
      name: "application_entered", fields: { entry_point: "your_turn" }
    });
    if (emitted) sessionStorage.setItem("spyglass_application_entered", "1");
  } catch {
    // Optional measurement never interrupts the application shell.
  }
});

const navigation = [
  { to: "/app/your-turn", label: "Your Turn" },
  { to: "/app/work", label: "Work" },
  { to: "/app/knowledge", label: "Knowledge" },
  { to: "/app/agents", label: "Agents" },
  { to: "/app/schedules", label: "Schedules" },
  { to: "/app/account", label: "Account" },
  { to: "/app/billing", label: "Billing" },
  { to: "/app/security", label: "Security" },
  { to: "/app/account-exports", label: "Exports" },
  { to: "/app/account-closures", label: "Lifecycle" },
  { to: "/app/affiliate", label: "Affiliate" },
  { to: "/app/privacy", label: "Privacy" }
];

async function selectAccount(event: Event): Promise<void> {
  const target = event.target as HTMLSelectElement;
  const previous = session.selectedID ?? "";
  try {
    await session.select(target.value);
  } catch {
    target.value = previous;
  }
}
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>
  <div class="app-shell">
    <header class="mobile-header">
      <RouterLink to="/app/your-turn"><IoLogo compact /></RouterLink>
      <strong>{{ String(route.meta.title ?? "Spyglass") }}</strong>
      <button class="menu-button" type="button" :aria-expanded="menuOpen" aria-controls="app-navigation" @click="menuOpen = !menuOpen">
        <span class="sr-only">{{ menuOpen ? "Close" : "Open" }} navigation</span>
        <span aria-hidden="true">{{ menuOpen ? "×" : "☰" }}</span>
      </button>
    </header>

    <aside id="app-navigation" class="sidebar" :class="{ 'sidebar--open': menuOpen }">
      <RouterLink class="brand" to="/app/your-turn"><IoLogo /></RouterLink>
      <nav aria-label="Main navigation">
        <RouterLink v-for="item in navigation" :key="item.to" :to="item.to" class="nav-link">
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="account-switcher">
        <label for="account">Account</label>
        <select id="account" :value="session.selectedID" :disabled="session.loading || session.selecting || session.accounts.length === 0" @change="selectAccount">
          <option v-if="session.accounts.length === 0" value="">{{ session.loading ? "Loading…" : "No Account" }}</option>
          <option v-for="account in session.accounts" :key="account.account_id" :value="account.account_id">{{ account.display_name }}</option>
        </select>
      </div>
    </aside>
    <button v-if="menuOpen" class="scrim" aria-label="Close navigation" @click="menuOpen = false" />

    <main id="main" tabindex="-1">
      <div v-if="session.unavailable" class="session-notice" role="status">
        We could not load your Account. <a href="/login?return_to=%2Fapp">Sign in again</a>
      </div>
      <div v-else-if="session.selectionError" class="session-notice" role="alert">{{ session.selectionError }}</div>
      <RouterView />
    </main>
  </div>
</template>
