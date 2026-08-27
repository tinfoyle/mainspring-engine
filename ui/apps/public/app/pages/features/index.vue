<script setup lang="ts">
import { onMounted } from "vue";
import { publicFeatures } from "~/content/features";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicSeo } from "~/composables/usePublicSeo";
usePublicSeo({
  title: "Spyglass features · Infinite Ocean",
  path: "/features",
  description: "See how Spyglass helps your team manage work, schedules, business information, AI agents, finances, marketing and connected tools.",
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
  if (await analyticsConsent.resolve()) {
    await analyticsConsent.track({ name: "feature_viewed", fields: { feature_code: "feature_index", route_name: "features" } });
  }
});
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">What Spyglass does</p><h1>Keep the whole business<br />in view.</h1><p>See what needs doing, who is handling it and what is waiting on you. Your team and AI agents can move the routine work while the important calls stay yours.</p></section>
  <section id="your-turn" class="feature-grid section-frame"><article v-for="(feature, index) in publicFeatures" :key="feature.slug"><small>{{ String(index + 1).padStart(2, '0') }} · {{ feature.name }}</small><h2>{{ feature.title }}</h2><p>{{ feature.summary }}</p><NuxtLink :to="`/features/${feature.slug}`">Learn more →</NuxtLink></article></section>
  <section class="workflow-story section-frame"><p class="eyebrow">It all works together</p><h2>Keep the facts. Assign the work. Let agents help. Make the final call.</h2><p>Spyglass connects the parts instead of making you chase them across different apps. A saved answer can unblock a task. An AI agent can prepare the next step. If something important needs approval, it shows up in Your Turn with the details you need.</p><NuxtLink to="/pricing">See pricing →</NuxtLink></section>
</template>
