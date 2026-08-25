<script setup lang="ts">
import { emitAnalytics, getPrivacyConsent } from "@spyglass/api";
import { onMounted } from "vue";
import { publicFeatures } from "~/content/features";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicSeo } from "~/composables/usePublicSeo";
usePublicSeo({
  title: "Spyglass features · Infinite Ocean",
  path: "/features",
  description: "Explore Your Turn, Work, Knowledge, Agents, Finance, Marketing and governed integrations in Spyglass.",
  schema: {
    "@context": "https://schema.org",
    "@type": "ItemList",
    name: "Spyglass feature map",
    itemListElement: publicFeatures.map((feature, index) => ({
      "@type": "ListItem",
      position: index + 1,
      name: feature.name,
      url: `https://www.infiniteocean.net/features/${feature.slug}`
    }))
  }
});
const analyticsConsent = useAnalyticsConsent();
onMounted(async () => {
  try {
    const consent = await getPrivacyConsent();
    analyticsConsent.apply(consent);
    await emitAnalytics(analyticsConsent.allowed.value, { name: "feature_viewed", fields: { feature_code: "feature_index", route_name: "features" } });
  } catch { analyticsConsent.failClosed(); }
});
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">Complete feature map</p><h1>One operating loop.<br />Clear boundaries.</h1><p>Spyglass connects what the business knows, what needs doing, what Agents can move, and what still requires human authority.</p></section>
  <section id="your-turn" class="feature-grid section-frame"><article v-for="(feature, index) in publicFeatures" :key="feature.slug"><small>{{ String(index + 1).padStart(2, '0') }} · {{ feature.name }}</small><h2>{{ feature.title }}</h2><p>{{ feature.summary }}</p><NuxtLink :to="`/features/${feature.slug}`">Learn more →</NuxtLink></article></section>
  <section class="workflow-story section-frame"><p class="eyebrow">Cross-package workflow</p><h2>Knowledge explains the gap. Work moves it. Agents help. Your Turn keeps judgment human.</h2><p>Spyglass packages share one governed operating loop without collapsing their authority boundaries. A cited fact can answer a blocked task; an Agent can prepare a release; the consequential approval still arrives with exact evidence in Your Turn.</p><NuxtLink to="/pricing">Compare published plans →</NuxtLink></section>
</template>
