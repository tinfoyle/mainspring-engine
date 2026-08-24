<script setup lang="ts">
import { IoLogo } from "@spyglass/design-system";
import { useRoute, useRuntimeConfig } from "#imports";
import { ref, watch } from "vue";
import ConsentBanner from "~/components/ConsentBanner.vue";

const menuOpen = ref(false);
const route = useRoute();
const appOrigin = useRuntimeConfig().public.appOrigin;
watch(() => route.fullPath, () => { menuOpen.value = false; });
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>
  <header class="site-header">
    <div class="site-header__inner">
      <NuxtLink to="/" class="site-brand"><IoLogo /></NuxtLink>
      <button class="site-menu" type="button" :aria-expanded="menuOpen" aria-controls="site-navigation" @click="menuOpen = !menuOpen">
        <span>{{ menuOpen ? "Close" : "Menu" }}</span><b aria-hidden="true">{{ menuOpen ? "×" : "☰" }}</b>
      </button>
      <nav id="site-navigation" :class="{ 'site-nav--open': menuOpen }" aria-label="Primary">
        <NuxtLink to="/features">Features</NuxtLink>
        <NuxtLink to="/pricing">Pricing</NuxtLink>
        <NuxtLink to="/privacy">Privacy</NuxtLink>
        <a :href="`${appOrigin}/login?return_to=%2Fapp`">Sign in</a>
        <a class="nav-cta" :href="`${appOrigin}/signup`">Start free</a>
      </nav>
    </div>
  </header>
  <main id="main"><NuxtPage /></main>
  <footer class="site-footer">
    <IoLogo />
    <p>Guided work for businesses that need decisions to turn into progress.</p>
    <nav aria-label="Legal"><NuxtLink to="/privacy">Privacy</NuxtLink><a href="mailto:support@infiniteocean.net">Support</a></nav>
  </footer>
  <ConsentBanner />
</template>
