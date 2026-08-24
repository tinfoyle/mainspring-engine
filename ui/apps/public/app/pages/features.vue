<script setup lang="ts">
import { useSeoMeta } from "#imports";

useSeoMeta({ title: "Spyglass features · Infinite Ocean", description: "Explore Your Turn, Work, Knowledge, Agents, Finance, Marketing and governed integrations in Spyglass." });
const outcomes = [
  ["Your Turn", "Decide with context", "Questions, reviews, approvals and recovery in one action-first queue."],
  ["Work", "Turn gaps into progress", "Durable work, ownership, priorities and history across people and Agents."],
  ["Knowledge", "Build memory with evidence", "Facts, documents, revisions and citations stay attributable."],
  ["Agents", "Delegate inside boundaries", "Versioned personas, approved tools and inspectable runs."],
  ["Baseline", "See what is missing", "A guided assessment connects evidence gaps to practical work."],
  ["Finance", "Keep records governed", "Balanced entries, reconciliation and approvals without mixing product billing."],
  ["Marketing", "Move from plan to release", "Governed campaigns, assets, channels and consequential publishing."],
  ["Integrations", "Connect without surrendering control", "Scoped credentials, health evidence and bounded execution."]
];
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">Complete feature map</p><h1>One operating loop.<br />Clear boundaries.</h1><p>Spyglass connects what the business knows, what needs doing, what Agents can move, and what still requires human authority.</p></section>
  <section id="your-turn" class="feature-grid section-frame"><article v-for="([name, title, copy], index) in outcomes" :key="name"><small>0{{ index + 1 }} · {{ name }}</small><h2>{{ title }}</h2><p>{{ copy }}</p><NuxtLink :to="`/features/${String(name).toLowerCase().replace(' ', '-')}`">Learn more →</NuxtLink></article></section>
</template>
