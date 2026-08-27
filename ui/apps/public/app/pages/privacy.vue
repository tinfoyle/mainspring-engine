<script setup lang="ts">
import { usePublicSeo } from "~/composables/usePublicSeo";

usePublicSeo({ title: "Privacy choices · Infinite Ocean", path: "/privacy", description: "Understand and manage how Infinite Ocean uses necessary storage and optional first-party analytics." });
</script>

<template>
  <article class="legal-copy section-frame"><p class="eyebrow">Policy version 1 draft</p><h1>Privacy, in plain language.</h1><p class="lede">We use necessary storage to keep Infinite Ocean secure and make signup and checkout work. Optional analytics stays off until you accept it.</p><h2>What analytics never contains</h2><p>We do not put your name, email, user or team ID, payment details, prompts, answers, documents, evidence, task content, full URLs, query strings or referring pages into analytics events.</p><h2>Your choices</h2><p>You can accept or reject optional analytics without changing your access to the site or Spyglass. You can change your choice here or inside the application at any time.</p><h2>How long we keep it</h2><p>We keep raw analytics events for up to 395 days, then delete them. Erasing this browser's privacy data removes its saved choices and raw analytics sooner. Team, billing and Affiliate records follow separate contract and legal requirements.</p><PublicConsentHistory /><h2>Before production</h2><p>Before launch, this page will list the company responsible for the data, the privacy contact, service providers, international transfers, legal reasons for processing and location-specific retention periods. Those details still require owner and legal review.</p></article>
</template>
