<script setup lang="ts">
import { IoLogo } from "@spyglass/design-system";
import { useRoute, useRuntimeConfig } from "#imports";
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import ConsentBanner from "~/components/ConsentBanner.vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";

const menuOpen = ref(false);
const menuButton = ref<HTMLButtonElement>();
const navigation = ref<HTMLElement>();
const main = ref<HTMLElement>();
const route = useRoute();
const appOrigin = useRuntimeConfig().public.appOrigin;
const analyticsConsent = useAnalyticsConsent();
let desktopQuery: MediaQueryList | undefined;

watch(() => route.fullPath, async () => {
  menuOpen.value = false;
  await nextTick();
  main.value?.focus();
});

watch(menuOpen, (open) => {
  if (typeof document !== "undefined") document.body.classList.toggle("site-menu-open", open);
});

onMounted(() => {
  desktopQuery = window.matchMedia("(min-width: 48rem)");
  desktopQuery.addEventListener("change", closeAtDesktop);
});

onBeforeUnmount(() => {
  desktopQuery?.removeEventListener("change", closeAtDesktop);
  document.body.classList.remove("site-menu-open");
});

function closeAtDesktop(event: MediaQueryListEvent): void {
  if (event.matches) void closeMenu();
}

async function toggleMenu(): Promise<void> {
  if (menuOpen.value) {
    await closeMenu(true);
    return;
  }
  menuOpen.value = true;
  await nextTick();
  navigation.value?.querySelector<HTMLElement>(".site-nav-close")?.focus();
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
  const focusable = Array.from(navigation.value?.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])'
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

async function startSignup(event: MouseEvent): Promise<void> {
  event.preventDefault();
  const destination = `${appOrigin}/signup?offer=team-monthly-v2`;
  try {
    await Promise.race([
      Promise.all([
        analyticsConsent.track({
          name: "primary_cta_selected",
          fields: { cta_code: "navigation_create_team", route_name: route.name?.toString() ?? "unknown" }
        }),
        analyticsConsent.track({ name: "signup_handoff_started", fields: {} })
      ]),
      new Promise((resolve) => window.setTimeout(resolve, 180))
    ]);
  } catch { /* Optional measurement never interrupts signup. */ }
  window.location.assign(destination);
}
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>
  <header class="site-header">
    <div class="site-header__inner">
      <NuxtLink to="/" class="site-brand"><IoLogo /></NuxtLink>
      <button ref="menuButton" class="site-menu" type="button" :aria-expanded="menuOpen" aria-controls="site-navigation" @click="toggleMenu">
        <span>{{ menuOpen ? "Close" : "Menu" }}</span><b aria-hidden="true">{{ menuOpen ? "×" : "☰" }}</b>
      </button>
      <!-- The key handler is active only when this container becomes the mobile dialog. -->
      <!-- eslint-disable-next-line vuejs-accessibility/no-static-element-interactions -->
      <div
        id="site-navigation"
        ref="navigation"
        class="site-navigation"
        :class="{ 'site-navigation--open': menuOpen }"
        :role="menuOpen ? 'dialog' : undefined"
        :aria-modal="menuOpen ? 'true' : undefined"
        :aria-label="menuOpen ? 'Site menu' : undefined"
        @keydown="containMenuFocus"
      >
        <button class="site-nav-close" type="button" aria-label="Close site menu" @click="closeMenu(true)">×</button>
        <nav aria-label="Primary">
          <NuxtLink to="/features">Features</NuxtLink>
          <NuxtLink to="/pricing">Pricing</NuxtLink>
          <NuxtLink to="/privacy">Privacy</NuxtLink>
          <a :href="`${appOrigin}/login?return_to=%2Fapp`">Sign in</a>
          <a class="nav-cta" :href="`${appOrigin}/signup?offer=team-monthly-v2`" @click="startSignup">Create your team</a>
        </nav>
      </div>
    </div>
  </header>
  <button v-if="menuOpen" class="site-menu-scrim" type="button" tabindex="-1" aria-hidden="true" @click="closeMenu(true)" />
  <div class="site-page" :inert="menuOpen || undefined">
    <main id="main" ref="main" tabindex="-1"><NuxtPage /></main>
    <footer class="site-footer">
      <IoLogo />
      <p>Keep the work moving without letting the business get away from you.</p>
      <nav aria-label="Legal"><NuxtLink to="/privacy">Privacy</NuxtLink><NuxtLink to="/terms">Terms</NuxtLink><NuxtLink to="/affiliate-terms">Affiliate terms</NuxtLink><a href="mailto:support@infiniteocean.net">Support</a></nav>
    </footer>
    <ConsentBanner />
  </div>
</template>
