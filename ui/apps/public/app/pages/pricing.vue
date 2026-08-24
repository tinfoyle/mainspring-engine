<script setup lang="ts">
import { useRuntimeConfig, useSeoMeta } from "#imports";

useSeoMeta({ title: "Spyglass pricing · Infinite Ocean", description: "Start Spyglass free and choose a published plan when your team needs more capacity." });
const appOrigin = useRuntimeConfig().public.appOrigin;
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">Simple starting point</p><h1>Start free.<br />Upgrade with context.</h1><p>Creating an Account never requires payment. Paid offers are revalidated by Spyglass before Stripe Checkout, so the browser never chooses a provider price.</p></section>
  <section class="pricing-grid section-frame"><article><p class="eyebrow">Free</p><h2>$0</h2><p>Build the baseline, organize Work and experience Your Turn before making a purchase decision.</p><a class="button button--secondary" :href="`${appOrigin}/signup`">Start free</a></article><article class="pricing-card--featured"><p class="eyebrow">Published plan</p><h2>Catalog price</h2><p>The live customer-facing price and package limits will come from the versioned public Catalog—not hard-coded marketing copy.</p><a class="button button--primary" :href="`${appOrigin}/signup`">Choose after signup</a></article></section>
</template>
