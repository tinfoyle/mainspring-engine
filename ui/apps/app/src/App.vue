<script setup lang="ts">
import { IoLogo } from "@spyglass/design-system";
import { emitAnalytics, getPrivacyConsent, type CatalogPackageCode } from "@spyglass/api";
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute } from "vue-router";
import { useSessionStore } from "./stores/session";

const route = useRoute();
const session = useSessionStore();
const menuOpen = ref(false);
const menuButton = ref<HTMLButtonElement>();
const sidebar = ref<HTMLElement>();
const main = ref<HTMLElement>();

watch(() => route.fullPath, async () => {
  menuOpen.value = false;
  await nextTick();
  main.value?.focus();
});
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

interface NavigationItem {
  to: string;
  label: string;
  packageCode?: CatalogPackageCode;
}

const workspaceNavigation: NavigationItem[] = [
  { to: "/app/your-turn", label: "Your Turn" },
  { to: "/app/work", label: "Work", packageCode: "work" },
  { to: "/app/knowledge", label: "Knowledge", packageCode: "knowledge" },
  { to: "/app/baseline", label: "Baseline" },
  { to: "/app/agents", label: "Agents", packageCode: "agents" },
  { to: "/app/schedules", label: "Schedules", packageCode: "agents" },
  { to: "/app/finance", label: "Finance", packageCode: "finance" },
  { to: "/app/integrations", label: "Integrations", packageCode: "integrations" },
  { to: "/app/marketing", label: "Marketing", packageCode: "marketing" }
];
const accountNavigation: NavigationItem[] = [
  { to: "/app/account", label: "Account" },
  { to: "/app/billing", label: "Billing" },
  { to: "/app/security", label: "Security" },
  { to: "/app/account-exports", label: "Exports" },
  { to: "/app/account-closures", label: "Lifecycle" },
  { to: "/app/affiliate", label: "Affiliate" },
  { to: "/app/privacy", label: "Privacy" }
];
const availablePackageCodes = computed(() => new Set(
  session.selected?.entitlements.packages
    .filter((item) => item.mode !== "suspended")
    .map((item) => item.code) ?? []
));
const visibleWorkspaceNavigation = computed(() => workspaceNavigation.filter(
  (item) => !item.packageCode || availablePackageCodes.value.has(item.packageCode)
));
const hiddenPackageCount = computed(() => new Set(
  workspaceNavigation
    .map((item) => item.packageCode)
    .filter((code): code is CatalogPackageCode => code !== undefined && !availablePackageCodes.value.has(code))
).size);

async function selectAccount(event: Event): Promise<void> {
  const target = event.target as HTMLSelectElement;
  const previous = session.selectedID ?? "";
  try {
    await session.select(target.value);
  } catch {
    target.value = previous;
  }
}

async function toggleMenu(): Promise<void> {
  if (menuOpen.value) {
    closeMenu(true);
    return;
  }
  menuOpen.value = true;
  await nextTick();
  sidebar.value?.querySelector<HTMLElement>(".sidebar-close")?.focus();
}

async function closeMenu(restoreFocus = false): Promise<void> {
  menuOpen.value = false;
  if (restoreFocus) {
    await nextTick();
    menuButton.value?.focus();
  }
}

function containMenuFocus(event: KeyboardEvent): void {
  if (!menuOpen.value) return;
  if (event.key === "Escape") {
    event.preventDefault();
    void closeMenu(true);
    return;
  }
  if (event.key !== "Tab") return;
  const focusable = Array.from(sidebar.value?.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
  ) ?? []).filter((item) => !item.hidden);
  const first = focusable[0];
  const last = focusable.at(-1);
  if (!first || !last) return;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>
  <div class="app-shell">
    <header class="mobile-header" :inert="menuOpen || undefined">
      <RouterLink to="/app/your-turn"><IoLogo compact /></RouterLink>
      <strong>{{ String(route.meta.title ?? "Spyglass") }}</strong>
      <button ref="menuButton" class="menu-button" type="button" :aria-expanded="menuOpen" aria-controls="app-navigation" @click="toggleMenu">
        <span class="sr-only">{{ menuOpen ? "Close" : "Open" }} navigation</span>
        <span aria-hidden="true">{{ menuOpen ? "×" : "☰" }}</span>
      </button>
    </header>

    <!-- The key handler is active only when this landmark becomes the mobile dialog. -->
    <!-- eslint-disable-next-line vuejs-accessibility/no-static-element-interactions -->
    <aside
      id="app-navigation"
      ref="sidebar"
      class="sidebar"
      :class="{ 'sidebar--open': menuOpen }"
      :role="menuOpen ? 'dialog' : undefined"
      :aria-modal="menuOpen ? 'true' : undefined"
      :aria-label="menuOpen ? 'Application navigation' : undefined"
      @keydown="containMenuFocus"
    >
      <div class="sidebar-header">
        <RouterLink class="brand" to="/app/your-turn"><IoLogo /></RouterLink>
        <button class="sidebar-close" type="button" aria-label="Close navigation" @click="closeMenu(true)">×</button>
      </div>
      <nav aria-label="Main navigation">
        <div class="nav-group" role="group" aria-labelledby="workspace-navigation-label">
          <p id="workspace-navigation-label" class="nav-group-title">Workspace</p>
          <RouterLink v-for="item in visibleWorkspaceNavigation" :key="item.to" :to="item.to" class="nav-link">
            <span>{{ item.label }}</span>
          </RouterLink>
          <RouterLink v-if="hiddenPackageCount" to="/app/checkout" class="nav-link nav-link--packages">
            <span>Explore plans</span><small>{{ hiddenPackageCount }} more areas</small>
          </RouterLink>
        </div>
        <div class="nav-group" role="group" aria-labelledby="account-navigation-label">
          <p id="account-navigation-label" class="nav-group-title">Account</p>
          <RouterLink v-for="item in accountNavigation" :key="item.to" :to="item.to" class="nav-link">
            <span>{{ item.label }}</span>
          </RouterLink>
        </div>
      </nav>
      <div class="account-switcher">
        <label for="account">Account</label>
        <select id="account" :value="session.selectedID" :disabled="session.loading || session.selecting || session.accounts.length === 0" @change="selectAccount">
          <option v-if="session.accounts.length === 0" value="">{{ session.loading ? "Loading…" : "No Account" }}</option>
          <option v-for="account in session.accounts" :key="account.account_id" :value="account.account_id">{{ account.display_name }}</option>
        </select>
      </div>
    </aside>
    <button v-if="menuOpen" class="scrim" type="button" tabindex="-1" aria-hidden="true" @click="closeMenu(true)" />

    <main id="main" ref="main" tabindex="-1" :inert="menuOpen || undefined">
      <div v-if="session.unavailable" class="session-notice" role="status">
        We could not load your Account. <a href="/login?return_to=%2Fapp">Sign in again</a>
      </div>
      <div v-else-if="session.selectionError" class="session-notice" role="alert">{{ session.selectionError }}</div>
      <RouterView />
    </main>
  </div>
</template>
