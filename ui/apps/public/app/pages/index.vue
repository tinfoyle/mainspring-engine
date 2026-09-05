<script setup lang="ts">
import { useRuntimeConfig } from "#imports";
import { onMounted } from "vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicSeo } from "~/composables/usePublicSeo";
import { publicFeatures } from "~/content/features";

usePublicSeo({
  title: "Spyglass — Tasks, records and AI agents",
  path: "/",
  description: "Spyglass keeps your jobs, customers, schedule, notes and AI agents together, so you can see what needs attention next.",
  schema: {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "Infinite Ocean: Spyglass",
    url: "https://www.infiniteocean.net/",
    applicationCategory: "BusinessApplication",
    operatingSystem: "Web",
    description: "Spyglass keeps jobs, customers, schedules, notes and AI agents in one place, while important decisions stay with the people responsible for them.",
    featureList: publicFeatures.map((feature) => feature.name),
    provider: { "@type": "Organization", name: "Infinite Ocean", url: "https://www.infiniteocean.net/" },
    offers: { "@type": "Offer", price: "50.00", priceCurrency: "USD", description: "Complete Infinite Ocean team subscription", url: "https://www.infiniteocean.net/pricing" }
  }
});
const appOrigin = useRuntimeConfig().public.appOrigin;
const analyticsConsent = useAnalyticsConsent();

async function startTeam(event: MouseEvent, ctaCode: string): Promise<void> {
  event.preventDefault();
  const destination = `${appOrigin}/signup?offer=team-monthly-v2`;
  try {
    await Promise.race([
      Promise.all([
        analyticsConsent.track({ name: "primary_cta_selected", fields: { cta_code: ctaCode, route_name: "landing" } }),
        analyticsConsent.track({ name: "signup_handoff_started", fields: {} })
      ]),
      new Promise((resolve) => window.setTimeout(resolve, 180))
    ]);
  } catch { /* Optional measurement never interrupts signup. */ }
  window.location.assign(destination);
}

onMounted(async () => {
  if (await analyticsConsent.resolve()) {
    await analyticsConsent.track({ name: "landing_viewed", fields: { route_name: "landing" } });
  }
});
</script>

<template>
  <div>
    <section class="hero">
      <div class="hero__wash" aria-hidden="true" />
      <div class="hero__inner">
        <h1>Manage your tasks<br />and <em>AI agents.</em></h1>
        <p class="hero__lead">Keep your work, schedule and business notes together. Assign tasks to AI agents and review their questions and requests.</p>
        <div class="hero__actions"><a class="button button--primary" :href="`${appOrigin}/signup?offer=team-monthly-v2`" @click="startTeam($event, 'hero_create_team')">Create your team</a><NuxtLink class="button button--secondary" to="/features">See how it works</NuxtLink></div>
        <p class="hero__note">$50 USD per team each month, plus applicable tax.</p>
      </div>
      <div class="turn-preview" aria-label="Preview of Your Turn">
        <header><span>Your Turn</span><strong>3 need you</strong></header>
        <article><small>Approval · 18 min ago</small><h2>Approve the August campaign launch</h2><p>Review the final channels and audience before anything goes live.</p><span>Review approval →</span></article>
        <article><small>Information · 2 hr ago</small><h2>What is our standard refund window?</h2><span>Answer question →</span></article>
      </div>
    </section>

    <section class="promise section-frame">
      <h2>How Spyglass works</h2>
      <ol><li><b>01</b><span><strong>Add your business information</strong>Bring in what you already know about your customers, jobs, people and day-to-day work.</span></li><li><b>02</b><span><strong>Make a plan</strong>Loose ends and missing information become clear tasks.</span></li><li><b>03</b><span><strong>Assign work</strong>Your team and AI agents handle routine work within the limits you set.</span></li><li><b>04</b><span><strong>Step in when needed</strong>Your Turn collects the questions, reviews and approvals that need you.</span></li></ol>
    </section>

    <section class="your-turn-story">
      <div><h2>Questions and approvals in one place.</h2><p>Your Turn puts the decisions only you can make at the top. Every item tells you what happened, what it needs from you and what happens after you answer.</p><NuxtLink to="/features#your-turn">Explore Your Turn →</NuxtLink></div>
    </section>

    <section class="closing-cta section-frame"><h2>Create a Spyglass account for your business.</h2><a class="button button--primary" :href="`${appOrigin}/signup?offer=team-monthly-v2`" @click="startTeam($event, 'closing_create_team')">Create your team</a></section>
  </div>
</template>
