<script setup lang="ts">
import { createError, useRoute } from "#imports";
import { computed, onMounted } from "vue";
import { featureBySlug } from "~/content/features";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicCatalog } from "~/composables/usePublicCatalog";
import { usePublicSeo } from "~/composables/usePublicSeo";

const route = useRoute();
const feature = featureBySlug(String(route.params.slug));
if (!feature) throw createError({ statusCode: 404, statusMessage: "Feature not found" });
usePublicSeo({
  title: `${feature.name} · Spyglass features`,
  path: `/features/${feature.slug}`,
  description: feature.summary,
  schema: {
    "@context": "https://schema.org",
    "@type": "WebPage",
    name: feature.title,
    description: feature.summary,
    url: `https://www.infiniteocean.net/features/${feature.slug}`,
    isPartOf: { "@type": "SoftwareApplication", name: "Infinite Ocean: Spyglass", url: "https://www.infiniteocean.net/" },
    about: { "@type": "Thing", name: feature.name, description: feature.purpose }
  }
});
const { data: catalog, error: catalogError, state: catalogState, publishedLabel } = await usePublicCatalog();
const analyticsConsent = useAnalyticsConsent();
const availablePlans = computed(() => {
  if (!feature.packageCode || catalogState.value === "unavailable" || catalogError.value) return [];
  return (catalog.value?.plans ?? []).filter((plan) => plan.packages[feature.packageCode!] && plan.packages[feature.packageCode!] !== "suspended");
});
onMounted(async () => {
  if (await analyticsConsent.resolve()) {
    await analyticsConsent.track({ name: "feature_viewed", fields: { feature_code: feature.slug, ...(feature.packageCode ? { package_code: feature.packageCode } : {}) } });
  }
});
</script>

<template>
  <article class="feature-detail section-frame">
    <NuxtLink class="feature-back" to="/features">← All features</NuxtLink>
    <header><p class="eyebrow">{{ feature.name }}</p><h1>{{ feature.title }}</h1><p class="lede">{{ feature.purpose }}</p></header>
    <div class="feature-detail__grid"><section><h2>What you can do</h2><ol><li v-for="workflow in feature.workflows" :key="workflow">{{ workflow }}</li></ol></section><section><h2>Built-in safeguards</h2><ul><li v-for="boundary in feature.boundaries" :key="boundary">{{ boundary }}</li></ul></section></div>
    <section class="availability"><h2>Is it included?</h2><p v-if="catalogState === 'stale'" class="catalog-freshness" role="status">We are showing the last confirmed plan details from version {{ catalog?.version }}, published {{ publishedLabel }}. We check them again before checkout.</p><p v-else-if="catalogState === 'unavailable' || catalogError" class="catalog-freshness" role="status">We cannot confirm the current plan details right now, so we will not make a promise we cannot verify.</p><p v-if="feature.packageCode && availablePlans.length">Yes. <strong>{{ feature.name }}</strong> is included with: {{ availablePlans.map((plan) => plan.name).join(', ') }}.</p><p v-else-if="feature.packageCode && catalogState !== 'unavailable' && !catalogError">We could not confirm that this feature is included in a current plan.</p><p v-else-if="!feature.packageCode">Yes. This is part of Spyglass. What you can change depends on your role and your team's current access.</p><NuxtLink class="button button--primary" to="/pricing">See current pricing</NuxtLink></section>
  </article>
</template>
