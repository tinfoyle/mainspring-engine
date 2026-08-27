<script setup lang="ts">
import { onMounted } from "vue";
import { featureBySlug, publicFeatures, type PublicFeature } from "~/content/features";
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

function features(...slugs: string[]): ReadonlyArray<PublicFeature> {
  return slugs.map((slug) => featureBySlug(slug)).filter((feature): feature is PublicFeature => Boolean(feature));
}

const workflowGroups = [
  {
    number: "01",
    eyebrow: "Know the business",
    title: "Get the business out of your head.",
    description: "Bring in the answers, documents and hard-won knowledge your team uses every day. Spyglass helps you spot what is missing without making you start over.",
    features: features("baseline", "knowledge")
  },
  {
    number: "02",
    eyebrow: "Keep the day moving",
    title: "Jobs get owners. Repeat work stays on schedule.",
    description: "See what is open, what is stuck and who has it. Set up inspections, follow-ups and other repeat work once so they do not keep sneaking up on you.",
    features: features("work", "schedules")
  },
  {
    number: "03",
    eyebrow: "Delegate the routine",
    title: "Give AI agents real jobs—and clear limits.",
    description: "Connect the tools an agent needs, give it a specific responsibility and decide what it can do on its own. Important actions still come back to you.",
    features: features("agents", "integrations")
  }
] as const;

const businessAreas = features("marketing", "finance");
const controlFeatures = features("account-administration", "security", "export-lifecycle");

onMounted(async () => {
  if (await analyticsConsent.resolve()) {
    await analyticsConsent.track({ name: "feature_viewed", fields: { feature_code: "feature_index", route_name: "features" } });
  }
});
</script>

<template>
  <div class="features-page">
    <section class="content-hero feature-index-hero section-frame"><p class="eyebrow">One place to run the work</p><h1>Keep the whole business<br />in view.</h1><p>Spyglass keeps the jobs, schedule, answers and people together. Your team and AI agents can handle the routine while the questions that need you come back to one clear place.</p></section>

    <section id="your-turn" class="feature-turn-showcase section-frame">
      <div class="feature-turn-showcase__copy">
        <p class="eyebrow">Start with Your Turn</p>
        <h2>What actually needs me today?</h2>
        <p>Open Spyglass and get the short list: a customer question, finished work to review, an approval before something goes out or a problem nobody should guess their way through.</p>
        <p>Each item shows what happened, why it is waiting on you and what your answer will do.</p>
        <NuxtLink to="/features/your-turn">See how Your Turn works →</NuxtLink>
      </div>
      <div class="feature-turn-board" aria-label="Example Your Turn list">
        <header><span>Your Turn</span><strong>3 need you</strong></header>
        <ol>
          <li><small>Customer question · 12 min ago</small><b>Can we move the service call to Friday?</b><span>Answer question →</span></li>
          <li><small>Work review · 48 min ago</small><b>Quarterly equipment checklist is complete</b><span>Review work →</span></li>
          <li><small>Approval · 2 hr ago</small><b>Approve next week's customer reminder</b><span>Review approval →</span></li>
        </ol>
      </div>
    </section>

    <section class="feature-flow-heading section-frame">
      <p class="eyebrow">While you do the actual work</p>
      <h2>Spyglass keeps the rest from slipping.</h2>
      <p>The parts work together. Information turns into work. Work can be scheduled or handed off. Anything important comes back through Your Turn.</p>
    </section>

    <section class="feature-flow section-frame">
      <article v-for="group in workflowGroups" :key="group.number" class="feature-flow__step">
        <div class="feature-flow__copy"><span>{{ group.number }}</span><p class="eyebrow">{{ group.eyebrow }}</p><h2>{{ group.title }}</h2><p>{{ group.description }}</p></div>
        <nav :aria-label="`${group.eyebrow} features`" class="feature-flow__links">
          <NuxtLink v-for="feature in group.features" :key="feature.slug" :to="`/features/${feature.slug}`"><span><strong>{{ feature.name }}</strong><small>{{ feature.summary }}</small></span><b aria-hidden="true">→</b></NuxtLink>
        </nav>
      </article>
    </section>

    <section class="feature-business-areas">
      <div class="section-frame">
        <header><p class="eyebrow">Across the business</p><h2>Use the same system where the work happens.</h2><p>Marketing plans and financial records do not have to become another pile of disconnected approvals and loose ends.</p></header>
        <div class="feature-business-areas__grid">
          <article v-for="feature in businessAreas" :key="feature.slug"><p class="eyebrow">{{ feature.name }}</p><h3>{{ feature.title }}</h3><p>{{ feature.purpose }}</p><NuxtLink :to="`/features/${feature.slug}`">Explore {{ feature.name }} →</NuxtLink></article>
        </div>
      </div>
    </section>

    <section class="feature-controls section-frame">
      <header><p class="eyebrow">You stay in control</p><h2>Your business. Your people. Your data.</h2><p>Spyglass makes it clear who can do what, asks for stronger proof before sensitive changes and gives you a way to take your data with you.</p></header>
      <nav aria-label="Team, security and data features">
        <NuxtLink v-for="feature in controlFeatures" :key="feature.slug" :to="`/features/${feature.slug}`"><strong>{{ feature.name }}</strong><span>{{ feature.summary }}</span><b aria-hidden="true">→</b></NuxtLink>
      </nav>
    </section>

    <section class="feature-page-closing section-frame"><p class="eyebrow">One connected product</p><h2>See what needs you.<br />Know what is handled.</h2><p>The complete Spyglass product is $50 per team each month.</p><NuxtLink class="button button--primary" to="/pricing">See pricing</NuxtLink></section>
  </div>
</template>
