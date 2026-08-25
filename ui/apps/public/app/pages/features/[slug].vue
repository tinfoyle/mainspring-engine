<script setup lang="ts">
import { emitAnalytics, getPrivacyConsent, type PublicCatalog } from "@spyglass/api";
import { createError, useFetch, useRoute, useSeoMeta } from "#imports";
import { computed, onMounted } from "vue";
import { featureBySlug } from "~/content/features";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";

const route = useRoute();
const feature = featureBySlug(String(route.params.slug));
if (!feature) throw createError({ statusCode: 404, statusMessage: "Feature not found" });
useSeoMeta({ title: `${feature.name} · Spyglass features`, description: feature.summary });
const { data: catalog } = await useFetch<PublicCatalog>("/catalog.json", { key: "public-catalog" });
const analyticsConsent = useAnalyticsConsent();
const availablePlans = computed(() => {
  if (!feature.packageCode) return [];
  return (catalog.value?.plans ?? []).filter((plan) => plan.packages[feature.packageCode!] && plan.packages[feature.packageCode!] !== "suspended");
});
onMounted(async () => {
  try {
    const consent = await getPrivacyConsent();
    analyticsConsent.apply(consent);
    await emitAnalytics(analyticsConsent.allowed.value, { name: "feature_viewed", fields: { feature_code: feature.slug, ...(feature.packageCode ? { package_code: feature.packageCode } : {}) } });
  } catch { analyticsConsent.failClosed(); }
});
</script>

<template>
  <article class="feature-detail section-frame">
    <NuxtLink class="feature-back" to="/features">← All features</NuxtLink>
    <header><p class="eyebrow">{{ feature.name }}</p><h1>{{ feature.title }}</h1><p class="lede">{{ feature.purpose }}</p></header>
    <div class="feature-detail__grid"><section><h2>Primary workflows</h2><ol><li v-for="workflow in feature.workflows" :key="workflow">{{ workflow }}</li></ol></section><section><h2>Governance boundaries</h2><ul><li v-for="boundary in feature.boundaries" :key="boundary">{{ boundary }}</li></ul></section></div>
    <section class="availability"><h2>Availability</h2><p v-if="feature.packageCode && availablePlans.length">The published <strong>{{ feature.packageCode }}</strong> package is available in: {{ availablePlans.map((plan) => plan.name).join(', ') }}.</p><p v-else-if="feature.packageCode">Current plan availability is supplied by the published Catalog. No unavailable plan is promised here.</p><p v-else>This is a core Spyglass workflow. Effective actions still depend on Account role, security state and current entitlements.</p><NuxtLink class="button button--primary" to="/pricing">See current pricing</NuxtLink></section>
  </article>
</template>
