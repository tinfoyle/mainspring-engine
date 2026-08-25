<script setup lang="ts">
import { useRuntimeConfig } from "#imports";
import { onMounted } from "vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicSeo } from "~/composables/usePublicSeo";
import { publicFeatures } from "~/content/features";

usePublicSeo({
  title: "Spyglass — Know what needs you next",
  path: "/",
  description: "Spyglass turns business context into guided work, then brings the consequential decisions back to you.",
  schema: {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "Infinite Ocean: Spyglass",
    url: "https://www.infiniteocean.net/",
    applicationCategory: "BusinessApplication",
    operatingSystem: "Web",
    description: "Spyglass turns business context into guided work, then brings consequential decisions back to the people who hold authority.",
    featureList: publicFeatures.map((feature) => feature.name),
    provider: { "@type": "Organization", name: "Infinite Ocean", url: "https://www.infiniteocean.net/" },
    offers: { "@type": "Offer", price: "0", priceCurrency: "USD", description: "Free Account creation" }
  }
});
const appOrigin = useRuntimeConfig().public.appOrigin;
const analyticsConsent = useAnalyticsConsent();

async function startFree(event: MouseEvent, ctaCode: string): Promise<void> {
  event.preventDefault();
  const destination = `${appOrigin}/signup`;
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
        <p class="eyebrow">A clearer operating rhythm</p>
        <h1>Know what needs <em>you</em> next.</h1>
        <p class="hero__lead">Spyglass turns scattered business context into guided work. Agents move the routine forward; important questions, reviews and approvals arrive in one calm place.</p>
        <div class="hero__actions"><a class="button button--primary" :href="`${appOrigin}/signup`" @click="startFree($event, 'hero_start_free')">Start free</a><NuxtLink class="button button--secondary" to="/features">See how it works</NuxtLink></div>
        <p class="hero__note">No payment required to start. Optional analytics stays off unless you accept it.</p>
      </div>
      <div class="turn-preview" aria-label="Preview of Your Turn">
        <header><span>Your Turn</span><strong>3 need you</strong></header>
        <article><small>Approval · 18 min ago</small><h2>Approve the August campaign launch</h2><p>Review the final channels and audience before anything goes live.</p><span>Review approval →</span></article>
        <article><small>Information · 2 hr ago</small><h2>What is our standard refund window?</h2><span>Answer question →</span></article>
      </div>
    </section>

    <section class="promise section-frame">
      <p class="eyebrow">The operating loop</p>
      <h2>Your business keeps moving. Authority stays with you.</h2>
      <ol><li><b>01</b><span><strong>Understand</strong>Spyglass builds an attributable picture of your business.</span></li><li><b>02</b><span><strong>Organize</strong>Gaps become visible, bounded work.</span></li><li><b>03</b><span><strong>Move</strong>Agents work within explicit tools and policy.</span></li><li><b>04</b><span><strong>Decide</strong>Your Turn brings the consequential moments home.</span></li></ol>
    </section>

    <section class="your-turn-story">
      <div><p class="eyebrow">Built around attention</p><h2>A to-do list that knows the difference between busywork and judgment.</h2><p>Your Turn combines questions, work reviews, approvals and uncertain outcomes without pretending they carry the same authority. Each item explains what is needed, why it matters and what happens next.</p><NuxtLink to="/features#your-turn">Explore Your Turn →</NuxtLink></div>
      <blockquote>“What needs my judgment today?”<small>Spyglass answers this before it shows you everything else.</small></blockquote>
    </section>

    <section class="closing-cta section-frame"><p class="eyebrow">Start with clarity</p><h2>Bring the business you have.<br />Build the operating system it needs.</h2><a class="button button--primary" :href="`${appOrigin}/signup`" @click="startFree($event, 'closing_start_free')">Create your free Account</a></section>
  </div>
</template>
